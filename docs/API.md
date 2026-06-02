# NiceAgent API

本文记录当前外部 API 和内部服务 API。接口路径和 JSON 字段保持英文，说明文字使用中文。

## 外部 API

### 认证与请求标识

Control Plane 支持 `AUTH_MODE=demo|trusted-header|oidc`：

- `demo`：默认模式，所有外部 API 映射到 `demo-user/demo-project`，用于本地开发和演示。
- `trusted-header`：要求上游网关已完成 OIDC/session/JWT 校验，并在外部 API 请求中传入 `X-NiceAgent-User-ID` 和 `X-NiceAgent-Project-ID`。可选传入 `X-NiceAgent-Org-ID`、`X-NiceAgent-Roles`、`X-NiceAgent-User-Email`、`X-NiceAgent-User-Name`、`X-NiceAgent-Identity-Provider`、`X-NiceAgent-Identity-Issuer`、`X-NiceAgent-Identity-Subject`。缺少 actor header 时返回 401；缺少 roles 时，普通项目 API 会从持久 `project_members` 读取角色，组织成员 API 会从持久 `organization_members` 读取角色，仍找不到成员关系则返回 403。如果请求携带 identity issuer/subject，Control Plane 会把该外部身份绑定到内部 `user_id`；同一个外部身份不能绑定到多个用户，同一个用户同一 provider 也不能换绑到另一个外部身份。接受邀请时必须有可信邮箱 header，且邮箱必须匹配邀请邮箱。
- `oidc`：作为 API resource server 校验 `Authorization: Bearer <jwt>`，支持 RS256、JWKS、issuer、audience、`exp`、`nbf` 校验，并把 JWT claims 映射为 `ActorContext`。该模式不读取 trusted header；同时提供最小浏览器 OIDC authorization code flow，支持 login callback、HttpOnly session cookie、refresh token 刷新、服务端 session 撤销和 logout。

OIDC 浏览器登录接口：

- `GET /auth/oidc/login`：生成 state cookie，并跳转到 `OIDC_AUTH_URL`。
- `GET /auth/oidc/callback?code=...&state=...`：校验 state，向 `OIDC_TOKEN_URL` 交换 token，校验 `id_token`，写入 `niceagent_session` HttpOnly cookie 和前端可读的 `niceagent_csrf` cookie，然后跳转到 `/`。
- `POST /auth/oidc/refresh`：使用 session 中的 refresh token 换取新 token，并刷新 session cookie。请求必须携带 `X-NiceAgent-CSRF`，值与 `niceagent_csrf` cookie 一致；刷新成功后会撤销旧 `session_id` 并签发新的 browser session。
- `POST /auth/logout`：撤销当前 OIDC browser session，并清理 OIDC session、state 和 CSRF cookie。如果请求带有有效 session，也必须携带 `X-NiceAgent-CSRF`。

启用浏览器登录时需要配置 `OIDC_CLIENT_ID`、`OIDC_AUTH_URL`、`OIDC_TOKEN_URL`、`OIDC_SESSION_SECRET`；`OIDC_CLIENT_SECRET`、`OIDC_REDIRECT_URL` 和 `OIDC_SESSION_TTL_SECONDS` 可按 IdP 和部署环境配置。前端调用会话刷新和退出时会自动从 `niceagent_csrf` cookie 读取 token，并写入 `X-NiceAgent-CSRF` header。Control Plane 会把 browser session 写入 `oidc_browser_sessions`，外部 API 读取 cookie session 时会校验该 session 未撤销且未过期。

`X-NiceAgent-Roles` 的最小 RBAC 语义：`owner`、`admin`、`member`、`editor`、`writer` 可以执行普通写操作；`viewer` 只能读。Control Plane 默认 action-level policy 会把 `chat.create`、`message.create`、`skill.create` 等 action 映射到 write requirement，把 `project.quota.update`、`project.member.upsert` 等 action 映射到 project admin requirement，把 `organization.member.upsert`、`invitation.create`、`invitation.email_suppression.delete` 等 action 映射到 organization admin requirement。网关没有传 roles 时，Control Plane 优先使用 `project_members` 的持久项目角色；组织成员 API 会读取 `organization_members`；如果项目属于当前 actor 的组织，项目 API 也可以继承 `organization_members` 中的组织角色。部署侧可通过 `ACTION_POLICY_FILE` 指向 JSON 文件覆盖或新增 action requirement，格式为 `{"actions":{"action.name":"write|project_admin|organization_admin"}}`；非法 requirement 会导致 Control Plane 启动失败。数据库策略源或 OPA/Casbin 仍属于后续扩展。

所有 Control Plane 请求都会返回 `X-Request-ID`。如果请求头已提供合法 `X-Request-ID`，服务会透传；否则服务会生成一个新的 request id。request log 和 audit event 会记录同一个 request id，便于串联排障。

三服务都会返回 `X-Trace-ID`。如果请求带 `X-Trace-ID` 或标准 `Traceparent`，服务会复用其中的 trace id；否则生成新的 trace id。Control Plane 调 Agent Runtime、Agent Runtime 回写 Control Plane、Agent Runtime 调 Sandbox Executor 时会继续透传 `X-Trace-ID` 和标准 `traceparent`。默认 `OTEL_TRACES_EXPORTER=none`；配置 `OTEL_TRACES_EXPORTER=otlp` 后，三服务会初始化 OpenTelemetry tracer provider，并通过 OTLP HTTP exporter 上报 spans。当前 OTel 覆盖 HTTP 服务入口、Control Plane 调度/回写、Redis run queue、Redis 命令、Postgres repository 命令、Runtime run/tool/model、HTTP Skill、Sandbox HTTP/exec 和标准 trace context 传播。

三服务都提供 `GET /metrics`，返回 Prometheus text exposition 风格的基础指标，包括 HTTP 请求总数/耗时，以及部分领域计数，例如 run 创建、quota deny、runtime run 结果和 sandbox exec 退出码。`/metrics` 当前不改变外部业务 API；生产部署时应通过网关或内网策略限制访问。

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

如果项目级 quota policy 或 env fallback 配置了 `max_concurrent_runs`、`max_runs_per_hour`、`max_model_tokens_per_day`、`max_tool_calls_per_day` 或 `max_sandbox_seconds_per_day`，超过当前 actor 在当前项目下的 run 配额时返回 `429 Too Many Requests`。创建 run 时，`max_model_tokens_per_day` 可按 fixed/dynamic 模式做模型 token 预占；`max_tool_calls_per_day` 和 `max_sandbox_seconds_per_day` 会先基于 UTC 自然日内已有 `RunUsage` 做创建前保护，后续 Runtime 每次 tool 调用前还会通过内部 quota reserve 做最小实时预占。响应体仍使用统一错误结构：

```json
{ "error": "已达到当前项目并发任务上限，请稍后再试。" }
```

`GET /api/runs/{run_id}`

获取 run 状态。

`GET /api/runs/{run_id}/events`

订阅 run 的 Server-Sent Events。服务端会把事件 `seq` 写为 SSE `id`，并按 `?after={seq}` 或请求头 `Last-Event-ID` 重放断线期间错过的事件；`?after` 优先级高于 `Last-Event-ID`。前端应记录每个 run 已处理的最大 `seq`，重连时带 `after`，并在应用事件前丢弃 `seq <= lastSeq` 的重复事件。

多 Control Plane 副本下，`EVENT_FANOUT_MODE=redis` 只用于实时唤醒当前副本上的 SSE 连接；事件正文仍从 repository/Postgres replay 读取。因此客户端仍必须实现 `after` 和去重，不能把实时连接视为唯一可靠来源。

`GET /api/runs/{run_id}/artifacts`

列出当前用户可访问 run 下已登记的 artifact metadata。响应体：

```json
{
  "artifacts": [
    {
      "id": "art_xxx",
      "run_id": "run_xxx",
      "workspace_id": "ws_xxx",
      "path": "output/report.txt",
      "name": "report.txt",
      "mime_type": "text/plain",
      "size_bytes": 128,
      "expires_at": "2026-06-09T12:00:00Z"
    }
  ]
}
```

如果 artifact 已被标记删除，或 `expires_at` 已过期，列表、详情、下载和内容读取接口都会按不可见处理并返回空列表或 `404`。

`GET /api/artifacts/{artifact_id}`

获取单个 artifact metadata。

`GET /api/artifacts/{artifact_id}/download`

下载 artifact 文件。当前只支持 `storage_backend=local` 的最小闭环；服务会按当前用户、run workspace、`output/` 相对路径和 symlink 解析结果做越界检查。默认响应使用 `Content-Disposition: attachment`，用于下载；前端预览可显式传 `?disposition=inline`，让浏览器以内联方式渲染图片、PDF、音频和视频等安全预览。

`GET /api/artifacts/{artifact_id}/content`

读取已登记文本 artifact 的内容摘要，用于前端表格/文本类轻量预览。查询参数：

- `max_bytes`：可选，默认 `32768`，最大 `131072`。

该接口和下载接口使用相同的 actor/run/artifact 权限校验，并复用 workspace root、`output/` 路径限制、symlink escape 检查、regular file 检查、MIME 文本限制和读取大小限制。二进制 artifact、未登记文件、`../`、绝对路径和 symlink escape 会被拒绝。

响应体：

```json
{
  "artifact": {
    "id": "art_xxx",
    "path": "output/report.csv",
    "mime_type": "text/csv"
  },
  "content": "name,value\nfoo,1\n",
  "truncated": false,
  "bytes_read": 17
}
```

前端会对 `image/*`、`application/pdf`、`audio/*` 和 `video/*` artifact 使用 inline URL 渲染预览；对 `text/csv`、`text/tab-separated-values`、`.csv` 和 `.tsv` 使用 `content` API 渲染前几行表格预览；其他类型仍以下载为主。

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

创建当前用户的 HTTP Skill，并自动 grant 到当前项目。`bearer_token` 只进入后端 secret 存储，不会出现在后续前端 API 响应。生产环境建议优先传 `bearer_token_secret_ref`，当前 Runtime 支持 `env://ENV_NAME` 和 `file:///absolute/path` 两种本地 resolver 形式；`file://` 路径必须位于 `NICEAGENT_SECRET_FILE_ROOTS` 配置的目录下。

```json
{
  "name": "Weather API",
  "description": "Fetch weather information",
  "method": "POST",
  "url": "https://example.com/weather",
  "timeout_seconds": 15,
  "retry_max_attempts": 3,
  "rate_limit_per_minute": 60,
  "auth_type": "bearer",
  "bearer_token_secret_ref": "env://WEATHER_TOKEN"
}
```

`retry_max_attempts` 可选，默认 1，最大 5；Runtime 只会对 HTTP Skill 的 429、5xx 和网络/超时类错误重试。`rate_limit_per_minute` 可选，默认 0 表示不启用，最大 600；当前是 Agent Runtime 进程内的 per-skill 最小限流，不是跨副本强一致配额。

`POST /api/skills/import/openapi/preview`

预览 OpenAPI JSON/YAML 文档中可转换为 HTTP Skill 的 operation。该接口只做 dry-run，不创建 skill、不保存 secret、不修改 grant。当前只转换 `GET` 和 `POST` operation，并要求最终 base URL 为 `https`。

请求体：

```json
{
  "document": "{\"openapi\":\"3.1.0\",\"servers\":[{\"url\":\"https://api.example.com\"}],\"paths\":{}}",
  "base_url": "https://api.example.com"
}
```

`base_url` 可选；如果不传，则使用 OpenAPI `servers[0].url`。响应体：

```json
{
  "candidates": [
    {
      "name": "getWeather",
      "description": "Get weather",
      "method": "POST",
      "url": "https://api.example.com/weather",
      "path": "/weather",
      "operation_id": "getWeather",
      "input_schema": "{\"type\":\"object\"}",
      "output_schema": "{\"type\":\"object\"}",
      "auth_type": "bearer",
      "requires_secret": true,
      "security_scheme": "bearerAuth"
    }
  ]
}
```

如果 OpenAPI security scheme 不是当前 HTTP Skill 支持的 bearer 类型，响应会标记 `unsupported_auth=true`，由后续导入 UI 引导用户重新配置鉴权。

`POST /api/skills/import/mcp/preview`

预览 MCP `tools/list` 结果或等价 manifest 中可转换为 NiceAgent skill manifest 的 tool。该接口只做 dry-run，不创建 skill、不保存 secret、不修改 grant。当前支持两种 JSON 形态：

- `{"tools":[...]}`
- `{"result":{"tools":[...]}}`

请求体：

```json
{
  "document": "{\"tools\":[{\"name\":\"weather.lookup\",\"inputSchema\":{\"type\":\"object\"}}]}"
}
```

响应体：

```json
{
  "candidates": [
    {
      "name": "weather.lookup",
      "description": "Look up weather.",
      "input_schema": "{\"type\":\"object\"}",
      "output_schema": "{\"type\":\"object\"}",
      "annotations": "{\"readOnlyHint\":true}",
      "read_only_hint": true,
      "open_world_hint": true
    }
  ]
}
```

MCP annotations 只作为模型提示和 UI 提示，不作为安全边界。

`POST /api/skills/import/mcp`

把预览中的某个 MCP tool 保存为当前用户/项目下的 MCP Skill。请求必须携带同一份 MCP `tools/list` JSON，并用 `tool_name` 选择 tool。当前实现的是最小 HTTP JSON-RPC adapter：Runtime 调用时会向 `server_url` 发送 `{"jsonrpc":"2.0","method":"tools/call"}`，并把模型传入的 tool arguments 放到 `params.arguments`。该路径不实现 MCP stdio transport、SSE transport、initialize/session negotiation 或 `tools/list_changed` 动态通知。

```json
{
  "document": "{\"tools\":[{\"name\":\"weather.lookup\",\"inputSchema\":{\"type\":\"object\"}}]}",
  "tool_name": "weather.lookup",
  "server_url": "https://mcp.example.com/rpc",
  "auth_type": "bearer",
  "bearer_token_secret_ref": "env://MCP_TOKEN",
  "timeout_seconds": 15,
  "retry_max_attempts": 1,
  "rate_limit_per_minute": 60
}
```

`server_url` 必须是 `https`，且不能包含用户名或密码。Bearer token 只进入后端 secret 存储，不会出现在前端 API 响应中；`bearer_token` 和 `bearer_token_secret_ref` 只能填写一个。响应体为创建后的 `Skill`，`kind=mcp`，并写入 `skill.import.mcp.create` audit event。

`POST /api/skills/import/openapi`

把预览中的某个 OpenAPI operation 保存为当前用户/项目下的 HTTP Skill。请求必须携带同一份 OpenAPI 文档，并用 `operation_id` 或 `method + path` 选择 operation；服务端会重新解析文档、生成 `HTTPSkillInput`、复用 HTTP Skill 校验和 secret redaction 存储路径。Bearer operation 必须提供 `bearer_token` 或 `bearer_token_secret_ref`，且二者只能选一个。`bearer_token_secret_ref` 支持 `env://ENV_NAME` 或受 `NICEAGENT_SECRET_FILE_ROOTS` 限制的 `file:///absolute/path`。

请求体：

```json
{
  "document": "{\"openapi\":\"3.1.0\",\"servers\":[{\"url\":\"https://api.example.com\"}],\"paths\":{}}",
  "operation_id": "getWeather",
  "bearer_token_secret_ref": "env://WEATHER_TOKEN",
  "timeout_seconds": 20,
  "retry_max_attempts": 2,
  "rate_limit_per_minute": 30
}
```

也可以使用 `method` 和 `path` 选择没有 `operationId` 的 operation：

```json
{
  "document": "...",
  "method": "GET",
  "path": "/status"
}
```

响应体为创建后的 `Skill`。该接口会写入 `skill.import.create` audit event；secret 不会出现在 `Skill.runtime_config` 或前端响应中。

`PATCH /api/skills/{skill_id}`

更新当前用户拥有的 HTTP Skill。系统固定 skill 不允许通过该接口修改。

`POST /api/skills/{skill_id}/enable`

启用当前用户拥有的用户 Skill，包括 HTTP Skill 和 MCP Skill。

`POST /api/skills/{skill_id}/disable`

停用当前用户拥有的用户 Skill，包括 HTTP Skill 和 MCP Skill。

`POST /api/skills/{skill_id}/approve`

已废弃的占位接口。当前前端不调用它；未来如果某些非 CLI skill 需要用户确认，可在此基础上扩展 run-specific approval API。

`GET /api/organizations/{organization_id}/members`

列出当前组织成员。`organization_id` 必须等于当前 actor 所在组织；否则返回 404。响应体：

```json
{
  "members": [
    {
      "id": "orgmem_xxx",
      "user_id": "demo-user",
      "organization_id": "demo-org",
      "role": "owner",
      "email": "demo@niceagent.local",
      "name": "Demo User"
    }
  ]
}
```

`POST /api/organizations/{organization_id}/members`

添加或更新当前组织成员，只允许 `owner/admin`。第一版要求传入 `user_id` 和 `role`，`email`、`name` 可选；如果用户不存在，Control Plane 会创建一个最小用户记录。支持角色：`owner`、`admin`、`member`、`editor`、`writer`、`viewer`。为避免误锁，当前不允许通过该接口修改自己的组织成员角色。

```json
{
  "user_id": "user-123",
  "email": "user@example.com",
  "name": "User",
  "role": "admin"
}
```

`PATCH /api/organizations/{organization_id}/members/{user_id}`

修改当前组织内某个成员的角色或展示信息，只允许 `owner/admin`。路径中的 `user_id` 为准，当前不允许修改自己的组织成员角色。

`DELETE /api/organizations/{organization_id}/members/{user_id}`

移除当前组织成员，只允许 `owner/admin`。当前不允许移除自己的组织成员关系。

`GET /api/organizations/{organization_id}/invitations`

列出当前组织的邀请记录，只允许 `owner/admin`。列表不会返回完整 token；创建邀请时才会在响应中返回 token，便于本地开发或 SMTP 邮件服务发送邀请链接。

```json
{
  "invitations": [
    {
      "id": "inv_xxx",
      "organization_id": "demo-org",
      "project_id": "demo-project",
      "email": "user@example.com",
      "role": "viewer",
      "status": "pending",
      "invited_by_user_id": "demo-user",
      "expires_at": "2026-06-07T00:00:00Z"
    }
  ]
}
```

`POST /api/organizations/{organization_id}/invitations`

创建组织或项目邀请，只允许 `owner/admin`。`project_id` 为空表示接受后加入组织；`project_id` 非空表示接受后加入该项目，且项目必须属于当前组织。`expires_in_hours` 默认 168 小时，最大 720 小时。

```json
{
  "email": "user@example.com",
  "role": "viewer",
  "project_id": "demo-project",
  "expires_in_hours": 168
}
```

创建响应会额外包含一次性可见的 `token`。如果 Control Plane 配置 `INVITATION_EMAIL_MODE=smtp`，服务会同时向邀请邮箱发送包含 `/?invitation_token={token}` 链接的邮件，并写入 `invitation.email.send` audit event；邮件 subject/body 可通过 `INVITATION_EMAIL_SUBJECT_TEMPLATE` 和 `INVITATION_EMAIL_BODY_TEMPLATE` 配置。`INVITATION_EMAIL_QUEUE_MODE=memory` 时，接口只保证邮件已进入当前 Control Plane 进程的内存队列；后台 worker 会按配置重试发送。`INVITATION_EMAIL_QUEUE_MODE=outbox` 时，接口会先把邮件投递任务写入 `invitation_email_outbox`，再由后台 worker claim due jobs、重试并标记 `sent/failed`，Control Plane 重启后仍可继续处理未完成任务。SMTP 发送、内存入队或 outbox 入队失败不会回滚已创建的邀请。邮件服务商的投递、退信、投诉和丢弃回调可通过 `invitation-email-events` 接口记录，便于后续运营排查和停发策略。

```json
{
  "invitation": {
    "id": "inv_xxx",
    "token": "invite_token_xxx",
    "organization_id": "demo-org",
    "project_id": "demo-project",
    "email": "user@example.com",
    "role": "viewer",
    "status": "pending"
  }
}
```

`POST /api/organizations/{organization_id}/invitations/{invitation_id}/resend`

重新发送某个当前组织内的邀请邮件，只允许 `owner/admin`。该接口不会重新生成 invitation token，也不会修改邀请的角色、项目或过期时间；它只把仍处于 `pending` 且未过期的邀请重新交给当前配置的邀请邮件发送器。未配置 `INVITATION_EMAIL_MODE=smtp` 时返回 `503`。如果目标邮箱已经因为 `bounced`、`complaint` 或 `dropped` 事件被当前组织 suppression，接口返回 `409`，不会重新投递。在 `outbox` 模式下，该接口会把对应 `invitation_email_outbox` delivery 重置为 `pending`、清除锁和上次错误，等待后台 worker 重新投递；响应不会返回完整 invitation token。

```json
{
  "delivery": {
    "id": "invmail_xxx",
    "invitation_id": "inv_xxx",
    "status": "pending",
    "attempts": 0,
    "max_attempts": 1,
    "next_attempt_at": "2026-06-02T00:00:00Z"
  }
}
```

`POST /api/invitations/{token}/accept`

当前登录 actor 接受邀请。该接口允许尚未有组织/项目成员关系的已认证用户调用；接受组织邀请会写入 `organization_members`，接受项目邀请会写入 `project_members`。在 `trusted-header`/`oidc` 边界下，请求必须携带可信邮箱 claim/header，且该邮箱必须与邀请邮箱一致。当前已支持可选 SMTP 邀请邮件、可信 `issuer + sub + email` 绑定、最小 NiceAgent OIDC 浏览器登录/session，以及默认 action-level policy 和 `ACTION_POLICY_FILE` 文件化覆盖；生产环境仍需要结合真实 IdP、回调域名、cookie 安全策略、外部策略源和更细 action 条件做联调。

如果上游同时传入 `X-NiceAgent-Identity-Issuer` 和 `X-NiceAgent-Identity-Subject`，接受邀请前也会经过 `user_identities` 绑定校验；如果只传其中一个会返回 401，发生身份冲突会返回 409。

```json
{ "name": "User" }
```

`GET /api/organizations/{organization_id}/invitation-email-events`

查询当前组织的邀请邮件事件，只允许 `owner/admin`。支持 `invitation_id`、`delivery_id` 和 `limit` 查询参数。事件按 `occurred_at` 倒序返回。

```json
{
  "events": [
    {
      "id": "invmailevt_xxx",
      "invitation_id": "inv_xxx",
      "delivery_id": "invmail_xxx",
      "provider": "smtp-provider",
      "provider_message_id": "message-123",
      "type": "bounced",
      "reason": "mailbox unavailable",
      "payload": { "smtp_code": "550" },
      "occurred_at": "2026-06-01T00:00:00Z",
      "created_at": "2026-06-01T00:00:01Z"
    }
  ]
}
```

`POST /api/organizations/{organization_id}/invitation-email-events`

记录 provider-neutral 的邀请邮件事件，只允许 `owner/admin`。`invitation_id` 必填且必须属于当前组织；`delivery_id` 可选，如果填写必须属于同一邀请。`type` 支持 `delivered`、`bounced`、`complaint`、`dropped`。记录 `delivered` 会把对应 outbox delivery 标记为 `sent`；记录 `bounced`、`complaint` 或 `dropped` 会把对应 delivery 标记为 `bounced`，保存 `reason` 到 `last_error`，并按 `organization_id + email` 写入 `invitation_email_suppressions`，后续邀请邮件创建发送、outbox claim 和重发都会自动停发该邮箱。

```json
{
  "invitation_id": "inv_xxx",
  "delivery_id": "invmail_xxx",
  "provider": "smtp-provider",
  "provider_message_id": "message-123",
  "type": "bounced",
  "reason": "mailbox unavailable",
  "payload": { "smtp_code": "550" }
}
```

`POST /webhooks/invitation-email-events`

邮件服务商 webhook 入口。该接口不使用用户登录态，而是要求至少配置一种 webhook 验证方式：`INVITATION_EMAIL_WEBHOOK_SECRET`、`INVITATION_EMAIL_SENDGRID_PUBLIC_KEY`、`INVITATION_EMAIL_MAILGUN_SIGNING_KEY` 或 `INVITATION_EMAIL_SNS_SIGNATURE_VERIFICATION=true`。NiceAgent 兼容签名算法为 `X-NiceAgent-Webhook-Signature: sha256=<hex(hmac_sha256(secret, raw_body))>`；SendGrid 原生签名使用 `X-Twilio-Email-Event-Webhook-Timestamp` 和 `X-Twilio-Email-Event-Webhook-Signature`；Mailgun 原生签名使用 payload 中的 `signature.timestamp`、`signature.token` 和 `signature.signature`；Amazon SES SNS notification 会校验 SNS envelope 中的 `SigningCertURL`、`SignatureVersion` 和 `Signature`，并可通过 `INVITATION_EMAIL_SNS_TOPIC_ARN` 限制来源 topic。未配置任何验证方式时接口返回 404，签名错误返回 401。

请求体继续兼容上面的 provider-neutral `InvitationEmailEventInput`。此外，webhook adapter 已支持最小服务商原生字段映射：

- SendGrid Event Webhook：支持单条或数组 payload，读取 `event`、`sg_message_id`、`reason`、`timestamp`，并从 `custom_args` / `unique_args` / `metadata` 等字段读取 `invitation_id` 和 `delivery_id`。
- Amazon SES SNS notification：支持 SNS envelope 中的 JSON 字符串 `Message`，读取 `notificationType`、`mail.messageId`、`mail.tags`、`bounce`、`complaint` 和 `delivery` 字段；开启 `INVITATION_EMAIL_SNS_SIGNATURE_VERIFICATION=true` 后会按 SNS 证书签名校验 envelope。
- Mailgun webhook：支持 `event-data.event`、`event-data.message.headers.message-id`、`event-data.user-variables`、`reason` 和 `delivery-status.message`。

原生 payload 仍必须携带可映射到 NiceAgent 的 `invitation_id` 或 `delivery_id`；通常应在发送邀请邮件时把这些值作为服务商 metadata/custom args/tags 注入。单条事件响应为 `InvitationEmailEventResponse`，批量事件响应为 `InvitationEmailEventsResponse`。

```http
X-NiceAgent-Webhook-Signature: sha256=...
X-Twilio-Email-Event-Webhook-Timestamp: 1717238400
X-Twilio-Email-Event-Webhook-Signature: ...
```

`GET /api/organizations/{organization_id}/invitation-email-suppressions`

查询当前组织内被自动停发的邀请邮箱，只允许 `owner/admin`。支持 `limit` 查询参数，默认最多返回 100 条，按 `updated_at` 倒序。

```json
{
  "suppressions": [
    {
      "id": "invmailsup_xxx",
      "organization_id": "demo-org",
      "email": "user@example.com",
      "reason": "mailbox unavailable",
      "source_event_id": "invmailevt_xxx",
      "provider": "smtp-provider",
      "provider_message_id": "message-123",
      "created_at": "2026-06-01T00:00:01Z",
      "updated_at": "2026-06-01T00:00:01Z"
    }
  ]
}
```

`DELETE /api/organizations/{organization_id}/invitation-email-suppressions/{suppression_id}`

解除当前组织内某个邮箱的自动停发状态，只允许 `owner/admin`。该接口不会删除历史 `invitation_email_events`，只删除 suppression 记录；删除后，仍处于 `pending` 且未过期的邀请可以再次通过 resend API 重新投递。

```json
{
  "suppression": {
    "id": "invmailsup_xxx",
    "organization_id": "demo-org",
    "email": "user@example.com",
    "reason": "mailbox unavailable"
  }
}
```

`GET /api/projects/{project_id}/members`

列出当前项目成员。`project_id` 必须等于当前 actor 所在项目；否则返回 404。响应体：

```json
{
  "members": [
    {
      "id": "prjmem_xxx",
      "user_id": "demo-user",
      "project_id": "demo-project",
      "role": "owner",
      "email": "demo@niceagent.local",
      "name": "Demo User"
    }
  ]
}
```

`POST /api/projects/{project_id}/members`

添加或更新当前项目成员，只允许 `owner/admin`。第一版要求传入 `user_id` 和 `role`，`email`、`name` 可选；如果用户不存在，Control Plane 会创建一个最小用户记录。支持角色：`owner`、`admin`、`member`、`editor`、`writer`、`viewer`。为避免误锁，当前不允许通过该接口修改自己的项目成员角色。

```json
{
  "user_id": "user-123",
  "email": "user@example.com",
  "name": "User",
  "role": "viewer"
}
```

`PATCH /api/projects/{project_id}/members/{user_id}`

修改当前项目内某个成员的角色或展示信息，只允许 `owner/admin`。路径中的 `user_id` 为准，当前不允许修改自己的成员角色。

`DELETE /api/projects/{project_id}/members/{user_id}`

移除当前项目成员，只允许 `owner/admin`。当前不允许移除自己的项目成员关系。

`GET /api/projects/{project_id}/quota`

获取当前项目的有效 quota policy。若项目尚未持久化配置，则返回 env fallback 值；默认都是 `0`，表示不限制。

```json
{
  "policy": {
    "project_id": "demo-project",
    "max_concurrent_runs": 1,
    "max_runs_per_hour": 100,
    "max_model_tokens_per_day": 100000,
    "max_tool_calls_per_day": 1000,
    "max_sandbox_seconds_per_day": 3600
  }
}
```

`PATCH /api/projects/{project_id}/quota`

设置当前项目的持久 quota policy，只允许 `owner/admin`。所有字段必须是非负整数，`0` 表示关闭对应限制。默认 quota 统计基于 Postgres/memory 的 run 状态和 run usage；当 Control Plane 配置 `QUOTA_COUNTER_MODE=redis` 时，`max_concurrent_runs` 和 `max_runs_per_hour` 会先通过 Redis 预占，run 终态释放并发计数。`max_model_tokens_per_day` 可配合 `QUOTA_MODEL_TOKEN_RESERVATION_MODE=fixed|dynamic` 做创建前预占：fixed 使用 `QUOTA_MODEL_TOKEN_RESERVATION_PER_RUN`，dynamic 按用户消息长度估算并叠加 `QUOTA_MODEL_TOKEN_DYNAMIC_OUTPUT_BUFFER`；`QUOTA_MODEL_TOKEN_ESTIMATOR_MODEL` 可指定动态预占的 tokenizer 模型，支持 `gpt-4o`/`gpt-5-*`/`o*`、`deepseek-*`、`qwen-*`、`kimi-*`、`moonshot-*`、`doubao-*`、`glm-*`、`mistral-*`、`llama-*` 等常见模型族映射，未知模型回退到 `heuristic_rune_div4`。run 终态按真实 `RunUsage.total_tokens` 结算差额。`max_tool_calls_per_day` 和 `max_sandbox_seconds_per_day` 会在创建 run 时按已有 `RunUsage` 做保护，并在 Runtime 调用 tool 前通过内部 quota reserve 做最小实时预占。

```json
{
  "max_concurrent_runs": 1,
  "max_runs_per_hour": 100,
  "max_model_tokens_per_day": 100000,
  "max_tool_calls_per_day": 1000,
  "max_sandbox_seconds_per_day": 3600
}
```

`GET /api/projects/{project_id}/runtime-policy`

获取当前项目的有效 Runtime policy。若项目尚未持久化配置，则返回默认 `skill_risk_policy=allow`。该策略会在 Control Plane 创建 HTTP dispatch payload 或 Redis execution context 时下发到 `RunRequest.skill_risk_policy`，使同一组 Agent Runtime 可以按项目执行不同的 skill 风险门禁。

```json
{
  "policy": {
    "project_id": "demo-project",
    "skill_risk_policy": "block-destructive"
  }
}
```

`PATCH /api/projects/{project_id}/runtime-policy`

设置当前项目的持久 Runtime policy，只允许 `owner/admin`。当前字段：

- `skill_risk_policy=allow`：只使用用户/项目授权列表，不额外阻断风险 skill。
- `skill_risk_policy=block-high`：阻断 `risk=high` 的 skill。
- `skill_risk_policy=block-destructive`：阻断 `annotations.destructiveHint=true` 的 skill。
- `skill_risk_policy=read-only`：仅允许 `readOnlyHint=true` 且非 destructive/high risk 的 skill。

```json
{ "skill_risk_policy": "read-only" }
```

`GET /api/projects/{project_id}/usage?window=24h|7d|30d`

按 provider、model、currency、`estimated` 和 `token_estimator` 聚合当前项目的 run usage，只允许 `owner/admin`。`project_id` 必须等于当前 actor 所在项目。默认窗口为 `24h`；也可以传 `since=<RFC3339>` 做自定义起点，此时响应中的 `window` 为 `custom`。该接口面向用量看板和账单维度分析，不改变 quota 判定逻辑。

```json
{
  "project_id": "demo-project",
  "window": "24h",
  "since": "2026-06-01T00:00:00Z",
  "buckets": [
    {
      "provider": "openai-compatible",
      "model": "deepseek-chat",
      "currency": "USD",
      "estimated": true,
      "token_estimator": "heuristic_rune_div4",
      "run_count": 3,
      "input_tokens": 1200,
      "output_tokens": 800,
      "total_tokens": 2000,
      "cost": 0.01,
      "tool_calls": 4,
      "sandbox_duration_millis": 1500,
      "artifact_bytes": 4096
    }
  ],
  "total": {
    "run_count": 3,
    "total_tokens": 2000,
    "cost": 0.01,
    "tool_calls": 4,
    "artifact_bytes": 4096
  }
}
```

`GET /api/audit/events`

列出当前 actor 在当前项目下最近的 audit events。支持 `limit`、`request_id`、`run_id`、`action`、`resource_id` 过滤，`limit` 最大 100。audit metadata 会按敏感 key 脱敏，API key、Authorization header、token、secret、password 和 cookie 不应出现在响应中。Agent Runtime 写入 `tool.started` 和 `tool.finished` run event 时，Control Plane 会额外写入 `skill.invoke.start` / `skill.invoke.finish` audit event；这些审计事件只包含 skill、tool、event id/seq 和完成状态，不包含 `tool.output` 原始内容。

```json
{
  "events": [
    {
      "id": "audit_xxx",
      "actor_user_id": "demo-user",
      "actor_project_id": "demo-project",
      "action": "run.create",
      "resource_type": "run",
      "resource_id": "run_xxx",
      "decision": "allow",
      "request_id": "req_xxx",
      "trace_id": "trc_xxx",
      "run_id": "run_xxx",
      "created_at": "2026-05-31T00:00:00Z"
    }
  ]
}
```

`GET /api/skill-invocations`

列出当前 actor 在当前项目下最近的 skill invocation 记录。支持 `limit`、`run_id`、`skill_id`、`status` 过滤，`limit` 最大 100。该接口用于排查 agent 调用了哪些 skill、调用是否成功以及对应 run event id/seq；它不返回 tool 原始 input/output，也不返回 secret。

`status` 取值：

- `started`
- `succeeded`
- `failed`

响应体：

```json
{
  "invocations": [
    {
      "id": "skillinv_xxx",
      "run_id": "run_xxx",
      "chat_id": "chat_xxx",
      "user_id": "demo-user",
      "project_id": "demo-project",
      "skill_id": "cli.exec",
      "tool_name": "cli_exec",
      "status": "succeeded",
      "decision": "allow",
      "request_id": "req_xxx",
      "trace_id": "trc_xxx",
      "started_event_id": "evt_start",
      "started_event_seq": 2,
      "finished_event_id": "evt_finish",
      "finished_event_seq": 4,
      "duration_ms": 180,
      "metadata": {
        "tool": "cli_exec",
        "ok": true
      },
      "started_at": "2026-05-31T00:00:00Z",
      "finished_at": "2026-05-31T00:00:01Z",
      "updated_at": "2026-05-31T00:00:01Z"
    }
  ]
}
```

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
    "attempt_id": "attempt_xxx",
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

Agent Runtime 向 Control Plane 写入单条 `RunEvent`。当任一服务配置了 `INTERNAL_API_TOKEN` 时，对应内部 API 需要请求头 `Authorization: Bearer <token>`。本地开发可以让 token 为空；如果设置 `INTERNAL_API_TOKEN_REQUIRED=true`，或 `NICEAGENT_ENV` 不是 `local/dev/development/test/ci`，三服务都会在缺少 `INTERNAL_API_TOKEN` 时启动失败。

请求体：

```json
{
  "type": "model.token",
  "message": "hello",
  "payload": null,
  "attempt_id": "attempt_xxx"
}
```

如果 run 已经被某个 active attempt claim，`events`、`complete` 和 `fail` 都必须携带相同的 `attempt_id`；旧 attempt 或未携带 attempt 的迟到回写会返回 `409 Conflict`。

`POST /internal/runs/{run_id}/claim`

Agent Runtime 在真正执行 run 之前 claim 当前 attempt。Control Plane 会把 `attempt_id` 写入 run 的 active attempt，并记录 `claimed_by`、`lease_expires_at` 和 `attempt_count`。如果已有未过期的不同 attempt，返回 `409 Conflict`；如果 run 已经处于终态，则返回当前 run，Runtime 应跳过执行。

请求体：

```json
{
  "attempt_id": "attempt_xxx",
  "claimed_by": "agent-runtime-1",
  "lease_seconds": 600
}
```

`GET /internal/runs/{run_id}/execution-context`

Redis worker 消费 queue payload 后，通过该接口按 `run_id` 拉取完整执行上下文。可选查询参数 `attempt_id` 会被写入返回的 `RunExecutionRequest.request.attempt_id`。

响应体复用 `RunExecutionRequest`，包含最新用户消息、workspace、当前用户可用 `RuntimeSkill` 和 `control_plane_url`。Redis queue payload 只保存 `run_id`、`attempt_id` 和入队时间，不保存 skill secret 或完整用户消息。

`POST /internal/runs/{run_id}/quota-reserve`

Agent Runtime 在执行 Eino tool 前调用该接口预占 tool/sandbox 用量。Control Plane 会校验 active `attempt_id`，读取当前项目 quota policy，并将本次预占写入 run 级 `RunUsage`。如果达到上限，接口仍返回 `200 OK`，但 `allowed=false`，Runtime 会把该结果作为 tool observation 返回给模型，不会真正执行 tool。

请求体：

```json
{
  "attempt_id": "attempt_xxx",
  "skill_id": "cli.exec",
  "tool_calls": 1,
  "sandbox_seconds": 10
}
```

响应体：

```json
{
  "allowed": false,
  "message": "已达到当前项目每日工具调用上限，请明天再试。",
  "quota": "tool_calls_per_day",
  "usage": {
    "tool_calls": 1000,
    "sandbox_commands": 42,
    "sandbox_duration_millis": 3600000
  }
}
```

当前预占是最小治理边界：它保护单 Control Plane repository 路径和 Runtime 调用前的 obvious over-limit，不等同于分布式强一致 token bucket。run 完成时，Runtime 仍会通过 `complete.usage` 回写实际用量，并覆盖此前的预占快照。

`POST /internal/runs/{run_id}/complete`

Agent Runtime 通知 Control Plane 写入最终 assistant 消息、登记 artifact，并将 run 置为 `succeeded`。如果 run 已经是 `canceled`、`failed` 或 `succeeded`，Control Plane 不会覆盖终态，也不会登记迟到 artifact。

请求体：

```json
{
  "content": "最终回复内容",
  "attempt_id": "attempt_xxx",
  "artifacts": [
    {
      "path": "output/report.txt",
      "name": "report.txt",
      "mime_type": "text/plain",
      "size_bytes": 128,
      "sha256": "..."
    }
  ]
}
```

`POST /internal/runs/{run_id}/fail`

Agent Runtime 或 dispatcher 通知 Control Plane 将 run 置为 `failed`，并写入 `run.failed` 事件。终态 run 不会被覆盖。

请求体：

```json
{ "error": "runtime unavailable", "attempt_id": "attempt_xxx" }
```

`GET /internal/runs/{run_id}/status`

Agent Runtime 查询 run 状态，用于识别用户取消。

`GET /internal/runs/{run_id}/artifacts`

Agent Runtime 的 `workspace.read` 使用该接口列出当前 run 已登记 artifacts。请求会校验 active `attempt_id`，因此 Redis/HTTP Runtime 执行路径应带上 `?attempt_id=attempt_xxx`。

响应体复用 `ArtifactListResponse`。

`POST /internal/runs/{run_id}/artifacts`

Agent Runtime 在 sandbox/CLI tool 调用结束后使用该接口增量登记本次工具调用产生的 artifact metadata。Control Plane 会校验 active `attempt_id`，补齐 run/chat/user/workspace/project 归属，写入 `artifacts`，并为每个新 artifact 写入 `artifact.created` event。若配置了 `ARTIFACT_RETENTION_DAYS`，Control Plane 会为未显式设置 `expires_at` 的 artifact 补默认过期时间。若后续 `complete` 再携带相同 artifact id，Control Plane 会按已有 artifact 处理，不重复发 `artifact.created`。

请求体：

```json
{
  "attempt_id": "attempt_xxx",
  "artifacts": [
    {
      "path": "output/report.txt",
      "name": "report.txt",
      "mime_type": "text/plain",
      "size_bytes": 128,
      "sha256": "..."
    }
  ]
}
```

响应体复用 `ArtifactListResponse`，返回已保存并补齐 ID/归属字段的 artifacts。

`workspace.read` 当前支持三个 action：

- `summary`：返回当前 run/workspace 的 artifact metadata 摘要，包括 artifact 数量、总大小、MIME 分布、文本 artifact 数量、latest artifact 和 artifact 路径清单；不读取文件内容。
- `list`：返回已登记 artifact metadata 列表。
- `read`：读取指定文本 artifact 的内容摘要，需要传 `artifact_id`，可选 `max_bytes`。

`GET /internal/artifacts/{artifact_id}/content`

Agent Runtime 的 `workspace.read` 使用该接口读取已登记文本 artifact 的内容摘要。查询参数：

- `run_id`：必填或由 artifact 反查，建议显式传入当前 run。
- `attempt_id`：当前 active attempt。
- `max_bytes`：最大读取字节数，默认 64 KiB，服务端上限 256 KiB。

该接口只读取通过 artifact metadata 登记的文件，并复用 workspace root、`output/` 路径限制、symlink escape 检查、regular file 检查、MIME 文本限制和读取大小限制。二进制 artifact、未登记文件、`../`、绝对路径和 symlink escape 会被拒绝。

`POST /internal/artifacts/cleanup-expired`

Control Plane 运维/后台任务使用该接口清理已过期 artifact。请求需要内部 bearer token；默认会把 metadata 标记为 `deleted_at`。如果请求传 `delete_files=true`，或 Control Plane 配置 `ARTIFACT_CLEANUP_DELETE_FILES=true`，还会对 `storage_backend` 为空或 `local` 的 artifact 做本地文件回收。文件回收会复用 workspace root、`output/` 相对路径和 symlink escape 校验；对象存储对象仍需要后续生命周期策略处理。

请求体：

```json
{ "limit": 100, "delete_files": true }
```

响应体：

```json
{
  "artifacts": [],
  "deleted_files": 0,
  "file_errors": []
}
```

`artifacts` 返回本次被标记删除的 metadata；`deleted_files` 是本次成功删除的本地文件数；`file_errors` 是不能安全删除或删除失败的 artifact 摘要。`limit` 默认 100，最大 1000。

响应体：

```json
{
  "artifact": {
    "id": "art_xxx",
    "path": "output/report.txt",
    "mime_type": "text/plain"
  },
  "content": "文本内容摘要",
  "truncated": false,
  "bytes_read": 18
}
```

`POST /internal/sandbox/exec`

Sandbox Executor 在策略约束下执行命令的入口，由 Agent Runtime 的 HTTP sandbox executor 调用。

请求体复用 `SandboxCommand`，响应体复用 `SandboxResult`。

Sandbox Executor 支持 `EXECUTOR_MODE=local|container`。`container` 模式可通过 `SANDBOX_CONTAINER_IMAGE`、`SANDBOX_CONTAINER_CPUS`、`SANDBOX_CONTAINER_MEMORY`、`SANDBOX_CONTAINER_PIDS_LIMIT` 和 `SANDBOX_CONTAINER_*` 安全参数配置；Docker 不可用时可按 `SANDBOX_CONTAINER_LOCAL_FALLBACK` 回退到 local executor。`SANDBOX_WORKSPACE_ROOT` 应在 Control Plane 和 Sandbox Executor 间保持一致，便于 local artifact 下载闭环。

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

Skill 元数据以 `skills` 和 `skill_versions` 为权威，`skill_grants` 表示用户/项目可用性，`skill_secrets` 只保存 secret 引用或本地开发密文。`input_schema`、`output_schema`、`annotations` 和 `runtime_config` 使用 JSON/JSONB；`annotations` 采用 MCP 风格字段，例如 `readOnlyHint`、`destructiveHint`、`idempotentHint`、`openWorldHint`。当前用户 Skill 支持 `kind=http` 和 `kind=mcp`：HTTP Skill 直接调用配置的 HTTPS endpoint；MCP Skill 通过最小 HTTP JSON-RPC `tools/call` adapter 调用远端 MCP-compatible endpoint。

Agent Runtime 可通过 `SKILL_RISK_POLICY=allow|block-high|block-destructive|read-only` 配置默认风险门禁；Control Plane 也可以通过项目级 Runtime policy 把 `RunRequest.skill_risk_policy` 下发给单次 run，并优先覆盖 Runtime 默认值。被拦截的调用会返回 `skill_policy_denied` 结构化 observation，并写入 `tool.output` / `tool.finished` 事件；Runtime 不会发起真实 tool 请求，也不会预占 skill quota。

Agent Runtime 可以使用 mock provider 或 OpenAI-compatible provider。当前主执行路径通过 Eino ADK `ChatModelAgent + Runner` 和 Eino 原生 `ToolCallingChatModel` 运行 agentic loop；模型输出仍通过 `model.token` 类型的 `RunEvent` 写回 Control Plane，并由前端折叠成 assistant 消息。

OpenAI-compatible provider 通过 Eino `eino-ext` OpenAI ChatModel 使用 `/v1/chat/completions` 协议，并支持模型原生 tool calling；该能力不改变外部 Web API 和 `RunExecutionRequest`。DeepSeek 使用同一 provider 路径，可设置 `MODEL_PROVIDER_PROFILE=deepseek` 作为部署侧配置预设，Runtime 会把 usage provider 标记为 `deepseek`，并在未显式配置 `MODEL_BASE_URL` 时使用 DeepSeek 官方 OpenAI-compatible 根地址；模型名和 API key 仍由环境变量或 Secret 显式注入。

Runtime 完成 run 时会在 `RunCompleteRequest.usage` 回写模型与工具运营数据。Control Plane 会持久化到 run 级 usage，并在 `GET /api/runs/{run_id}` 的 `Run.usage` 中返回。真实 provider usage 优先；provider 缺失 usage 或 mock provider 会返回估算 token，并设置 `estimated=true`。估算 usage 会额外返回 `token_estimator`，当前内置值可能为 `tiktoken_o200k_base`、`tiktoken_cl100k_base` 或 `heuristic_rune_div4`；前两者使用 tiktoken 兼容编码，分别覆盖 OpenAI 新一代模型族和常见 OpenAI-compatible 模型族，未知模型会回退到按文本 rune 数粗略估算。字段包括 provider、model、input/output/reasoning/cached/total tokens、latency、retry_count、fallback_from/fallback_to、error_class、cost、currency，以及 run 级工具/sandbox 聚合字段：`tool_calls`、`tool_errors`、`sandbox_commands`、`sandbox_duration_millis`、`sandbox_output_bytes`、`sandbox_cpu_millis`、`sandbox_memory_max_bytes`、`artifact_count`、`artifact_bytes`。这些工具字段用于审计、排障和 quota/账单聚合；当前项目 quota 已能按 UTC 自然日限制每日 tool calls 和 sandbox seconds，并在 Runtime tool 调用前做最小实时预占。

OpenAI-compatible/DeepSeek 错误分类约定：401=`auth_error`，402=`billing_error`，400/422=`request_error`，429=`rate_limited`，500/503/网关错误=`provider_unavailable`，超时/连接错误=`network_error`。API key、Authorization header、token、secret、password 和 cookie 不应出现在日志、event payload 或 run error 中。

Agent Runtime 的 `GET /healthz` 会返回模型 provider 健康快照。默认情况下快照来自实际请求后的运行状态；如果开启 `MODEL_HEALTH_PROBE_ENABLED=true`，Runtime 会按配置间隔主动发起最小模型探针，并在快照中展示 probe 状态。health 响应不会暴露 API key。

```json
{
  "status": "ok",
  "model_provider": {
    "provider": "openai-compatible",
    "model": "deepseek-v4-flash",
    "status": "healthy",
    "probe_enabled": true,
    "probe_status": "healthy",
    "probe_count": 2,
    "probe_success": 2,
    "last_probe_at": "2026-05-31T14:00:00Z",
    "request_count": 3,
    "success_count": 3,
    "error_count": 0,
    "retry_count": 1,
    "last_latency_ms": 521
  }
}
```

当配置了 fallback provider 时，`model_provider` 会包含 `fallback_enabled`、`last_fallback`、`fallback_from`、`fallback_to` 和 `targets`，用于观察最近一次是否触发了后备模型。

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
