# NiceAgent 路线图

## 阶段目标

当前仓库已经完成多 Go module 拆分、React + Rspack 前端迁移、三服务 HTTP 解耦链路、Postgres 持久化基础、本地 K8s 验证路径和 CI/CD 基础。下一阶段目标是继续补 agent runtime、sandbox 生产化能力和前端产品体验。

优先级从高到低：

1. 服务间真实解耦。
2. 状态持久化。
3. Agent Runtime 能力补强。
4. Sandbox 与远端 CLI 生产化起步。
5. 前端产品化。

## Phase 1：服务间真实解耦（基础完成）

- Control Plane 默认使用 HTTP dispatcher，通过 `AGENT_RUNTIME_URL` 调用独立 Agent Runtime。
- Agent Runtime 执行后通过 Control Plane 内部 API 回写 `RunEvent`、最终消息和状态。
- Agent Runtime 默认通过 `SANDBOX_EXECUTOR_URL` 调用独立 Sandbox Executor 执行 `/cli`。
- 保留本地 demo dispatcher 作为开发 fallback，但文档中明确它不是生产路径。
- 增加跨服务契约测试：创建 run、runtime 接收、事件回写、终态更新、sandbox HTTP 调用。
- Redis queue 主路径后移，本阶段先把 HTTP 直连契约打稳。

验收标准：

- `services/control-plane` 不 import `services/agent-runtime/internal/*`。
- 启动 Control Plane、Agent Runtime 和 Sandbox Executor 三个进程后，普通消息和 `/cli echo hello` 能走跨服务链路。
- Agent Runtime 重启或不可用时，Control Plane 能将 run 标记为失败或保留可重试状态。

## Phase 2：状态持久化（基础完成）

- 将 memory store 切换为可配置 repository。
- 补齐 Postgres repository 的错误处理、事务一致性和测试。
- Compose 中提供 Postgres 模式启动说明和环境变量。
- `RunEvent` replay 以 Postgres 为权威。
- 保留 memory store 作为本地快速 demo。

验收标准：

- 重启 Control Plane 后，Postgres 模式下会话、消息、run 和 events 仍可恢复。
- SSE `after` replay 使用数据库事件序号。
- memory 模式和 Postgres 模式都有清晰启动方式。

## Phase 3：Agent Runtime 能力（Phase 3B 当前落地）

- Phase 3A：落地 OpenAI-compatible provider，并保留 mock provider 作为默认本地路径。
- Phase 3B：接入 Eino ADK `ChatModelAgent + Runner` 主路径。
- 通过 `ToolBridge` 将平台 `RuntimeSkill` 映射为 runtime tools。
- 支持最大步数、超时、取消、工具失败和事件审计。
- 定义 checkpoint/resume 的最小协议，先不要求完整长任务恢复。

验收标准：

- Runtime 可通过配置选择 mock provider 或 OpenAI-compatible provider，并由 Eino agent loop 统一驱动。
- 工具调用、工具失败、模型错误都会产生标准化 `RunEvent`。
- 取消 run 后，runtime 不再写入成功终态。

## Phase 3C：Skill Registry 与 Secret 后端

- Skill manifest 目前已拆成 metadata、version、grant 和 secret 存储。
- HTTP Skill v1 支持无鉴权和 bearer token，本地开发 token 存在 `skill_secrets.encrypted_value`。
- 后续接入阿里云 KMS、Vault 或 External Secrets，替换本地开发密文。
- 后续支持从 OpenAPI/MCP 导入 skill manifest。

## Phase 4：Sandbox 与 CLI（Phase 4B 当前落地）

- `services/sandbox-executor` 成为远端 CLI 的默认执行路径。
- Agent Runtime 通过 HTTP 调用 Sandbox Executor。
- Phase 4A：曾先落地通用审批事件和等待状态，保留给未来真正需要用户确认的非 CLI skill。
- Phase 4B：CLI 调整为系统级 agent 工具，不需要用户逐次授权；只读网络型命令可执行，破坏性命令作为策略拒绝返回。
- 后续加强 workspace、输出截断、环境变量过滤和更细的出站网络策略。
- local executor 只用于单元测试和本地 fallback。
- 记录命令审计事件，包括 command、exit code、duration、stdout/stderr 摘要。

验收标准：

- `/cli echo hello` 通过 Sandbox Executor 服务执行。
- 高风险命令不会直接执行，会作为系统 CLI 策略拒绝进入普通 tool output，不再触发用户授权。
- stdout/stderr 超长输出会被截断并标记。

## Phase 5：前端产品化（Phase 5C 当前落地）

- React 前端继续贴近 ChatGPT 网页版风格。
- Phase 5A：完善会话搜索、归档/恢复、最近 run replay 和基础 loading/error/empty 状态。
- Phase 5B：隐藏原始运行事件和 CLI 调试面板，把 SSE 折叠成 assistant 流式回复和 agent 当前状态；skills 区改为当前可用能力。
- Phase 5C：skills 区拆成系统能力和我的能力，支持添加/启停 HTTP Skill。
- 后续补 artifact 展示和更完整的错误恢复。
- 保持黑、白、微黄色，少圆角，面性+线性风格。
- 增加前端侧基础测试或至少稳定的 Rspack build 检查。

验收标准：

- 刷新页面后能恢复聊天和最近 run 状态。
- 用户能在聊天流里看懂 agent 当前阶段、工具调用结果和失败原因。
- `make check-js` 能稳定验证前端构建。

## 后续生产化方向

- Kubernetes 部署和弹性扩缩容。
- Secret 管理和模型 provider key 管理。
- 完整审计日志、指标、trace 和 run replay。
- 沙箱镜像白名单、网络策略和资源配额。
- 管理后台：用户、实例、runs、skills、错误排查。
