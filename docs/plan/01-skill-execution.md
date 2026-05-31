# Skill 执行生产化

## 产品功能

Skill 是 NiceAgent 的能力系统。用户看到的是“系统能力”和“我的能力”，Agent Runtime 看到的是 Control Plane 按用户/项目授权后下发的 `RuntimeSkill`。生产化目标是让 skill 可注册、可版本化、可授权、可校验、可审计，并且在执行时不会泄露 secret。

第一阶段继续以两类 skill 为主：

- 系统固定 skill：`cli.exec`、`workspace.read` 等平台内置能力，由 Runtime 调内部 executor。
- 用户可添加 skill：先支持 `kind=http`，通过 JSON Schema 描述输入输出，通过 `runtime_config` 描述 HTTP 调用，通过 `skill_secrets` 或外部 secret backend 注入鉴权材料。

## 成熟方案调研

MCP Tool 模型适合做 skill manifest 参考。MCP 的 tool 具有 `name`、`description`、`inputSchema`、可选 `outputSchema` 和 `annotations`；`annotations` 中的 `readOnlyHint`、`destructiveHint`、`idempotentHint`、`openWorldHint` 可用于描述风险和执行语义，但只能作为模型提示和 UI 提示，不能作为安全边界。参考：[MCP Schema Reference](https://modelcontextprotocol.io/specification/2025-11-25/schema)。

OpenAPI 3.1 适合做 HTTP Skill 导入源。它用 JSON Schema 描述 request/response，并用 `securitySchemes` 表达鉴权机制，适合把一个 API operation 转换成一个 HTTP Skill。参考：[OpenAPI Specification 3.1.1](https://spec.openapis.org/oas/v3.1.1.html)。

JSON Schema 建议统一采用 Draft 2020-12，用于三处校验：创建/更新 skill 时校验 schema 本身，Runtime 调用 skill 前校验 tool arguments，调用后校验 structured output。参考：[JSON Schema Draft 2020-12](https://json-schema.org/draft/2020-12)。

Secret 不应直接混入 skill manifest。Kubernetes Secret 更像运行时投递载体，生产环境应优先使用外部 secret 系统或云 KMS。可选路径包括 External Secrets Operator、Vault Transit、Vault KV、阿里云 KMS Secrets Manager。参考：[External Secrets Operator](https://external-secrets.io/latest/)、[Vault Transit](https://developer.hashicorp.com/vault/docs/secrets/transit)、[阿里云 KMS SDK/Secret SDK](https://www.alibabacloud.com/help/en/kms/key-management-service/developer-reference/sdk-user-guide/)。

HTTP Skill 还要按 SSRF 和第三方 API 风险处理：默认只允许 `https`，限制 host/port/scheme，阻断 loopback、link-local、private CIDR 和云 metadata 地址，控制 redirect、timeout、response size、content-type，并把外部返回作为 observation 而不是直接信任。

## 当前仓库现状

协议层已经有 `Skill`、`RuntimeSkill`、`SkillGroups`、`HTTPSkillInput`，位于 `packages/common/protocol/skill.go`。`Skill` 中的 `InputSchema`、`OutputSchema`、`Annotations`、`RuntimeConfig` 仍以 JSON 字符串形式对外传输，但仓库已新增 `packages/common/skillmanifest`，用于解析和校验 HTTP Skill 的 JSON Schema 与 typed runtime config。

Postgres schema 已有 `skills`、`skill_versions`、`skill_grants`、`skill_secrets`。`input_schema`、`output_schema`、`annotations`、`runtime_config` 是 `jsonb`；`skill_secrets` 同时预留了 `secret_ref` 和 `encrypted_value`。当前 runtime 通过 `SecretResolver` 接口解析 secret，本地实现支持 `encrypted_value`，并支持 `env://ENV_NAME` 形式的 `secret_ref`，可配合 K8s Secret/External Secrets 注入环境变量。Vault、KMS 或 External Secrets 原生 resolver 仍未接入。

Control Plane 已经支持：

- `GET /api/skills` 返回当前 demo 用户的系统/用户 skill 分组。
- `POST /api/skills/http` 创建 HTTP Skill。
- `PATCH /api/skills/{id}` 更新 HTTP Skill。
- `POST /api/skills/{id}/enable|disable` 启停用户 skill。

创建/更新 HTTP Skill 时已经校验 `name`、`GET|POST`、`auth_type`、`https` URL、URL 不含 credentials、timeout 范围、`input_schema` 和 `output_schema`。`runtime_config` 会由 `HTTPSkillRuntimeConfig` 统一生成，避免 token 进入 runtime config。OpenAPI/MCP 导入仍未实现。

Agent Runtime 的 `ToolBridge` 已能把 `RuntimeSkill` 转成 Eino tool。builtin skill 调 `cli.exec` 或 `workspace.read`；HTTP Skill 会按 `runtime_config` 构造请求，支持 bearer token，响应限制为 64KB。Runtime 调用 HTTP Skill 前会校验 arguments；返回后会按 `output_schema` 校验 structured output；非 2xx、DNS、TLS、timeout、响应过大、schema 错误会转成结构化 observation。HTTP Skill 默认只允许 `https`，禁用重定向，拒绝 loopback、`.local`、metadata host 和字面量 private/link-local IP。当前仍缺 DNS 解析后的私网 IP 防护、per-skill retry/rate limit 和导入能力。

HTTP dispatcher 已经把完整 `RuntimeSkill` 下发给 Runtime。Redis queue 路径已经收敛为最小 `run_id/attempt_id` payload，并由 Agent Runtime worker 通过 Control Plane execution context API 拉取当前授权后的完整 `RuntimeSkill`，因此 HTTP Skill 执行材料不再依赖 queue payload。

## 扩展点

- 增加 `SkillManifest` 领域模型，保留现有 JSON 字段兼容，同时内部用 typed config 解析。
- 增加 `SecretResolver` 接口，支持 `local_encrypted`、`env://` secret_ref、Vault、阿里云 KMS、Kubernetes Secret 引用。
- 增加 `SkillMaterializer`：Control Plane 按 user/project/grant 读取当前版本 manifest，并只在内部请求中附带 Runtime 必需 secret。
- 拆出 Runtime `SkillExecutionService` 和 HTTP Skill executor，统一校验、调用、错误分类、redaction 和事件输出。
- 增加 OpenAPI/MCP importer：先做后端转换和 dry-run，不急着做完整 UI。
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
  -> SkillExecutor 执行 builtin/http
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
- Secret 解析从 repository 读取迁移到 `SecretResolver`，repository 只负责返回 secret ref 或本地开发密文。

OpenAPI/MCP 导入放在下一层：

- OpenAPI operation -> `HTTPSkillInput` + `input_schema` + `output_schema` + auth secret binding。
- MCP tool -> NiceAgent skill manifest，保留 MCP annotations。
- 导入必须先生成 preview，由用户选择 operation 和绑定 secret，再保存。

## 分阶段落地

1. 最小生产闭环：schema validation、HTTP Skill 错误模型、secret redaction、Runtime 输入校验。
2. Secret resolver：本地开发继续支持 `encrypted_value`，`env://` secret_ref 已可用；生产继续补阿里云 KMS/Vault/External Secrets 原生 resolver。
3. 导入能力：实现 OpenAPI/MCP manifest 转换器和 dry-run API。
4. 治理能力：skill invocation 审计、per-skill rate limit、风险策略和 metrics/tracing。

## 风险与验收

风险：

- secret 进入 run event、日志或前端 API。
- HTTP Skill SSRF 打到内网、metadata service 或本机服务。
- 第三方 API 返回 prompt injection 或超大响应。
- annotations 被误当成强安全策略。
- Redis queue worker 拉取 execution context 失败时会保留 pending entry；后续需要避免长期 pending 堆积并补 DLQ/claim 策略。

验收：

- `GET /api/skills`、run events、日志均不包含 bearer token。
- invalid JSON Schema 创建/更新返回 400。
- invalid tool arguments 不发起 HTTP 请求。
- timeout、DNS、TLS、非 2xx、invalid output 均转成结构化 tool observation。
- HTTP Skill 默认拒绝 private/link-local/metadata IP。
- HTTP dispatcher 和未来 queue dispatcher 都能让 Runtime 拿到完整授权后的 skill manifest。

## 参考资料

- [MCP Schema Reference](https://modelcontextprotocol.io/specification/2025-11-25/schema)
- [OpenAPI Specification 3.1.1](https://spec.openapis.org/oas/v3.1.1.html)
- [JSON Schema Draft 2020-12](https://json-schema.org/draft/2020-12)
- [External Secrets Operator](https://external-secrets.io/latest/)
- [Vault Transit](https://developer.hashicorp.com/vault/docs/secrets/transit)
- [阿里云 KMS SDK/Secret SDK](https://www.alibabacloud.com/help/en/kms/key-management-service/developer-reference/sdk-user-guide/)
