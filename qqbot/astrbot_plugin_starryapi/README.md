# StarryAPI AstrBot 插件

这个目录是原 `qqbot/` 服务的 AstrBot 插件重写版。把整个
`astrbot_plugin_starryapi` 目录复制到 `AstrBot/data/plugins/`，由 AstrBot
负责 QQ 官方机器人连接、事件验签和消息发送；不再运行 `qqbot/main.go`、PHP
入口或单独的 QQ Bot 守护进程。

## 功能

- `/sapi`：显示帮助菜单。
- `/sapi 邀请码 邮箱`：私聊或群聊获取邀请码，NewAPI 将邀请码邮件发送到该邮箱，QQ 消息不会回显邀请码。
- `/gptstatus`：无需插件密钥或额外设置，直接读取 OpenAI 状态页并生成状态图片；普通用户每 3 分钟可成功查询一次，AstrBot 管理员不受限制。`auto on/off` 已移除并返回“指令不存在”。

## 安装

```text
AstrBot/
└── data/
    └── plugins/
        └── astrbot_plugin_starryapi/
            ├── __init__.py
            ├── main.py
            ├── core.py
            ├── metadata.yaml
            ├── _conf_schema.json
            └── requirements.txt
```

在 AstrBot 管理面板重载插件，或重启 AstrBot。插件依赖 `aiohttp`，AstrBot 通常已经安装；如果没有，插件管理器会按 `requirements.txt` 安装。

AstrBot 中还要先配置 QQ 官方适配器（WebSocket 或 Webhook）。插件不再读取 QQ App ID/App Secret，也不再创建 QQ 平台连接。

## 配置

在插件配置面板填写：

- `new_api_url`：NewAPI 的 HTTPS 根地址，例如 `https://newapi.example.com`；本机开发可以填 `http://127.0.0.1:3000`。
- `starry_api_bot_secret`：与 NewAPI 环境变量 `STARRY_API_BOT_SECRET` 完全一致的随机值，至少 32 个字符。这个值只放在配置中，插件不会写日志或回复消息。

NewAPI 环境变量示例：

```env
STARRY_API_BOT_SECRET=与AstrBot插件完全一致的至少32字符随机值
```

共享密钥用于对邀请码请求计算 HMAC-SHA256；NewAPI 校验签名、时间戳和 nonce，防止伪造和重放请求。

## 消息平台说明

插件的消息处理由 AstrBot 完成，因此需要先在 AstrBot 启用 QQ 官方平台并订阅消息事件。

## 迁移旧服务

停止旧的 Go/PHP QQ Bot 后，再启用这个插件。旧文件保留在仓库的 `qqbot/` 目录中作为兼容参考；运行时只应部署本目录到 AstrBot 插件目录，避免两个服务同时处理同一 QQ 回调。

纯函数测试不需要安装 AstrBot：

```bash
python -m unittest discover -s qqbot/astrbot_plugin_starryapi/tests -p 'test_*.py'
python -m py_compile qqbot/astrbot_plugin_starryapi/core.py qqbot/astrbot_plugin_starryapi/main.py
```
