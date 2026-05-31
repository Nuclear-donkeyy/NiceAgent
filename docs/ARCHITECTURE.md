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
```

## Control Plane

Control Plane 是用户状态和调度事实的权威来源。它暴露 Web/API，包括聊天、消息、run、skills 和 SSE 事件流。

当前默认仍使用 memory store，便于本地演示；同时已经抽象出：

- `Repository`：会话、消息、run、events、skills 持久化。
- `RunDispatcher`：run 派发边界。
- `RunQueue`：run 入队、消费、ack/retry 边界。
- `EventBus`：事件持久化、replay、fanout 边界。

## Agent Runtime

Agent Runtime 是独立部署的 agent 实例服务，不应由 Control Plane 作为库直接 import。它的核心接口包括：

- `AgentEngine`：agentic loop 执行入口。
- `ModelProvider`：模型供应商适配。
- `ToolBridge`：平台 skill 到 runtime tool 的桥。
- `SandboxExecutor`：CLI/sandbox 调用边界。

当前实现保留 mock provider，用于验证事件链路；后续 Eino ADK 应接在 `services/agent-runtime/internal/runtime` 这一层。

## Sandbox Executor

Sandbox Executor 独立部署，负责命令执行策略。当前 `packages/common/sandbox` 提供 local executor 和 Docker CLI container executor 入口。

当前 CLI 是系统级 agent 工具，用于在沙箱中获取外部信息，不需要用户逐次授权。生产化还需要继续补齐：

- CPU、内存、磁盘、超时限制。
- 只读网络型出站访问策略。
- workspace 挂载、artifact 归档和 diff 摘要。
- 命令审计、输出截断和敏感操作策略拒绝。

## 前端

前端位于 `frontend`，使用 React + Rspack。设计风格参考 ChatGPT 网页版：左侧会话和当前可用能力，右侧主对话区和输入框。底层 `RunEvent` 通过 SSE 接收，但会折叠成 assistant 流式文本和 agent 当前状态，不再默认展示原始事件列表或 CLI 调试输出。视觉以黑、白、微黄色为主，减少圆角，强调面性和线性结构。

开发模式下由 Rspack dev server 代理 API；生产模式下由 Control Plane 托管 `frontend/dist`。
