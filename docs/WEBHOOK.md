# 收到短信时通过 Webhook 转发

设置 `DJONEHUB_WEBHOOK_URL` 后，DJOneHub 会将读取到的短信以 HTTP POST、JSON 格式发送到指定地址。地址留空时关闭转发。支持 USB AT 和原生串口接收路径；演示模式不转发。

## Docker 配置

在项目目录执行：

```sh
cp .env.example .env
```

编辑 `.env`：

```dotenv
DJONEHUB_WEBHOOK_URL=https://your-server.example/sms
DJONEHUB_WEBHOOK_TOKEN=
```

然后执行 `make up`。修改地址或认证令牌后，也需要重新执行 `make up`。`.env` 已加入 Git 和 Docker 构建的忽略列表。

如果接收服务运行在这台 Mac 上，例如监听 8080 端口，容器应使用 `http://host.docker.internal:8080/sms`，而不是 `127.0.0.1`。

原生 macOS 版使用相同的环境变量，在启动程序前设置并导出：

```sh
export DJONEHUB_WEBHOOK_URL=https://your-server.example/sms
export DJONEHUB_WEBHOOK_TOKEN=your-token
djonehub start
```

`DJONEHUB_WEBHOOK_TOKEN` 可选。填写后，请求会携带 `Authorization: Bearer your-token`。短信正文、验证码、令牌和完整目标地址不会写入 Webhook 日志或返回给状态接口。

## 请求格式

请求头：

```text
Content-Type: application/json
X-DJOneHub-Event-ID: <与请求体 id 相同的事件 ID>
Authorization: Bearer <可选令牌>
```

请求体示例：

```json
{
  "id": "<短信事件的稳定 ID>",
  "event": "sms.received",
  "received_at": "2026-10-08T10:00:01Z",
  "sms": {
    "sender": "test-sender",
    "content": "Test verification code: 482913",
    "code": "482913",
    "timestamp": "2026-10-08T10:00:00Z"
  }
}
```

`timestamp` 是模块提供的短信时间，`received_at` 是程序首次将短信加入转发队列的时间。没有识别到验证码时，省略 `code`。长短信在拼接完成后转发；PDU 解析失败的诊断记录不转发。

## 接收、重试和去重

- 接收服务返回任意 `2xx` 状态码即视为成功。
- 请求超时为 10 秒。不跟随 HTTP 重定向。
- 超时或非 `2xx` 响应会自动重试，间隔从 2 秒逐步增加，最多 5 分钟，持续保留待转发记录。
- 转发在后台执行，等待网络响应不会阻塞短信列表或 USB 轮询。
- 待转发记录和成功事件 ID 保存在本地；成功后删除队列中的短信正文，只保留事件 ID。Docker 中使用现有数据卷的 `/data/DJOneHub/sms-webhook.json`；原生 macOS 中使用用户配置目录下的 `DJOneHub/sms-webhook.json`。
- 重启或重建容器后继续未完成的转发，同一短信不会因重复轮询再次加入队列。更换目标地址后，待转发短信发往新地址，已经成功的短信不会自动重放。

接收服务应按 `id` 去重：如果接收服务已经处理短信，但响应丢失，或本机未能保存成功状态，程序会用相同 ID 重试。这是“至少一次”投递，不能保证 HTTP 请求只发生一次。

首次启用时，模块中已有、尚无成功转发记录的短信也会转发。关闭 Webhook 后保留本地记录，重新启用会继续处理。删除 Docker 数据卷会同时删除去重记录。

短信页面会显示 Webhook 的待转发数量和最近错误。也可以访问 `GET /api/sms/status` 查看 `webhook` 字段中的 `enabled`、`pending`、`delivered`、`last_success` 和 `last_error`，或运行 `make logs` 查看失败重试摘要。

这是通用 JSON Webhook，不直接套用企业微信、钉钉或飞书机器人的专用消息格式；这些平台需要接收端将 JSON 转换为对应格式。
