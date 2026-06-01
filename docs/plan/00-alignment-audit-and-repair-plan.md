# docs/plan 与当前系统对齐审计及修复计划

本文对照 `docs/plan/` 下六条生产化主线与当前仓库实现，判断功能与架构是否一致，并给出后续修复计划。

结论：当前系统已经实现了六条主线里的多项“最小闭环”，但还没有完全达到 `docs/plan/` 描述的目标态。主要问题分两类：

- 文档现状过期：部分专题文档仍写着“尚未实现”，但代码已经落地，例如 artifact 表/API/前端、Playwright smoke、`AUTH_MODE=demo|trusted-header|oidc` 边界、audit events、SSE replay、Go 1.23 基线。
- 架构能力缺口：Redis queue 已补上最小 Runtime consumer 闭环，attempt/lease/fencing 也已有权威字段、回写校验、heartbeat 续租、idle pending auto-claim 和 DLQ；`workspace.read` 已能列出 artifact、读取文本 artifact，并返回 workspace/artifact 元数据摘要；Sandbox Executor 已有 K8s 基础 NetworkPolicy、ResourceQuota、LimitRange 和 securityContext；三服务已具备轻量 `X-Trace-ID`/`X-Request-ID` 传播、结构化 request log、`/metrics` 指标、基础 Prometheus 告警规则、OpenTelemetry OTLP HTTP exporter、入站 HTTP span、Control Plane 调度/回写、Runtime run/tool/model、HTTP Skill、Sandbox HTTP/exec、Redis queue 处理 span、Redis 低层命令级 span 和 Postgres repository 低层命令级 span 边界；quota 已有项目 policy、Redis 并发/小时预占、模型 token 预扣/结算、tool/sandbox 最小实时预占和按 provider/model 的项目 usage 聚合查询；但 OIDC 还只是 header 边界，真实 tokenizer、强一致账单级 quota、真实 DeepSeek 冒烟、复杂模型路由仍待补齐。

## 当前对齐度

| 主线 | 当前对齐度 | 已对上的能力 | 没对上的能力 |
| --- | --- | --- | --- |
| Skill 执行生产化 | 部分对齐 | HTTP Skill schema/runtime_config 校验、Runtime 入参/出参校验、SSRF 基础拦截、DNS 解析后私网地址拦截、SecretResolver 接口、本地 secret/redaction、`env://` secret_ref、结构化 observation、per-skill retry、Runtime 进程内 rate limit、OpenAPI JSON/YAML preview dry-run；Redis queue worker 可通过 execution context materialize 完整 `RuntimeSkill` | KMS/Vault/External Secrets 原生 backend 未接；OpenAPI 完整保存向导和 MCP 导入未做；跨副本强一致 skill rate limit 和更细审计未做 |
| Sandbox 与 Artifact | 中度对齐 | workspace 登记、artifact 表/API/download、前端 artifact 展示、sandbox 输出扫描、path/symlink 越界检查、container executor 可配置；`workspace.read` 可列出当前 run artifacts、读取已登记文本 artifact 摘要，并返回 workspace/artifact 元数据摘要；K8s 已有基础 `NetworkPolicy`、`ResourceQuota`、`LimitRange` 和 Sandbox Executor `securityContext` | Compose 默认仍是 local executor；artifact 只在 run complete 时持久化和发事件；K8s RuntimeClass、独立节点池、egress policy 和强隔离方案未落地 |
| 平台认证、权限与可观测 | 部分对齐 | `AUTH_MODE=demo|trusted-header|oidc` 边界、`ActorContext`、可信邮箱/name header、可信 `issuer + subject` 到 `user_identities` 的绑定、读写路径基础隔离、`X-Request-ID` 三服务传播、三服务结构化 request log、轻量 `X-Trace-ID` 传播、标准 `traceparent` 传播、三服务 `/metrics`、基础 Prometheus 告警规则、可选 OpenTelemetry OTLP HTTP exporter、入站 HTTP server span、Control Plane 调度/回写 span、Runtime run/tool/model span、HTTP Skill span、Sandbox HTTP/exec span、Redis queue 处理 span、Redis Streams / PubSub / quota counter 低层命令级 span、Postgres repository `db.command` span、audit_events 表/API、audit redaction、trusted-header 最小 RBAC、`organization_members`/`project_members` 持久成员表、当前组织/项目成员管理 API、邀请创建/接受最小闭环、邀请接受邮箱 claim 匹配、组织成员 API 的持久角色解析、同组织项目 API 继承组织角色、项目级持久 quota policy、最小 run quota、Redis 并发/小时窗口 quota 预占、固定或动态模型 token 预扣/结算；`RunUsage` 已记录 tool/sandbox/artifact 聚合用量；估算 token 会持久化 `token_estimator=heuristic_rune_div4`；每日模型 token、tool calls、sandbox seconds quota 已落地，Runtime 调用 tool 前也会做最小 tool/sandbox 实时预占；项目 usage 可按 provider/model/currency/估算来源聚合查询；非本地或显式 required 模式会强制 `INTERNAL_API_TOKEN` | 真 OIDC 登录/JWT/session 未接；邮件发送已有可选 SMTP 最小闭环但缺投递模板/退信/队列化；quota 仍缺真实 tokenizer/按模型动态估算和强一致账单级 quota；告警路由/值班系统未接 |
| 多实例队列与事件流 | 高度对齐但仍有小偏差 | SSE `id`、`Last-Event-ID`、`?after=`、前端 seq 去重；Redis Streams `XADD/XREADGROUP/XACK` adapter；`DISPATCH_MODE=redis` 入队；`RUNTIME_QUEUE_MODE=redis` Runtime worker；queue payload 最小化；execution context 内部 API；attempt claim、lease 字段和 callback fencing；worker heartbeat 续租、`XAUTOCLAIM` 回收 idle pending、DLQ；主 queue stream 和 DLQ stream 支持可配置近似裁剪；Redis worker 暴露 message/reclaim/ack/error/DLQ counter，并采样 pending entries 与 DLQ length gauges；Redis nudge fanout 已能唤醒多 Control Plane SSE 副本；`make smoke-three-services-redis` 会启动临时 Redis 和两个 Runtime consumer，并自动校验 run 被不同 consumer claim；`make smoke-control-plane-fanout` 会启动临时 Postgres/Redis 和两个 Control Plane 进程，验证第二个 Control Plane 能收到第一个 Control Plane 写入的 run SSE 事件；上述两个 Redis smoke 已纳入 CI 默认门禁 | 生产级 Redis 高可用、外部告警和容量压测仍待补 |
| 前端产品体验与 E2E | 高度对齐但仍有小偏差 | React/TS/SCSS Modules、聊天优先、artifact 展示/下载、HTTP Skill 表单校验、保存状态、Playwright mock smoke、三服务真实 UI smoke、SSE replay 去重；断线重连中和恢复补齐状态已折叠进 agent 状态气泡；HTTP Skill URL 已同步为仅允许 `https`；`make smoke-three-services` 可启动三服务真实进程做 `/cli echo hello` 冒烟；`make smoke-three-services-ui` 可构建前端并由 Control Plane 托管静态产物，做真实浏览器 smoke | Redis dispatcher 冒烟需要外部 Redis；更复杂的 SSE 断线重连真实服务场景仍待补强 |
| 模型运营与 DeepSeek | 部分对齐 | Eino 原生 `ToolCallingChatModel`、OpenAI-compatible provider、`MODEL_PROVIDER_PROFILE=deepseek` 配置预设、错误分类、retry transport、单一后备 provider fallback、usage tracker、redactor、run_usage 持久化、配置化 cost/pricing、provider health 快照、模型运行指标、可选主动 provider 探针和基础告警指标；provider 缺失 usage 时会标记 `estimated=true` 和 `token_estimator` | 未完成真实 DeepSeek API 冒烟；复杂多 provider 路由未做；真实告警系统接入未做；真实 usage 依赖 provider callback，缺失时仍估算 |

## 当前系统证据

以下是本次审计确认过的当前实现位置：

- Skill manifest 校验：`packages/common/skillmanifest/*`
- Runtime HTTP Skill 执行：`services/agent-runtime/internal/tools/http_skill.go`
- Control Plane skill API：`services/control-plane/internal/httpapi/server.go`
- workspace/artifact 协议与表：`packages/common/protocol/workspace.go`、`migrations/004_artifacts_and_workspace_metadata.sql`
- artifact API/download：`services/control-plane/internal/httpapi/server.go`
- sandbox artifact 扫描：`packages/common/sandbox/executor.go`
- container executor：`packages/common/sandbox/container.go`
- auth/audit 边界：`services/control-plane/internal/app/interfaces.go`、`packages/common/protocol/audit.go`、`migrations/006_audit_events.sql`
- Redis queue adapter：`services/control-plane/internal/dispatch/queue.go`
- SSE replay：`services/control-plane/internal/httpapi/server.go`、`frontend/src/app/useNiceAgentWorkspace.ts`
- 前端 artifact/E2E：`frontend/src/features/artifacts/*`、`frontend/e2e/smoke.spec.ts`
- 模型运营骨架：`services/agent-runtime/internal/modelprovider/*`

## 关键不一致

### 1. `docs/plan/` 的“当前仓库现状”已经部分过期

`01` 到 `06` 中多处仍描述为“尚未实现”，但现在已经实现了对应最小闭环。例如：

- `02-sandbox-artifact.md` 仍写“没有 artifacts 表、前端没有 artifact 展示区”，但当前已有 migration、repository、API 和 UI。
- `03-platform-auth-observability.md` 仍写“外部 API 硬编码 demo-user、没有 audit_events 表”，但当前已有 `AUTH_MODE`、`ActorContext` 和 audit API。
- `05-frontend-product-e2e.md` 仍写“没有 Playwright 依赖和 E2E 目录”，但当前已经有 `frontend/e2e/smoke.spec.ts`。

修复方向：先把六份专题规划改成“已落地 / 部分落地 / 待落地”三段，避免后续开发者基于过期现状做重复设计。

### 2. Redis queue 架构还不能真正替代 HTTP dispatcher

当前 `DISPATCH_MODE=redis` 会把 run 写进 Redis Streams，Agent Runtime 在 `RUNTIME_QUEUE_MODE=redis` 下已经可以消费同一个 stream/group，并通过 Control Plane 内部 API 拉取完整 execution context。因此 Redis queue 已从“只入队”推进到最小可执行闭环。

已补齐：Redis pending entries 支持 `XAUTOCLAIM` 和 DLQ；执行期间会按 heartbeat 续租 run lease；Control Plane 可用 `EVENT_FANOUT_MODE=redis` 发布轻量 nudge，其他副本收到后仍从 repository 按 `seq` 补读权威事件。`make smoke-three-services-redis` 已经是本地 Redis 多 Runtime consumer 冒烟入口，会启动临时 Redis、两个 Runtime consumer、发送两个 run，并自动校验每个 run 的 `claimed_by` 属于预期 consumer 且多 consumer 实际参与。`make smoke-control-plane-fanout` 已经是多 Control Plane 进程级 SSE fanout 冒烟入口，会启动临时 Postgres、Redis、两个 Control Plane、Agent Runtime 和 Sandbox Executor，请求从第一个 Control Plane 创建 run，第二个 Control Plane 订阅同一 run 的 SSE，并验证 `tool.output`、`model.token` 和 `run.succeeded` 经 Redis nudge + 共享 Postgres 到达。两个 smoke 已进入 GitHub `Test and build` 默认门禁。主 queue stream 可通过 `RUN_QUEUE_MAX_LEN` 近似裁剪，DLQ stream 可通过 `RUN_QUEUE_DLQ_MAX_LEN` 近似裁剪。Agent Runtime `/metrics` 已暴露 Redis worker message、reclaim、ack、error、DLQ counters、pending entries 和 DLQ length gauges，可用于最小告警。仍待修复：生产级 Redis 高可用、外部告警和容量压测仍待补。

### 3. attempt/lease/fencing 已有权威状态，但恢复策略仍不完整

`Run`、`RunRequest`、`RunCompleteRequest` 已有 `attempt_id` 字段，数据库 `runs` 表也已补上 `active_attempt_id`、`claimed_by`、`lease_expires_at` 和 `attempt_count`。Runtime 执行前会调用 `/internal/runs/{run_id}/claim`，内部 `/complete`、`/fail`、`/events` 会校验 `attempt_id`，旧 runtime 的迟到回写会被 `409 Conflict` 拒绝。

已补齐：lease 过期后的 `XAUTOCLAIM` 会生成新的 `attempt_id` 重新 claim；heartbeat 会续租当前 attempt；超过最大投递次数的 pending message 会进入 DLQ。

### 4. `workspace.read` 与 artifact/workspace 已有只读闭环

artifact 已经能创建、列表、下载，Runtime 内的 `workspace.read` 也已支持列出当前 run artifacts、返回 workspace/artifact 元数据摘要，并可读取已登记文本 artifact 的内容摘要。读取走 Control Plane 内部 API，复用 artifact metadata、workspace path、`output/` 限制、MIME 限制和 symlink escape 检查。

仍待修复：artifact 生成中的增量可见性、更丰富的 artifact 类型预览还没有完成。

### 5. Auth/Observability 还只是平台边界，不是生产能力

当前 `AUTH_MODE=trusted-header` 依赖可信上游传 `X-NiceAgent-User-ID` 和 `X-NiceAgent-Project-ID`，可选传 `X-NiceAgent-User-Email`、`X-NiceAgent-User-Name`、`X-NiceAgent-Identity-Issuer`、`X-NiceAgent-Identity-Subject`，`AUTH_MODE=oidc` 只是兼容别名；系统并没有真实 OIDC login/session/JWT 校验。trusted-header roles 已有最小 RBAC：`viewer` 只读，`owner/admin/member/editor/writer` 可写，组织/项目成员管理只允许 `owner/admin`；如果 header 未传 roles，普通项目 API 会优先从 `project_members` 读取持久角色绑定，组织成员 API 会从 `organization_members` 读取持久角色绑定；当 `projects.organization_id` 与 actor 的 `OrgID` 匹配时，项目 API 也可以继承组织角色。当前组织/项目成员管理 API 已支持列出、添加/更新、移除 `organization_members` 和 `project_members`；邀请流程已有最小闭环：管理员可创建组织或项目邀请，已认证 actor 可用 token 接受邀请并写入对应 membership，接受时要求可信邮箱 claim 与邀请邮箱一致。如果上游传入 `issuer + subject`，Control Plane 会持久绑定到 `user_identities`，并拒绝同一外部身份跨用户绑定或同一用户同 provider 换绑。邮件发送已有可选 SMTP 最小闭环；NiceAgent 内置 OIDC 登录、投递模板/退信/队列化和更细 action policy 仍未完成。最小 run quota 已可按 `user_id + project_id` 限制并发 run、每小时 run 数和 UTC 自然日模型 token/tool/sandbox 用量；项目级持久 quota policy 已通过 `project_quota_policies` 和 `GET/PATCH /api/projects/{id}/quota` 落地，`QUOTA_COUNTER_MODE=redis` 已能对并发 run 和每小时 run 数做 Redis 预占，并在 run 终态释放并发占用；模型 token 预扣支持 fixed 和 dynamic 两种模式，dynamic 会按用户消息长度估算 input tokens，并叠加输出缓冲，run 完成后按真实 `RunUsage.total_tokens` 结算差额。`RunUsage` 已追加 tool/sandbox/artifact 聚合字段，可记录 tool 调用数、sandbox 命令数、sandbox 耗时/输出/资源和 artifact 数量/大小；`max_tool_calls_per_day` 和 `max_sandbox_seconds_per_day` 已能按已有 `RunUsage` 限制后续 run 创建，Runtime 每次 tool 调用前还会通过内部 quota reserve 预占工具调用和 sandbox 秒数；`GET /api/projects/{id}/usage` 已能按 provider、model、currency、`estimated` 和 `token_estimator` 做项目级 usage 聚合。真实 tokenizer/按模型动态估算和强一致账单级 quota 仍未完成。三服务已返回并传播 `X-Trace-ID`、`X-Request-ID` 和标准 `traceparent`，通过 JSON request log 记录 `request_id`、`trace_id`、HTTP 方法、路径、状态和耗时，通过 `/metrics` 输出 HTTP 请求、run、quota、runtime 和 sandbox 的最小指标，并提供 `deployments/monitoring/prometheus-alerts.yml` 作为基础 Prometheus 告警规则；同时支持 `OTEL_TRACES_EXPORTER=otlp` 时把入站 HTTP server span、Control Plane 调度/回写、Runtime run/tool/model、HTTP Skill、Sandbox HTTP/exec、Redis queue 处理 span、Redis Streams / PubSub / quota counter 低层命令级 span，以及 Postgres repository `db.command` span 导出到 OTLP HTTP collector；告警路由/值班系统仍未接入。内部服务鉴权已支持 `INTERNAL_API_TOKEN_REQUIRED=true`，且 `NICEAGENT_ENV` 为非本地值时会默认强制 token；K8s manifest 默认开启。这个实现可以作为 gateway 后面的边界，但还不能直接对公网承担身份认证。

修复方向：先明确部署模式：若短期放在网关之后，则文档和配置要写成“trusted header auth”；若 NiceAgent 自己做登录，则新增 OIDC callback/session/membership/RBAC。

### 6. 前端与后端对 HTTP Skill URL 策略不一致

后端 `ValidateHTTPSkillInput` 只允许 `https`。前端 `SkillPanel` 已同步该策略，默认只允许 `https`，并拒绝带用户名/密码的 URL。

### 7. 模型运营还有“骨架已落、生产未闭环”的差距

当前 provider 已有 retry、错误分类、usage、redaction、单一后备 provider fallback、配置化 pricing/cost、`/healthz` 模型健康快照、`/metrics` 模型运行指标，以及可选主动 provider 探针。探针默认关闭，打开后会发起真实模型调用，并通过 `model_provider.probe_*` 字段和 `niceagent_model_health_probe_*` 指标暴露结果。provider 缺失 usage 或 mock provider 会回退到共享估算器，并在 `RunUsage` 中持久化 `estimated=true` 与 `token_estimator=heuristic_rune_div4`，方便后续替换成真实 tokenizer。DeepSeek 已有 `MODEL_PROVIDER_PROFILE=deepseek` 配置预设：复用 OpenAI-compatible provider，默认补齐官方 base URL，并把 usage provider 标记为 `deepseek`；模型名仍要求显式配置并以官方文档为准。还没有真实 DeepSeek 冒烟记录、复杂多 provider 路由和外部告警系统接入。

## 修复 PR 顺序

### PR 1：同步规划文档与实际状态

目标：

- 更新 `docs/plan/01..06` 的“当前仓库现状”，改成真实状态。
- 每份文档增加“已落地能力”和“仍待落地能力”。
- 保留长期目标，不把当前半成品写成生产完成。

验收：

- `rg "尚未|没有|未实现" docs/plan` 的命中必须逐条确认是否仍真实。
- `docs/plan/README.md` 明确本审计文档是当前对齐入口。

### PR 2：Redis queue 可执行闭环（已完成最小闭环）

已完成：

- Agent Runtime 增加 `RUNTIME_QUEUE_MODE=redis` worker。
- Control Plane 增加内部 API：`GET /internal/runs/{run_id}/execution-context`，返回用户消息、run、control plane url、授权后的 `RuntimeSkill`。
- Redis queue payload 收敛到 `run_id`、`attempt_id`、`enqueued_at`。
- worker 成功完成后 `XACK`，获取 execution context 失败时保留 pending。

真实 Redis/三服务冒烟已补强：

- 本地已新增 `scripts/smoke_three_services.py`，HTTP dispatcher 三服务进程冒烟可执行；Redis dispatcher 可通过 `make smoke-three-services-redis` 启动临时 Redis、两个 Runtime consumer 和两个 run，并校验 `claimed_by` 分布。
- 两个 Runtime consumer 并发消费时 run 不重复完成，并且至少两个 consumer 实际 claim 到工作。
- HTTP dispatcher 路径不回退。
- CI 默认执行 `make smoke-three-services-redis` 和 `make smoke-control-plane-fanout`。

### PR 3：attempt/lease/fencing（已完成权威字段、回写校验和 worker 恢复）

已完成：

- 增加 migration：`runs.active_attempt_id/claimed_by/lease_expires_at/attempt_count`。
- Runtime claim run 后才执行。
- `/internal/runs/{id}/events|complete|fail` 校验 `attempt_id`。
- 旧 attempt 的 event/complete/fail 返回 `409 Conflict`，不会写 assistant message 或 terminal event。

已补强：

- `XAUTOCLAIM` 后会使用新 attempt 执行。
- 超过最大投递次数的 pending message 写入 DLQ。
- Runtime 执行期间会按 heartbeat 续租 lease。

### PR 4：workspace.read 只读闭环（已完成基础闭环）

已完成：

- Runtime `workspace.read` 支持列出当前 run artifacts。
- 可读取已登记 artifact 的文本内容摘要，限制大小和 MIME。
- 可返回当前 run/workspace 的 artifact metadata 摘要，不读取文件内容。
- 所有读取走 Control Plane artifact metadata 和 workspace path 安全校验。

仍待验收/后续补强：

- 跨用户/跨项目 artifact 不可读。
- 非文本 artifact 预览。

### PR 5：Auth/RBAC/Quota/OTel 生产化补齐

目标：

- 明确并实现二选一模式：
  - `AUTH_MODE=trusted-header`：只允许来自可信网关，文档说明边界。已完成基础模式。
  - `AUTH_MODE=oidc`：当前是 trusted-header 兼容别名；NiceAgent 自己做 OIDC callback/session/JWT 仍待实现；邀请邮件已具备可选 SMTP 最小路径。
- 非本地或显式 required 模式强制 `INTERNAL_API_TOKEN` 已完成；后续仍需要把 token rotation 和 Secret backend 纳入运维流程。
- 新增 membership/RBAC 表和基础角色已完成最小版本：`organization_members`、`project_members`、`invitations`、`user_identities`，并支持 trusted-header 缺少 roles 时从持久 membership 解析角色；当前组织/项目成员管理 API 已有最小闭环，组织成员 API 和同组织项目 API 可从 `organization_members` 解析持久角色；邀请创建/接受已有最小闭环，接受时会校验可信邮箱 claim；可信 `issuer + subject` 可持久绑定到内部用户并拒绝冲突；可选 SMTP 邀请邮件已有最小闭环；NiceAgent 内置 OIDC 登录、投递模板/退信/队列化和更细粒度 action policy 仍待补齐。
- 增加最小 quota：`concurrent_runs`、`runs_per_hour`、`model_tokens_per_day`、`tool_calls_per_day`、`sandbox_seconds_per_day` 已完成；项目级持久配置模型已完成最小闭环；Redis 并发/小时窗口预占、固定/动态模型 token 预扣/结算、run 级 tool/sandbox/artifact 用量记录和 Runtime tool/sandbox 最小实时预占已完成；估算 token 已通过共享 `TokenEstimator` 边界和 `RunUsage.token_estimator` 标记来源；项目 usage 已支持按 provider/model/currency/估算来源聚合查询。真实 tokenizer/按模型动态估算和强一致账单级 quota 仍待补齐。
- 三服务已接入轻量 `X-Trace-ID`、`X-Request-ID`、标准 `traceparent`、结构化 request log、`/metrics`、基础 Prometheus 告警规则、OpenTelemetry OTLP HTTP exporter、入站 HTTP server span、调度/回写、run/tool/model、HTTP Skill、Sandbox、Redis queue span、Redis 低层命令级 span 和 Postgres repository `db.command` span；后续仍需补告警路由/值班系统。

验收：

- 不同用户/项目不能互相读 chat/run/artifact/skill/audit。
- quota deny 可在前端显示中文错误。
- 用 `run_id`、`request_id` 或 `trace_id` 能串起 request log、audit event 和跨服务调用；服务间调用能透传 `X-Request-ID`、`X-Trace-ID` 和 `traceparent`；配置 `OTEL_TRACES_EXPORTER=otlp` 后至少能看到三服务入站 HTTP、调度/回写、run/tool/model、HTTP Skill、Sandbox、Redis queue、Redis 低层命令级 span 和 Postgres repository `db.command` span；`make check-alerts` 能校验基础 Prometheus 告警规则。告警路由/值班系统仍是后续验收项。

### PR 6：前端策略一致性与三服务 E2E

目标：

- 前端 HTTP Skill URL 校验改为默认只允许 `https`。
- Playwright 增加三服务集成 smoke：构建前端，启动 Control Plane、Agent Runtime 和 Sandbox Executor，由 Control Plane 托管静态产物，覆盖 `/cli echo hello`、artifact list API 和刷新恢复。
- 已新增非 Playwright 的三服务进程 smoke，覆盖 `/cli echo hello`；`make smoke-three-services-ui` 已补上真实浏览器 UI smoke。更复杂的 SSE 断线重连场景仍待补。
- UI 已增加更明确的重连/恢复状态：断线时显示“连接暂时中断，正在重连”，恢复时显示“连接已恢复，正在补齐事件”，Playwright mock smoke 会模拟断线、重放重复 seq，并验证 token 不重复追加。

验收：

- `npm run smoke:e2e` 继续通过 mock smoke。
- 新增集成 smoke 在本地可运行，CI 可先设为手动或 nightly。
- 前后端 URL 校验一致。

### PR 7：模型运营闭环

目标：

- DeepSeek 接入文档只保留稳定配置原则，不硬编码易过期模型名。
- `MODEL_PROVIDER_PROFILE=deepseek` 的配置模板和 smoke checklist 已补齐，真实 API key 冒烟仍待执行。
- model pricing 已有环境变量配置，下一步可以按 provider/model/version 持久化为配置表。
- 增加 fallback config 已完成单一后备 provider 版本，默认关闭；provider health 快照、基础 metrics 和可选主动探针已完成，后续再做复杂路由、真实 DeepSeek 冒烟和外部告警系统接入。

验收：

- fake DeepSeek/OpenAI-compatible server 覆盖普通回复、tool calling、401、402、429、503。
- 真实 API key 冒烟结果记录在本地文档模板或运维 checklist，不提交 key。
- usage/cost 可按 run 查询，也可按项目窗口聚合；估算值明确标记 `estimated=true` 和 `token_estimator`。

## 推荐近期行动

建议下一步先做 PR 1 和 PR 6 的小修，再进入 PR 2/3 的队列架构：

1. PR 1 能立刻消除文档与代码现状不一致，避免后续重复劳动。
2. PR 6 修掉用户可见的前后端策略不一致，并把 E2E 从 mock 推向真实链路。
3. 下一条真正的平台化关键路径是 NiceAgent 内置 OIDC/session/JWT、邮件投递模板/退信/队列化、真实 tokenizer/按模型动态估算与强一致账单级 quota，以及告警路由/值班系统。

## 最小验收命令

每个修复 PR 至少执行：

```bash
PATH=/usr/local/go/bin:$PATH GOCACHE=/private/tmp/niceagent-go-cache make test
make check-js
make compose-config
git diff --check
```

涉及 Docker/部署时额外执行：

```bash
make docker-build IMAGE_REGISTRY=niceagent-ci IMAGE_TAG=<branch>
```

涉及前端体验时额外执行：

```bash
cd frontend && npm run smoke:e2e
```
