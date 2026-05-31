# NiceAgent 本地开发指南

## 环境准备

推荐安装：

- Go：运行和测试三个后端服务。
- Node.js/npm：运行 React + Rspack 前端。
- Docker/Docker Compose：启动 Postgres、Redis 和服务拓扑。

## 后端服务

仓库根目录使用 `go.work` 组织多个 module：

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

如需模拟内部鉴权，三个服务使用同一个 `INTERNAL_API_TOKEN`；为空时内部 API 不校验 bearer token。

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

`MODEL_BASE_URL` 不需要包含 `/v1/chat/completions`，runtime 会自动调用 `{MODEL_BASE_URL}/v1/chat/completions` 并解析 SSE token。

Postgres 持久化路径：

```bash
docker compose -f deployments/docker-compose.yml up
```

Compose 会启动 Postgres、Redis、Control Plane、Agent Runtime 和 Sandbox Executor。Control Plane 在该拓扑中使用 `STORE_DRIVER=postgres`，数据库连接来自 `DATABASE_URL`。重启 Control Plane 后，会话、消息、run 和 run events 应继续保留。

Skill manifest 会写入 `skills`、`skill_versions`、`skill_grants` 和 `skill_secrets`。如果本地 schema 已经旧了，可以用 `docker compose -f deployments/docker-compose.yml down -v` 清理 volume 后重新启动。

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

Control Plane 默认托管 `../../frontend/dist`。如需指定其他目录：

```bash
WEB_DIST_DIR=/absolute/path/to/dist make run-control
```

## 检查命令

```bash
make test
make check-js
make compose-config
git diff --check
```

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
docker run --rm -v "$PWD":/workspace -w /workspace golang:1.22 \
  go test ./packages/common/... ./services/control-plane/... ./services/agent-runtime/... ./services/sandbox-executor/...
```

## 开发约定

- 面向人的文档和 UI 文案默认使用中文。
- 服务间共享代码只能放入 `packages/common`，不要跨 module import 其他服务的 `internal` 包。
- Control Plane、Agent Runtime、Sandbox Executor 应保持可独立部署。
- 代码标识符、API path、JSON 字段、环境变量保持英文。
- 不要在文档中把尚未完成的能力写成已完成能力。
