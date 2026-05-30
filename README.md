# NiceAgent

NiceAgent 是一个网页端远端 agent 服务框架的早期骨架。它的目标是把“用户会话管理、agentic loop、skill 系统、远端 CLI、事件流和运行时调度”拆成清晰的服务边界，方便后续逐步替换为 Postgres、Redis、Eino、容器沙箱等生产化组件。

当前已包含：

- Go Control Plane：提供聊天、run、skill 和 SSE API。
- 无状态 Agent Runtime：负责执行一次 `RunRequest` 的 agentic loop 边界。
- Sandbox Executor 服务入口：为远端 CLI skill 预留执行边界。
- 静态 Web 聊天页面：由 Control Plane 托管。
- Postgres 迁移脚本和 Redis/Postgres Docker Compose 拓扑。

## 当前状态与限制

当前仓库可以作为本地演示和后续扩展的起点，但还不是生产系统：

- `internal/controlplane` 仍使用 memory store，进程重启会丢失会话、消息、run 和事件。
- `internal/runtime` 当前是 mock runtime，只实现最小 agentic loop 演示，还没有真实接入 CloudWeGo Eino ADK。
- `internal/sandbox` 当前是 local executor，只允许少量命令，尚未提供容器级隔离。
- sandbox 尚未生产化，缺少完整的资源限制、网络策略、镜像策略、审计和审批闭环。
- 认证、组织/项目、多租户配额、模型密钥管理仍处于规划阶段。

## 目录结构

```text
cmd/control-plane      Web/API 服务和当前内嵌的本地 run dispatcher
cmd/agent-runtime      无状态 runtime 服务入口
cmd/sandbox-executor   CLI sandbox 服务入口
internal/protocol      共享 JSON 协议与领域类型
internal/controlplane  当前 memory control plane 状态和 HTTP handlers
internal/runtime       agentic loop 边界，预留 Eino 接入
internal/sandbox       本地命令策略 executor
web/static             浏览器聊天 UI
migrations             Postgres schema
deployments            本地 Docker Compose 拓扑
docs                   中文架构、API、开发、路线图和运维文档
```

## 本地运行

安装 Go 后可以直接启动 Control Plane：

```bash
go run ./cmd/control-plane
```

然后打开 `http://localhost:8080`。

内存演示版支持普通聊天和受控 CLI skill：

```text
/cli echo hello
```

使用 Docker Compose 启动依赖和服务：

```bash
docker compose -f deployments/docker-compose.yml up
```

## 本地检查命令

```bash
make check-js
make compose-config
make test
git diff --check
```

如果本机没有 Go 工具链，可以参考 `docs/DEVELOPMENT.md` 中的 Docker 化测试命令。

## 下一阶段开发计划

下一阶段采用多个子 agent 并行完善 NiceAgent。阶段目标是把当前“可演示的内存版 Go 骨架”推进为“模块边界清晰、可替换外部依赖、具备继续生产化基础”的远端 agent 服务框架。

### 多子 agent 分工

- Agent A：Control Plane 持久化与队列。抽象 `Store`，保留 memory 实现，新增 Postgres repository 和 Redis Streams run queue/event fanout 边界。
- Agent B：Agent Runtime 与 Eino 接入边界。拆分 runtime pipeline，定义 `AgentEngine`、`ToolBridge`、`ModelProvider`，保留 mock provider 并预留 Eino ADK adapter。
- Agent C：Sandbox Executor 与远端 CLI。抽象 `SandboxExecutor`，增加 workspace、超时、环境过滤、输出限制、审批语义和 Docker/container executor 入口。
- Agent D：Web 前端体验。中文化 UI，完善聊天管理、run 状态、事件面板、CLI 输出和 skills 展示区，继续保持静态前端。
- Agent E：中文文档与开发者体验。维护 README 和 `docs/*`，补充本地开发、路线图、运维说明和检查入口。
- Agent F：集成测试与契约检查。补充 API handler、runtime、event replay、terminal state、SSE 和取消语义测试。

### 验收标准

- 创建聊天，发送普通消息，能收到 assistant 回复和 run events。
- 发送 `/cli echo hello`，能看到 tool started/output/finished/succeeded 事件。
- 刷新页面后能恢复会话和最近 run 状态。
- 取消 run 后，后台完成不能覆盖 canceled 终态。
- Postgres/Redis 模式和 memory 模式至少各有一条可运行路径。
- README 明确记录下一阶段计划，所有面向人的说明文档使用中文。
- `git diff --check`、`make check-js`、`make compose-config` 和 `go test ./...` 在具备对应工具链的环境中通过。

更多拆解见 `docs/ROADMAP.md`，架构边界见 `docs/ARCHITECTURE.md`，API 契约见 `docs/API.md`。
