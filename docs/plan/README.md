# NiceAgent 生产化主线规划

本文是 `docs/ROADMAP.md` 的专题展开，记录 NiceAgent 从当前可演示平台走向生产化远端 agent 平台时需要补齐的六条工程主线。这里不替代路线图，而是给后续 PR 提供可执行的产品功能、架构方案和验收标准。

当前实现与六条规划的对齐审计见：[docs/plan 与当前系统对齐审计及修复计划](00-alignment-audit-and-repair-plan.md)。后续开发应优先参考该文档里的差距矩阵和 PR 顺序，避免基于过期“当前仓库现状”重复设计。

## 当前定位

当前仓库已经具备三服务解耦、React 前端、Postgres 持久化、Eino Runtime、系统级 CLI 和用户 HTTP Skill 的基础链路。下一阶段的核心不是继续堆 UI，而是把 agent runtime、skill、sandbox、平台治理和模型运营这些“能长期运行”的底座打稳。

六条主线之间的关系如下：

1. [Skill 执行生产化](01-skill-execution.md)：定义 agent 能调用什么、如何校验、如何拿 secret、如何审计。
2. [Sandbox 与 Artifact](02-sandbox-artifact.md)：定义 CLI 和文件类能力在哪里安全执行，产物如何登记和恢复。
3. [平台认证、权限与可观测](03-platform-auth-observability.md)：定义真实用户、项目边界、配额、审计、metrics 和 trace。
4. [多实例队列与事件流](04-multi-instance-queue-events.md)：定义多个 Control Plane 和 Agent Runtime 如何可靠协作。
5. [前端产品体验与 E2E](05-frontend-product-e2e.md)：定义聊天优先体验、artifact 展示、skill 配置和端到端验收。
6. [模型运营与 DeepSeek 接入](06-model-operations.md)：定义真实模型 provider、token/cost、限流、fallback 和日志脱敏。

## 建议 PR 顺序

1. DeepSeek 真实 key 冒烟与模型运营记录：在现有 OpenAI-compatible + Eino `ToolCallingChatModel` 路径上完成可复现 smoke，不提交密钥。
2. Skill 治理补强：在已有 HTTP Skill schema validation、错误模型、secret redaction、OpenAPI preview/save、MCP manifest preview API/前端 dry-run 面板、per-skill retry、进程内/Redis 跨副本 rate limit 基础上，继续补 MCP 保存/执行 adapter 和更细审计。
3. Sandbox 生产化：把 Compose/部署默认路径从 local executor 推向 container executor，在已有 K8s egress NetworkPolicy 和镜像白名单基础上补云侧出口控制、强隔离和更完整 artifact 预览。
4. 平台化收口：在 `AUTH_MODE=trusted-header|oidc`、ActorContext、OIDC 浏览器 login/session/refresh token、前端登录会话入口、RBAC、邀请、quota、metrics/tracing、告警规则、Alertmanager 路由样例、durable 邀请邮件 outbox、邀请邮件重发 API、provider-neutral 退信事件记录、HMAC webhook 入口、SendGrid/SES/Mailgun 最小原生字段映射、组织级自动停发、suppression 查询/解除 API 和前端“邮件治理”管理面板已有最小闭环基础上，补更完整 tokenizer 覆盖、服务商原生签名校验和真实值班系统接入。
5. Redis 与多副本韧性：在 Redis Streams worker、attempt/lease/DLQ、consumer group lag 指标、跨 Control Plane nudge fanout 和 CI smoke 已落地后，继续补 Redis HA、容量压测和外部告警。
6. 前端真实链路 E2E：在 artifact、skill 表单、mock smoke 和三服务 UI smoke 已有基础上，补真实 HTTP Skill 后端流和复杂 SSE 断线重连场景。

## 文档约定

- 面向人的说明使用中文。
- 代码路径、API path、JSON 字段、环境变量和协议字段保持英文。
- 每份文档都区分“当前仓库现状”和“目标技术方案”，避免把尚未完成的能力写成已完成。
- 参考资料优先使用官方或一手来源。
