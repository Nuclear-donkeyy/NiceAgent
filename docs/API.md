# NiceAgent API

本文记录当前外部 API 和内部服务 API。接口路径和 JSON 字段保持英文，说明文字使用中文。

## 外部 API

`POST /api/chats`

创建聊天会话。

`GET /api/chats`

获取当前用户的聊天会话列表。默认只返回未归档会话。

查询参数：

- `q`：按会话标题搜索，大小写不敏感。
- `include_archived=true`：同时返回已归档会话。

`GET /api/chats/{chat_id}`

获取一个聊天会话及其消息。

`POST /api/chats/{chat_id}/archive`

归档一个会话。归档不会删除消息、run 或 events；归档会话默认不出现在 `GET /api/chats` 结果中。

`POST /api/chats/{chat_id}/restore`

恢复一个已归档会话。

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

列出当前用户在当前项目可用的 skills。响应同时包含兼容旧前端的扁平 `skills` 和分组后的 `groups.system`、`groups.user`。

```json
{
  "skills": [],
  "groups": {
    "system": [],
    "user": []
  }
}
```

系统级 CLI 也通过这个列表下发给 Agent Runtime，但不需要用户逐次授权。

`POST /api/skills/http`

创建当前用户的 HTTP Skill，并自动 grant 到当前项目。`bearer_token` 只进入后端 secret 存储，不会出现在后续前端 API 响应。

```json
{
  "name": "Weather API",
  "description": "Fetch weather information",
  "method": "POST",
  "url": "https://example.com/weather",
  "auth_type": "bearer",
  "bearer_token": "secret"
}
```

`PATCH /api/skills/{skill_id}`

更新当前用户拥有的 HTTP Skill。系统固定 skill 不允许通过该接口修改。

`POST /api/skills/{skill_id}/enable`

启用当前用户拥有的 HTTP Skill。

`POST /api/skills/{skill_id}/disable`

停用当前用户拥有的 HTTP Skill。

`POST /api/skills/{skill_id}/approve`

已废弃的占位接口。当前前端不调用它；未来如果某些非 CLI skill 需要用户确认，可在此基础上扩展 run-specific approval API。

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
    "skills": [
      {
        "skill": {
          "id": "cli.exec",
          "scope": "system",
          "kind": "builtin"
        }
      }
    ],
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

当命令触发系统 CLI 策略拒绝时，`SandboxResult` 会包含：

```json
{
  "approval_required": false,
  "reason": "command is blocked by the system CLI read-only policy",
  "policy": "dangerous_command",
  "command": ["rm", "-rf", "/"]
}
```

## Skill 存储与模型输出

Skill 元数据以 `skills` 和 `skill_versions` 为权威，`skill_grants` 表示用户/项目可用性，`skill_secrets` 只保存 secret 引用或本地开发密文。`input_schema`、`output_schema`、`annotations` 和 `runtime_config` 使用 JSON/JSONB；`annotations` 采用 MCP 风格字段，例如 `readOnlyHint`、`destructiveHint`、`idempotentHint`、`openWorldHint`。

Agent Runtime 可以使用 mock provider 或 OpenAI-compatible provider。当前主执行路径通过 Eino ADK `ChatModelAgent + Runner` 运行 agentic loop；模型输出仍通过 `model.token` 类型的 `RunEvent` 写回 Control Plane，并由前端折叠成 assistant 消息。

OpenAI-compatible provider 使用 `/v1/chat/completions` 的 streaming 协议；该能力不改变外部 Web API 和 `RunExecutionRequest`。

## 事件约定

`RunEvent` 是审计、恢复、排障和前端状态折叠的统一事件协议。事件需要保持递增 `seq`，并允许前端通过 `after` 参数补齐断线期间的事件。产品界面默认不直接展示原始事件列表。

常见事件包括：

- run 状态变化，例如 queued、running、succeeded、failed、canceled。
- 模型流式 token。
- tool/skill 调用开始、输出、完成或失败。
- `approval.needed`：保留给未来真正需要用户确认的非 CLI skill。系统 CLI 不使用该事件。
- CLI stdout/stderr 摘要，前端默认折叠为 agent 状态和 assistant 回复。
- artifact 或文件变更提示。

## 兼容性要求

- 下一阶段不得破坏上述外部 API 路径。
- 新增字段应尽量保持向后兼容，旧前端忽略未知字段也应能继续运行。
- `canceled`、`failed`、`succeeded` 等终态不应被后台迟到事件覆盖。
