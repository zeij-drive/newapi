# StarryAPI QQ Bot（独立部署）

QQ Bot 是独立服务，不随 NewAPI 构建或启动。它可以部署在单独的 Linux 虚拟主机上，通过 HTTPS 调用 NewAPI 的邀请码接口。NewAPI 和 QQ Bot 必须配置完全相同的 `STARRY_API_BOT_SECRET`（至少 32 个字符）。

## 1. PHP 虚拟主机面板部署（无需 Go、Docker 或常驻进程）

如果虚拟主机面板只提供 PHP 网站（例如宝塔、cPanel 或普通共享主机），直接上传 `qqbot/php/` 目录即可。该版本使用 QQ 官方 HTTPS Webhook，每次回调由 PHP 处理，不需要启动守护进程。

### 1.1 环境要求

- PHP 7.4 或更高版本；启用 `sodium` 扩展（用于 QQ Ed25519 签名）、`gd` 扩展（生成状态 PNG）以及 `curl` 扩展（推荐，用于调用 QQ 和 NewAPI）。没有 curl 时脚本会回退到 PHP HTTPS stream。
- 网站必须启用 HTTPS，且 QQ 开放平台可以访问该地址。
- NewAPI 必须配置 `STARRY_API_BOT_SECRET`；PHP 站点配置的值必须逐字相同，长度至少 32 个字符。NewAPI 还需配置 SMTP，邀请码将由 NewAPI 邮件发送。

### 1.2 上传与配置

将 `qqbot/php/index.php` 和 `.htaccess` 上传到一个独立的 PHP 站点目录（例如 `/qqbot/`）。在虚拟主机面板设置以下环境变量：

```env
QQ_BOT_APP_ID=你的QQ机器人AppID
QQ_BOT_APP_SECRET=你的QQ机器人AppSecret
NEW_API_URL=https://newapi.example.com
STARRY_API_BOT_SECRET=与NewAPI完全相同的随机密钥
QQ_BOT_PUBLIC_URL=https://你的机器人域名/qqbot
```

`QQ_BOT_PUBLIC_URL` 是 `/gptstatus` 图片访问端点所在目录的公网 HTTPS URL，必须能被 QQ 平台服务器访问；例如 PHP 文件位于网站 `/qqbot/` 目录，就填写 `https://你的机器人域名/qqbot`。每次 `/gptstatus` 查询都会直接抓取 `https://status.openai.com/` 网页 HTML 源码并解析，然后发送状态图片；不需要 Cron 或自动推送。PHP 状态快照文件仅供最近一次查询的签名图片接口使用，默认保存在系统临时目录；自定义 `GPTSTATUS_STATE_FILE` 时，请放在网站根目录以外且 PHP-FPM 可读写的位置。

如果面板不能设置环境变量，把 `config.php.example` 复制为 `config.php`，填写其中的值；脚本会优先读取环境变量。若面板支持 `QQ_BOT_CONFIG_FILE`，可将它指向网站根目录之外的配置文件。`.htaccess` 已禁止直接下载 `config.php`，但不要把真实密钥提交到 Git 或公开目录。

### 1.3 QQ 回调地址

将 QQ 开放平台的 Webhook 地址设置为 `https://你的机器人域名/qqbot/`（如果上传到网站根目录则使用 `https://你的机器人域名/`），订阅 `C2C_MESSAGE_CREATE` 和 `GROUP_AT_MESSAGE_CREATE` 两个事件。群聊中必须 @机器人；群聊 `/sapi` 会回复帮助，群聊 `/sapi 邀请码 邮箱` 只提示改用私聊，不会发送邀请码。私聊使用 `/sapi 邀请码 邮箱`，邀请码会发到指定邮箱，不会明文回复在 QQ 聊天中。`.htaccess` 会把不存在的路径转发到 `index.php`；若主机不支持 Apache rewrite，可直接填写 `https://你的机器人域名/qqbot/index.php`。

`GET /healthz` 会返回 204，可用于面板健康检查；其他 GET 请求返回 405。QQ 的校验、心跳、签名验证、`/sapi` 和 `/gptstatus` 群聊命令均由 `index.php` 完成。群聊中 @机器人后发送 `/gptstatus` 会抓取 `https://status.openai.com/` 网页 HTML 源码、解析 GPT 状态并发送状态图片；每次指令均为单次查询，不提供自动推送，旧的 `/gptstatus auto on/off` 命令会提示“指令不存在”。图片使用带过期签名的私有接口生成，不会把状态图片或长期访问令牌放在公开目录。私聊发送 `/sapi 邀请码 邮箱` 后，NewAPI 会按现有邮箱验证策略校验地址，并通过管理员配置的 SMTP 发出邀请码；QQ 仅收到发送结果。请求使用短时效 HMAC-SHA256 签名（时间戳、随机 nonce、请求体摘要）调用 NewAPI `/api/bot/registration-invite`，邮箱也包含在签名请求体内；NewAPI 原子拒绝重复 nonce，并保证每个 QQ OpenID 只有一个邀请码。

PHP 文件和示例配置位于 `qqbot/php/`：

- `index.php`：Webhook 主入口；
- `gptstatus-image.php`：生成并提供带短时签名的 PNG 状态图；
- `.htaccess`：Apache 路由和配置文件保护；
- `config.php.example`：无法设置面板环境变量时的配置模板。

若虚拟主机提供 PHP CLI，可在上传前运行协议回归测试：`php qqbot/tests/php_signature_test.php`。它只验证签名规范，不访问 NewAPI 或 QQ。

## 2. 配置 NewAPI

在 NewAPI 的环境变量中配置一个随机共享密钥，并重启 NewAPI：

```env
STARRY_API_BOT_SECRET=在此粘贴至少32字符的随机值
```

例如可在安全环境中运行 `openssl rand -hex 32` 生成。不要把密钥提交到仓库或公开日志。

## 3. 在 Linux 虚拟主机部署

安装 Go 1.25.1 或更高版本、Git 和 CA 证书，然后将 `qqbot/` 目录复制到虚拟主机，或在构建机上编译后复制 Linux 二进制：

```sh
cd qqbot
go test ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o starryapi-qqbot .
```

按虚拟主机架构选择 `GOARCH`（如 ARM64 使用 `GOARCH=arm64`）。将二进制安装到 `/usr/local/bin/starryapi-qqbot`，设置环境文件：

```sh
sudo install -m 0755 starryapi-qqbot /usr/local/bin/starryapi-qqbot
sudo install -m 0600 deploy/qqbot.env.example /etc/starryapi-qqbot.env
sudoedit /etc/starryapi-qqbot.env
```

填写 QQ Bot App ID、App Secret、NewAPI 对外 HTTPS 地址，以及与 NewAPI **完全一致**的共享密钥：

```env
QQ_BOT_APP_ID=你的QQ机器人AppID
QQ_BOT_APP_SECRET=你的QQ机器人AppSecret
NEW_API_URL=https://newapi.example.com
STARRY_API_BOT_SECRET=与NewAPI完全相同的随机密钥
QQ_BOT_PUBLIC_URL=https://你的机器人域名
QQ_BOT_LISTEN_ADDR=127.0.0.1:9000
QQ_BOT_WEBHOOK_PATH=/qqbot
```

启用 systemd 服务：

```sh
sudo install -m 0644 deploy/starryapi-qqbot.service /etc/systemd/system/starryapi-qqbot.service
sudo systemctl daemon-reload
sudo systemctl enable --now starryapi-qqbot
sudo systemctl status starryapi-qqbot
sudo journalctl -u starryapi-qqbot -f
```

服务使用独立的动态用户运行，并自动重启。使用 Nginx/Caddy 等反向代理，将公开 HTTPS 的 `/qqbot` 路径转发至 `127.0.0.1:9000/qqbot`；然后在 QQ Bot 开放平台将 HTTPS 回调地址设为 `https://你的机器人域名/qqbot`，并订阅 `C2C_MESSAGE_CREATE` 与 `GROUP_AT_MESSAGE_CREATE`。群聊中必须 @机器人；`/sapi 邀请码 邮箱` 只在 C2C 私聊中生效。`GET /healthz` 可用于健康检查。

`/gptstatus` 每次都会抓取 `https://status.openai.com/` 网页 HTML 源码并解析状态，然后发送 PNG 状态图；自动推送和订阅功能已移除，旧的 `/gptstatus auto on/off` 命令会提示“指令不存在”。私聊 `/sapi 邀请码 邮箱` 会通过 NewAPI SMTP 发送邀请码，机器人不会在 QQ 消息中回传邀请码明文；邮箱受 NewAPI 邮箱策略限制。图片链接由 Bot 提供并使用短时签名，`QQ_BOT_PUBLIC_URL` 必须填写 QQ 服务器能够访问的 HTTPS origin（不包含路径），反向代理需同时转发 `/qqbot` 与 `/qqbot/gptstatus/image`。

反向代理和虚拟主机需允许 QQ 平台向 webhook 入站访问，并允许 QQ Bot 主机通过 HTTPS 访问 NewAPI。不要把未经 TLS 保护的 webhook 端口直接暴露到公网。

## 4. 单独构建 Docker 镜像（可选）

从仓库根目录构建独立镜像，镜像中不包含 NewAPI：

```sh
docker build -f qqbot/Dockerfile -t starryapi-qqbot:latest .
docker run -d --name starryapi-qqbot --restart unless-stopped \
  --env-file /etc/starryapi-qqbot.env -e QQ_BOT_LISTEN_ADDR=:9000 \
  -p 127.0.0.1:9000:9000 starryapi-qqbot:latest
```

## 5. 环境变量

| 变量 | 必填 | 说明 |
| --- | --- | --- |
| `QQ_BOT_APP_ID` | 是 | QQ Bot 应用 ID。 |
| `QQ_BOT_APP_SECRET` | 是 | QQ Bot 应用密钥，用于 QQ 平台鉴权和 webhook 签名验证。 |
| `NEW_API_URL` | 是 | NewAPI 对外基础地址；远程地址必须使用 HTTPS，本地回环开发可用 HTTP。 |
| `STARRY_API_BOT_SECRET` | 是 | NewAPI 与 QQ Bot 的 HMAC 共享密钥，至少 32 个字符，双方必须完全一致；不要通过 URL 或普通日志传输。 |
| `QQ_BOT_PUBLIC_URL` | 启用 GPT 状态图片时必填 | Bot 图片回调的 HTTPS origin，不包含路径；必须能被 QQ 平台访问。仅回环开发允许 HTTP。 |
| `QQ_BOT_LISTEN_ADDR` | 否 | webhook 监听地址，默认 `:9000`；本机反代时建议 `127.0.0.1:9000`。 |
| `QQ_BOT_WEBHOOK_PATH` | 否 | webhook 路径，默认 `/qqbot`。 |
| `QQ_BOT_TLS_CERT_FILE` | 否 | webhook TLS 证书路径；与 TLS key 一起配置时 Bot 自行提供 HTTPS。 |
| `QQ_BOT_TLS_KEY_FILE` | 否 | webhook TLS 私钥路径。 |

PHP 面板部署还支持以下可选变量：

| 变量 | 说明 |
| --- | --- |
| `QQ_BOT_CONFIG_FILE` | PHP 配置文件的绝对路径；用于面板不能注入环境变量时指定站点根目录外的配置文件。 |
| `QQ_BOT_TOKEN_CACHE_FILE` | PHP QQ access token 缓存文件路径；默认写入系统临时目录，文件权限为 0600。 |
| `QQ_BOT_PUBLIC_URL` | PHP 状态图片端点所在目录的公开 HTTPS URL；例如 `https://bot.example.com/qqbot`。使用 `/gptstatus` 时需要配置。 |
| `GPTSTATUS_STATE_FILE` | PHP 最近一次状态查询快照及图片签名数据文件的绝对路径；默认位于按 QQ App ID 隔离的系统临时目录。应放在网站公开目录之外。 |
| `GPTSTATUS_CACHE_DIR` | `GPTSTATUS_STATE_FILE` 未设置时使用的私有缓存目录；默认位于系统临时目录。 |
| `GPTSTATUS_IMAGE_URL` | 可选的自定义状态图 PHP 端点完整 URL；默认自动由 `QQ_BOT_PUBLIC_URL` 加 `/gptstatus-image.php`。 |

Go 版和 PHP 版的 `/gptstatus` 都会在收到指令时直接抓取 `https://status.openai.com/` 网页 HTML 源码并解析，然后发送状态图片；两种版本均无自动推送、订阅或 Cron 轮询。PHP 版需要 GD 扩展；Go 版不需要额外状态文件配置。

`STARRY_API_BOT_SECRET` 不等于 QQ App Secret；使用随机生成的专用值。Bot 请求 `/api/bot/registration-invite` 时发送签名 JSON `{ "open_id": "...", "email": "..." }`，NewAPI 仅返回邮件发送结果，不返回邀请码；同一 QQ OpenID 每分钟最多发送一次，发送失败会释放冷却以便重试。请求携带以下请求头：

- `X-QQ-Bot-Timestamp`：Unix 秒时间戳，服务端只接受前后 5 分钟；
- `X-QQ-Bot-Nonce`：16 字节随机值的 32 位小写十六进制字符串，每个值只能使用一次；
- `X-QQ-Bot-Signature`：HMAC-SHA256 十六进制值。

签名原文为 `POST\n/api/bot/registration-invite\n时间戳\nnonce\n小写十六进制的SHA256(原始请求体)`，再用 `STARRY_API_BOT_SECRET` 计算 HMAC-SHA256。服务端使用常量时间比较签名，并优先用 Redis `SET NX` 做跨实例防重放；未启用 Redis 时使用有界进程内缓存（多实例部署应启用 Redis）。接口还保留 NewAPI 的关键接口限流；每个 QQ OpenID 只能领取一个邀请码。请保持系统时钟同步，并只通过 HTTPS 调用 NewAPI。
