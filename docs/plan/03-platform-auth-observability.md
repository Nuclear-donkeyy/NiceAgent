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

Control Plane 外部 API 仍硬编码 `demo-user` 和 `demo-project`。会话、消息、skills CRUD 都从 `app.DemoUserID`、`app.DemoProjectID` 取身份，没有登录态、session、JWT/OIDC 中间件。

Repository 已经在部分写路径按 user 过滤，例如创建消息、归档会话、列出会话。但 `GetChat`、`GetRun`、`ListEvents` 等读路径还缺 actor/project 权限校验。

内部服务鉴权已有可选 `INTERNAL_API_TOKEN` bearer token。环境变量为空时不校验，生产应改为非本地环境必填。

日志层已有 `slog` JSON request log，但没有统一 `request_id`、`trace_id`、metrics、OTel span，也没有独立 `audit_events` 表。

## 扩展点

- Control Plane 增加 `internal/auth`：解析 session/JWT，产出 `ActorContext`。
- Repository 方法从 `userID string` 扩展到 `ActorContext + projectID`，所有读写都走授权检查。
- 新增身份/权限表：`user_identities`、`organization_members`、`project_members`、`role_bindings`。
- 新增 `audit_events` 表和 `AuditLogger`。
- 新增 quota 数据模型：`quotas`、`quota_usage`，Redis 作为高频计数缓存。
- `packages/common/platform` 增加 request id、trace context、log redactor。
- 三服务接入 OpenTelemetry，跨 Control Plane、Runtime、Sandbox 传播 trace context。

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

RBAC 第一版角色：

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
- `AUTH_MODE=oidc`：启用 OIDC 登录，设置 `OIDC_ISSUER_URL`、`OIDC_CLIENT_ID`、`OIDC_CLIENT_SECRET`、`SESSION_SECRET`。
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

1. Auth middleware：增加 `AUTH_MODE=demo|oidc` 和 `ActorContext`，外部 API 保持行为不变。
2. 身份/成员表：新增 identity 和 membership migrations，保留 demo 数据。
3. API 去 demo 常量：所有 handler 从 `ActorContext` 获取 user/project。
4. RBAC：加入 resource/action 检查和基础角色。
5. Quota：dispatch 前预占，run 完成后结算 token、tool、sandbox 用量。
6. Audit：新增 append-only 审计事件。
7. Observability：加入 request_id、trace_id、OTel traces/metrics/log correlation。

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
- `INTERNAL_API_TOKEN` 在非本地环境必填。
- 每次 run 都能用 `run_id` 查到 request log、audit event、trace。
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
