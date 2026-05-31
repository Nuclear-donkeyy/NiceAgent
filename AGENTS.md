# NiceAgent 项目导览

本文件面向 Codex、子 agent 和人类协作者。进入仓库后先读这里，用来快速理解 NiceAgent 的产品目标、服务边界、目录结构和修改约定。

## 项目是什么

NiceAgent 是一个网页端远端 agent 服务框架。用户在 Web 聊天界面里管理会话并发起任务；Control Plane 管理用户、会话、消息、run、events、skills 和调度；Agent Runtime 执行 agentic loop、模型调用和 skill/tool 调用；Sandbox Executor 为系统 CLI 和文件类能力提供隔离执行边界。

核心产品形态：

- 前端是聊天优先的工作台：左侧会话和能力列表，右侧主对话区。
- 每次用户消息会创建一个可追踪的 `Run`。
- 会话状态不放在 Agent Runtime 内，权威状态由 Control Plane 和存储维护。
- Skill 分为系统固定能力和用户可添加能力；Control Plane 按用户/项目 grant 后下发给 Runtime。
- 系统 CLI 是 agent 内部工具，用于获取外部信息；它不走用户逐次授权，但必须通过 sandbox 和策略限制。

长期目标和边界见 [target.md](target.md)，当前架构说明见 [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)。

## 仓库结构

```text
frontend
  React + Rspack + TypeScript + CSS Modules 前端应用。
  负责聊天 UI、会话管理、run 状态折叠、系统/用户 Skill 展示。

services/control-plane
  前置服务器。负责用户/会话/消息/run/events/skills、SSE、调度和持久化。
  本地可用 memory store，compose 可用 Postgres。

services/agent-runtime
  Agent 实例服务。负责 Eino agentic loop、模型 provider、ToolBridge、skill 执行和事件回写。
  Runtime 不做用户会话管理。

services/sandbox-executor
  Sandbox 执行服务。负责系统 CLI 的策略检查、命令执行、输出截断和审计结果。

packages/common
  跨服务共享 Go module。包含 protocol、platform helper、sandbox HTTP/local/container executor。

migrations
  Postgres schema 初始化和迁移 SQL。

deployments
  Docker Compose 和 Kubernetes 配置。

build/docker
  三个后端服务的生产镜像 Dockerfile。

docs
  中文架构、API、开发、运维、CI/CD、路线图和 ADR。
```

## 前端结构

前端目录按职责拆分：

```text
frontend/src/app
  应用装配、跨 feature 状态编排、run event 折叠。

frontend/src/api
  API client 和 chats/runs/skills 请求函数。

frontend/src/domain
  前端领域类型、状态枚举和展示文案。

frontend/src/features
  chats、conversation、skills 等业务组件。

frontend/src/components
  跨 feature 复用的小组件。

frontend/src/styles/global.css
  全局 CSS variables、reset 和页面基础背景。
```

新增前端代码默认使用 TypeScript。组件样式默认使用 CSS Modules，例如 `SkillPanel.tsx` 搭配 `SkillPanel.module.css`。不要把业务逻辑塞回单个 `App.tsx`，也不要扩张一个全局大 CSS 文件。

## 后端结构

根目录使用 `go.work` 协调多个 Go module。三个后端服务是独立部署单元：

- `cmd/main.go`：服务装配入口，尽量只负责读取配置、组装依赖和启动 HTTP server。
- `internal/config`：环境变量解析和启动配置。
- Control Plane 的核心接口目前在 `internal/controlplane`，包含 repository、dispatcher、HTTP handler、event replay 等边界。
- Agent Runtime 的核心逻辑目前在 `internal/runtime`，包含 Eino engine、model provider、ToolBridge 和 Control Plane sink。
- Sandbox Executor 已有 `internal/httpapi`，负责内部 sandbox API。

`packages/common/protocol` 按领域文件维护，例如 chat、run、event、skill、workspace、model、sandbox。保持 package/import path 不变，不要恢复成一个巨大的 `types.go`。

## 关键数据流

1. 前端调用 `POST /api/chats/{chat_id}/messages` 发送用户消息。
2. Control Plane 写入 message、创建 run、写 `run.queued` event。
3. Control Plane 通过 HTTP dispatcher 调用 Agent Runtime 的 `/internal/runs/execute`。
4. Runtime 根据请求中的 `RuntimeSkill` 构造 Eino tools，执行 agentic loop。
5. Runtime 将 `run.started`、`tool.*`、`model.token`、`run.succeeded/failed` 回写 Control Plane。
6. 前端通过 SSE 订阅 run events，并折叠成 assistant 文本和 agent 当前状态。

## 修改约定

- 面向人的文档、UI 文案和说明默认使用中文。
- 代码标识符、API path、JSON 字段和环境变量保持英文。
- 不要把尚未完成的能力写成已完成能力；文档必须区分当前状态和目标状态。
- 修改外部 API、内部 API、共享协议或数据库 schema 时，同步更新测试和 `docs/API.md`。
- 三个后端服务必须保持独立部署，不要跨服务 import 其他服务的 `internal` 包。
- 公共协议和跨服务工具只能放在 `packages/common`。

## 常用命令

常规 PR 至少执行：

```bash
make check-js
GOCACHE=/private/tmp/niceagent-go-cache make test
make compose-config
git diff --check
```

涉及前端交互时，还需要浏览器验证关键路径：会话、发送消息、SSE 回复、系统/用户 Skill 展示。完整本地开发说明见 [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md)。
