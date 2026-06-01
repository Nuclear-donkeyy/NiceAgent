# Skill 执行生产化

## 产品功能

Skill 是 NiceAgent 的能力系统。用户看到的是“系统能力”和“我的能力”，Agent Runtime 看到的是 Control Plane 按用户/项目授权后下发的 `RuntimeSkill`。生产化目标是让 skill 可注册、可版本化、可授权、可校验、可审计，并且在执行时不会泄露 secret。

第一阶段继续以两类 skill 为主：

- 系统固定 skill：`cli.exec`、`workspace.read` 等平台内置能力，由 Runtime 调内部 executor。
- 用户可添加 skill：当前支持 `kind=http` 和 `kind=mcp`。两者都通过 JSON Schema 描述输入输出，通过 `runtime_config` 描述运行时调用方式，通过 `skill_secrets` 或外部 secret backend 注入鉴权材料。

## 成熟方案调研

MCP Tool 模型适合做 skill manifest 参考。MCP 的 tool 具有 `name`、`description`、`inputSchema`、可选 `outputSchema` 和 `annotations`；`annotations` 中的 `readOnlyHint`、`destructiveHint`、`idempotentHint`、`openWorldHint` 可用于描述风险和执行语义，但只能作为模型提示和 UI 提示，不能作为安全边界。参考：[MCP Schema Reference](https://modelcontextprotocol.io/specification/2025-11-25/schema)。

OpenAPI 3.1 适合做 HTTP Skill 导入源。它用 JSON Schema 描述 request/response，并用 `securitySchemes` 表达鉴权机制，适合把一个 API operation 转换成一个 HTTP Skill。参考：[OpenAPI Specification 3.1.1](https://spec.openapis.org/oas/v3.1.1.html)。

JSON Schema 建议统一采用 Draft 2020-12，用于三处校验：创建/更新 skill 时校验 schema 本身，Runtime 调用 skill 前校验 tool arguments，调用后校验 structured output。参考：[JSON Schema Draft 2020-12](https://json-schema.org/draft/2020-12)。

Secret 不应直接混入 skill manifest。Kubernetes Secret 更像运行时投递载体，生产环境应优先使用外部 secret 系统或云 KMS。可选路径包括 External Secrets Operator、Vault Transit、Vault KV、阿里云 KMS Secrets Manager。参考：[External Secrets Operator](https://external-secrets.io/latest/)、[Vault Transit](https://developer.hashicorp.com/vault/docs/secrets/transit)、[阿里云 KMS SDK/Secret SDK](https://www.alibabacloud.com/help/en/kms/key-management-service/developer-reference/sdk-user-guide/)。

HTTP Skill 还要按 SSRF 和第三方 API 风险处理：默认只允许 `https`，限制 host/port/scheme，阻断 loopback、link-local、private CIDR 和云 metadata 地址，控制 redirect、timeout、response size、content-type，并把外部返回作为 observation 而不是直接信任。

## 当前仓库现状

协议层已经有 `Skill`、`RuntimeSkill`、`SkillGroups`、`HTTPSkillInput`，位于 `packages/common/protocol/skill.go`。`Skill` 中的 `InputSchema`、`OutputSchema`、`Annotations`、`RuntimeConfig` 仍以 JSON 字符串形式对外传输，但仓库已新增 `packages/common/skillmanifest`，用于解析和校验 HTTP Skill 的 JSON Schema 与 typed runtime config。

Postgres schema 已有 `skills`、`skill_versions`、`skill_grants`、`skill_secrets`。`input_schema`、`output_schema`、`annotations`、`runtime_config` 是 `jsonb`；`skill_secrets` 同时预留了 `secret_ref` 和 `encrypted_value`。当前 runtime 通过 `SecretResolver` 接口解析 secret，本地实现支持 `encrypted_value`，并支持 `env://ENV_NAME` 和 `file:///absolute/path` 形式的 `secret_ref`。`env://` 可配合 K8s Secret/External Secrets 注入环境变量；`file://` 可读取挂载到 Runtime 容器内、受 `NICEAGENT_SECRET_FILE_ROOTS` 限制的 secret 文件。Vault、KMS 或 External Secrets 原生 resolver 仍未接入。

Control Plane 已经支持：

- `GET /api/skills` 返回当前 demo 用户的系统/用户 skill 分组。
- `POST /api/skills/http` 创建 HTTP Skill。
- `POST /api/skills/import/mcp/preview` 预览 MCP `tools/list` manifest。
- `POST /api/skills/import/mcp` 把选中的 MCP tool 保存为用户 MCP Skill。
- `PATCH /api/skills/{id}` 更新 HTTP Skill。
- `POST /api/skills/{id}/enable|disable` 启停用户 skill。

创建/更新 HTTP Skill 时已经校验 `name`、`GET|POST`、`auth_type`、`https` URL、URL 不含 credentials、timeout 范围、`input_schema` 和 `output_schema`。`runtime_config` 会由 `HTTPSkillRuntimeConfig` 统一生成，避免 token 进入 runtime config。OpenAPI JSON/YAML preview dry-run 已有最小入口：`POST /api/skills/import/openapi/preview` 会把 `GET/POST` operation 转成 HTTP Skill 候选项；`POST /api/skills/import/openapi` 已能把用户选中的 operation 保存成 HTTP Skill，并复用 bearer token / `env://` / `file://` secret_ref 绑定路径。前端 HTTP Skill 表单和 OpenAPI 导入表单也支持 Secret Ref 输入，生产环境可避免在浏览器里粘贴明文 token。

MCP `tools/list` 风格 manifest preview 也已落地：`POST /api/skills/import/mcp/preview` 支持 top-level `tools` 和 JSON-RPC `result.tools` 形态，能保留 MCP `inputSchema`、`outputSchema` 和 annotations 并生成候选 skill manifest；`POST /api/skills/import/mcp` 会重新解析同一份 manifest，按 `tool_name` 选择 tool，生成 `kind=mcp` 用户 Skill，保存 `server_url`、`tool_name`、timeout、auth、retry、rate limit 等 runtime config，并复用 bearer token / `env://` / `file://` secret_ref 绑定路径。前端已提供 MCP preview 和保存入口。

Agent Runtime 的 `ToolBridge` 已能把 `RuntimeSkill` 转成 Eino tool。builtin skill 调 `cli.exec` 或 `workspace.read`；HTTP Skill 会按 `runtime_config` 构造请求，支持 bearer token，响应限制为 64KB。Runtime 调用 HTTP Skill 前会校验 arguments；返回后会按 `output_schema` 校验 structured output；非 2xx、DNS、TLS、timeout、响应过大、schema 错误会转成结构化 observation。HTTP Skill 默认只允许 `https`，禁用重定向，拒绝 loopback、`.local`、metadata host、字面量 private/link-local IP，并会在发出请求前解析域名，拒绝解析到 private/link-local/loopback/metadata 类地址的 host。HTTP Skill 还支持可选 `retry.max_attempts` 和 `rate_limit.requests_per_minute`；rate limit 默认是 Agent Runtime 进程内固定窗口，也可通过 `SKILL_RATE_LIMIT_MODE=redis` 切到 Redis 分钟窗口，让多个 Runtime 副本共享同一个 skill rate limit。

MCP Skill 当前实现为最小 HTTP JSON-RPC adapter：Runtime 调用前校验 arguments，执行时向配置的 `server_url` 发送 `tools/call`，把模型传入的 arguments 放入 `params.arguments`，支持 bearer secret、SSRF/DNS 拦截、64KB 响应限制、结构化 observation、JSON-RPC error 分类和可选 `structuredContent` output schema 校验。该路径还不是完整 MCP client：stdio transport、SSE/streamable HTTP transport、initialize/session negotiation、`tools/list_changed` 动态通知和连接级缓存失效仍待补齐。Control Plane 内部 event 写入路径会把 `tool.started` 和 `tool.finished` 同步写入脱敏后的 `audit_events` 和 `skill_invocations`，只记录 `skill_id`、tool 名、event id/seq、完成状态和失败原因，不复制 `tool.output` 原始内容。当前仍缺生产 secret resolver 和更细风险策略。

HTTP dispatcher 已经把完整 `RuntimeSkill` 下发给 Runtime。Redis queue 路径已经收敛为最小 `run_id/attempt_id` payload，并由 Agent Runtime worker 通过 Control Plane execution context API 拉取当前授权后的完整 `RuntimeSkill`，因此 HTTP Skill 执行材料不再依赖 queue payload。

已落地能力：

- Postgres/memory skill registry、版本、grant 和 secret 引用/本地密文结构。
- 系统 skill 与用户 HTTP Skill 分组查询、创建、更新、启停 API。
- HTTP Skill manifest、runtime_config、JSON Schema、URL 和 auth 配置校验。
- Runtime `ToolBridge` materialize 授权后的 `RuntimeSkill`，支持 builtin、HTTP Skill、secret redaction、`env://` / `file://` secret_ref、入参/出参 schema validation、结构化 observation、SSRF 基础拦截、retry、进程内 per-skill rate limit 和可选 Redis 跨副本 rate limit。
- Redis queue worker 可通过 execution context 拉取完整 skill manifest，不依赖 queue payload 携带 secret 或大对象。
- OpenAPI JSON/YAML preview dry-run API 已能生成候选 HTTP Skill；保存 API 已能把选中的 operation 创建为用户 HTTP Skill，并绑定 bearer secret。
- MCP tools/list manifest preview API 与前端预览面板已能生成候选 skill manifest，并保留 MCP annotations；保存 API 已能把选中的 tool 创建为用户 MCP Skill；Runtime 已能通过最小 HTTP JSON-RPC `tools/call` adapter 执行。
- `tool.started`/`tool.finished` 会进入 `audit_events` 和 `skill_invocations`，提供 run 级 skill invocation 轨迹；审计和 invocation metadata 不包含 tool 原始输入和输出。

仍待落地能力：

- Vault、KMS、External Secrets 等生产 secret resolver。
- 完整 MCP stdio/SSE/streamable HTTP transport、initialize/session negotiation 和动态 tool list invalidation。
- 更细粒度风险策略和 Redis rate limit 容量/告警。

## 扩展点

- 增加 `SkillManifest` 领域模型，保留现有 JSON 字段兼容，同时内部用 typed config 解析。
- 增加 `SecretResolver` 接口，支持 `local_encrypted`、`env://` secret_ref、Vault、阿里云 KMS、Kubernetes Secret 引用。
- 增加 `SkillMaterializer`：Control Plane 按 user/project/grant 读取当前版本 manifest，并只在内部请求中附带 Runtime 必需 secret。
- 拆出 Runtime `SkillExecutionService` 和 HTTP Skill executor，统一校验、调用、错误分类、redaction 和事件输出。
- 增加 OpenAPI/MCP importer：OpenAPI JSON/YAML preview/dry-run 与选中 operation 保存已落地；MCP manifest preview、选中 tool 保存、前端导入和 Runtime 最小 HTTP JSON-RPC adapter 已落地；下一步补完整 MCP transport/session 支持和更复杂的 secret 绑定 UI。
- 继续加固 Redis queue 路径：当前 worker 已按 `run_id` 拉取当前授权后的 manifest，下一步需要配合 attempt fencing 固化授权快照和幂等边界。

## 技术架构

推荐链路：

```text
用户配置 HTTP Skill
  -> Control Plane 校验 schema/runtime_config
  -> 保存 skill + skill_version + skill_grant + secret_ref
  -> 用户发起 Run
  -> Control Plane materialize 当前可用 RuntimeSkill
  -> Agent Runtime 构造 Eino tools
  -> ToolBridge 校验 arguments
  -> SkillExecutor 执行 builtin/http/mcp
  -> 写入 tool.started/tool.output/tool.finished
  -> 模型基于 observation 生成最终回复
```

HTTP Skill observation 建议统一为：

```json
{
  "ok": false,
  "status_code": 502,
  "error_type": "upstream_timeout",
  "message": "HTTP skill request timed out",
  "data": null
}
```

event/log 中只允许出现脱敏后的 endpoint、status、duration、size、error_type，不写入 bearer token、Authorization header 或完整 secret。

## 技术方案

第一版不要大改协议字段，先在 Control Plane 和 Runtime 内部增加 parser/validator：

- 创建/更新 HTTP Skill 时校验 `input_schema` 和 `output_schema` 是合法 JSON Schema。
- `runtime_config` 解析为 typed struct，并校验 method、url、timeout、auth_type。
- Runtime 调用前用 schema 校验 arguments，失败则返回 agent 可读 observation，不发起 HTTP 请求。
- Runtime 调用 HTTP Skill 时禁用或限制 redirect，默认拒绝私网、link-local、metadata IP。
- 非 2xx、DNS、TLS、timeout、响应过大、输出 schema 不匹配统一映射成结构化错误。
- HTTP Skill 可配置 `retry.max_attempts`，只对 429、5xx 和网络/超时类错误重试，并在 observation 中返回 `retry_count`。
- HTTP Skill 可配置 `rate_limit.requests_per_minute`，按 Runtime 进程内或 Redis 跨副本 skill id 做固定窗口限流，命中时返回 `rate_limited` observation。
- Secret 解析从 repository 读取迁移到 `SecretResolver`，repository 只负责返回 secret ref 或本地开发密文。

OpenAPI/MCP 导入放在下一层：

- OpenAPI JSON/YAML preview 已先把 operation 转成 HTTP Skill 候选项，并能通过保存接口把用户选择的 operation 转成 `HTTPSkillInput` 后创建 skill。
- MCP tool -> NiceAgent skill manifest preview 已能保留 MCP annotations，前端可做 dry-run 预览；保存接口已能按 `tool_name` 创建 `kind=mcp` 用户 skill；Runtime 已能通过 HTTP JSON-RPC `tools/call` 执行该 skill。
- 导入必须先生成 preview，由用户选择 operation 和绑定 secret，再保存；后端保存接口会重新解析文档，避免信任前端候选项。

## 分阶段落地

1. 最小生产闭环：schema validation、HTTP Skill 错误模型、secret redaction、Runtime 输入校验。
2. Secret resolver：本地开发继续支持 `encrypted_value`，`env://` 和 `file://` secret_ref 已可用；生产继续补阿里云 KMS/Vault/External Secrets 原生 resolver。
3. 导入能力：OpenAPI JSON/YAML dry-run API 与最小保存向导已落地；MCP manifest preview、保存 API、前端导入入口和 Runtime 最小 HTTP JSON-RPC 执行 adapter 已落地；继续实现完整 MCP transport/session 支持和更完整前端导入体验。
4. 治理能力：Runtime 进程内和 Redis 跨副本 per-skill rate limit 已有最小闭环；`audit_events` 和 `skill_invocations` 已记录脱敏 skill invocation 起止轨迹；后续继续补风险策略和更完整 metrics/tracing。

## 风险与验收

风险：

- secret 进入 run event、日志或前端 API。
- HTTP Skill SSRF 打到内网、metadata service 或本机服务。
- 第三方 API 返回 prompt injection 或超大响应。
- annotations 被误当成强安全策略。
- Redis queue worker 拉取 execution context 失败时会保留 pending entry；当前已有 heartbeat、idle pending reclaim 和 DLQ。Redis skill rate limit 会在 Redis 异常时 fail closed，后续风险在于生产容量、外部告警和第三方 API 突发流量策略。

验收：

- `GET /api/skills`、run events、日志均不包含 bearer token。
- invalid JSON Schema 创建/更新返回 400。
- invalid tool arguments 不发起 HTTP 请求。
- timeout、DNS、TLS、非 2xx、invalid output 均转成结构化 tool observation。
- HTTP Skill 默认拒绝 private/link-local/metadata IP，并在 DNS 解析后再次拦截解析到私网/本机/metadata 类地址的 host。
- HTTP Skill 对 429、5xx、网络/超时错误按配置重试，且 `retry_count` 可观测。
- HTTP Skill 命中 per-skill rate limit 时返回 `rate_limited` observation 且不发起请求；`SKILL_RATE_LIMIT_MODE=redis` 时多个 Runtime 副本共享同一 Redis 计数窗口。
- OpenAPI JSON/YAML preview 不创建 skill、不保存 secret，只返回可供用户选择的 HTTP Skill 候选项；保存接口会按选中 operation 单独创建 skill。
- MCP preview 不创建 skill、不保存 secret，只返回候选 skill manifest；保存接口会重新解析 manifest、按 `tool_name` 选择 tool，并创建 `kind=mcp` 用户 skill；Runtime MCP adapter 调用 `tools/call` 时不会把 bearer token 写入 run event、audit、invocation 或前端响应；MCP annotations 只作为提示，不作为安全边界。
- `tool.output` 原始内容不会写入 `audit_events` 或 `skill_invocations`；审计和 invocation 列表只暴露脱敏 metadata。
- HTTP dispatcher 和未来 queue dispatcher 都能让 Runtime 拿到完整授权后的 skill manifest。

## 参考资料

- [MCP Schema Reference](https://modelcontextprotocol.io/specification/2025-11-25/schema)
- [OpenAPI Specification 3.1.1](https://spec.openapis.org/oas/v3.1.1.html)
- [JSON Schema Draft 2020-12](https://json-schema.org/draft/2020-12)
- [External Secrets Operator](https://external-secrets.io/latest/)
- [Vault Transit](https://developer.hashicorp.com/vault/docs/secrets/transit)
- [阿里云 KMS SDK/Secret SDK](https://www.alibabacloud.com/help/en/kms/key-management-service/developer-reference/sdk-user-guide/)
