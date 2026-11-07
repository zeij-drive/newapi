"""Pure helpers used by the StarryAPI AstrBot plugin.

The module intentionally has no AstrBot imports so command parsing, signatures,
and HTML parsing can be regression-tested on a machine that only has Python.
"""
from __future__ import annotations

from dataclasses import dataclass
from email.utils import parseaddr
import hashlib
import hmac
from html.parser import HTMLParser
import json
import math
import re
import time
from typing import Any


STATUS_PAGE_URL = "https://status.openai.com/"
MAX_STATUS_BODY = 2 * 1024 * 1024
GPTSTATUS_COOLDOWN_SECONDS = 180


@dataclass(frozen=True, slots=True)
class StatusComponent:
    name: str
    status: str
    component_id: str


@dataclass(frozen=True, slots=True)
class StatusSnapshot:
    indicator: str
    description: str
    components: tuple[StatusComponent, ...] = ()

    def text(self) -> str:
        description = self.description or "状态未知"
        return f"GPT 状态：{description}\n查看详情：https://status.openai.com/"


def strip_message_mentions(content: str) -> str:
    """Remove leading QQ/AstrBot mention components from command text."""
    value = content.strip()
    mention_patterns = (
        re.compile(r"^<@!?[^>]+>\s*"),
        re.compile(r"^\[CQ:at,qq=[^\]]+\]\s*", re.IGNORECASE),
        re.compile(r"^@[^\s]+\s*"),
    )
    changed = True
    while changed and value:
        changed = False
        for pattern in mention_patterns:
            updated = pattern.sub("", value, count=1)
            if updated != value:
                value = updated.strip()
                changed = True
                break
    return value


def parse_sapi_command(content: str) -> tuple[str, list[str]]:
    fields = strip_message_mentions(content).split()
    if not fields or fields[0].casefold() not in {"/sapi", "／sapi", "sapi"}:
        return "", []
    if len(fields) == 1 or (len(fields) == 2 and fields[1].casefold() == "help"):
        return "help", []
    if fields[1] == "邀请码":
        return ("invite", fields[2:]) if len(fields) == 3 else ("invite_usage", fields[2:])
    return "invalid", fields[1:]


def parse_gptstatus_command(content: str) -> str:
    fields = strip_message_mentions(content).split()
    if not fields or fields[0].casefold() not in {"/gptstatus", "／gptstatus", "gptstatus"}:
        return ""
    if len(fields) == 1:
        return "query"
    if len(fields) == 2 and fields[1].casefold() == "help":
        return "usage"
    return "invalid"


def gptstatus_cooldown_remaining(last_success: float | None, now: float) -> int:
    """Return remaining non-admin cooldown seconds, rounded up for display."""
    if last_success is None:
        return 0
    return max(0, math.ceil(GPTSTATUS_COOLDOWN_SECONDS - (now - last_success)))


def valid_email(value: str) -> bool:
    if not value or len(value) > 254 or value.strip() != value or any(char.isspace() for char in value):
        return False
    name, address = parseaddr(value)
    if name or address != value or "@" not in address:
        return False
    local, domain = address.rsplit("@", 1)
    return bool(local and domain and "." in domain and not domain.startswith(".") and not domain.endswith("."))


def is_loopback_host(host: str) -> bool:
    import ipaddress

    if host.casefold() == "localhost":
        return True
    try:
        return ipaddress.ip_address(host).is_loopback
    except ValueError:
        return False


def callback_path_is_safe(path: str) -> bool:
    return bool(path.startswith("/") and len(path) <= 256 and not any(part in path for part in ("..", "?", "#")))


def sign_request(secret: str, method: str, path: str, timestamp: str, nonce: str, body: bytes) -> str:
    digest = hashlib.sha256(body).hexdigest()
    canonical = "\n".join((method.upper(), path, timestamp, nonce, digest))
    return hmac.new(secret.encode(), canonical.encode(), hashlib.sha256).hexdigest()


def verify_signed_request(
    secret: str,
    method: str,
    path: str,
    timestamp: str,
    nonce: str,
    signature: str,
    body: bytes,
    now: int | None = None,
) -> bool:
    if len(secret) < 32 or not callback_path_is_safe(path):
        return False
    if not re.fullmatch(r"[0-9a-f]{32}", nonce or ""):
        return False
    if not re.fullmatch(r"[0-9a-f]{64}", signature or ""):
        return False
    try:
        request_time = int(timestamp)
    except (TypeError, ValueError):
        return False
    current_time = int(time.time() if now is None else now)
    if abs(current_time - request_time) > 300:
        return False
    expected = sign_request(secret, method, path, timestamp, nonce, body)
    return hmac.compare_digest(expected, signature)


def _normalize_indicator(value: str) -> str:
    return {
        "operational": "operational",
        "degraded": "minor",
        "degraded-performance": "minor",
        "partial-outage": "major",
        "full-outage": "critical",
        "maintenance": "maintenance",
        "under-maintenance": "maintenance",
    }.get(value.strip().casefold(), "")


def _infer_indicator(text: str) -> str:
    value = text.casefold()
    if (
        "fully operational" in value
        or "all systems operational" in value
        or "all services operational" in value
        or "all components operational" in value
        or "no issues" in value
    ):
        return "operational"
    if "full outage" in value:
        return "critical"
    if "maintenance" in value:
        return "maintenance"
    if "partial outage" in value:
        return "major"
    if "degraded" in value:
        return "minor"
    return ""


def _component_id(name: str) -> str:
    return hashlib.sha256(name.encode()).hexdigest()[:16]


class _StatusPageHTMLParser(HTMLParser):
    """Extract visible status content without reading CSS or script text."""

    def __init__(self) -> None:
        super().__init__(convert_charrefs=True)
        self.sections: dict[str, dict[str, list[Any]]] = {
            "heads-up": {"text": [], "statuses": []},
            "system-status": {"text": [], "statuses": [], "components": []},
        }
        self.visible_text: list[str] = []
        self.visible_statuses: list[str] = []
        self._stack: list[tuple[str, int]] = []
        self._active_sections: list[tuple[str, int]] = []
        self._ignored_depths: list[int] = []
        self._pending_component_status = ""
        self._current_heading: dict[str, Any] | None = None

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        depth = len(self._stack) + 1
        self._stack.append((tag, depth))
        attributes = dict(attrs)
        section_name = attributes.get("data-testid")
        if section_name in self.sections:
            self._active_sections.append((section_name, depth))
        if tag in {"script", "style", "noscript"}:
            self._ignored_depths.append(depth)
        if self._ignored_depths:
            return
        classes = (attributes.get("class") or "").split()
        active_section = self._active_sections[-1][0] if self._active_sections else ""
        for class_name in classes:
            if not class_name.casefold().startswith("text-icon-"):
                continue
            status = _normalize_indicator(class_name[len("text-icon-") :])
            if not status:
                continue
            self.visible_statuses.append(status)
            for section_name, _depth in self._active_sections:
                self.sections[section_name]["statuses"].append(status)
            if active_section == "system-status":
                self._pending_component_status = status

        if not active_section:
            return

        if active_section == "system-status" and tag == "h3":
            self._current_heading = {
                "depth": depth,
                "parts": [],
                "status": self._pending_component_status,
            }
            self._pending_component_status = ""

    def handle_data(self, data: str) -> None:
        if self._ignored_depths:
            return
        text = " ".join(data.split())
        if not text:
            return
        self.visible_text.append(text)
        for section_name, _depth in self._active_sections:
            self.sections[section_name]["text"].append(text)
        if self._current_heading is not None:
            self._current_heading["parts"].append(text)

    def handle_endtag(self, tag: str) -> None:
        stack_index = next(
            (index for index in range(len(self._stack) - 1, -1, -1) if self._stack[index][0] == tag),
            None,
        )
        if stack_index is None:
            return
        depth = self._stack[stack_index][1]
        if self._current_heading is not None and self._current_heading["depth"] >= depth:
            name = " ".join(self._current_heading["parts"]).strip()
            status = self._current_heading["status"]
            if name and status and len(name) <= 160:
                self.sections["system-status"]["components"].append(
                    StatusComponent(name, status, _component_id(name)),
                )
            self._current_heading = None
        self._active_sections = [
            section for section in self._active_sections if section[1] < depth
        ]
        self._ignored_depths = [item_depth for item_depth in self._ignored_depths if item_depth < depth]
        self._stack = self._stack[:stack_index]


def _status_description(text_parts: list[str], indicator: str) -> str:
    visible_text = [" ".join(part.split()) for part in text_parts if part.strip()]
    if not visible_text:
        return "OpenAI status details are unavailable"

    first = visible_text[0]
    if indicator != "operational" and len(visible_text) > 1:
        return f"{first}: {visible_text[1]}"
    return first


def parse_status_html(source: str) -> StatusSnapshot:
    if not source.strip():
        raise ValueError("OpenAI status page is empty")

    parser = _StatusPageHTMLParser()
    parser.feed(source)
    parser.close()
    banner = parser.sections["heads-up"]
    system = parser.sections["system-status"]
    indicators = banner["statuses"]
    if indicators:
        indicator = indicators[0]
        description = _status_description(banner["text"], indicator)
    else:
        banner_text = " ".join(banner["text"])
        system_text = " ".join(system["text"])
        page_text = " ".join(parser.visible_text)
        indicator = _infer_indicator(banner_text)
        if not indicator:
            indicator = _infer_indicator(system_text)
        if not indicator:
            indicator = _infer_indicator(page_text)
        status_candidates = system["statuses"] or parser.visible_statuses
        if not indicator and status_candidates:
            severity = {"critical": 5, "major": 4, "minor": 3, "maintenance": 2, "operational": 1}
            indicator = max(status_candidates, key=lambda item: severity.get(item, 0))
        if not indicator:
            raise ValueError("OpenAI status page has no visible status indicator")
        description_parts = banner["text"] or system["text"]
        if not description_parts and page_text:
            matching_text = [part for part in parser.visible_text if _infer_indicator(part) == indicator]
            description_parts = matching_text or parser.visible_text
        description = _status_description(description_parts, indicator)
        if description in {"OpenAI status details are unavailable", "System status"}:
            description = {
                "operational": "All systems operational",
                "minor": "Degraded performance",
                "major": "Partial outage",
                "critical": "Major outage",
                "maintenance": "Scheduled maintenance in progress",
            }[indicator]

    seen: set[str] = set()
    components: list[StatusComponent] = []
    for component in system["components"]:
        if component.name.casefold() in seen:
            continue
        seen.add(component.name.casefold())
        components.append(component)
    return StatusSnapshot(indicator, description[:240], tuple(components))


def decode_json_object(body: bytes) -> dict[str, Any]:
    value = json.loads(body.decode("utf-8"))
    if not isinstance(value, dict):
        raise ValueError("JSON object required")
    return value


__all__ = [
    "GPTSTATUS_COOLDOWN_SECONDS",
    "MAX_STATUS_BODY",
    "STATUS_PAGE_URL",
    "StatusComponent",
    "StatusSnapshot",
    "callback_path_is_safe",
    "decode_json_object",
    "gptstatus_cooldown_remaining",
    "is_loopback_host",
    "parse_gptstatus_command",
    "parse_sapi_command",
    "parse_status_html",
    "sign_request",
    "strip_message_mentions",
    "valid_email",
    "verify_signed_request",
]
