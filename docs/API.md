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

Agent Runtime 执行 `RunExecutionRequest` 的入口，由 Control Plane 的 HTTP dispatcher 调用。

请求体：

```json
{
  "request": {
    "run_id": "run_xxx",
    "chat_id": "chat_xxx",
    "user_id": "demo-user",
    "workspace_id": "ws_xxx",
    "skill_ids": ["workspace.read", "cli.exec"],
    "model_policy": "mock-default"
  },
  "user_message": "/cli echo hello",
  "control_plane_url": "http://127.0.0.1:8080"
}
```

`POST /internal/runs/{run_id}/events`

Agent Runtime 向 Control Plane 写入单条 `RunEvent`。当任一服务配置了 `INTERNAL_API_TOKEN` 时，对应内部 API 需要请求头 `Authorization: Bearer <token>`。

请求体：

```json
{
  "type": "model.token",
  "message": "hello",
  "payload": null
}
```

`POST /internal/runs/{run_id}/complete`

Agent Runtime 通知 Control Plane 写入最终 assistant 消息，并将 run 置为 `succeeded`。如果 run 已经是 `canceled`、`failed` 或 `succeeded`，Control Plane 不会覆盖终态。

请求体：

```json
{ "content": "最终回复内容" }
```

`POST /internal/runs/{run_id}/fail`

Agent Runtime 或 dispatcher 通知 Control Plane 将 run 置为 `failed`，并写入 `run.failed` 事件。终态 run 不会被覆盖。

请求体：

```json
{ "error": "runtime unavailable" }
```

`GET /internal/runs/{run_id}/status`

Agent Runtime 查询 run 状态，用于识别用户取消。

`POST /internal/sandbox/exec`

Sandbox Executor 在策略约束下执行命令的入口，由 Agent Runtime 的 HTTP sandbox executor 调用。

请求体复用 `SandboxCommand`，响应体复用 `SandboxResult`。

当命令触发审批策略时，`SandboxResult` 会包含：

```json
{
  "approval_required": true,
  "reason": "command requires explicit approval",
  "policy": "dangerous_command",
  "command": ["rm", "-rf", "/"]
}
```

## 模型输出

Agent Runtime 可以使用 mock provider 或 OpenAI-compatible provider。无论 provider 类型如何，模型流式内容都通过 `model.token` 类型的 `RunEvent` 写回 Control Plane，并由前端 SSE 展示。

OpenAI-compatible provider 使用 `/v1/chat/completions` 的 streaming 协议；该能力不改变外部 Web API 和 `RunExecutionRequest`。

## 事件约定

`RunEvent` 是前端展示、审计和恢复的统一事件协议。事件需要保持递增 `seq`，并允许前端通过 `after` 参数补齐断线期间的事件。

常见事件包括：

- run 状态变化，例如 queued、running、succeeded、failed、canceled。
- 模型流式 token。
- tool/skill 调用开始、输出、完成或失败。
- `approval.needed`：高风险 skill 需要用户审批。CLI 场景 payload 包含 `skill_id`、`command`、`reason`、`policy`、`workspace_id`，Control Plane 会将 run 状态置为 `waiting_for_approval`。
- CLI stdout/stderr 摘要。
- artifact 或文件变更提示。

## 兼容性要求

- 下一阶段不得破坏上述外部 API 路径。
- 新增字段应尽量保持向后兼容，旧前端忽略未知字段也应能继续运行。
- `canceled`、`failed`、`succeeded` 等终态不应被后台迟到事件覆盖。
