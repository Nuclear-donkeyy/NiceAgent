# NiceAgent 目标文档

## 产品目标

NiceAgent 的目标是构建一个通用的远端 agent 服务框架。产品形态是网页端应用：主区域是聊天对话框，用户可以管理自己的聊天会话，并通过自然语言让远端 agent 执行任务。

用户发起一次聊天请求后，系统会创建一个可追踪的 `Run`。`Run` 会被前置服务器调度到某个 agent 实例执行。前端需要持续展示消息、运行事件、工具调用状态、CLI 输出、失败原因、授权请求和 artifacts。

这个系统面向的不是单机聊天 demo，而是一个可横向扩展、可审计、可接入多模型和多 skill 的远端 agent 平台。

## 核心交互

1. 用户在 Web 前端选择或创建一个聊天会话。
2. 用户发送 query。
3. Control Plane 保存消息，创建 `Run`，记录权威状态。
4. Control Plane 将 `Run` 调度给一个 Agent Runtime 实例。
5. Agent Runtime 执行 agentic loop，调用模型、skills 和 sandbox。
6. 执行过程产生标准化 `RunEvent`，前端通过 SSE/WebSocket 实时展示。
7. 任务完成后，assistant 消息、artifacts、审计事件和最终状态写回 Control Plane。

同一个聊天会话的多次 query 不要求命中同一个 agent 实例。agent 实例应该尽量无状态，只作为夹在用户实例信息和大模型/工具调用之间的处理层。

## 服务边界

### Control Plane

前置服务器，也可以理解为用户态和运行态的控制面。它负责：

- 用户、组织、项目、权限和配额。
- 聊天会话、消息、run、run events 和 artifacts 的权威状态。
- agent 实例注册、健康检查、调度、取消和重试。
- skill 注册、授权、版本和审计。
- 面向前端的 REST API、SSE/WebSocket 事件流。

Control Plane 不应该承载复杂 agentic loop，也不应该直接执行不受控 CLI。

### Agent Runtime

远端 agent 实例服务，类似 Codex 或 Claude Code 的远程 CLI 执行核心，但不做用户级会话管理。它负责：

- 接收 `RunRequest`。
- 拉取或接收必要上下文。
- 执行 agentic loop。
- 调用模型 provider。
- 调用 skill/tool bridge。
- 调用 Sandbox Executor 执行 CLI 或文件系统类操作。
- 将执行事件和最终结果写回 Control Plane 或事件总线。

Agent Runtime 应该可以横向扩展。一次 query 有可能被任意可用实例处理。

### Sandbox Executor

CLI 和文件系统类 skill 的隔离执行边界。它负责：

- 创建或挂载 workspace。
- 执行允许的命令。
- 限制 CPU、内存、磁盘、网络和超时。
- 截断和记录 stdout/stderr。
- 产出命令审计事件。
- 对高风险操作触发审批或拒绝。

CLI 不应该直接在 Agent Runtime 进程中裸跑。

### Frontend

网页端产品界面，使用 React + Rspack。它负责：

- 用户级聊天管理。
- 主聊天对话体验。
- run 状态展示。
- 事件流展示。
- CLI 输出展示。
- skill 风险和授权状态展示。
- artifacts 和错误恢复入口。

视觉风格保持简洁，参考 ChatGPT 网页版，以黑、白、微黄色为主，减少圆角，使用面性和线性结构。

### Common Package

公共依赖包，只放跨服务真正共享的内容：

- 协议类型。
- 通用 platform helper。
- 可复用的 sandbox 基础组件。

服务之间不能互相 import 对方的 `internal` 包。

## 架构原则

- 会话状态外置到 Control Plane、Postgres 和事件流。
- Agent Runtime 尽量无状态，方便横向扩展和失败恢复。
- 每次用户 query 都转化为可追踪、可取消、可重放事件的 `Run`。
- `RunEvent` 是前端展示、审计、恢复和排障的共同语言。
- Skills 必须可注册、可授权、可版本化、可审计。
- CLI 必须通过 Sandbox Executor 执行。
- 模型调用层必须支持多 provider 适配。
- 本地 demo 能力可以存在，但不能成为生产路径的架构依赖。

## 当前仓库映射

```text
services/control-plane       Control Plane 前置服务器
services/agent-runtime       Agent Runtime 实例服务
services/sandbox-executor    Sandbox Executor 服务
packages/common              共享协议、公共工具和可复用 sandbox 组件
frontend                     React + Rspack 前端应用
migrations                   Postgres schema
deployments                  本地 Docker Compose 拓扑
docs                         架构、API、开发、路线图和运维文档
```

## 当前阶段非目标

当前阶段先把服务边界、核心协议和可运行链路打稳，暂不追求：

- 完整商业计费系统。
- 完整 Kubernetes 生产部署。
- 强安全等级的沙箱承诺。
- 完整组织级权限、审计和合规体系。
- 全量模型路由、成本优化和自动 fallback。

这些能力应该建立在稳定的 Control Plane、Agent Runtime、Sandbox Executor 和事件协议之上逐步推进。
