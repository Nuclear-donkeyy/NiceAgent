# NiceAgent 路线图

## 阶段目标

下一阶段的目标是把当前可演示骨架推进到可扩展基础平台：服务可独立部署，状态可持久化，run 可队列化，runtime 可替换，CLI 可进入沙箱，React 前端能展示完整运行态，测试能守住核心契约。

## 多子 agent 分工

### Agent A：Control Plane 持久化与队列

- 抽象 `Store` 接口，保留 memory 实现。
- 新增 Postgres repository，覆盖 users/chats/messages/runs/events/skills/workspaces。
- 新增 Redis Streams run queue 和 event fanout 边界。
- 创建 run 后写入队列，为多个 runtime 消费做准备。
- 保持 `services/control-plane` 为独立 Go module，不跨服务 import runtime 代码。

### Agent B：Agent Runtime 与 Eino 接入边界

- 拆分 runtime pipeline：上下文加载、模型调用、工具选择、事件输出、最终消息写回。
- 定义 `AgentEngine`、`ToolBridge`、`ModelProvider`。
- 保留 mock provider，新增 OpenAI-compatible provider 配置。
- 预留 CloudWeGo Eino ADK adapter 接入点。
- 保持 `services/agent-runtime` 可独立部署，通过协议和队列/HTTP 与 Control Plane 通信。

### Agent C：Sandbox Executor 与远端 CLI

- 抽象 `SandboxExecutor`。
- 增加 workspace、超时、网络开关、环境变量过滤、输出大小限制。
- 设计 Docker/container executor 路径。
- 高风险命令返回 approval-needed 语义，不直接执行。

### Agent D：Web 前端体验

- 将 Web UI 文案中文化。
- 完善聊天管理、run 状态展示、事件面板和 CLI 输出展示。
- 增加 skills 展示区，显示风险等级和授权状态。
- 使用 React + Rspack 继续完善前端，不引入 Next.js。
- 视觉保持黑、白、微黄色，少圆角，靠近 ChatGPT 网页版的简洁工作台。

### Agent E：中文文档与开发者体验

- 维护 README 和 `docs/*` 中文化。
- 补充本地开发、Docker Compose、测试、常见问题。
- 明确标记 memory store、mock runtime、local executor 和 sandbox 未生产化等限制。
- 增加 `compose-config` 和 `check-js` 检查入口。
- 持续记录多 Go module、`go.work`、React/Rspack 的开发方式。

### Agent F：集成测试与契约检查

- 补充 API handler 测试。
- 补充 run event seq、SSE replay、terminal state 保护测试。
- 补充 runtime 普通回复、CLI 工具调用、工具失败和取消测试。
- 在文档中记录无 Go 工具链时的 Docker 化测试方式。

## 集成验收标准

- 创建聊天并发送普通消息后，能收到 assistant 回复和 run events。
- 发送 `/cli echo hello` 后，能看到 tool started/output/finished/succeeded 事件。
- 刷新页面后能恢复会话和最近 run 状态。
- 取消 run 后，后台完成不能覆盖 canceled 终态。
- Postgres/Redis 模式和 memory 模式至少各有一条可运行路径。
- 所有面向人的说明文档使用中文。

## 后续生产化方向

- Kubernetes 部署和弹性扩缩容。
- Secret 管理和模型 provider key 管理。
- 完整审计日志、指标、trace 和 run replay。
- 沙箱镜像白名单、网络策略和资源配额。
- 管理后台：用户、实例、runs、skills、错误排查。
