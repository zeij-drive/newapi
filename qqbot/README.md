# StarryAPI QQ Bot（AstrBot 插件）

QQ Bot 已重写为 `qqbot/astrbot_plugin_starryapi/` AstrBot 插件。部署时把该目录复制到 `AstrBot/data/plugins/`，由 AstrBot 的 QQ 官方适配器负责 QQ 连接、Webhook/WebSocket、平台签名和消息发送；不再启动 `qqbot/main.go`、PHP 入口或独立 QQ Bot 守护进程。

## 快速部署

```text
AstrBot/data/plugins/astrbot_plugin_starryapi/
├── __init__.py
├── main.py
├── core.py
├── metadata.yaml
├── _conf_schema.json
└── requirements.txt
```

1. 在 AstrBot 先配置 QQ 官方机器人适配器（WebSocket 或 Webhook）。
2. 上传 `qqbot/astrbot_plugin_starryapi/` 到 AstrBot 的 `data/plugins/`。
3. 在插件配置页填写 `new_api_url` 和 `starry_api_bot_secret`，重载插件。
4. 在 NewAPI 设置同一个 `STARRY_API_BOT_SECRET`，供邀请码请求进行 HMAC 验签。

## 配置

- `new_api_url`：NewAPI 根地址。远程地址使用 HTTPS；本机回环开发可用 `http://127.0.0.1:3000`。
- `starry_api_bot_secret`：与 NewAPI 的 `STARRY_API_BOT_SECRET` 完全一致，至少 32 个字符。配置面板会遮罩该值，插件不会回显或写日志。

NewAPI 示例：

```env
STARRY_API_BOT_SECRET=与AstrBot插件完全一致的至少32字符随机值
```

邀请码请求使用共享密钥计算 HMAC-SHA256，并由 NewAPI 校验时间戳和 nonce，阻止伪造和重放。

## 指令

- `/sapi`：帮助菜单。
- `/sapi 邀请码 邮箱`：私聊或群聊请求邀请码邮件。邀请码不会出现在 QQ 消息中。
- `/gptstatus`：群聊读取 OpenAI 状态页 HTML 源码并生成状态图片，无需额外配置；普通用户每 3 分钟可查询一次，管理员不受限制。
- `/gptstatus auto on/off`：返回“指令不存在”，自动推送已移除。

## 验证

插件纯函数测试和语法检查不依赖 AstrBot 本体：

```bash
python -m unittest discover -s qqbot/astrbot_plugin_starryapi/tests -p 'test_*.py'
python -m py_compile qqbot/astrbot_plugin_starryapi/core.py qqbot/astrbot_plugin_starryapi/main.py
```

旧 Go/PHP 文件暂时保留在 `qqbot/` 目录作为迁移参考；运行时只部署 AstrBot 插件，避免旧服务和 AstrBot 同时消费同一 QQ 事件。
