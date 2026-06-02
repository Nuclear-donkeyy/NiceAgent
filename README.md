# NiceAgent

NiceAgent 是一个网页端远端 agent 服务框架。它把前置服务器、agent 实例服务、sandbox 执行器、React 前端和共享协议拆成可独立部署、可独立演进的模块。

长期产品目标和架构原则见 [target.md](target.md)。下一阶段工程路线见 [docs/ROADMAP.md](docs/ROADMAP.md)。

## 仓库结构

```text
services/control-plane       前置服务器，负责用户会话、run、events、skills 和 Web/API
services/agent-runtime       agent 实例服务，负责无状态 agentic loop 执行边界
services/sandbox-executor    CLI/sandbox 执行服务，负责命令执行边界
packages/common              公共 Go module，放共享协议、平台工具和可复用 sandbox 组件
frontend                     React + Rspack 前端应用
migrations                   Postgres schema
deployments                  Docker Compose 拓扑
build/docker                 三个后端服务的生产镜像 Dockerfile
.github/workflows            CI 和阿里云 CD 工作流
docs                         中文架构、API、开发、路线图和运维文档
```

根目录使用 `go.work` 协调多个 Go module。三个后端服务各自拥有 `go.mod`，公共代码只通过 `packages/common` 复用。

## 当前状态

- Control Plane 默认使用 memory store，适合本地演示；配置 `STORE_DRIVER=postgres` 和 `DATABASE_URL` 后可切到 Postgres 持久化。
- Control Plane 的主路径是 HTTP dispatcher，可通过 `AGENT_RUNTIME_URL` 调度独立 Agent Runtime；未配置时回退到本地 demo dispatcher。
- Agent Runtime 使用 Eino ADK `ChatModelAgent + Runner` 和 Eino 原生 `ToolCallingChatModel`；mock provider 用于本地/CI，OpenAI-compatible provider 通过 `eino-ext` OpenAI ChatModel 接入，ToolBridge 负责执行系统 CLI、用户 HTTP Skill 和最小 MCP HTTP JSON-RPC Skill。
- Sandbox 当前提供独立 Sandbox Executor 服务、local executor 和 container executor 入口；系统 CLI 是 agent runtime 的无用户授权工具，按只读网络型策略执行或拒绝命令。
- Artifact 支持 workspace metadata、列表、下载、内容预览、可选 `expires_at`、内部过期 metadata 清理和本地 workspace 文件回收；对象存储归档/生命周期后续继续补齐。
- Frontend 使用 React + Rspack + TypeScript + SCSS Modules，支持会话搜索、归档/恢复、最近 run replay、系统能力/我的能力分组展示、HTTP Skill 添加入口和 MCP tools/list 导入入口；主界面以聊天和 agent 当前状态为中心。
- Skill 元数据采用 Postgres/memory 双实现，按用户/项目 grant 加载；HTTP/MCP Skill 的 bearer token 不返回前端，当前支持明文开发密钥、`env://` 和受限 `file://` secret ref，并可用 Redis 做跨 Runtime 的 per-skill rate limit；Runtime 可通过 `SKILL_RISK_POLICY` 配置默认门禁，Control Plane 也可按项目下发 `skill_risk_policy` 并在前端展示/调整；生产级 KMS/Vault/External Secret 原生 resolver 后续补齐。
- 三服务已支持 `X-Trace-ID`、标准 `traceparent`、`/metrics` 和可选 OpenTelemetry OTLP HTTP exporter；OTel 已覆盖 HTTP 入口、调度/回写、run/tool/model、HTTP Skill、Sandbox 和 Redis queue 关键 span，默认关闭 exporter，本地/CI 不依赖外部 collector。
- Redis Streams 已有 Control Plane 入队、Agent Runtime worker、attempt/lease fencing 和跨副本 event fanout 最小闭环；集中式 secret backend、NiceAgent 内置登录、多租户商业化配额和完整审批恢复仍在后续阶段。

## 本地运行

三服务 HTTP 直连本地启动：

```bash
make run-sandbox
SANDBOX_EXECUTOR_URL=http://127.0.0.1:8082 make run-runtime
AGENT_RUNTIME_URL=http://127.0.0.1:8081 CONTROL_PLANE_PUBLIC_URL=http://127.0.0.1:8080 make run-control
```

验证 Postgres 持久化链路：

```bash
docker compose -f deployments/docker-compose.yml up
```

启动 React 前端开发服务器：

```bash
cd frontend
npm install
npm run dev
```

Rspack dev server 默认运行在 `http://localhost:3000`，并把 `/api`、`/healthz` 代理到 `http://127.0.0.1:8080`。

构建前端静态产物：

```bash
make build-web
```

## 常用检查

```bash
make test
make check-js
make compose-config
make docker-build
git diff --check
```

如果本机 Go cache 权限受限：

```bash
GOCACHE=/private/tmp/niceagent-go-cache make test
```

## 文档入口

- [target.md](target.md)：产品目标和长期架构原则。
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)：当前模块边界和架构说明。
- [docs/API.md](docs/API.md)：外部和内部 API 契约。
- [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md)：本地开发指南。
- [docs/LOCAL_K8S.md](docs/LOCAL_K8S.md)：本地 Kubernetes 部署测试流程。
- [docs/ROADMAP.md](docs/ROADMAP.md)：下一阶段行动计划。
- [docs/OPERATIONS.md](docs/OPERATIONS.md)：运行和排障说明。
- [docs/CICD.md](docs/CICD.md)：CI/CD 和阿里云发布说明。
- [AGENTS.md](AGENTS.md)：AI coding 和子 agent 协作规则。
