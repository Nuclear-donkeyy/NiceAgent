# 平台认证、权限与可观测

## 产品功能

NiceAgent 需要从 `demo-user` 演示模式升级为真实多用户平台。产品层要支持：

- 用户登录和退出。
- organization/project 空间。
- project 内会话、run、workspace、artifact、skill 隔离。
- 用户/项目级 skill grant。
- 配额、限流和并发控制。
- 审计日志、metrics、trace，能按 `run_id` 追踪一次 agent 执行。

目标不是自研完整 IAM，而是在 Control Plane 内建立足够清晰的身份、权限和审计边界。

## 成熟方案调研

登录建议使用 OIDC/OAuth2，不自建密码体系。Web 应用可以采用 Authorization Code + PKCE 或后端 confidential client。外部身份的 `issuer + sub` 应作为稳定身份键，`email` 和 `name` 只用于展示和同步。参考：[OpenID Connect Core](https://openid.net/specs/openid-connect-core-1_0-18.html)、[OAuth 2.0 RFC 6749](https://datatracker.ietf.org/doc/rfc6749/)。

权限建议先做应用内 RBAC。organization/project 是 domain，role binding 决定 actor 对 resource/action 的权限；复杂策略后续再接 OPA、OpenFGA 或 Casbin。参考：[Casbin RBAC](https://casbin.org/docs/rbac/)、[Open Policy Agent](https://www.openpolicyagent.org/docs/latest/)。

审计日志应独立于普通应用日志和 run events。它需要 append-only、可查询、可导出，记录 actor、project、action、resource、decision、reason、request_id、trace_id、run_id、ip、user_agent。参考：[OWASP Logging Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html)、[Kubernetes Auditing](https://kubernetes.io/docs/tasks/debug/debug-cluster/audit/)。

可观测建议使用 OpenTelemetry。HTTP、DB、dispatch、model、tool、sandbox 都应建立 span；metrics 和 logs 通过 OTLP Collector 或云厂商采集。模型调用可参考 OpenTelemetry GenAI semantic conventions。参考：[OpenTelemetry Documentation](https://opentelemetry.io/docs/)、[OpenTelemetry Go](https://opentelemetry.io/docs/languages/go/)、[OpenTelemetry GenAI semantic conventions](https://opentelemetry.io/docs/specs/semconv/gen-ai/)。

配额/限流需要两层：边缘限流可交给 API Gateway/Ingress，业务配额必须由 Control Plane 按 user/project/org/model/skill 计量。Redis 可用于分布式 token bucket、sliding window 和并发 run 计数。参考：[Redis rate limiter](https://redis.io/docs/latest/develop/use-cases/rate-limiter/)。

## 当前仓库现状

数据层已经有平台雏形：`users`、`organizations`、`projects` 表；`chat_sessions` 包含 `user_id` 和 `project_id`；`runs` 包含 `user_id`；`skills`、`skill_grants` 也有 user/project 维度。

Control Plane 已新增 `ActorContext` 和 `AUTH_MODE=demo|trusted-header|oidc` 边界。`demo` 模式继续映射到 `demo-user/demo-project`；`trusted-header` 模式要求可信上游已完成 OIDC/session/JWT 校验，并传入 `X-NiceAgent-User-ID` 和 `X-NiceAgent-Project-ID`，可选 `X-NiceAgent-Org-ID`、`X-NiceAgent-Roles`、`X-NiceAgent-User-Email`、`X-NiceAgent-User-Name`、`X-NiceAgent-Identity-Provider`、`X-NiceAgent-Identity-Issuer` 和 `X-NiceAgent-Identity-Subject`。`oidc` 模式已能校验 `Authorization: Bearer <jwt>`，支持 RS256、issuer/audience/exp/nbf、JWKS 拉取和 claims 到 `ActorContext` 的映射；同时已有最小浏览器 OIDC authorization code flow，支持 login callback、HttpOnly session cookie、refresh token 刷新和 logout。

Repository 仍保留偏底层的数据访问接口，权限主要在 HTTP handler 层按 actor 校验。会话、消息、run、events、artifact、skill 和 audit 的外部 API 已有基础 user/project 隔离。`X-NiceAgent-Roles` 已有最小 RBAC：`viewer` 只读，`owner/admin/member/editor/writer` 可写；组织/项目成员管理只允许 `owner/admin`。如果 header/JWT/session 没有 roles，普通项目 API 会优先从 `project_members` 持久角色绑定读取角色；组织成员 API 会从 `organization_members` 读取角色；当 `projects.organization_id` 与 actor 的 `OrgID` 匹配时，项目 API 也可以继承 `organization_members` 中的组织角色。当前已新增 `organization_members`、`project_members`、`invitations`、`user_identities`、`invitation_email_outbox` 和 `invitation_email_suppressions` migration，并种子化 `demo-user/demo-org/demo-project owner`；外部 API 已支持列出、添加/更新、移除当前组织成员和当前项目成员，也支持创建组织/项目邀请并由已认证 actor 接受邀请。邀请接受会校验可信身份中的邮箱 claim 与邀请邮箱一致；如果上游传入 `issuer + subject`，Control Plane 会绑定并校验外部身份不能跨用户换绑。可选 SMTP 邀请邮件、subject/body 模板、进程内内存队列、durable outbox、重试、邀请邮件重发 API、provider-neutral 投递/退信/投诉/丢弃事件记录、HMAC webhook 入口、SendGrid/SES/Mailgun 最小原生字段映射、provider-neutral 自动停发、suppression 查询/解除 API 和前端“邮件治理”管理面板已有最小闭环；服务商原生签名校验还没有落地。

内部服务鉴权已有 `INTERNAL_API_TOKEN` bearer token。默认本地允许为空；当 `INTERNAL_API_TOKEN_REQUIRED=true`，或 `NICEAGENT_ENV` 不是 `local/dev/development/test/ci` 时，三服务都会在缺少 token 时启动失败。Kubernetes manifest 默认开启该检查，并要求 `niceagent-internal-api` Secret 存在。

日志层已有 `slog` JSON request log 和 `X-Request-ID` 生成/透传。仓库已新增 `audit_events` 表、memory/Postgres repository、`GET /api/audit/events` 查询 API，并在 chat/run/skill/artifact/auth deny/quota deny 等关键路径写入 audit event。三服务已新增轻量 `X-Trace-ID` context 和标准 `traceparent` 传播：入口请求可传 `X-Trace-ID` 或 `Traceparent`，Control Plane -> Agent Runtime -> Control Plane callback -> Sandbox Executor 会透传同一个 trace id，Control Plane request log 和 audit event 会记录它。三服务也已提供 `/metrics`，以 Prometheus text exposition 风格暴露 HTTP 请求总数/耗时和最小领域计数。当前已支持 `OTEL_TRACES_EXPORTER=otlp`：启动时会初始化 OpenTelemetry tracer provider，用 OTLP HTTP exporter 上报入站 HTTP server spans；默认 `none`，不影响本地和 CI。内部 spans 已覆盖 Control Plane HTTP dispatcher、Redis run queue enqueue/process/fetch execution context、Runtime run execute、模型调用、tool invoke、HTTP Skill 请求、Runtime 调 Sandbox Executor、Sandbox Executor 命令执行、Runtime 回写 Control Plane、Redis Streams / PubSub / quota counter 低层命令，以及 Postgres repository `db.command`。run quota 已有最小边界：`QUOTA_MAX_CONCURRENT_RUNS`、`QUOTA_RUNS_PER_HOUR`、`QUOTA_MODEL_TOKENS_PER_DAY`、`QUOTA_TOOL_CALLS_PER_DAY`、`QUOTA_SANDBOX_SECONDS_PER_DAY` 作为 env fallback；项目级持久 quota policy 已通过 `project_quota_policies`、`GET/PATCH /api/projects/{id}/quota` 落地。`QUOTA_COUNTER_MODE=redis` 已能对并发 run 和每小时 run 数做 Redis 预占，并在 run 进入终态后释放并发占用；模型 token 预扣支持 fixed 和 dynamic 两种模式，dynamic 按用户消息长度估算 input tokens，并可叠加输出缓冲，run 完成后按真实 `RunUsage.total_tokens` 结算差额。`RunUsage` 已追加 tool/sandbox/artifact 聚合字段，能按 run 记录工具调用数、错误数、sandbox 命令耗时/输出/资源和 artifact 数量/大小；tool calls 和 sandbox seconds 已进入项目级 quota，并且 Runtime 每次 tool 调用前会做最小实时预占；`GET /api/projects/{id}/usage` 已支持按 provider/model/currency/估算来源做项目 usage 聚合。当前 tiktoken 兼容估算已经覆盖常见 OpenAI/DeepSeek 模型；更多 provider 原生 tokenizer 覆盖和强一致账单级 quota 仍待补齐。

已落地能力：

- `AUTH_MODE=demo|trusted-header|oidc`、`ActorContext`、可信 header 模式和 OIDC bearer JWT/JWKS 资源服务器校验。
- user/project/org 基础数据模型，`organization_members`、`project_members`、`invitations`、`user_identities`、最小成员管理 API 和邀请接受闭环。
- 会话、消息、run、event、artifact、skill、audit 外部 API 的基础 user/project 隔离，以及 trusted-header/JWT roles 和持久 membership fallback。
- 内部服务 token 强制策略、`X-Request-ID`、`X-Trace-ID`、`traceparent`、JSON request log、audit events、`/metrics`、Prometheus 告警规则、Alertmanager 路由样例和 OTLP HTTP exporter。
- 项目级 quota policy、Redis 并发/小时预占、模型 token 预扣/结算、tool/sandbox 实时预占、run_usage 聚合和项目 usage 查询。

仍待落地能力：

- 前端登录入口、生产 IdP 联调、session 撤销/轮换策略和更细 action-level policy。
- 邀请邮件 subject/body 模板、进程内内存队列、durable outbox、投递重试、邀请邮件重发 API、provider-neutral delivery/bounce/complaint/drop 事件记录、HMAC webhook 入口、SendGrid/SES/Mailgun 最小原生字段映射、组织级自动停发、suppression 查询/解除 API 和前端“邮件治理”管理面板已落地；更细 action-level policy、服务商原生签名校验和管理后台 UI 仍待补。
- 更多 provider 原生 tokenizer 覆盖、强一致账单级 quota、真实值班系统接入和容量看板。

## 扩展点

- Control Plane 增加 `internal/auth`：解析 session/JWT，产出 `ActorContext`。
- Repository 方法从 `userID string` 扩展到 `ActorContext + projectID`，所有读写都走授权检查。
- 新增身份/权限表：`organization_members`、`project_members`、`invitations`、`user_identities`、`invitation_email_outbox` 和 `invitation_email_suppressions` 已有最小版本，当前组织/项目成员管理 API 和邀请接受 API 已有最小闭环，组织成员 API 和同组织项目 API 可从 `organization_members` 解析持久角色，邀请接受已支持可信邮箱 claim 匹配；可信网关传入 `issuer + subject` 时会绑定外部身份并拒绝冲突；可选 SMTP 邀请邮件、subject/body 模板配置、进程内内存队列、durable outbox、重试、邀请邮件重发 API、provider-neutral 退信事件记录、HMAC webhook 入口、SendGrid/SES/Mailgun 最小原生字段映射、自动停发、suppression 查询/解除 API 和前端“邮件治理”管理面板已补；服务商原生签名校验、管理后台 UI 和更通用的 `role_bindings` 仍待补。
- 新增 `audit_events` 表和 `AuditLogger`。
- 当前最小 quota 支持两种计数路径：默认从 Postgres/memory run 状态和 `run_usage` 统计；`QUOTA_COUNTER_MODE=redis` 时用 Redis 预占 `concurrent_runs`、`runs_per_hour`，并可通过 fixed 或 dynamic 模型 token reservation 对每日模型 token 做预扣/结算。项目级持久配置模型 `project_quota_policies` 已有最小闭环。`run_usage` 已能沉淀 tool/sandbox/artifact 聚合用量，并支持 `tool_calls_per_day`、`sandbox_seconds_per_day` 限额；Runtime 调用 tool 前会通过内部 quota reserve 预占工具调用和 sandbox 秒数；项目 usage 可按 provider/model/currency/估算来源聚合查询。后续再新增更专门的 `quota_usage` 或 billing ledger，支持更多 provider 原生 tokenizer 覆盖和分布式 token bucket。
- `packages/common/platform` 已有 request id、trace context、metrics、log redactor、OpenTelemetry 初始化、OTLP HTTP exporter、HTTP server span middleware 和通用 `StartSpan` helper；DB repository 和 Redis 低层命令已接入同一条 trace。
- 三服务已能跨 Control Plane、Runtime、Sandbox 传播标准 trace context；仓库已有 Alertmanager 路由样例，后续要补 logs/metrics 与 trace 的更强关联、真实值班系统接入和容量看板。

## 技术架构

推荐链路：

```text
Browser
  -> Control Plane auth middleware
  -> ActorContext(user, org, project, roles)
  -> Authorization(resource, action)
  -> Quota check/pre-allocate
  -> Repository write/read
  -> Audit event
  -> Dispatch run with request_id/trace_id
  -> Runtime/Sandbox span
  -> Usage settle
```

RBAC 当前最小角色和后续第一版角色：

- `owner`：管理 organization/project、成员、billing、所有资源。
- `admin`：管理 project、skill、run、workspace。
- `member`：创建 chat/run，使用已授权 skill。
- `viewer`：只读 chat/run/artifact。

动作第一版覆盖：

- `chat.read`
- `chat.write`
- `run.read`
- `run.cancel`
- `skill.use`
- `skill.manage`
- `workspace.read`
- `artifact.read`
- `audit.read`

## 技术方案

保留 `AUTH_MODE=demo`，不要一次性删除 `demo-user`：

- `AUTH_MODE=demo`：继续映射到 `demo-user/demo-project`，本地开发默认。
- `AUTH_MODE=trusted-header`：启用可信网关透传身份，设置上游网关并要求 actor headers。
- `AUTH_MODE=oidc`：当前既可作为 API 资源服务器校验 bearer JWT，也支持浏览器 authorization code callback/session/refresh token；需要设置 `OIDC_ISSUER_URL`、`OIDC_AUDIENCE`、可选 `OIDC_JWKS_URL` 和 claims 映射。启用浏览器登录时额外设置 `OIDC_CLIENT_ID`、`OIDC_AUTH_URL`、`OIDC_TOKEN_URL`、`OIDC_SESSION_SECRET`，可选设置 `OIDC_CLIENT_SECRET`、`OIDC_REDIRECT_URL` 和 `OIDC_SESSION_TTL_SECONDS`。
- 首次登录创建或绑定内部 `users.id`，创建默认 organization/project/member 关系。
- 外部 API 不再直接读 `app.DemoUserID`，而是从 `ActorContext` 取当前用户和 project。

配额建议先做最小集合：

- `runs_per_hour`
- `concurrent_runs`
- `model_tokens_per_day`
- `sandbox_seconds_per_day`
- `http_skill_calls_per_day`

审计第一版覆盖：

- login/logout
- project/member/role 变更
- skill create/update/enable/disable/grant
- run create/cancel/fail
- quota deny
- internal auth failure
- sandbox exec
- secret resolve

## 分阶段落地

1. Auth middleware：增加 `AUTH_MODE=demo|trusted-header|oidc` 和 `ActorContext`，外部 API 保持行为不变。
2. 身份/成员表：membership migration、当前组织/项目成员管理 API、邀请创建/接受 API、`user_identities` 绑定已落地并保留 demo 数据；组织成员 API 与同组织项目 API 已支持缺少 header roles 时从 `organization_members` 解析角色；邀请接受已校验可信邮箱 claim；NiceAgent OIDC 浏览器 login/session/refresh token 已有最小闭环；可选 SMTP 邀请邮件、subject/body 模板、进程内内存队列、durable outbox、重试、邀请邮件重发 API、provider-neutral 退信事件记录、HMAC webhook 入口、SendGrid/SES/Mailgun 最小原生字段映射、组织级自动停发、suppression 查询/解除 API 和前端“邮件治理”管理面板已有最小闭环；前端登录入口、服务商原生签名校验和管理后台 UI 仍待补。
3. API 去 demo 常量：所有 handler 从 `ActorContext` 获取 user/project。
4. RBAC：加入 resource/action 检查和基础角色。
5. Quota：项目级持久 policy、Redis 并发/小时窗口预占、固定/动态模型 token 预扣/结算、run 级 tool/sandbox/artifact 用量记录和 tool/sandbox 最小实时预占已落地；后续需要支持更多 provider 原生 tokenizer 覆盖、分布式强一致 token bucket 和账单维度聚合。
6. Audit：新增 append-only 审计事件。
7. Observability：轻量 `trace_id`、`/metrics`、OTLP HTTP exporter、入站 HTTP span、调度/回写、run/tool/model、HTTP Skill、Sandbox、Redis queue、Redis 低层命令、Postgres repository span 和 Alertmanager 路由样例已落地；下一步补日志/指标/trace 更强关联、真实值班系统接入和容量看板。

## 风险与验收

风险：

- 老数据迁移时 demo 项目不可访问。
- 读路径漏鉴权导致跨用户访问 chat/run/artifact。
- 审计日志误写入 secret。
- 分布式配额与 run 终态不一致。
- trace/log 高基数字段导致观测系统成本膨胀。

验收：

- `AUTH_MODE=demo` 下现有本地流程不受影响。
- `AUTH_MODE=oidc` 下不同用户不能互相看到 chat、run、skill、workspace、artifact。
- `INTERNAL_API_TOKEN` 在非本地环境或显式 required 模式下必填。
- 每次 run 都能用 `run_id` 查到 request log、audit event；三服务响应和服务间调用都能看到同一个 `X-Trace-ID` 和 `traceparent`；配置 `OTEL_TRACES_EXPORTER=otlp` 后至少能导出三服务入站 HTTP、调度/回写、run/tool/model、HTTP Skill、Sandbox、Redis queue、Redis 低层命令和 Postgres repository spans。
- quota deny 可被前端展示为可读错误。
- 日志、audit、trace 中不出现 API key 或 skill secret。

## 参考资料

- [OpenID Connect Core](https://openid.net/specs/openid-connect-core-1_0-18.html)
- [OAuth 2.0 RFC 6749](https://datatracker.ietf.org/doc/rfc6749/)
- [Casbin RBAC](https://casbin.org/docs/rbac/)
- [Open Policy Agent](https://www.openpolicyagent.org/docs/latest/)
- [OpenTelemetry Documentation](https://opentelemetry.io/docs/)
- [OpenTelemetry GenAI semantic conventions](https://opentelemetry.io/docs/specs/semconv/gen-ai/)
- [Redis rate limiter](https://redis.io/docs/latest/develop/use-cases/rate-limiter/)
