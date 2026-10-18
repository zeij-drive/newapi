# StarryAPI QQ bot

This standalone Go service receives QQ Bot webhook events, responds to `/sapi` with help, and lets each QQ open ID claim its own registration invitation with `/sapi 邀请码`.

## Prerequisites

- Go 1.25.1 or later.
- A QQ bot application with its App ID and App Secret.
- A publicly reachable HTTPS webhook URL, provided by a reverse proxy or by the bot's optional built-in TLS listener.
- `STARRY_API_BOT_SECRET`: the same randomly generated secret (at least 32 characters) configured in both the NewAPI process and this bot.

## NewAPI setup

1. Start NewAPI and open **System Settings → Authentication → Registration invitations**.
2. Create invitation codes. Each code can register one account.
3. Enable **Require registration invitation** in **Basic Authentication** if all new accounts must provide a code.
4. Set `STARRY_API_BOT_SECRET` in the NewAPI service environment and restart NewAPI. Generate a separate random value for this integration; do not use the QQ App Secret.

NewAPI encrypts recoverable invitation codes using its persisted configuration-encryption key. QQ open IDs are stored as keyed digests, and each QQ open ID is assigned no more than one invitation.

## Configure the QQ bot

Set these environment variables in the bot service:

| Variable | Required | Description |
| --- | --- | --- |
| `QQ_BOT_APP_ID` | Yes | QQ Bot application ID. |
| `QQ_BOT_APP_SECRET` | Yes | QQ Bot application Secret. |
| `NEW_API_URL` | Yes | NewAPI base URL. HTTPS is required except for loopback development. |
| `STARRY_API_BOT_SECRET` | Yes | The same 32-or-more-character secret configured for NewAPI. |
| `QQ_BOT_LISTEN_ADDR` | No | HTTP listen address; defaults to `:9000`. |
| `QQ_BOT_WEBHOOK_PATH` | No | Webhook path; defaults to `/qqbot`. |
| `QQ_BOT_TLS_CERT_FILE` | No | PEM certificate path. Configure together with `QQ_BOT_TLS_KEY_FILE` for direct HTTPS. |
| `QQ_BOT_TLS_KEY_FILE` | No | PEM private-key path for the direct HTTPS listener. |

Build and run from this directory:

```powershell
go test ./...
go build -o starryapi-qqbot.exe .
./starryapi-qqbot.exe
```

In the QQ Bot developer console, register the public HTTPS URL ending in `/qqbot` as the event callback and subscribe to `C2C_MESSAGE_CREATE`. With the TLS variables configured, the bot serves HTTPS directly. Without them, put a reverse proxy in front of the HTTP listener; the proxy must preserve the request body and QQ signature headers. Restrict direct access to the bot listener when using a reverse proxy. The bot calls NewAPI at `/api/bot/registration-invite` over HTTPS using the shared integration secret.

`GET /healthz` returns HTTP 204 for local health checks. The service validates QQ callback signatures and performs the official callback handshake through Tencent's `botgo` SDK.
