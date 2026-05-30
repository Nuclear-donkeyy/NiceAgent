# NiceAgent API

本文记录当前外部 API 和内部服务 API。接口路径和 JSON 字段保持英文，说明文字使用中文。

## 外部 API

`POST /api/chats`

创建聊天会话。

`GET /api/chats`

获取当前用户的活跃聊天会话列表。

`GET /api/chats/{chat_id}`

获取一个聊天会话及其消息。

`POST /api/chats/{chat_id}/messages`

创建用户消息并创建/排队一个 run。

请求体：

```json
{ "content": "Explain the current workspace" }
```

`GET /api/runs/{run_id}`

获取 run 状态。

`GET /api/runs/{run_id}/events`

订阅 run 的 Server-Sent Events。使用 `?after={seq}` 可以重放断线期间错过的事件。

`POST /api/runs/{run_id}/cancel`

取消一个 run。

`GET /api/skills`

列出当前用户可用 skills。

`POST /api/skills/{skill_id}/approve`

批准一次敏感 skill 调用。

## 内部 API

`POST /internal/runs/execute`

Agent Runtime 执行 `RunRequest` 的入口。

`POST /internal/sandbox/exec`

Sandbox Executor 在策略约束下执行命令的入口。

## 事件约定

`RunEvent` 是前端展示、审计和恢复的统一事件协议。事件需要保持递增 `seq`，并允许前端通过 `after` 参数补齐断线期间的事件。

常见事件包括：

- run 状态变化，例如 queued、running、succeeded、failed、canceled。
- 模型流式 token。
- tool/skill 调用开始、输出、完成或失败。
- CLI stdout/stderr 摘要。
- artifact 或文件变更提示。

## 兼容性要求

- 下一阶段不得破坏上述外部 API 路径。
- 新增字段应尽量保持向后兼容，旧前端忽略未知字段也应能继续运行。
- `canceled`、`failed`、`succeeded` 等终态不应被后台迟到事件覆盖。
