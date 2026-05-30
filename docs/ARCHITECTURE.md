# NiceAgent 架构说明

NiceAgent 被拆成 Control Plane 和一组无状态执行 worker。Control Plane 维护用户可见状态和调度事实，Agent Runtime 与 Sandbox Executor 只负责执行被分派的工作。

## Control Plane

Control Plane 负责用户、组织/项目、聊天会话、消息、runs、events、skills、workspaces、审批、配额和审计数据。它向 Web 客户端暴露 REST API，并为每个 run 提供 SSE 事件流。

当前实现使用 memory store，方便在没有外部依赖时跑通完整链路。`migrations/001_init.sql` 定义了后续替换 memory store 的 Postgres schema 契约。

## Agent Runtime

Runtime 设计为无状态服务。它接收 `RunRequest`，获取或接收上下文，执行 agentic loop，调用 skills，并持续产出标准化 `RunEvent`。

`internal/runtime` 是后续接入 CloudWeGo Eino ADK 的位置：

- 将 NiceAgent `Skill` 映射成 Eino tools。
- 将模型供应商 adapter 包装成 Eino chat model。
- 使用 Eino Runner/TurnLoop 或 Graph/Workflow 承载 agentic loop。
- 在模型、工具、checkpoint 等边界产出事件。

当前 runtime 是 mock runtime，只用于验证协议、事件流和工具调用链路。

## Sandbox Executor

Sandbox Executor 是 CLI 和文件系统类 skill 的执行边界。当前 local executor 只允许少量命令，尚未提供生产级隔离。

生产化方向应替换为容器或 Kubernetes Job 执行，并加入：

- CPU、内存、磁盘和超时限制。
- 网络开关和出站访问策略。
- workspace 挂载和 artifact 管理。
- 命令审计、输出截断和敏感操作审批。

## 事件流

1. Web 客户端提交用户消息。
2. Control Plane 保存消息并创建 queued run。
3. Runtime 执行 run 并产出 events。
4. Control Plane 持久化 events，并通过 SSE fanout 给前端。
5. 最终 assistant 消息写回聊天会话。

## 生产化 Adapter

当前骨架把外部服务放在明确边界之后，后续可以逐步替换：

- Postgres store adapter：承载权威状态。
- Redis Streams queue：负责 run dispatch 和 event fanout。
- Eino runtime adapter：承载 agentic loop 编排。
- Model provider adapters：接入 OpenAI、Anthropic 和 OpenAI-compatible endpoint。
- Container sandbox adapter：执行 CLI 和文件系统类 skill。

## 当前限制

- memory store 不是持久化方案，不能用于多实例共享状态。
- mock runtime 不具备真实模型推理、工具规划和长任务恢复能力。
- local executor 不是安全沙箱，只适合本地演示。
- sandbox 尚未生产化，不能承诺强隔离或恶意命令防护。
