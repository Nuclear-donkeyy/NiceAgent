# NiceAgent 路线图

本文记录 NiceAgent 的当前工程状态、主要差距和下一阶段开发顺序。长期产品目标见 [`target.md`](../target.md)，当前架构边界见 [`docs/ARCHITECTURE.md`](ARCHITECTURE.md)，更细的对齐审计见 [`docs/plan/00-alignment-audit-and-repair-plan.md`](plan/00-alignment-audit-and-repair-plan.md)。

## 当前状态快照

当前仓库已经从早期 demo 推进到多服务、多 module、前后端分层的基础框架：

- 前端：`frontend` 使用 React + Rspack + TypeScript + SCSS Modules，已拆分为 `app`、`api`、`domain`、`features`、`components` 和 `styles`，支持会话管理、assistant 流式回复、agent 状态折叠、系统能力/我的能力展示和 HTTP Skill 添加/启停。
- Control Plane：`services/control-plane` 负责 chats、messages、runs、events、skills、SSE 和调度，已按 `httpapi`、`app`、`repository`、`dispatch`、`events` 分层，支持 memory store 与 Postgres repository。
- Agent Runtime：`services/agent-runtime` 已接入 Eino ADK `ChatModelAgent + Runner` 主路径，模型层切到 Eino 原生 `ToolCallingChatModel`，支持 mock provider、OpenAI-compatible provider、ToolBridge、系统 CLI 和用户 HTTP Skill。
- Sandbox Executor：`services/sandbox-executor` 已独立成服务，作为系统级 CLI 执行边界，当前以只读网络型命令、策略拒绝、超时和输出截断为主。
- 公共协议：`packages/common` 保存跨服务 protocol、platform helper 和 sandbox executor client，不跨服务 import 其他服务的 `internal` 包。
- 部署与工程化：已有 Docker Compose、本地 kind K8s 路径、GitHub Actions CI/CD 基础、服务级 `AGENTS.md`、ADR、Prettier、前端检查和 Go 测试入口。

## 关键差距

当前系统已经具备可演示链路，但还不能被描述为生产可用的远端 agent 平台：

- Runtime 的模型层已经切到 Eino 原生 ChatModel，下一步仍需继续压实真实模型 tool calling 的端到端覆盖，并减少 provider-specific 兼容风险。
- Skill registry 已有 metadata、version、grant、secret 存储模型，但 schema validation、HTTP Skill 错误模型、secret backend 抽象、OpenAPI/MCP 导入还不完整。
- Sandbox 还不是强隔离生产沙箱。当前 CLI 策略偏本地开发可用，仍需容器默认执行路径、workspace 隔离、artifact 归档、网络策略和资源配额。
- 前端已经隐藏原始事件面板，但 artifact 展示、skill 配置校验、端到端测试和错误恢复体验还需要补强。
- Control Plane 仍缺 NiceAgent 内置 OIDC/session/JWT、邮件发送、强一致账单级 quota、DB 低层 spans、日志关联和外部告警；Redis Runtime worker、重试/lease、跨副本 event fanout、审计、基础 metrics/tracing 和 Redis 低层命令 spans 已有最小闭环。
- 云部署目前适合作为近云验证，不应把 memory demo 或未完成 sandbox 当作生产方案直接发布。

## 下一阶段优先级

下一阶段的主线是先把 agent runtime 和 skill 执行变得真实可靠，再补 sandbox/artifact 和平台化能力。

1. Runtime tool calling 收口：让真实模型 provider、Eino loop 和 ToolBridge 的职责稳定下来。
2. Skill execution 稳定化：补 schema、错误、secret、导入和 grant 的最小闭环。
3. Sandbox workspace/artifact：让 CLI 和文件产物进入可审计、可恢复、可下载的执行边界。
4. 前端产品体验与测试：继续维持聊天优先界面，同时补 artifact、skill 配置和 E2E。
5. Control Plane 平台化：补认证、多租户、Redis 多副本基础、审计和观测。

## 建议 PR 顺序

### Phase 6A：Runtime 原生 Tool Calling（当前落地）

- OpenAI-compatible provider 改为通过 Eino `eino-ext` OpenAI ChatModel 接入。
- Runtime engine 直接使用 Eino `ToolCallingChatModel`，删除项目内自定义模型桥接主路径。
- 保留 `/cli ...` 作为开发测试入口；普通自然语言不再靠关键词启发式触发工具。
- 补测试：fake Eino model 返回 tool call、Runtime 执行 builtin/http tool、tool result 回到模型、最终 assistant 回复成功。

验收标准：

- 普通消息不调用工具时仍能通过 mock 和 OpenAI-compatible provider 回复。
- 模型返回 `cli.exec` tool call 时，Runtime 通过 Sandbox Executor 执行并产出 `tool.*` events。
- 模型返回 HTTP Skill tool call 时，只能调用 RunRequest 下发的用户可用 skill。
- 取消 run 后不写成功终态。

### Phase 6B：Skill 执行生产化最小闭环

- 为 HTTP Skill 增加 input schema validation 和更明确的 runtime_config 校验。
- 标准化 HTTP Skill 错误输出：网络错误、超时、非 2xx、无效响应都转为 agent 可读 observation 和审计事件。
- 抽象 secret resolver：本地继续支持 `encrypted_value`，生产路径预留阿里云 KMS、Vault 或 External Secrets。
- 预留 OpenAPI/MCP 导入入口，先落 manifest 转换接口和文档，不要求完整 UI。

验收标准：

- `GET /api/skills` 不返回 secret。
- Runtime 调用 HTTP Skill 时能拿到必要 secret，但不会把 secret 写入 event、日志或前端响应。
- 错误 skill 不会拖垮整个 runtime 进程，agent 能拿到结构化失败观察。

### Phase 6C：Sandbox Workspace 与 Artifact

- 让 Sandbox Executor 默认走容器执行路径，local executor 只作为测试和 fallback。
- 按 run/workspace 隔离工作目录，记录命令、退出码、耗时、输出截断状态和文件变更摘要。
- 将生成文件登记为 `Artifact`，由 Control Plane 保存元数据并通过前端展示。
- 强化策略：只读网络型命令白名单、写磁盘范围限制、资源限制、超时和环境变量过滤。

验收标准：

- `/cli echo hello` 和只读网络型命令仍可执行。
- 破坏性命令被策略拒绝，不进入用户授权流。
- 生成 artifact 后刷新页面仍能看到 artifact 元数据。

### Phase 6D：前端体验与 E2E

- 在聊天优先界面中增加 artifact 展示和下载入口。
- 为 HTTP Skill 表单补最小校验和更清晰的错误提示。
- 增加浏览器 E2E 冒烟：创建会话、发送普通消息、触发 CLI tool、添加/启停 HTTP Skill、刷新恢复。
- 继续保持黑、白、微黄色、少圆角、面性+线性的产品风格。

验收标准：

- 用户不需要看原始 RunEvent，也能理解 agent 正在调用 skill、正在获取外部信息或已经失败。
- `make check-js`、Rspack build 和 E2E 冒烟稳定通过。

### Phase 6E：Control Plane 平台化

- 引入真实认证边界，替代固定 `demo-user`。
- 完善 organization/project/user 权限模型，让 skill grants、runs、workspaces 都有明确租户边界。
- 把 Redis Streams 从可运行路径继续推进到生产运维能力：stream 保留策略、高可用 Redis、DLQ 告警、pending entries 观测和容量压测。
- 在已有 audit、metrics、trace id、run replay 和 Redis 低层命令 spans 基础上补 DB 低层 spans、日志关联、外部告警和基础管理排障视图。

验收标准：

- 多用户不会互相看到 chat、run、skill、workspace 和 artifact。
- 多个 Agent Runtime 可以并发消费 run，Control Plane 多副本下 SSE/replay 不丢事件。
- 运维侧能按 run id 排查模型、tool、sandbox 和回写链路。

## 已完成阶段记录

- Phase 1：服务间 HTTP 解耦基础完成。Control Plane 可通过 HTTP 调用独立 Agent Runtime，Runtime 可回写 Control Plane 并调用 Sandbox Executor。
- Phase 2：Postgres 持久化基础完成。memory 和 Postgres repository 均可用，run events 支持 replay，Compose 默认可走 Postgres。
- Phase 3A/3B：模型 provider 与 Eino 路径基础完成。mock 与 OpenAI-compatible provider 可配置，Eino `ChatModelAgent + Runner` 已成为 Runtime 主路径，但真实模型原生 tool calling 仍是 Phase 6A 重点。
- Phase 4A/4B：CLI 语义完成一次修正。通用审批事件保留给未来非 CLI 高风险 skill，系统 CLI 不再走用户逐次授权，而是通过 sandbox 策略限制。
- Phase 5A/5B/5C：前端从 demo 聊天框推进到聊天优先工作台，支持会话搜索、归档/恢复、run replay、事件折叠、系统能力和用户 HTTP Skill 管理。
- 工程化：前端已迁移到 TypeScript + SCSS Modules + Prettier，后端三服务已做基础分层，仓库已有服务级 `AGENTS.md` 和 ADR。
