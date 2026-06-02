# NiceAgent 运维说明

## 运行模式

当前仓库支持两种运行思路：

- memory demo：直接启动 `services/control-plane`，无需 Postgres/Redis，适合本地验证 UI、API 和事件流。
- Compose 拓扑：通过 `deployments/docker-compose.yml` 启动服务和依赖，Control Plane 使用 `STORE_DRIVER=postgres` 验证持久化路径。
- ACK 拓扑：通过 `deployments/k8s` 将三服务发布到阿里云 ACK，适合验证镜像发布和服务解耦链路。
- 前端开发：启动 `frontend` 的 Rspack dev server，并通过代理访问 Control Plane API。

## 状态与数据

memory 模式的权威状态在进程内存中，进程重启会丢失数据。Postgres 模式下，以下数据以数据库为准：

- 用户、组织、项目。
- 聊天会话和消息。
- runs、run events、artifacts。
- skills、skill_versions、用户/项目 skill_grants、skill_secrets、配额、审计记录。

当前 Postgres 模式支持基于数据库的 `RunEvent` replay。单 Control Plane 副本会使用进程内 fanout；多 Control Plane 副本可设置 `EVENT_FANOUT_MODE=redis`，事件写入后发布轻量 nudge，其他副本收到 nudge 后仍从 repository 按 `seq` 补读权威事件。Redis nudge 只负责实时唤醒，不替代 Postgres 的权威持久化；即使通知丢失，前端也可以通过 `?after=` 或 `Last-Event-ID` replay 补齐。

Redis Streams 已有 run queue 最小闭环：`DISPATCH_MODE=redis` 时 Control Plane 会把 run 写入 `RUN_QUEUE_STREAM`，`RUNTIME_QUEUE_MODE=redis` 时 Agent Runtime 会通过同一个 consumer group 消费 run，并回调 Control Plane 拉取完整 execution context。Runtime 执行前会 claim `attempt_id`，后续 event/complete/fail 都按 active attempt fencing。Runtime worker 也已经支持 idle pending `XAUTOCLAIM`、超最大投递次数写 DLQ、执行期间 heartbeat 续租。Redis 不替代 Postgres 的权威持久化。

Artifact 默认不过期，适合本地开发。需要让新登记的 artifact 自动带上过期时间时，设置：

```bash
ARTIFACT_RETENTION_DAYS=7
```

Control Plane 会在 Runtime 通过内部 artifact/complete API 登记产物时，为没有显式 `expires_at` 的 artifact 写入默认过期时间。过期 artifact 会从外部 list/get/download/content API 中隐藏。后台清理可以调用：

```bash
curl -X POST http://control-plane:8080/internal/artifacts/cleanup-expired \
  -H "Authorization: Bearer $INTERNAL_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"limit":100}'
```

默认清理只把 metadata 标记为 `deleted_at`。如果需要同时删除本地 workspace 文件，可以单次请求传 `delete_files=true`，或在 Control Plane 配置：

```bash
ARTIFACT_CLEANUP_DELETE_FILES=true
```

本地文件回收只处理 `storage_backend` 为空或 `local` 的 artifact，并复用 workspace root、`output/` 路径限制、symlink escape 和 regular file 校验；不能安全解析或删除的文件会出现在响应的 `file_errors` 中。对象存储对象仍需要后续生命周期策略或归档任务处理。

## 认证与配额

外部 API 支持三种模式：

- `AUTH_MODE=demo`：默认本地模式，所有请求映射到 `demo-user/demo-project`。
- `AUTH_MODE=trusted-header`：生产网关模式，要求可信上游完成登录和 JWT/session 校验，再透传 `X-NiceAgent-User-ID`、`X-NiceAgent-Project-ID`、可选 `X-NiceAgent-Org-ID` 和 `X-NiceAgent-Roles`。
- `AUTH_MODE=oidc`：Control Plane 直接校验 `Authorization: Bearer <jwt>`。当前支持 RS256 JWT、`iss`、`aud`、`exp`、`nbf` 校验和 JWKS 拉取，并把 claims 映射为 `ActorContext`；同时支持最小浏览器 OIDC authorization code flow，提供 login callback、HttpOnly session cookie、refresh token 刷新、服务端 session 撤销和 logout。

OIDC JWT 模式需要配置：

- `OIDC_ISSUER_URL`：JWT `iss`，同时作为默认 JWKS 根地址来源。
- `OIDC_AUDIENCE`：必须匹配 JWT `aud`。
- `OIDC_JWKS_URL`：可选，默认使用 `<OIDC_ISSUER_URL>/.well-known/jwks.json`。如果你的 IdP 只通过 discovery 暴露 JWKS URI，请显式配置该值。
- `OIDC_PROJECT_ID_CLAIM`、`OIDC_ORG_ID_CLAIM`、`OIDC_ROLES_CLAIM`：默认分别是 `niceagent_project_id`、`niceagent_org_id`、`niceagent_roles`。如果 roles claim 为空，Control Plane 会继续从持久 membership 解析角色。
- `OIDC_DEFAULT_PROJECT_ID`、`OIDC_DEFAULT_ORG_ID`：可选 fallback，适合单项目部署。
- `OIDC_USER_ID_CLAIM`、`OIDC_EMAIL_CLAIM`、`OIDC_NAME_CLAIM`：默认分别是 `sub`、`email`、`name`。

OIDC 浏览器登录额外配置：

- `OIDC_CLIENT_ID`：IdP 中注册的 client id。`id_token` 的 audience 通常应与 `OIDC_AUDIENCE` 保持一致。
- `OIDC_CLIENT_SECRET`：可选 client secret，只能通过环境变量或 Secret 注入。
- `OIDC_AUTH_URL`：authorization endpoint。
- `OIDC_TOKEN_URL`：token endpoint，用于 authorization code 和 refresh token exchange。
- `OIDC_REDIRECT_URL`：可选 callback URL；为空时按当前请求 host 推导 `/auth/oidc/callback`。
- `OIDC_SESSION_SECRET`：签名 session cookie 的随机密钥，启用浏览器登录时必填。
- `OIDC_SESSION_TTL_SECONDS`：session cookie 有效期，默认 43200 秒。

浏览器入口为 `GET /auth/oidc/login`；callback 成功后写入 `niceagent_session` HttpOnly cookie 和 `niceagent_csrf` cookie，后续外部 API 在没有 bearer token 时会读取该 session。前端侧栏的“登录会话”面板会触发 OIDC 登录、`POST /auth/oidc/refresh` 刷新 session，以及 `POST /auth/logout` 清理 session；这两个 POST 会把 `niceagent_csrf` cookie 写入 `X-NiceAgent-CSRF` header，Control Plane 会校验 header、cookie 和签名 session 中的 token 三者一致。Control Plane 会把 browser session 写入 `oidc_browser_sessions`，refresh 成功会撤销旧 session 并签发新 session，logout 会撤销当前 session；旧 cookie 即使被重放也会被拒绝。当前 session cookie 采用 HMAC 签名和 HttpOnly/SameSite=Lax，生产部署应使用 HTTPS、稳定域名、足够长的 `OIDC_SESSION_SECRET`，并结合 IdP 侧 refresh token 生命周期和撤销策略。

最小 RBAC 优先读取 trusted header 中的 `X-NiceAgent-Roles`：`viewer` 只允许读取，`owner/admin/member/editor/writer` 允许创建聊天、发送消息、取消 run 和管理 HTTP Skill。Control Plane 已有内置 action-level policy，把普通写 action 映射到 write requirement，把项目配置/成员 action 映射到 project admin requirement，把组织成员、邀请、邮件治理 action 映射到 organization admin requirement；拒绝时会写入带 `required_roles` 的 deny audit event。缺少 roles 时会从 `project_members` 持久角色绑定中读取；仍找不到成员关系时返回 `403`，并写入 `auth.authorize` deny audit event。当前 migration 会给 `demo-user/demo-project` 写入 `owner` 角色。

项目成员管理 API 已有最小版本：

```bash
GET /api/projects/{project_id}/members
POST /api/projects/{project_id}/members
PATCH /api/projects/{project_id}/members/{user_id}
DELETE /api/projects/{project_id}/members/{user_id}
```

这些 API 只管理当前 actor 所在项目的 `project_members`，不会跨项目修改成员；第一版也不允许修改或删除自己的成员关系，避免把自己锁出项目。organization 级成员管理、邀请创建/接受、可信身份绑定、可选 SMTP 邀请邮件、NiceAgent 最小 OIDC 浏览器登录/session 和内置 action policy 已有闭环；可配置策略源、生产 IdP 联调和更细 action 条件仍是后续工作。

邀请邮件默认关闭，适合本地开发。需要由 Control Plane 直接发送邀请邮件时配置：

```bash
INVITATION_EMAIL_MODE=smtp
INVITATION_PUBLIC_BASE_URL=https://app.example.com
SMTP_HOST=smtp.example.com
SMTP_PORT=587
SMTP_USERNAME=niceagent
SMTP_PASSWORD=<secret>
SMTP_FROM="NiceAgent <noreply@example.com>"
```

`INVITATION_PUBLIC_BASE_URL` 用于生成邮件中的 `/?invitation_token={token}` 链接。邀请邮件支持 `text/template` 语法的纯文本模板：

- `INVITATION_EMAIL_SUBJECT_TEMPLATE`：默认 `NiceAgent invitation`。
- `INVITATION_EMAIL_BODY_TEMPLATE`：为空时使用内置模板。

可用字段包括 `.Email`、`.Role`、`.OrganizationID`、`.ProjectID`、`.ProjectIDOrDash`、`.AcceptURL`、`.ExpiresAt`。启用 SMTP 时，Control Plane 启动会校验模板语法和字段名；错误模板会导致启动失败，避免发出坏邮件。SMTP 发送失败不会回滚已创建的邀请，排查时查看 `invitation.email.send` audit event 和 Control Plane 日志。生产环境不要把 `SMTP_PASSWORD` 放入 ConfigMap 或仓库；K8s 模板通过 `niceagent-smtp` Secret 注入该值。

邀请邮件默认使用同步 `inline` 发送路径。需要避免创建邀请接口被 SMTP 瞬时抖动长时间阻塞时，可以开启进程内内存队列：

```bash
INVITATION_EMAIL_QUEUE_MODE=memory
INVITATION_EMAIL_QUEUE_SIZE=100
INVITATION_EMAIL_QUEUE_WORKERS=1
INVITATION_EMAIL_RETRY_ATTEMPTS=3
INVITATION_EMAIL_RETRY_INITIAL_DELAY_MS=250
```

`memory` 队列会先把邀请邮件放入 Control Plane 进程内队列，再由后台 worker 重试发送。队列满时创建邀请仍会成功，但邮件入队会失败并写入 `invitation.email.send` deny audit event。该队列不是 durable queue：Control Plane 重启会丢失尚未发送的队列项。

Postgres 模式下更推荐使用 durable outbox：

```bash
INVITATION_EMAIL_QUEUE_MODE=outbox
INVITATION_EMAIL_QUEUE_WORKERS=1
INVITATION_EMAIL_RETRY_ATTEMPTS=3
INVITATION_EMAIL_RETRY_INITIAL_DELAY_MS=250
```

`outbox` 模式会把投递任务写入 `invitation_email_outbox` 表。后台 worker 会 claim due jobs、发送 SMTP、成功后标记 `sent`；失败时按指数退避重新置为 `pending`，超过最大次数后标记 `failed`。如果 Control Plane 在发送前或发送失败后重启，锁过期后其他 worker 可以重新 claim。

管理员可以用下面的接口重发仍处于 `pending` 且未过期的邀请邮件：

```bash
curl -X POST http://control-plane:8080/api/organizations/$ORG_ID/invitations/$INVITATION_ID/resend
```

该接口不会重新生成 invitation token；`outbox` 模式下会把对应 delivery 重置为 `pending`、清除锁和上次错误，再由后台 worker 重新投递。未启用邀请邮件发送器时会返回 `503`。如果邮件服务商已经通过 bounce/complaint/dropped webhook 标记过该邮箱，Control Plane 会按当前组织自动停发并返回 `409`。

邮件服务商的 delivery/bounce/complaint/drop 事件可以通过 `POST /api/organizations/{organization_id}/invitation-email-events` 写入 `invitation_email_events`。其中 `bounced`、`complaint` 和 `dropped` 会把对应 outbox delivery 标记为 `bounced`，把 provider 返回的原因写入 `last_error`，并写入 `invitation_email_suppressions`。后续同一组织内创建邀请时不会自动发送该邮箱，outbox worker 也不会 claim 该邮箱的 pending delivery，管理员重发会得到 `409`；`delivered` 会把 delivery 标记为 `sent`。

管理员可以查询和解除当前组织的停发邮箱。解除操作不会删除历史 provider event，只会删除 suppression 记录；解除后可以再调用 resend API 重新投递仍 `pending` 且未过期的邀请：

```bash
curl http://control-plane:8080/api/organizations/$ORG_ID/invitation-email-suppressions
curl -X DELETE http://control-plane:8080/api/organizations/$ORG_ID/invitation-email-suppressions/$SUPPRESSION_ID
```

前端侧栏的“邮件治理”面板会调用同一组 API，展示当前 demo 组织的停发邮箱并支持解除停发。真实多组织登录接入后，面板需要从 actor/session 读取当前组织，而不是使用 demo 组织。

生产接入邮件服务商 webhook 时，优先使用无登录态的 `POST /webhooks/invitation-email-events`，并配置：

```bash
INVITATION_EMAIL_WEBHOOK_SECRET=<random-secret>
INVITATION_EMAIL_SENDGRID_PUBLIC_KEY=<sendgrid-ecdsa-public-key>
INVITATION_EMAIL_MAILGUN_SIGNING_KEY=<mailgun-signing-key>
INVITATION_EMAIL_SNS_SIGNATURE_VERIFICATION=true
INVITATION_EMAIL_SNS_TOPIC_ARN=arn:aws:sns:us-east-1:123456789012:niceagent-email-events
```

调用方可以使用 NiceAgent 兼容签名 `X-NiceAgent-Webhook-Signature: sha256=<hex>`，其中 `<hex>` 是 `hmac_sha256(INVITATION_EMAIL_WEBHOOK_SECRET, raw_body)`；也可以直接配置服务商原生签名。SendGrid Event Webhook 使用 `INVITATION_EMAIL_SENDGRID_PUBLIC_KEY` 校验 `X-Twilio-Email-Event-Webhook-Timestamp` 和 `X-Twilio-Email-Event-Webhook-Signature`；Mailgun 使用 `INVITATION_EMAIL_MAILGUN_SIGNING_KEY` 校验 payload 中的 `signature.timestamp`、`signature.token` 和 `signature.signature`；Amazon SES 通过 SNS 投递时可开启 `INVITATION_EMAIL_SNS_SIGNATURE_VERIFICATION=true`，Control Plane 会校验 SNS envelope 的 AWS 证书签名，并可用 `INVITATION_EMAIL_SNS_TOPIC_ARN` 限制 topic。四种签名方式任一通过即可；未配置任何 webhook 验证方式时入口返回 404，签名错误返回 401。当前入口兼容 NiceAgent provider-neutral 事件格式，并支持 SendGrid Event Webhook、Amazon SES SNS notification 和 Mailgun webhook 的最小原生字段映射。生产接入时需要在邮件发送侧把 `invitation_id` / `delivery_id` 写入服务商 metadata/custom args/tags，否则 webhook 无法把服务商事件关联回 NiceAgent invitation。管理后台重发按钮仍可在后续 UI 层继续补齐。

run 配额是最小治理边界，默认关闭。env 配置是 fallback：

```bash
QUOTA_MAX_CONCURRENT_RUNS=0
QUOTA_RUNS_PER_HOUR=0
QUOTA_MODEL_TOKENS_PER_DAY=0
QUOTA_TOOL_CALLS_PER_DAY=0
QUOTA_SANDBOX_SECONDS_PER_DAY=0
QUOTA_COUNTER_MODE=repository
QUOTA_COUNTER_PREFIX=niceagent:quota
QUOTA_MODEL_TOKEN_RESERVATION_PER_RUN=0
QUOTA_MODEL_TOKEN_RESERVATION_MODE=fixed
QUOTA_MODEL_TOKEN_DYNAMIC_OUTPUT_BUFFER=0
QUOTA_MODEL_TOKEN_ESTIMATOR_MODEL=
```

当配额开启时，Control Plane 会在创建 run 前按当前 actor 的 `user_id + project_id` 统计 active runs、最近一小时 runs，以及 UTC 自然日内已有 `RunUsage` 的模型 token、tool calls 和 sandbox seconds 用量。超过限制时返回 `429 Too Many Requests`，前端会收到中文错误，同时写入 `quota.run.create` deny audit event。active run 状态包括 `queued`、`running` 和 `waiting_for_approval`。模型 token 可在 run 创建时做 fixed/dynamic 预占；tool calls 和 sandbox seconds 还会在 Runtime 每次 tool 调用前通过内部 `/internal/runs/{run_id}/quota-reserve` 做最小实时预占。

`QUOTA_COUNTER_MODE=repository` 是默认模式，直接从 Postgres/memory 统计 run 状态。`QUOTA_COUNTER_MODE=redis` 会在创建 run 前用 Redis 对 `max_concurrent_runs` 和 `max_runs_per_hour` 做预占：并发计数在 run 进入 `succeeded/failed/canceled` 后释放，小时窗口计数保留到窗口 TTL。Redis quota 只作为高频计数和预占层，项目 policy 和最终 run 状态仍以 Control Plane repository 为权威。

Redis quota 支持两种模型 token 预占模式。`QUOTA_MODEL_TOKEN_RESERVATION_MODE=fixed` 时，如果 `QUOTA_MODEL_TOKEN_RESERVATION_PER_RUN>0`，每个 run 创建前会按固定值预占每日 token；`dynamic` 时，Control Plane 会按当前用户消息长度估算 input tokens，并叠加 `QUOTA_MODEL_TOKEN_DYNAMIC_OUTPUT_BUFFER` 作为输出缓冲。run 成功时按真实 `RunUsage.total_tokens` 结算差额；失败或取消时按 0 用量释放预占。动态模式比固定值更贴近请求大小，但无法预知模型真实输出。`QUOTA_MODEL_TOKEN_ESTIMATOR_MODEL` 可指定动态预占使用的 tokenizer 模型，例如 `gpt-4o`、`gpt-5-*`、`o3-*` 和 `o4-*` 使用 `o200k_base`，`deepseek-*`、`qwen-*`、`kimi-*`、`moonshot-*`、`doubao-*`、`glm-*`、`mistral-*`、`llama-*` 等常见 OpenAI-compatible 模型族使用 `cl100k_base` 兼容估算；为空或未知模型时回退到 `heuristic_rune_div4`。估算路径会在 `RunUsage.token_estimator` 中标记当前估算器。更精细的按模型/租户/账单维度 token bucket 和 provider 官方 tokenizer 仍属于后续工作。

项目级持久 quota policy 已有最小版本：

```bash
GET /api/projects/{project_id}/quota
PATCH /api/projects/{project_id}/quota
GET /api/projects/{project_id}/usage?window=24h|7d|30d
```

如果 `project_quota_policies` 中存在当前项目配置，Control Plane 会优先使用持久 policy；如果不存在，则使用上述 env fallback。policy 字段 `max_concurrent_runs`、`max_runs_per_hour`、`max_model_tokens_per_day`、`max_tool_calls_per_day`、`max_sandbox_seconds_per_day` 都是非负整数，`0` 表示关闭对应限制。Redis 计数缓存、并发/小时窗口预占、固定或动态模型 token 预扣/结算，以及 Runtime 调 tool 前的 tool/sandbox 最小预占已有闭环。

`GET /api/projects/{project_id}/usage` 提供最小账单维度统计：按 provider、model、currency、是否估算和 token estimator 聚合 run usage，并返回窗口总计。当前支持 `window=24h|7d|30d` 或 `since=<RFC3339>`，只允许 `owner/admin` 访问。这个接口可以用于运营看板、成本排查和后续账单导出，但还不是强一致计费系统；更完整 tokenizer 覆盖、按租户/模型的分布式 token bucket 和外部告警仍是后续工作。

前端左侧“项目容量”区域会读取 `GET /api/projects/{project_id}/quota` 和 `GET /api/projects/{project_id}/usage?window=24h`，展示模型 token、tool calls、sandbox 秒数、run 数、artifact 大小和 provider/model 使用分布。这是最小容量视图，适合本地与早期运营排查；生产级容量看板仍需要接入 Prometheus/Grafana。Skill rate limit 的容量估算和告警排障流程见 [Skill Rate Limit 容量与告警 Runbook](runbooks/skill-rate-limit-capacity.md)，仓库也提供了 Grafana dashboard 样例：[`deployments/monitoring/grafana/dashboards/skill-governance.json`](../deployments/monitoring/grafana/dashboards/skill-governance.json)。

三服务内部 API 使用同一个 bearer token：

```bash
NICEAGENT_ENV=production
INTERNAL_API_TOKEN_REQUIRED=true
INTERNAL_API_TOKEN=<random-internal-token>
```

本地裸跑可以保持 `INTERNAL_API_TOKEN_REQUIRED=false`。当显式开启 required，或 `NICEAGENT_ENV` 不是 `local/dev/development/test/ci` 时，Control Plane、Agent Runtime 和 Sandbox Executor 都会在缺少 `INTERNAL_API_TOKEN` 时启动失败。Kubernetes manifest 默认开启该检查，并要求 `niceagent-internal-api` Secret 存在；不要把该 token 暴露给浏览器或外部客户端。

## 模型 Provider

Agent Runtime 默认使用 `MODEL_PROVIDER=mock`，适合本地演示和 CI。切到真实 OpenAI-compatible provider 时，需要配置：

- `MODEL_PROVIDER=openai-compatible`
- `MODEL_BASE_URL`：兼容服务根地址，不包含 `/v1/chat/completions`、`/chat/completions` 或 `/completions`；启动时会做 URL 和 endpoint 校验。
- `MODEL_API_KEY`：模型服务密钥，只能通过环境变量或 Kubernetes Secret 注入，不写入仓库。
- `MODEL_NAME`：请求体中的 `model`。
- `MODEL_FALLBACK_PROVIDER`：可选后备 provider，当前支持 `mock` 或 `openai-compatible`。为空时不启用 fallback。
- `MODEL_FALLBACK_BASE_URL`、`MODEL_FALLBACK_API_KEY`、`MODEL_FALLBACK_NAME`：当后备 provider 为 `openai-compatible` 时使用；fallback API key 只能通过环境变量或 Secret 注入。
- `MODEL_TIMEOUT_SECONDS`：模型 HTTP 请求超时，默认 120 秒。
- `MODEL_PROVIDER_PROFILE`：可选 provider profile。当前支持 `deepseek`，会复用 OpenAI-compatible provider、默认 `MODEL_BASE_URL=https://api.deepseek.com`，并把 usage provider 标记为 `deepseek`；API key 和模型名仍必须显式配置。
- `MODEL_INPUT_PRICE_PER_1M_TOKENS`、`MODEL_CACHED_INPUT_PRICE_PER_1M_TOKENS`、`MODEL_OUTPUT_PRICE_PER_1M_TOKENS`、`MODEL_REASONING_PRICE_PER_1M_TOKENS`：可选价格配置，单位是每 100 万 token 的价格；默认都是 0，不提交任何厂商实时价格。
- `MODEL_PRICE_CURRENCY`：价格币种，默认 `USD`。
- `MODEL_REQUESTS_PER_MINUTE`：Runtime 进程内模型请求固定窗口限流，默认 0 表示关闭。命中后返回 `rate_limited`，可触发已配置的 fallback。
- `MODEL_MAX_CONCURRENT_REQUESTS`：Runtime 进程内模型请求并发上限，默认 0 表示关闭。它用于保护本地进程和 provider 连接池，不替代供应商账号级限流。
- `MODEL_HEALTH_PROBE_ENABLED`：是否开启主动模型健康探针，默认 `false`，避免本地和 CI 无意产生真实模型调用。
- `MODEL_HEALTH_PROBE_INTERVAL_SECONDS`：主动探针间隔，默认 60 秒。
- `MODEL_HEALTH_PROBE_TIMEOUT_SECONDS`：单次探针超时，默认 10 秒。
- `MODEL_HEALTH_PROBE_INITIAL_DELAY_SECONDS`：Runtime 启动后首次探针延迟，默认 0 秒。

DeepSeek 接入不新增 `MODEL_PROVIDER`，仍走 OpenAI-compatible；建议增加 profile：

```bash
MODEL_PROVIDER=openai-compatible
MODEL_PROVIDER_PROFILE=deepseek
MODEL_BASE_URL=https://api.deepseek.com
MODEL_API_KEY=sk-...
MODEL_NAME=<以 DeepSeek 官方文档为准>
```

真实 key 冒烟使用可选脚本，不把密钥写入仓库：

```bash
export DEEPSEEK_API_KEY="sk-..."
export DEEPSEEK_MODEL="<以 DeepSeek 官方文档为准>"
make smoke-deepseek-runtime
```

该命令会启动临时 Agent Runtime，开启一次主动 model health probe，并轮询 `/healthz` 的 `model_provider` 快照。未设置 key 或模型名时会安全 `SKIP`，避免 CI 或本地默认检查产生真实模型调用。详细流程、结果记录和错误分类见 [DeepSeek Runtime 冒烟 Runbook](runbooks/deepseek-runtime-smoke.md)。

Runtime 当前通过 Eino ADK `ChatModelAgent + Runner` 和 Eino 原生 `ToolCallingChatModel` 执行 agentic loop。模型输出统一写成 `model.token` run event，tool 调用统一写成 `tool.started`、`tool.output`、`tool.finished`。OpenAI-compatible provider 通过 `eino-ext` OpenAI ChatModel 接入，优先采集 provider response 中的真实 token usage；缺失 usage 时按 run 的输入/输出文本做估算并标记 `estimated=true`、`token_estimator`，可为 `tiktoken_o200k_base`、`tiktoken_cl100k_base` 或 `heuristic_rune_div4`。其中 `o200k_base` 覆盖 OpenAI 新一代 `gpt-4o`、`gpt-4.1`、`gpt-4.5`、`gpt-5` 和 `o*` 模型族；`cl100k_base` 覆盖 DeepSeek、Qwen、Moonshot/Kimi、Doubao、GLM、Mistral、Llama 等 OpenAI-compatible 模型族的兼容估算。真实账单仍以 provider 返回的 usage 或官方控制台为准。如果配置了价格，Runtime 会在 `RunUsage.cost` 和 `RunUsage.currency` 中回写本次 run 的估算费用；模型价格仍以服务商官方控制台/文档为准，不在仓库中硬编码。

Control Plane 会把内部 `tool.started` 和 `tool.finished` event 同步写入 `audit_events`，形成 run 级 skill invocation 起止轨迹。审计 metadata 只保留 `skill_id`、tool 名、event id/seq 和完成状态，不复制 `tool.output` 原始内容；排查原始 observation 时仍以 run event 权威记录为准，并注意其中可能包含第三方响应文本。

`RunUsage` 也会记录 run 级工具/sandbox 聚合：tool 调用数、tool 错误数、sandbox 命令数、sandbox 执行耗时、stdout/stderr 输出字节数、sandbox CPU/内存使用摘要以及 artifact 数量/大小。它们来自 Eino tool observation 和 `SandboxResult`，用于排障、审计和配额/账单聚合。Runtime 执行 tool 前会先向 Control Plane 预占一次 `tool_calls`；`cli.exec` 还会按 timeout 预占 `sandbox_seconds`。如果预占被拒绝，Runtime 不会执行真实 tool，而是把中文 quota deny 作为 tool observation 交给模型。run 完成时会用实际 `RunUsage` 覆盖预占快照。当前还没有更细的按 skill/provider 计费和分布式强一致 token bucket。

模型错误会按运营类目归一化：401 为 `auth_error`，402 为 `billing_error`，400/422 为 `request_error`，429 为 `rate_limited`，500/503/网关错误为 `provider_unavailable`，超时和连接错误为 `network_error`。429、5xx 和网络瞬时错误会按指数退避重试并尊重 `Retry-After`；401、402、400、422 不重试。开启 `MODEL_FALLBACK_PROVIDER` 后，Runtime 只会对 `rate_limited`、`provider_unavailable`、`network_error` 触发后备 provider；成功后会在 `RunUsage.fallback_from`、`RunUsage.fallback_to` 和 `RunUsage.error_class` 记录切换原因。provider 错误、日志和 run error 会经过 redactor，默认不输出 API key、Authorization、token、secret、password 或 cookie。

`MODEL_REQUESTS_PER_MINUTE` 和 `MODEL_MAX_CONCURRENT_REQUESTS` 是 Runtime 进程内保护阀：它们会覆盖普通模型请求、streaming 请求和主动 probe，但不会跨多个 Runtime 副本做强一致配额。生产环境仍应结合 provider 控制台限流、网关限流和多副本容量规划。

Agent Runtime 的 `GET /healthz` 会包含 `model_provider` 快照，展示 provider/model、最近请求计数、成功/失败计数、错误分类、最近延迟、fallback 状态和可选主动 probe 状态。默认不开启主动 probe；生产环境可以打开 `MODEL_HEALTH_PROBE_ENABLED=true`，让 Runtime 周期性调用当前 Eino ChatModel。该探针不会把 token 计入 run usage，但会产生真实模型请求和供应商侧费用，应结合告警阈值谨慎启用。

建议告警规则：

- `model_provider.status != healthy` 或 `probe_status != healthy` 持续 3 个探针周期。
- `niceagent_model_health_probe_total{status="error"}` 在 5 分钟内持续增长。
- `niceagent_model_health_probe_duration_seconds_sum / niceagent_model_health_probe_duration_seconds_count` 明显高于业务 SLO。

## K8s Sandbox 加固

Kubernetes 模板包含 `deployments/k8s/sandbox-hardening.yaml`，由 `make k8s-apply` 和 ACK CD 工作流自动应用。当前加固边界包括：

- `LimitRange`：为没有显式 requests/limits 的容器补默认 CPU、内存和 ephemeral-storage。
- `ResourceQuota`：限制 `niceagent` namespace 的 Pod 数、CPU、内存和临时存储总量，避免 demo 环境被单个组件拖垮。
- `NetworkPolicy`：只允许带 `app=niceagent-agent-runtime` label 的 Pod 访问 `niceagent-sandbox-executor` 的 8082 端口；Sandbox Executor 出站默认受控，只允许访问 kube-system DNS、同 namespace 内 OTLP 常用端口 `4317/4318`，以及排除 RFC1918、link-local、loopback、metadata、CGNAT 和 multicast 网段后的公网地址。
- Sandbox Executor Pod/Container `securityContext`：非 root 运行、`RuntimeDefault` seccomp、禁止提权、drop Linux capabilities、只读 rootfs，并把 `/app/workspaces` 和 `/tmp` 作为可写 `emptyDir` 挂载。

这只是 K8s 层的最小防线，不等同于强多租户安全沙箱。仓库提供了可选的 `deployments/k8s/sandbox-runtimeclass.example.yaml`，用于已经安装 gVisor `runsc`、Kata 或其他 runtime handler 的集群，把 Sandbox Executor 调度到带 `niceagent.io/node-pool=sandbox` label 和 `niceagent.io/sandbox=true:NoSchedule` taint 的专用节点池。该文件不会被默认 `make k8s-apply` 应用，避免普通 kind/ACK 集群没有 handler 时 Pod 无法调度；生产启用前需要先确认节点池、RuntimeClass handler 和云侧出口策略已经就绪。

生产环境继续建议把 sandbox worker 放到独立节点池，并按风险等级评估 gVisor/Kata/Firecracker、RuntimeClass、云防火墙/NAT 出口控制、镜像白名单和更细的审计。仓库内可用下面的轻量检查确认 sandbox 加固 manifest 仍包含 ingress/egress、资源限制、关键地址段排除，以及可选 RuntimeClass/专用节点池模板：

```bash
make check-k8s-sandbox
```

Sandbox Executor 支持 `EXECUTOR_MODE=local|container`。本地和 Compose 默认仍是 `local`，避免没有 Docker CLI/socket 的开发容器直接失效；切到 `container` 前应先确保宿主 Docker 可用，并先执行一次容器路径 smoke：

```bash
make smoke-sandbox-container
```

该 smoke 会禁用 local fallback，确认 `/healthz` 报告 `executor_mode=container`，并实际执行一次容器内 `echo` 命令。没有 Docker CLI 或 daemon 时会 `SKIP`，生产发布前应在目标执行节点或等价环境中得到通过结果。手动配置如下：

```bash
EXECUTOR_MODE=container
SANDBOX_CONTAINER_IMAGE=alpine:3.20
SANDBOX_CONTAINER_ALLOWED_IMAGES=alpine:3.20
SANDBOX_CONTAINER_LOCAL_FALLBACK=false
```

`SANDBOX_CONTAINER_ALLOWED_IMAGES` 是逗号分隔白名单；当 `EXECUTOR_MODE=container` 且白名单非空时，Sandbox Executor 启动会拒绝不在白名单内的 `SANDBOX_CONTAINER_IMAGE`。`GET /healthz` 会返回 `executor_mode`、`container_image` 和 `container_allowed_images`，用于确认当前执行路径和镜像策略。生产环境建议关闭 `SANDBOX_CONTAINER_LOCAL_FALLBACK`，避免 Docker 不可用时静默回退到本地执行。

## Skill 与 Secret 排查

Skill 存储分为四层：

- `skills`：稳定身份、scope、kind、owner、status。
- `skill_versions`：name、description、JSON schema、MCP 风格 annotations、runtime_config。
- `skill_grants`：用户/项目可用性。
- `skill_secrets`：secret 引用或本地开发密文。

前端 `GET /api/skills` 不返回 secret。Runtime 通过 Control Plane 下发的内部 `RuntimeSkill` 获取执行所需 secret。当前本地开发允许把 bearer token 存入 `encrypted_value`；`secret_ref` 支持 `env://ENV_NAME` 和 `file:///absolute/path`。`env://` 适合把 K8s Secret 或外部 Secret Operator 注入为环境变量后再解析；`file://` 适合读取挂载到 Runtime 容器内的 secret 文件，默认只允许 `/var/run/secrets` 和 `/run/secrets`，可用 `NICEAGENT_SECRET_FILE_ROOTS=/path/a,/path/b` 覆盖。生产环境后续仍应接阿里云 KMS、Vault 或 External Secrets 原生 resolver，并避免长期使用明文环境变量作为唯一 secret backend。

HTTP Skill 默认只允许 `https` URL，禁用重定向，拒绝 URL 中携带用户名/密码，并阻断 `localhost`、`.local`、metadata host、字面量 private/link-local/loopback IP。Runtime 发出请求前还会解析目标 host；如果 DNS 结果包含 private、link-local、loopback、multicast 或 unspecified 地址，会返回 `ssrf_rejected` observation，不会发起外部请求。排查 HTTP Skill 失败时优先看 observation 的 `error_type`：`ssrf_rejected` 表示策略拒绝，`upstream_dns` 表示解析失败，`upstream_tls` 表示证书或 TLS 问题。

HTTP/MCP Skill `runtime_config` 支持最小 retry/rate limit：`retry.max_attempts` 默认 1、最大 5，只对 429、5xx 和网络/超时类错误重试；`rate_limit.requests_per_minute` 默认 0 表示关闭，最大 600。`SKILL_RATE_LIMIT_MODE=local` 时按单个 Agent Runtime 进程内的 skill id 做固定窗口限流；`SKILL_RATE_LIMIT_MODE=redis` 时使用 `REDIS_ADDR` 和 `SKILL_RATE_LIMIT_PREFIX` 在 Redis 中按分钟窗口计数，让多个 Runtime 副本共享同一个 skill rate limit。命中限流会返回 `rate_limited` observation，不会发起请求，并递增 `niceagent_skill_rate_limit_denials_total{kind,mode}`。仓库的 Prometheus 告警规则已包含 `NiceAgentSkillRateLimitDenials`，可用于发现第三方 API 容量不足、导入 Skill 配置过紧或模型反复调用同一工具；排障步骤、初始 `requests_per_minute` 建议和 local/redis 模式换算见 [Skill Rate Limit 容量与告警 Runbook](runbooks/skill-rate-limit-capacity.md)。仓库也提供可导入的 Grafana dashboard 样例，用于同时观察 skill rate-limit、risk policy、quota denial、tool 调用压力和模型失败。Redis 计数异常时当前策略是 fail closed，优先保护第三方 API；这不替代 Control Plane 的项目级 quota 和账单级配额。

Agent Runtime 还支持 `SKILL_RISK_POLICY` 作为执行前风险门禁，默认 `allow` 保持本地开发兼容。可选值：

- `allow`：只按具体 executor/HTTP/MCP 策略执行，不额外按 manifest 风险拦截。
- `block-high`：拒绝 `risk=high` 的 skill。
- `block-destructive`：拒绝 annotations 中 `destructiveHint=true` 的 skill。
- `read-only`：只允许 `readOnlyHint=true` 且非 destructive、非 high risk 的 skill。

Control Plane 还支持项目级 Runtime policy：

```http
GET /api/projects/{project_id}/runtime-policy
PATCH /api/projects/{project_id}/runtime-policy
```

项目级 `skill_risk_policy` 会随 HTTP dispatcher payload 或 Redis execution context 写入 `RunRequest.skill_risk_policy`，并覆盖 Agent Runtime 的环境变量默认值。前端“项目运行策略”区域也会展示和更新该值。被风险策略拒绝时，Runtime 不会调用真实 tool，也不会预占 tool quota；它会返回 `skill_policy_denied` observation，写入 `tool.output`/`tool.finished` 事件，并递增 `niceagent_skill_policy_denials_total{policy,reason,risk,kind}`。仓库的 Prometheus 告警规则已包含 `NiceAgentSkillPolicyDenials`，可用于发现项目策略过严、导入 manifest 风险标注异常或模型反复尝试不可用工具。K8s 默认配置使用 `block-destructive`，让导入的 MCP/OpenAPI Skill 至少不会执行 manifest 明确标记为破坏性的能力。注意 annotations 仍来自 skill manifest/importer，不能替代 sandbox、SSRF、secret redaction 和真实权限边界。

`workspace.read` 是系统内置只读 skill。它不会让 Runtime 直接读取任意磁盘路径，而是通过 Control Plane 内部 API 列出当前 run 已登记 artifacts，并只读取文本 artifact 的内容摘要。读取会校验 active `attempt_id`，并复用 artifact metadata、workspace root、`output/` 路径限制、symlink escape 检查、regular file 检查、MIME 文本限制和读取大小限制。前端 artifact 面板会对图片、PDF、音频和视频使用 `?disposition=inline` 做内联预览，下载按钮仍走默认 attachment；CSV/TSV 等文本表格会通过 `GET /api/artifacts/{id}/content` 读取前几行做表格预览。排查读取或预览失败时优先看 artifact 是否已登记、文件是否仍在 workspace、MIME 是否正确，以及路径是否在 `output/` 下。

## Postgres 模式排查

启动 Compose：

```bash
docker compose -f deployments/docker-compose.yml up
```

只重启 Control Plane：

```bash
docker compose -f deployments/docker-compose.yml restart control-plane
```

重启后访问 `GET /api/chats` 和 `GET /api/runs/{run_id}/events?after=0`，确认会话、消息、run 和 events 仍可恢复。

Redis run queue 基础配置：

```bash
EVENT_FANOUT_MODE=redis
EVENT_FANOUT_PREFIX=niceagent:run-events
DISPATCH_MODE=redis
RUNTIME_QUEUE_MODE=redis
REDIS_ADDR=redis:6379
RUN_QUEUE_STREAM=niceagent:runs
RUN_QUEUE_GROUP=agent-runtimes
RUN_QUEUE_CONSUMER=agent-runtime-1
AGENT_RUNTIME_ID=agent-runtime-1
RUN_QUEUE_MAX_LEN=100000
RUN_QUEUE_RECLAIM_MIN_IDLE_SECONDS=60
RUN_QUEUE_RECLAIM_COUNT=1
RUN_QUEUE_MAX_DELIVERIES=5
RUN_QUEUE_DLQ_STREAM=niceagent:runs:dlq
RUN_QUEUE_DLQ_MAX_LEN=10000
RUN_ATTEMPT_LEASE_SECONDS=600
RUN_ATTEMPT_HEARTBEAT_SECONDS=60
```

该模式需要 Control Plane 和 Agent Runtime module 的 `github.com/redis/go-redis/v9` 依赖。Control Plane 只负责 `XADD` 最小 payload；如果 `RUN_QUEUE_MAX_LEN>0`，入队时会使用 Redis Streams 近似裁剪控制主 stream 长度。Agent Runtime worker 负责 `XGROUP CREATE MKSTREAM`、`XREADGROUP`、通过 `/internal/runs/{run_id}/execution-context` 拉取执行上下文，随后调用 `/internal/runs/{run_id}/claim` claim 当前 attempt，并在处理成功后 `XACK`。获取 execution context、claim 或执行失败时不 ack，消息保留在 pending entries 中。普通读取没有新消息时，worker 会按 `RUN_QUEUE_RECLAIM_MIN_IDLE_SECONDS` 使用 `XAUTOCLAIM` 回收 pending message；回收后会生成新的 `attempt_id`，避免旧 runtime 迟到回写覆盖新 attempt。超过 `RUN_QUEUE_MAX_DELIVERIES` 的消息会写入 `RUN_QUEUE_DLQ_STREAM` 后 ack，避免无限重试；如果 `RUN_QUEUE_DLQ_MAX_LEN>0`，DLQ stream 也会近似裁剪。`0` 表示不主动裁剪，适合本地调试；生产应结合 Redis 持久化、备份、告警和业务保留窗口设置非零值。

attempt fencing 字段保存在 `runs` 上：`active_attempt_id`、`claimed_by`、`lease_expires_at`、`attempt_count`。如果旧 runtime 用旧 `attempt_id` 回写 event、complete 或 fail，Control Plane 会返回 `409 Conflict`，不会写 assistant message 或 terminal event。

本地可用以下命令跑近似多实例的 Redis queue 冒烟。脚本会启动临时 Redis 容器、一个 Control Plane、一个 Sandbox Executor 和两个 Agent Runtime consumer，发送两条 `/cli echo ...` run，并等待 assistant 回写：

```bash
make smoke-three-services-redis
```

输出中的 `claimed_by` 可用于确认 run 被 Redis worker claim；该 smoke 只验证最小多 runtime 消费和回写链路，不替代生产压测。

多 Control Plane Redis event fanout 也有默认 smoke：

```bash
make smoke-control-plane-fanout
```

该脚本会启动临时 Postgres、Redis、两个 Control Plane、Agent Runtime 和 Sandbox Executor，验证第二个 Control Plane 能通过 Redis nudge + 共享 Postgres 补齐第一个 Control Plane 写入的 `tool.output`、`model.token` 和 `run.succeeded` SSE 事件。CI 的 `Test and build` 已把 `smoke-three-services-redis` 和 `smoke-control-plane-fanout` 纳入默认门禁；生产仍需要 Redis 高可用、保留策略和告警。

Redis stream 保留策略建议：

- 本地或 CI：`RUN_QUEUE_MAX_LEN=0`、`RUN_QUEUE_DLQ_MAX_LEN=0`，便于排查。
- 生产：根据峰值 `runs_per_hour * 保留小时数` 设置 `RUN_QUEUE_MAX_LEN`，根据告警处理窗口设置 `RUN_QUEUE_DLQ_MAX_LEN`。
- 不要把 Redis stream 当作权威 run/event 存储；run、message、event 和 artifact 权威状态仍在 Postgres。
- 对 `RUN_QUEUE_DLQ_STREAM` 长度、Redis 内存、pending entries 数量和 worker reclaim 失败建立告警。

清理本地持久化数据：

```bash
docker compose -f deployments/docker-compose.yml down -v
```

这会删除本地 Postgres volume，适合重新初始化 schema。

## 日志与排查

排查问题时优先关注：

- `run_id`：贯穿一次用户请求的执行链路。
- `X-Trace-ID`：三服务之间会透传的轻量 trace id。浏览器或网关可传入 `X-Trace-ID`/`Traceparent`，Control Plane、Agent Runtime 和 Sandbox Executor 会在响应头继续返回 `X-Trace-ID`。
- `X-Request-ID`：三服务之间会透传的请求标识。浏览器或网关可传入 `X-Request-ID`；如果缺失，服务会生成一个 `req_` 前缀 ID，并在响应头继续返回。Control Plane request log、audit event、Agent Runtime request log 和 Sandbox Executor request log 都会记录它。
- `chat_id`：定位用户会话。
- event `seq`：确认 SSE replay 和事件顺序。
- run terminal state：确认 `succeeded`、`failed`、`canceled` 是否被迟到事件覆盖。
- `MODEL_PROVIDER` 和模型 HTTP 状态：定位真实模型调用失败。

OpenTelemetry traces 默认关闭，避免本地和 CI 误连外部 collector。需要导出到 OTLP HTTP collector 时，在三个服务上配置：

```bash
OTEL_TRACES_EXPORTER=otlp
OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4318
# 或只覆盖 traces endpoint:
# OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://otel-collector:4318/v1/traces
OTEL_EXPORTER_OTLP_INSECURE=true
# collector 需要鉴权时使用，生产环境建议通过 Secret 注入:
# OTEL_EXPORTER_OTLP_HEADERS="Authorization=Bearer <token>,x-tenant=niceagent"
```

服务启动后会以 `control_plane`、`agent_runtime`、`sandbox_executor` 作为默认 service name，也可以用 `OTEL_SERVICE_NAME` 覆盖。当前 OpenTelemetry 会为每个入站 HTTP 请求创建 server span，并通过 `traceparent` 在 Control Plane、Agent Runtime 和 Sandbox Executor 之间传播；`X-Trace-ID` 和 `X-Request-ID` 继续保留，便于日志、audit event 和非 OTel 工具串联。三服务的 JSON request log 都包含 `request_id`、`trace_id`、`method`、`path`、`status` 和 `duration`，Control Plane 还会额外记录 actor 信息。内部 span 已覆盖 Control Plane HTTP dispatcher、Redis run queue enqueue/process/fetch execution context、Runtime run execute、模型调用、tool invoke、HTTP Skill 请求、Runtime 调 Sandbox Executor、Sandbox Executor 命令执行和 Runtime 回写 Control Plane。Redis Streams、Redis PubSub event fanout 和 Redis quota counter 的底层命令会统一输出 `redis.command` span，并带上 `redis_command`、`stream`、`group` 或 `component` 等低基数字段。Postgres repository 会为 `SELECT`、`INSERT`、`UPDATE`、`DELETE`、`BEGIN`、`COMMIT`、`ROLLBACK` 等操作输出 `db.command` span，只记录 `db_system=postgresql`、`db_operation`、`component=repository`，不记录 SQL 文本或参数。

三服务都提供 `GET /metrics`，输出 Prometheus text exposition 风格指标。当前内置指标覆盖：

- `niceagent_http_requests_total`：按 `service/method/path/status` 统计 HTTP 请求。
- `niceagent_http_request_duration_seconds_*`：按同样标签统计 HTTP 请求耗时。
- `niceagent_runs_created_total`：Control Plane 成功创建 run 的次数。
- `niceagent_quota_denials_total`：Control Plane 配额拒绝次数。
- `niceagent_sse_connections_total`：Control Plane SSE 连接打开/关闭次数，当前按 `stream=run_events` 和 `event=opened|closed` 标记。
- `niceagent_sse_active_connections`：Control Plane 当前活跃 SSE 连接数，用于观察 run event 订阅连接压力。
- `niceagent_sse_connection_duration_seconds_*`：Control Plane SSE 连接持续时间 summary。
- `niceagent_runtime_runs_total`：Agent Runtime 执行结果次数。
- `niceagent_model_runs_total`：Agent Runtime 按 provider/model/status/error_class/fallback 统计模型 run。
- `niceagent_model_latency_seconds_*`：Agent Runtime 按 provider/model 统计模型调用耗时。
- `niceagent_model_health_probe_total`：Agent Runtime 主动模型探针成功/失败次数。
- `niceagent_model_health_probe_duration_seconds_*`：Agent Runtime 主动模型探针耗时。
- `niceagent_redis_queue_messages_total`：Agent Runtime Redis worker 读取到的消息数，按 `stream/group/consumer/reclaimed` 标记。
- `niceagent_redis_queue_reclaimed_total`：Agent Runtime 通过 `XAUTOCLAIM` 回收 pending message 的次数。
- `niceagent_redis_queue_acked_total`：Agent Runtime 成功 ack queue message 的次数。
- `niceagent_redis_queue_errors_total`：Agent Runtime Redis worker 在 `ensure_group/read/decode/pending/autoclaim/execute/ack/dead_letter` 等阶段的错误次数。
- `niceagent_redis_queue_dlq_messages_total`：Agent Runtime 写入 DLQ 的消息数，按 `reason` 和 `dlq_stream` 标记。
- `niceagent_redis_queue_lag_entries`：Agent Runtime 通过 Redis `XINFO GROUPS` 采样到的 consumer group lag，`state="known"` 表示 Redis 能确定尚未投递给 group 的 entries 数，`state="unknown"` 表示 Redis 返回 lag 不可确定。
- `niceagent_redis_queue_pending_entries`：Agent Runtime 采样到的当前 consumer group pending entries 总量。
- `niceagent_redis_queue_oldest_pending_idle_seconds`：Agent Runtime 采样到的最老 pending entry idle 秒数，用于发现长期未 ack 或 reclaim 的卡死消息。
- `niceagent_redis_queue_dlq_length`：Agent Runtime 采样到的 DLQ stream 长度。
- `niceagent_sandbox_exec_total`：Sandbox Executor 命令执行结果次数。

这些指标是 Prometheus 风格的最小观测面，适合本地、Compose 和 K8s 通过 Prometheus scraper 或网关转发采集。仓库提供了基础 Prometheus 告警规则文件：`deployments/monitoring/prometheus-alerts.yml`，覆盖 HTTP 5xx/延迟、runtime 失败、skill policy denial、skill rate limit denial、模型 provider 错误/探针失败/延迟、Redis queue error/lag/pending/oldest idle/DLQ、quota denial 和 sandbox failure。仓库也提供了 Alertmanager 路由样例：`deployments/monitoring/alertmanager.example.yml`，按 `severity` 和 `component` 将告警分到 on-call、platform、model-ops、sandbox、quota 等接收组；Grafana dashboard 样例放在 `deployments/monitoring/grafana/dashboards/skill-governance.json`，可导入后选择 Prometheus datasource。可以用下面的仓库内检查做结构验证：

```bash
make check-alerts
```

`alertmanager.example.yml` 中的 webhook URL 都使用 `example.invalid` 占位，生产部署时应替换为真实的告警路由器、IM、短信/电话或云监控地址，并在 Alertmanager Secret 中管理真实 webhook token。生产环境建议再用 Prometheus 自带的 `promtool check rules deployments/monitoring/prometheus-alerts.yml`、Alertmanager 的 `amtool check-config deployments/monitoring/alertmanager.example.yml` 和真实 Grafana 导入做最终校验。OpenTelemetry traces 已有 OTLP HTTP exporter、入站 HTTP span、主要 agent 执行内部 span、Redis 低层命令 span 和 Postgres repository `db.command` span；外部告警系统接入时优先把 `request_id`、`trace_id`、`run_id` 放入排障模板。

## Sandbox 安全边界

当前 local executor 只适合本地演示，不是生产沙箱。生产化前至少需要：

- 容器或更强隔离边界。
- CPU、内存、磁盘、进程数和超时限制。
- workspace 只读/读写挂载策略。
- 网络按只读外部信息获取策略开启。
- 输出大小限制和敏感信息过滤。
- 高风险命令策略拒绝和完整审计。

## CLI 策略排查

当前 CLI 是系统级 agent 工具，不需要用户逐次授权。CLI policy 分三类：

- allowlist 命令：例如 `curl`、`wget`、`dig`、`nslookup`、`echo`、`pwd`、`ls`、`date`，会正常执行并产生 `tool.output`。
- dangerous 命令：例如 `rm`、`sudo`、`chmod`、`chown`、shell 启动和文件写入类命令，不会执行，会作为普通 tool error 返回给 Agent Runtime。
- 非 allowlist 命令：直接策略拒绝，作为普通 tool error 暴露。

`approval.needed` 事件保留给未来真正需要用户确认的非 CLI skill。系统 CLI 不再使用该事件，也不会让 run 进入 `waiting_for_approval`。

## 常用运维检查

检查 Compose 配置：

```bash
make compose-config
```

检查前端脚本语法：

```bash
make check-js
```

构建 React/Rspack 前端：

```bash
make build-web
```

运行 Go 测试：

```bash
make test
```

检查未提交 diff 的空白问题：

```bash
git diff --check
```

构建三服务容器镜像：

```bash
make docker-build
```

## 当前限制

- memory store 无法支撑多实例共享状态；Postgres 模式当前先服务单 Control Plane 副本。
- OpenAI-compatible provider 已切到 Eino 原生 ChatModel；不同 OpenAI-compatible 服务的非标准参数兼容性仍需在后续按 provider-specific option 补强。
- local executor 不提供生产级命令隔离。
- sandbox、认证、租户配额、审批和审计仍需继续完善。
