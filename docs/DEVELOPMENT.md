# NiceAgent 本地开发指南

## 环境准备

推荐安装：

- Go：运行和测试三个后端服务。
- Node.js/npm：运行 React + Rspack 前端。
- Docker/Docker Compose：启动 Postgres、Redis 和服务拓扑。

## 后端服务

仓库根目录使用 `go.work` 组织多个 module：

三个服务都是独立部署单元，服务内按职责分层：

- `cmd/main.go`：只做配置读取、依赖装配和 HTTP server 启动。
- `internal/config`：环境变量解析。
- Control Plane：`httpapi/app/repository/dispatch/events`。
- Agent Runtime：`httpapi/engine/modelprovider/tools/sink`。
- Sandbox Executor：`httpapi/executor/policy`。

进入子项目修改前先读对应目录下的 `AGENTS.md`。

memory 快速开发路径：

```bash
make run-sandbox
make run-runtime
make run-control
```

三服务 HTTP 直连开发模式建议按以下顺序启动：

1. 启动 Sandbox Executor：`make run-sandbox`。
2. 启动 Agent Runtime，并让它通过 HTTP 调用 sandbox：`SANDBOX_EXECUTOR_URL=http://127.0.0.1:8082 make run-runtime`。
3. 启动 Control Plane，并让它通过 HTTP 调度 runtime：`AGENT_RUNTIME_URL=http://127.0.0.1:8081 CONTROL_PLANE_PUBLIC_URL=http://127.0.0.1:8080 make run-control`。

如果没有配置 `AGENT_RUNTIME_URL`，Control Plane 会回退到本地 demo dispatcher。如果没有配置 `SANDBOX_EXECUTOR_URL`，Agent Runtime 会回退到 local sandbox executor。

Sandbox Executor 默认使用 `EXECUTOR_MODE=local`。如需验证 Docker 容器执行路径，可在确认本机 Docker CLI 和 daemon 可用后运行自动 smoke：

```bash
make smoke-sandbox-container
```

该 smoke 会启动一个临时 Sandbox Executor，设置 `EXECUTOR_MODE=container` 和 `SANDBOX_CONTAINER_LOCAL_FALLBACK=false`，并通过 `/healthz` 与 `/internal/sandbox/exec` 验证容器执行路径。没有 Docker CLI 或 daemon 时会输出 `SKIP` 并成功退出，避免阻断普通本地开发。

也可以手动启动容器执行路径：

```bash
EXECUTOR_MODE=container \
SANDBOX_CONTAINER_IMAGE=alpine:3.20 \
SANDBOX_CONTAINER_ALLOWED_IMAGES=alpine:3.20 \
SANDBOX_CONTAINER_LOCAL_FALLBACK=false \
make run-sandbox
```

`SANDBOX_CONTAINER_ALLOWED_IMAGES` 是逗号分隔镜像白名单；设置后，`EXECUTOR_MODE=container` 只允许使用白名单中的 `SANDBOX_CONTAINER_IMAGE`。`GET /healthz` 会显示当前 `executor_mode`、`container_image` 和白名单，方便确认是否真的跑在 container profile。

多 Control Plane 副本需要实时唤醒 SSE 时，可以使用 Redis nudge fanout：

```bash
EVENT_FANOUT_MODE=redis
EVENT_FANOUT_PREFIX=niceagent:run-events
REDIS_ADDR=127.0.0.1:6379
```

该模式只把 `run_id/seq` 作为提醒发到 Redis，事件正文仍以 repository/Postgres 为权威；前端断线后继续依赖 `?after=` replay 补齐。

需要给 Redis Streams run queue 做轻量容量冒烟时，可以运行：

```bash
make smoke-redis-capacity
```

该命令默认启动临时 Redis 容器，写入并消费独立的 capacity stream，输出 JSON 报告；没有 Docker 时会 `SKIP`。如需验证已有 Redis，设置 `REDIS_ADDR=host:port` 并直接运行 `python3 scripts/smoke_redis_capacity.py`。

如需模拟内部鉴权，三个服务使用同一个 `INTERNAL_API_TOKEN`；本地裸跑默认允许为空，内部 API 不校验 bearer token。上线或近云环境应开启：

```bash
NICEAGENT_ENV=production
INTERNAL_API_TOKEN_REQUIRED=true
INTERNAL_API_TOKEN=<same-random-token-for-three-services>
```

当 `INTERNAL_API_TOKEN_REQUIRED=true`，或 `NICEAGENT_ENV` 不是 `local/dev/development/test/ci` 时，Control Plane、Agent Runtime 和 Sandbox Executor 都会在缺少 token 时启动失败。Compose 本地模式使用共享的 dev token 验证鉴权链路，但这个 token 不能用于生产。

OpenTelemetry 默认关闭。需要在本地把三服务 traces 发到 OTLP HTTP collector 时，给每个服务加上：

```bash
OTEL_TRACES_EXPORTER=otlp
OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318
OTEL_EXPORTER_OTLP_INSECURE=true
```

如果 collector 要求鉴权，使用 `OTEL_EXPORTER_OTLP_HEADERS`，例如 `Authorization=Bearer <token>`；生产环境应通过 Secret 注入。不开启 exporter 时，服务仍会透传 `X-Trace-ID` 和标准 `traceparent`。

外部 API 默认使用 `AUTH_MODE=demo`。如果要在本地模拟上游网关透传身份，可以设置 `AUTH_MODE=trusted-header`，并在请求里传：

```bash
X-NiceAgent-User-ID: user-a
X-NiceAgent-Project-ID: project-a
X-NiceAgent-Org-ID: org-a
X-NiceAgent-Roles: owner
```

`AUTH_MODE=oidc` 会直接校验 `Authorization: Bearer <jwt>`，要求配置 `OIDC_ISSUER_URL` 和 `OIDC_AUDIENCE`，并通过 `OIDC_JWKS_URL` 或默认 `<issuer>/.well-known/jwks.json` 拉取 RS256 JWKS。它也支持最小浏览器 OIDC authorization code flow：配置 `OIDC_CLIENT_ID`、`OIDC_AUTH_URL`、`OIDC_TOKEN_URL` 和 `OIDC_SESSION_SECRET` 后，`GET /auth/oidc/login` 会跳转 IdP，callback 会写入 `niceagent_session` HttpOnly cookie 和 `niceagent_csrf` cookie，`POST /auth/oidc/refresh` 与 `POST /auth/logout` 必须携带 `X-NiceAgent-CSRF`。浏览器 session 会持久化到 `oidc_browser_sessions`，refresh 会撤销旧 session 并签发新 session，logout 会撤销当前 session。

OIDC token 至少需要包含：

- `iss`、`sub`、`aud`、`exp`
- 项目 claim，默认 `niceagent_project_id`，也可以通过 `OIDC_DEFAULT_PROJECT_ID` 给单项目部署兜底
- 角色 claim，默认 `niceagent_roles`；如果没有 roles，Control Plane 会从持久 membership 表加载角色

`X-NiceAgent-Roles` 支持最小 RBAC：`viewer` 只能读，`owner/admin/member/editor/writer` 可以写。Control Plane 的 handler 通过默认 action-level policy 做授权，新增写接口时应在 `services/control-plane/internal/httpapi/action_policy.go` 中登记 action requirement，并继续写 deny audit event。如果 trusted header 请求没有传 roles，Control Plane 会从持久 `project_members` 中读取当前用户在当前项目的角色；没有成员关系时返回 `403`。本地 demo 模式仍固定使用 `demo-user/demo-project/owner`。

需要本地验证策略调整时，可设置 `ACTION_POLICY_FILE` 指向 JSON 文件，例如：

```json
{
  "actions": {
    "chat.create": "project_admin"
  }
}
```

文件会覆盖或新增默认 action requirement，合法值为 `write`、`project_admin`、`organization_admin`。非法文件会让 Control Plane 启动失败。

邀请邮件默认关闭。需要在本地验证 SMTP 配置时，可以设置 `INVITATION_EMAIL_MODE=smtp`、`SMTP_HOST`、`SMTP_FROM` 和可选 `INVITATION_EMAIL_SUBJECT_TEMPLATE`、`INVITATION_EMAIL_BODY_TEMPLATE`。邮件模板使用 Go `text/template` 语法，可用字段包括 `.Email`、`.Role`、`.OrganizationID`、`.ProjectIDOrDash`、`.AcceptURL` 和 `.ExpiresAt`；启动时会校验模板，避免未知字段进入运行期。需要测试进程内队列化投递时，可设置 `INVITATION_EMAIL_QUEUE_MODE=memory`、`INVITATION_EMAIL_QUEUE_SIZE`、`INVITATION_EMAIL_QUEUE_WORKERS` 和 `INVITATION_EMAIL_RETRY_ATTEMPTS`。需要测试可重启恢复的持久投递时，在 Postgres 模式下设置 `INVITATION_EMAIL_QUEUE_MODE=outbox`，投递任务会写入 `invitation_email_outbox`，后台 worker 会 claim due jobs 并重试。需要测试邮件服务商回调时，可以设置 `INVITATION_EMAIL_WEBHOOK_SECRET` 并附带 `X-NiceAgent-Webhook-Signature: sha256=<hmac_sha256(secret, raw_body)>`；也可以配置 `INVITATION_EMAIL_SENDGRID_PUBLIC_KEY`、`INVITATION_EMAIL_MAILGUN_SIGNING_KEY` 或 `INVITATION_EMAIL_SNS_SIGNATURE_VERIFICATION=true` 验证服务商原生签名。SES/SNS 场景可用 `INVITATION_EMAIL_SNS_TOPIC_ARN` 限制来源 topic。

最小 run 配额可通过环境变量开启，默认 `0` 表示不限制：

```bash
QUOTA_MAX_CONCURRENT_RUNS=0
QUOTA_RUNS_PER_HOUR=0
QUOTA_MODEL_TOKENS_PER_DAY=0
QUOTA_TOOL_CALLS_PER_DAY=0
QUOTA_SANDBOX_SECONDS_PER_DAY=0
QUOTA_COUNTER_MODE=repository
QUOTA_MODEL_TOKEN_RESERVATION_PER_RUN=0
QUOTA_MODEL_TOKEN_RESERVATION_MODE=fixed
QUOTA_MODEL_TOKEN_DYNAMIC_OUTPUT_BUFFER=0
QUOTA_MODEL_TOKEN_ESTIMATOR_MODEL=
```

超过配额时，`POST /api/chats/{chat_id}/messages` 会返回 `429`，并写入 `quota.run.create` deny audit event。默认 `QUOTA_COUNTER_MODE=repository` 直接按 repository 统计；需要更接近多副本部署时可设 `QUOTA_COUNTER_MODE=redis`，让并发 run 和每小时 run 数先走 Redis 预占。`QUOTA_MODEL_TOKEN_RESERVATION_MODE=fixed` 时使用 `QUOTA_MODEL_TOKEN_RESERVATION_PER_RUN` 固定预占；设为 `dynamic` 时会按当前用户消息长度估算 input tokens，并叠加 `QUOTA_MODEL_TOKEN_DYNAMIC_OUTPUT_BUFFER` 作为输出缓冲，run 结束时再按真实 `RunUsage.total_tokens` 结算差额。`QUOTA_MODEL_TOKEN_ESTIMATOR_MODEL` 可指定动态预占使用的 tokenizer 模型，例如 `gpt-4o`、`o3-mini`、`deepseek-chat`、`qwen-max`、`kimi-k2`、`doubao-pro`、`glm-4.5`、`mistral-large` 或 `llama-*`；为空或未知模型时回退到 `heuristic_rune_div4`。tool calls 和 sandbox seconds 会先按已有 `RunUsage` 做 run 创建保护，并在 Runtime 每次调用 tool 前通过内部 quota reserve 做最小实时预占；更细粒度账单维度、provider 官方 tokenizer 和分布式强一致 token bucket 仍是后续工作。

Runtime 可用 `SKILL_RISK_POLICY` 做额外 skill 风险门禁：`allow` 保持本地开发默认行为，`block-high` 拒绝 `risk=high`，`block-destructive` 拒绝 `destructiveHint=true`，`read-only` 只允许 `readOnlyHint=true` 且非 destructive/high risk 的能力。项目级配置可通过 `GET/PATCH /api/projects/{project_id}/runtime-policy` 管理，并会随 run 下发为 `RunRequest.skill_risk_policy`，优先覆盖 Runtime 默认值。策略拒绝会作为 `skill_policy_denied` observation 返回给 agent，不会调用真实 tool，也不会预占 tool quota。

Agent Runtime 默认使用 `MODEL_PROVIDER=mock`。如需接 OpenAI-compatible 模型服务：

```bash
MODEL_PROVIDER=openai-compatible \
MODEL_BASE_URL=https://api.example.com \
MODEL_API_KEY=replace-with-api-key \
MODEL_NAME=example-model \
MODEL_TIMEOUT_SECONDS=120 \
SANDBOX_EXECUTOR_URL=http://127.0.0.1:8082 \
make run-runtime
```

`MODEL_BASE_URL` 必须是 provider 根地址，不要包含 `/v1/chat/completions`、`/chat/completions` 或 `/completions`；runtime 启动时会校验 URL、API key 和模型名。Runtime 会通过 Eino `eino-ext` OpenAI ChatModel 调用 chat completions 协议，并使用 Eino 原生 tool calling 能力。

DeepSeek 仍使用同一个 OpenAI-compatible provider。推荐使用 `MODEL_PROVIDER_PROFILE=deepseek` 作为配置预设：Runtime 会自动把空的 `MODEL_BASE_URL` 补为官方 OpenAI-compatible 根地址 `https://api.deepseek.com`，并在 usage 里把 provider 标记为 `deepseek`；API key 和模型名仍必须显式配置，模型名以 DeepSeek 官方文档当前值为准。

```bash
MODEL_PROVIDER=openai-compatible \
MODEL_PROVIDER_PROFILE=deepseek \
MODEL_BASE_URL=https://api.deepseek.com \
MODEL_API_KEY=replace-with-deepseek-key \
MODEL_NAME=replace-with-official-deepseek-model \
MODEL_TIMEOUT_SECONDS=120 \
SANDBOX_EXECUTOR_URL=http://127.0.0.1:8082 \
make run-runtime
```

本地 API key 只放在未提交的 `.env` 或 shell 环境变量里。错误 key 应返回 `auth_error`，余额不足应返回 `billing_error`，日志和 run error 不应出现 `MODEL_API_KEY` 或 `Authorization` header。

拿到真实 DeepSeek key 后，可以运行可选冒烟脚本验证 Runtime provider/profile/health probe 路径：

```bash
export DEEPSEEK_API_KEY="sk-..."
export DEEPSEEK_MODEL="replace-with-official-deepseek-model"
make smoke-deepseek-runtime
```

未设置 key 或模型名时该目标会输出 `SKIP` 并返回成功，不会产生真实模型调用；需要强制要求 key 时运行 `python3 scripts/smoke_deepseek_runtime.py --require-key`。详细记录模板见 `docs/runbooks/deepseek-runtime-smoke.md`。

真实 key 冒烟会写入机器可读 JSON 报告，字段包含 `result=passed|failed|skipped`、provider/model、probe 状态、错误分类、延迟和 `checks`。失败时也会写入脱敏后的报告，方便作为发布验收或排障证据；报告默认在 `.local/deepseek-smoke/`，不会提交到仓库。脚本自身的报告构造和脱敏逻辑由 `make check-scripts` 覆盖。

如需本地验证 fallback，可先让主 provider 指向 fake/failing OpenAI-compatible 服务，再配置：

```bash
MODEL_FALLBACK_PROVIDER=mock
```

如果后备 provider 也是真实 OpenAI-compatible 服务，则使用 `MODEL_FALLBACK_BASE_URL`、`MODEL_FALLBACK_API_KEY`、`MODEL_FALLBACK_NAME`。fallback 只对 `rate_limited`、`provider_unavailable`、`network_error` 生效，不会掩盖鉴权、计费或请求格式错误。

如需在本地验证 usage cost，可以按 provider 官方价格自行配置，不要把实时价格写入仓库：

```bash
MODEL_INPUT_PRICE_PER_1M_TOKENS=0
MODEL_CACHED_INPUT_PRICE_PER_1M_TOKENS=0
MODEL_OUTPUT_PRICE_PER_1M_TOKENS=0
MODEL_REASONING_PRICE_PER_1M_TOKENS=0
MODEL_PRICE_CURRENCY=USD
```

Runtime 会把 token usage、tool usage 和可选费用写入 `RunUsage`，Control Plane 会随 `GET /api/runs/{run_id}` 返回。tool/sandbox 在调用前会先写入预占 usage，run 完成时再以实际 `RunUsage` 覆盖预占快照。

如需在本地避免误打过多真实模型请求，可以加 Runtime 进程内保护阀：

```bash
MODEL_REQUESTS_PER_MINUTE=30
MODEL_MAX_CONCURRENT_REQUESTS=2
```

命中 `MODEL_REQUESTS_PER_MINUTE` 会被归类为 `rate_limited`，如果配置了 `MODEL_FALLBACK_PROVIDER`，会走已有 fallback 策略。该限流只在当前 Runtime 进程内生效，不替代供应商账号级限流。

Postgres 持久化路径：

```bash
docker compose -f deployments/docker-compose.yml up
```

Compose 会启动 Postgres、Redis、Control Plane、Agent Runtime 和 Sandbox Executor。Control Plane 在该拓扑中使用 `STORE_DRIVER=postgres`，数据库连接来自 `DATABASE_URL`。重启 Control Plane 后，会话、消息、run 和 run events 应继续保留。

Skill manifest 会写入 `skills`、`skill_versions`、`skill_grants` 和 `skill_secrets`。如果本地 schema 已经旧了，可以用 `docker compose -f deployments/docker-compose.yml down -v` 清理 volume 后重新启动。

如需验证 Redis Streams 入队路径，可以把 Control Plane 切到：

```bash
DISPATCH_MODE=redis REDIS_ADDR=localhost:6379 RUN_QUEUE_STREAM=niceagent:runs make run-control
```

同时把 Agent Runtime 切到 Redis worker：

```bash
RUNTIME_QUEUE_MODE=redis \
CONTROL_PLANE_URL=http://127.0.0.1:8080 \
REDIS_ADDR=localhost:6379 \
RUN_QUEUE_STREAM=niceagent:runs \
RUN_QUEUE_GROUP=agent-runtimes \
RUN_QUEUE_CONSUMER=agent-runtime-local \
AGENT_RUNTIME_ID=agent-runtime-local \
SANDBOX_EXECUTOR_URL=http://127.0.0.1:8082 \
make run-runtime
```

Redis 模式下 queue payload 只保存 `run_id`、`attempt_id` 和入队时间；Agent Runtime 消费后会通过 Control Plane 内部 API 拉取完整 execution context，包括用户消息、workspace、当前用户可用 `RuntimeSkill` 和回调地址。执行前 Runtime 会 claim 当前 attempt，后续 event/complete/fail 都必须携带匹配的 `attempt_id`。该模式已经可以跑通最小端到端链路，并具备 pending reclaim、heartbeat 和 DLQ；跨 Control Plane event fanout 仍是后续工作。

Redis worker 还支持最小恢复配置：

```bash
RUN_QUEUE_RECLAIM_MIN_IDLE_SECONDS=60
RUN_QUEUE_RECLAIM_COUNT=1
RUN_QUEUE_MAX_DELIVERIES=5
RUN_QUEUE_DLQ_STREAM=niceagent:runs:dlq
RUN_QUEUE_MAX_LEN=0
RUN_QUEUE_DLQ_MAX_LEN=0
RUN_ATTEMPT_LEASE_SECONDS=600
RUN_ATTEMPT_HEARTBEAT_SECONDS=60
```

当普通 `XREADGROUP` 没有新消息时，Runtime 会用 `XAUTOCLAIM` 回收 idle pending message。回收消息会生成新的 `attempt_id` 并重新 claim run；超过最大投递次数的消息会写入 DLQ stream 后 ack。执行中的 run 会按 heartbeat 周期续租 lease，续租失败时停止当前 attempt。`RUN_QUEUE_MAX_LEN` 和 `RUN_QUEUE_DLQ_MAX_LEN` 为 `0` 时不裁剪；大于 `0` 时分别对主 queue stream 和 DLQ stream 使用 Redis 近似裁剪，方便本地模拟生产保留窗口。

如果只想本机直接连接已有 Postgres：

```bash
STORE_DRIVER=postgres DATABASE_URL=postgres://niceagent:niceagent@localhost:5432/niceagent?sslmode=disable make run-control
```

也可以进入单个服务目录运行：

```bash
cd services/control-plane && go run ./cmd
cd services/agent-runtime && go run ./cmd
cd services/sandbox-executor && go run ./cmd
```

## 前端应用

```bash
cd frontend
npm install
npm run dev
```

前端开发服务默认在 `http://localhost:3000`，API 代理到 `http://127.0.0.1:8080`。

构建静态产物：

```bash
make build-web
```

前端工程化约定：

- 使用 TypeScript，新增文件使用 `.ts` 或 `.tsx`。
- `src/app` 只负责应用装配和跨 feature 状态编排。
- `src/api` 放 HTTP client 和 API 函数。
- `src/domain` 放前端领域类型和展示 label。
- `src/features` 按业务能力放组件、hook 和同名 SCSS Module。
- `src/components` 放跨 feature 复用的小组件。
- 样式默认使用 SCSS Modules；`src/styles/global.scss` 只放 reset、CSS variables 和基础页面背景。
- Prettier 负责 TS/TSX/SCSS/JSON/MD 格式检查。

前端检查：

```bash
cd frontend
npm run format:check
npm run typecheck
npm run lint
npm run build
```

Control Plane 默认托管 `../../frontend/dist`。如需指定其他目录：

```bash
WEB_DIST_DIR=/absolute/path/to/dist make run-control
```

## 检查命令

```bash
make test
make check-js
make smoke-three-services
make smoke-three-services-ui
make smoke-three-services-redis
make smoke-control-plane-fanout
make compose-config
git diff --check
```

`make smoke-three-services` 会启动独立的 Control Plane、Agent Runtime 和 Sandbox Executor 进程，发送 `/cli echo hello`，并等待 assistant 消息回写。它默认走 HTTP dispatcher；如需验证 Redis dispatcher，可先启动 Redis，再运行：

```bash
python3 scripts/smoke_three_services.py --dispatch-mode redis --redis-addr 127.0.0.1:6379
```

如果本机有 Docker，也可以直接运行临时 Redis，并启动两个 Agent Runtime consumer 验证 Redis Streams 多实例消费路径：

```bash
make smoke-three-services-redis
```

该命令会使用唯一 stream/group，执行两次 `/cli echo ...`，并校验每个 run 的 `claimed_by` 都来自预期 Runtime consumer；当启动多个 Runtime 时，还会确认至少两个 consumer 实际 claim 到工作。

需要验证多 Control Plane 进程之间的 Redis event fanout 时运行：

```bash
make smoke-control-plane-fanout
```

该命令会启动临时 Postgres、Redis、两个 Control Plane、Agent Runtime 和 Sandbox Executor。请求从第一个 Control Plane 创建 run，Runtime 回写第一个 Control Plane；脚本从第二个 Control Plane 订阅同一个 run 的 SSE，并验证 `tool.output`、`model.token` 和 `run.succeeded` 能通过 Redis nudge + 共享 Postgres 被补齐。

需要验证真实浏览器 UI 与三服务联动时运行：

```bash
make smoke-three-services-ui
```

该命令会先构建前端，再启动 Sandbox Executor、Agent Runtime 和 Control Plane，由 Control Plane 托管 `frontend/dist`，最后用 Playwright 打开真实页面，发送 `/cli echo hello-ui-smoke`，验证 assistant 回复、run 终态、artifact list API 和刷新后的消息恢复。它依赖 `frontend/node_modules` 与 Playwright 浏览器已安装；如果只想验证前端交互而不启动真实后端，继续使用 `cd frontend && npm run smoke:e2e`。

Postgres repository 测试默认跳过；如需运行，需要提供测试数据库：

```bash
TEST_DATABASE_URL=postgres://niceagent:niceagent@localhost:5432/niceagent?sslmode=disable go test ./services/control-plane/...
```

## 本地 Kubernetes

如果想在本机跑更接近云部署的环境，推荐使用 kind。它会通过 Docker 启动一个轻量 Kubernetes 集群，适合验证 `deployments/k8s`。

完整部署、冒烟测试和常见问题见 [LOCAL_K8S.md](LOCAL_K8S.md)。

安装工具：

```bash
brew install kind kubectl
```

创建集群并部署：

```bash
make kind-create
make kind-deploy
```

`make k8s-apply` 会同时应用 `deployments/k8s/sandbox-hardening.yaml`，为 namespace 设置默认资源限制/配额，并限制只有 Agent Runtime Pod 可以访问 Sandbox Executor Service。

查看状态：

```bash
make k8s-status
```

把 Control Plane 转发到本机：

```bash
make k8s-port-forward
```

浏览器打开 `http://127.0.0.1:8080`。结束后可以删除本地集群：

```bash
make kind-delete
```

本地 kind 与 ACK 使用同一套 Kubernetes YAML；区别是 kind 不会创建阿里云 SLB，本地访问使用 `kubectl port-forward`。

如果 Go cache 目录不可写：

```bash
GOCACHE=/private/tmp/niceagent-go-cache make test
```

如果不想在本机装 Go，可以用 Docker：

```bash
docker run --rm -v "$PWD":/workspace -w /workspace golang:1.23 \
  go test ./packages/common/... ./services/control-plane/... ./services/agent-runtime/... ./services/sandbox-executor/...
```

## 开发约定

- 面向人的文档和 UI 文案默认使用中文。
- 服务间共享代码只能放入 `packages/common`，不要跨 module import 其他服务的 `internal` 包。
- Control Plane、Agent Runtime、Sandbox Executor 应保持可独立部署。
- 代码标识符、API path、JSON 字段、环境变量保持英文。
- 不要在文档中把尚未完成的能力写成已完成能力。
- AI/子 agent 修改仓库时优先阅读根目录 [AGENTS.md](../AGENTS.md)。
