# NiceAgent 架构说明

NiceAgent 当前采用多 module、多服务结构。每个后端服务都是独立 Go module，可以单独构建、发布和部署；公共协议与工具放在 `packages/common`。

## 模块边界

```text
frontend
  React + Rspack 前端应用，通过 HTTP/SSE 调 Control Plane。

services/control-plane
  前置服务器。管理用户会话、消息、run、run events、用户可用 skills 和调度事实。

services/agent-runtime
  agent 实例服务。接收 RunRequest，执行 agentic loop，调用模型与工具。

services/sandbox-executor
  sandbox 执行服务。为 CLI 和文件系统类 skill 提供执行边界。

packages/common
  公共 Go module。包含 protocol、platform helper、可复用 sandbox 执行组件。
  protocol 按 chat、run、event、skill、workspace、model、sandbox 等领域文件维护，package/import path 不变。
```

## Control Plane

Control Plane 是用户状态和调度事实的权威来源。它暴露 Web/API，包括聊天、消息、run、skills 和 SSE 事件流。

当前默认仍使用 memory store，便于本地演示；同时已经按服务内分层拆出：

- `internal/httpapi`：外部 API、内部 Runtime 回写 API、SSE 和鉴权。
- `internal/app`：Repository、RunDispatcher 等端口接口，以及 run 完成/失败的用例逻辑。
- `internal/repository`：memory/Postgres 持久化实现。
- `internal/dispatch`：local/http dispatcher 和 run queue 边界。
- `internal/events`：事件持久化、replay、fanout 边界。

Skill 元数据采用 manifest/version/grant/secret 分层：

- `skills` 保存稳定身份、scope、kind、owner、status 和当前版本指针。
- `skill_versions` 保存 name、description、schema、MCP 风格 annotations 和 runtime_config。
- `skill_grants` 保存用户/项目可用性。
- `skill_secrets` 保存 secret 引用或本地开发密文；前端 API 不返回 secret。

## Agent Runtime

Agent Runtime 是独立部署的 agent 实例服务，不应由 Control Plane 作为库直接 import。它的核心接口包括：

- `internal/httpapi`：内部执行 API 和 Control Plane sink 装配。
- `internal/engine`：AgentEngine、Eino agentic loop 和循环限制。
- `internal/modelprovider`：Eino `ToolCallingChatModel` factory，提供 mock 与 OpenAI-compatible 模型入口。
- `internal/tools`：平台 skill 到 runtime tool 的桥，以及 SandboxExecutor 端口。
- `internal/sink`：Control Plane event/status/complete/fail 回写客户端。

当前主路径已经接入 Eino ADK `ChatModelAgent + Runner`。Runtime 会接收 Control Plane 下发的 `RuntimeSkill` manifest，通过 ToolBridge 构造 Eino tools；系统 CLI 和用户 HTTP Skill 都作为 tool 被 agentic loop 调用。模型层已切到 Eino 原生 `ToolCallingChatModel`：mock provider 直接实现 Eino 接口，OpenAI-compatible provider 通过 `github.com/cloudwego/eino-ext/components/model/openai` 接入。

## Sandbox Executor

Sandbox Executor 独立部署，负责命令执行策略。当前 `packages/common/sandbox` 提供 local executor 和 Docker CLI container executor 入口。

当前 CLI 是系统级 agent 工具，用于在沙箱中获取外部信息，不需要用户逐次授权。生产化还需要继续补齐：

- CPU、内存、磁盘、超时限制。
- 只读网络型出站访问策略。
- workspace 挂载、artifact 归档和 diff 摘要。
- 命令审计、输出截断和敏感操作策略拒绝。

## 前端

前端位于 `frontend`，使用 React + Rspack + TypeScript + SCSS Modules。设计风格参考 ChatGPT 网页版：左侧会话、系统能力和我的能力，右侧主对话区和输入框。底层 `RunEvent` 通过 SSE 接收，但会折叠成 assistant 流式文本和 agent 当前状态，不再默认展示原始事件列表或 CLI 调试输出。HTTP Skill 可以在“我的能力”中最小化添加和启停。视觉以黑、白、微黄色为主，减少圆角，强调面性和线性结构。

前端目录按职责分层：

- `src/app`：应用装配和跨 feature 状态编排。
- `src/api`：HTTP client 和 API 函数。
- `src/domain`：前端领域类型和展示 label。
- `src/features`：聊天、对话、Skill 等业务组件。
- `src/components`：跨 feature 复用的小组件。
- `src/styles/global.scss`：全局 token/reset；组件样式使用 SCSS Modules。

开发模式下由 Rspack dev server 代理 API；生产模式下由 Control Plane 托管 `frontend/dist`。
