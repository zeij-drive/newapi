from __future__ import annotations

import asyncio
import json
from pathlib import Path
import secrets
import tempfile
import textwrap
import time
from urllib.parse import urlsplit

import aiohttp
from PIL import Image, ImageDraw, ImageFont

from astrbot.api import AstrBotConfig, logger
from astrbot.api.event import AstrMessageEvent, filter
from astrbot.api.star import Context, Star, register

from .core import (
    MAX_STATUS_BODY,
    GPTSTATUS_COOLDOWN_SECONDS,
    STATUS_PAGE_URL,
    StatusSnapshot,
    gptstatus_cooldown_remaining,
    is_loopback_host,
    parse_gptstatus_command,
    parse_sapi_command,
    parse_status_html,
    sign_request,
    strip_message_mentions,
    valid_email,
)


PLUGIN_NAME = "astrbot_plugin_starryapi"
INVITE_PATH = "/api/bot/registration-invite"
HELP_MESSAGE = (
    "StarryAPI\n"
    "/sapi - 显示帮助\n"
    "/sapi 邀请码 邮箱 - 获取邀请码并发送到邮箱\n"
    "/gptstatus - 查询并发送 GPT 状态图片（普通用户每 3 分钟一次）"
)
INVITE_USAGE = "用法：/sapi 邀请码 邮箱"
GPTSTATUS_USAGE = "用法：/gptstatus（查询并发送 GPT 状态图片）。"
COMMAND_NOT_FOUND = "指令不存在"


class StarryAPIPlugin(Star):
    """AstrBot version of the StarryAPI QQ bot.

    QQ platform connectivity is provided by AstrBot's configured QQ adapter. The
    plugin only owns StarryAPI commands and the signed NewAPI client.
    """

    def __init__(self, context: Context, config: AstrBotConfig):
        super().__init__(context)
        self.config = config
        self._session: aiohttp.ClientSession | None = None
        self._gptstatus_rate_lock = asyncio.Lock()
        self._gptstatus_last_success: dict[str, float] = {}
        self._gptstatus_pending_users: set[str] = set()
        self._gptstatus_last_cleanup = 0.0
        self._new_api_url = self._normalise_new_api_url(self._value("new_api_url", ""))
        secret = self._value("starry_api_bot_secret", "")
        self._secret = secret.strip() if isinstance(secret, str) else ""
        self._configuration_errors: list[str] = []
        if self._new_api_url is None:
            self._configuration_errors.append("new_api_url 无效")
        if len(self._secret) < 32:
            self._configuration_errors.append("starry_api_bot_secret 未配置或长度不足 32")

    def _value(self, key: str, default: object) -> object:
        value = self.config.get(key, default)
        return default if value is None else value

    @staticmethod
    def _normalise_new_api_url(value: object) -> str | None:
        if not isinstance(value, str):
            return None
        base = value.strip().rstrip("/")
        if not base:
            return None
        try:
            parsed = urlsplit(base)
        except ValueError:
            return None
        if (
            parsed.scheme not in {"http", "https"}
            or not parsed.hostname
            or parsed.username is not None
            or parsed.password is not None
            or parsed.query
            or parsed.fragment
            or (parsed.scheme != "https" and not is_loopback_host(parsed.hostname))
        ):
            return None
        return base

    async def initialize(self) -> None:
        self._session = aiohttp.ClientSession(
            timeout=aiohttp.ClientTimeout(total=10),
            raise_for_status=False,
            headers={"User-Agent": "StarryAPI-AstrBot/1.0"},
        )
        if self._configuration_errors:
            logger.error("StarryAPI plugin configuration error: %s", "; ".join(self._configuration_errors))

    async def terminate(self) -> None:
        if self._session is not None and not self._session.closed:
            await self._session.close()
            self._session = None

    def _is_configured(self) -> bool:
        return self._session is not None and self._new_api_url is not None and len(self._secret) >= 32

    async def _send_invite(self, open_id: str, email: str) -> str:
        if not self._is_configured():
            return "邀请码功能未配置，请先在 AstrBot 插件配置中填写 NewAPI 地址和共享密钥。"
        assert self._session is not None and self._new_api_url is not None
        payload = json.dumps({"open_id": open_id, "email": email}, ensure_ascii=False, separators=(",", ":")).encode()
        timestamp = str(int(time.time()))
        nonce = secrets.token_hex(16)
        path = INVITE_PATH
        headers = {
            "Content-Type": "application/json",
            "X-QQ-Bot-Timestamp": timestamp,
            "X-QQ-Bot-Nonce": nonce,
            "X-QQ-Bot-Signature": sign_request(self._secret, "POST", path, timestamp, nonce, payload),
        }
        try:
            async with self._session.post(
                f"{self._new_api_url}{path}",
                data=payload,
                headers=headers,
                allow_redirects=False,
            ) as response:
                response_body = await response.content.read(4096)
                if response.status == 429:
                    return "请求过于频繁，请 1 分钟后重试。"
                if response.status < 200 or response.status >= 300:
                    logger.warning("StarryAPI invite request returned HTTP %d", response.status)
                    return "邀请码邮件发送失败，请检查邮箱或稍后重试。"
                try:
                    result = json.loads(response_body.decode("utf-8"))
                except (UnicodeDecodeError, json.JSONDecodeError):
                    return "邀请码邮件发送失败，请检查邮箱或稍后重试。"
                data = result.get("data") if isinstance(result, dict) else None
                if not isinstance(result, dict) or result.get("success") is not True or not isinstance(data, dict) or data.get("sent") is not True:
                    return "邀请码邮件发送失败，请检查邮箱或稍后重试。"
                return "邀请码已发送至邮箱，请查收。"
        except (aiohttp.ClientError, asyncio.TimeoutError) as exc:
            logger.warning("StarryAPI invite request failed: %s", exc)
            return "邀请码邮件发送失败，请检查邮箱或稍后重试。"

    async def _begin_gptstatus_request(self, user_id: str) -> str | None:
        async with self._gptstatus_rate_lock:
            now = time.monotonic()
            if now - self._gptstatus_last_cleanup >= GPTSTATUS_COOLDOWN_SECONDS:
                self._gptstatus_last_success = {
                    current_user: last_success
                    for current_user, last_success in self._gptstatus_last_success.items()
                    if now - last_success < GPTSTATUS_COOLDOWN_SECONDS
                }
                self._gptstatus_last_cleanup = now
            if user_id in self._gptstatus_pending_users:
                return "状态查询正在进行，请稍候。"
            remaining = gptstatus_cooldown_remaining(
                self._gptstatus_last_success.get(user_id),
                now,
            )
            if remaining:
                return f"查询频率受限，请在 {remaining} 秒后再试。"
            self._gptstatus_pending_users.add(user_id)
        return None

    async def _finish_gptstatus_request(self, user_id: str, succeeded: bool) -> None:
        async with self._gptstatus_rate_lock:
            self._gptstatus_pending_users.discard(user_id)
            if succeeded:
                self._gptstatus_last_success[user_id] = time.monotonic()

    @staticmethod
    def _render_status_image(snapshot: StatusSnapshot) -> str:
        width, height = 1200, 760
        image = Image.new("RGB", (width, height), "#0b1020")
        draw = ImageDraw.Draw(image)

        def load_font(size: int):
            for path in (
                "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
                "/usr/share/fonts/truetype/liberation2/LiberationSans-Regular.ttf",
                "C:/Windows/Fonts/arial.ttf",
            ):
                try:
                    return ImageFont.truetype(path, size)
                except OSError:
                    continue
            return ImageFont.load_default(size=size)

        def image_text(value: str, limit: int = 100) -> str:
            # The status page is English; replace unsupported symbols so the
            # bundled default font still produces a valid image everywhere.
            value = value.translate(str.maketrans({"’": "'", "‘": "'", "–": "-", "—": "-", "…": "..."}))
            return "".join(char if char.isascii() and char.isprintable() else "?" for char in value)[:limit]

        title_font = load_font(42)
        label_font = load_font(20)
        body_font = load_font(28)
        small_font = load_font(18)
        status_colors = {
            "operational": (34, 197, 94),
            "minor": (234, 179, 8),
            "major": (249, 115, 22),
            "critical": (239, 68, 68),
            "maintenance": (99, 102, 241),
        }
        status_labels = {
            "operational": "OPERATIONAL",
            "minor": "DEGRADED PERFORMANCE",
            "major": "PARTIAL OUTAGE",
            "critical": "MAJOR OUTAGE",
            "maintenance": "UNDER MAINTENANCE",
        }
        component_status_labels = {
            "operational": "OPERATIONAL",
            "minor": "DEGRADED",
            "major": "PARTIAL",
            "critical": "OUTAGE",
            "maintenance": "MAINTENANCE",
        }
        accent = status_colors.get(snapshot.indicator, (148, 163, 184))

        draw.rounded_rectangle((52, 48, width - 52, height - 48), radius=30, fill="#111a2e", outline="#26334d", width=2)
        draw.text((92, 88), "OPENAI STATUS", font=title_font, fill="#f8fafc")
        draw.text((94, 148), "LIVE SERVICE OVERVIEW", font=label_font, fill="#94a3b8")

        draw.rounded_rectangle((88, 206, width - 88, 374), radius=22, fill="#18243a")
        draw.ellipse((122, 246, 148, 272), fill=accent)
        draw.text((168, 236), status_labels.get(snapshot.indicator, "STATUS UNKNOWN"), font=label_font, fill=accent)
        description_lines = textwrap.wrap(image_text(snapshot.description, 180), width=58)
        if not description_lines:
            description_lines = ["OpenAI status details are unavailable"]
        if len(description_lines) > 2:
            description_lines = [description_lines[0], f"{description_lines[1][:55].rstrip()}..."]
        for line_index, line in enumerate(description_lines):
            draw.text((122, 290 + line_index * 34), line, font=body_font, fill="#f8fafc")

        draw.text((92, 420), "COMPONENT STATUS", font=label_font, fill="#94a3b8")
        components = snapshot.components[:6]
        if components:
            row_y = 466
            row_height = 48
            for index, component in enumerate(components):
                column = index % 2
                row = index // 2
                left = 92 + column * 500
                top = row_y + row * 58
                color = status_colors.get(component.status, (148, 163, 184))
                draw.rounded_rectangle((left, top, left + 470, top + row_height), radius=12, fill="#18243a")
                draw.ellipse((left + 16, top + 17, left + 30, top + 31), fill=color)
                draw.text((left + 44, top + 12), image_text(component.name, 36), font=small_font, fill="#e2e8f0")
                draw.text((left + 350, top + 14), component_status_labels.get(component.status, "UNKNOWN"), font=load_font(14), fill=color)
        else:
            draw.text((92, 466), "No component details are available.", font=small_font, fill="#cbd5e1")

        if len(snapshot.components) > len(components):
            draw.text((92, 646), f"+ {len(snapshot.components) - len(components)} more components", font=small_font, fill="#64748b")
        draw.text((92, height - 78), "status.openai.com", font=small_font, fill="#64748b")
        with tempfile.NamedTemporaryFile(prefix="starryapi-gpt-status-", suffix=".png", delete=False) as output:
            image.save(output, format="PNG", optimize=True)
            return output.name

    # AstrBot removes the configured wake prefix (usually ``/``) before it
    # runs a command filter.  When a QQ group message wakes the bot through an
    # @ mention, however, there is no wake prefix to remove and the slash is
    # still present in ``event.message_str``.  Register both spellings so the
    # handler is reached in both paths; the parser below remains the single
    # source of truth for the command arguments.
    @filter.command(
        "sapi",
        alias={"/sapi", "／sapi", "SAPI", "/SAPI", "／SAPI"},
    )
    async def sapi(self, event: AstrMessageEvent):
        """StarryAPI 邀请码命令。"""
        content = strip_message_mentions(event.message_str)
        command, args = parse_sapi_command(content)
        # A few AstrBot adapters pass only the arguments to the handler after
        # command matching.  Parse that form as well instead of depending on
        # a particular adapter's event.message_str behavior.
        if not command:
            command, args = parse_sapi_command(f"sapi {content}".strip())
        if not command:
            return
        if command == "help":
            yield event.plain_result(HELP_MESSAGE)
            return
        if command == "invalid":
            yield event.plain_result(COMMAND_NOT_FOUND)
            return
        if command in {"invite_usage", "invite"}:
            if command == "invite_usage" or len(args) != 1 or not valid_email(args[0]):
                yield event.plain_result(INVITE_USAGE)
                return
            yield event.plain_result(await self._send_invite(str(event.get_sender_id()), args[0]))
            return
        yield event.plain_result(COMMAND_NOT_FOUND)

    @filter.command(
        "gptstatus",
        alias={"/gptstatus", "／gptstatus", "GPTSTATUS", "/GPTSTATUS", "／GPTSTATUS"},
    )
    async def gptstatus(self, event: AstrMessageEvent):
        """从 OpenAI 状态页 HTML 源码读取状态并发送图片。"""
        content = strip_message_mentions(event.message_str)
        command = parse_gptstatus_command(content)
        if not command:
            command = parse_gptstatus_command(f"gptstatus {content}".strip())
        if command == "invalid":
            yield event.plain_result(COMMAND_NOT_FOUND)
            return
        if command == "usage":
            yield event.plain_result(GPTSTATUS_USAGE)
            return
        if command != "query":
            return
        if not str(event.get_group_id() or "").strip():
            yield event.plain_result("GPT 状态查询仅支持群聊。")
            return
        if self._session is None:
            yield event.plain_result("暂时无法获取 OpenAI 状态，请稍后重试。")
            return
        user_id = str(event.get_sender_id() or "").strip()
        is_admin = bool(event.is_admin())
        if not is_admin:
            if not user_id:
                yield event.plain_result("暂时无法识别当前 QQ 账号，请稍后重试。")
                return
            rate_limit_reply = await self._begin_gptstatus_request(user_id)
            if rate_limit_reply:
                yield event.plain_result(rate_limit_reply)
                return
        succeeded = False
        image_path: str | None = None
        try:
            async with self._session.get(STATUS_PAGE_URL, headers={"Accept": "text/html,application/xhtml+xml"}, allow_redirects=False) as response:
                if response.status != 200:
                    raise RuntimeError(f"status page returned HTTP {response.status}")
                body = await response.content.read(MAX_STATUS_BODY + 1)
                if len(body) > MAX_STATUS_BODY:
                    raise RuntimeError("status page response is too large")
            snapshot = parse_status_html(body.decode("utf-8", errors="replace"))
        except (aiohttp.ClientError, asyncio.TimeoutError, RuntimeError, ValueError) as exc:
            logger.warning("StarryAPI OpenAI status query failed: %s", exc)
            yield event.plain_result("暂时无法获取 OpenAI 状态，请稍后重试。")
        else:
            try:
                image_path = await asyncio.to_thread(self._render_status_image, snapshot)
                track_temporary_file = getattr(event, "track_temporary_local_file", None)
                if callable(track_temporary_file):
                    track_temporary_file(image_path)
                succeeded = True
                yield event.image_result(image_path)
            except Exception as exc:  # noqa: BLE001 - Pillow or adapter image errors are platform-specific.
                logger.warning("StarryAPI status image generation failed: %s", exc)
                yield event.plain_result("状态已获取，但图片生成失败，请检查 AstrBot 插件依赖后重试。")
        try:
            if image_path and not callable(getattr(event, "track_temporary_local_file", None)):
                await asyncio.to_thread(Path(image_path).unlink, missing_ok=True)
        finally:
            if not is_admin:
                await self._finish_gptstatus_request(user_id, succeeded)


@register(PLUGIN_NAME, "StarryAPI", "StarryAPI QQ Bot 的 AstrBot 插件实现。", "2.1.4")
class RegisteredStarryAPIPlugin(StarryAPIPlugin):
    """AstrBot's loader uses the registered subclass as the plugin entry point."""


__all__ = ["RegisteredStarryAPIPlugin", "StarryAPIPlugin"]
