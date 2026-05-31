# NiceAgent 生产化主线规划

本文是 `docs/ROADMAP.md` 的专题展开，记录 NiceAgent 从当前可演示平台走向生产化远端 agent 平台时需要补齐的六条工程主线。这里不替代路线图，而是给后续 PR 提供可执行的产品功能、架构方案和验收标准。

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

1. Skill 执行最小闭环：先做 HTTP Skill schema validation、错误模型、secret redaction 和 Runtime 输入校验。
2. DeepSeek 接入验证：在现有 OpenAI-compatible + Eino `ToolCallingChatModel` 路径上完成真实 API key 冒烟。
3. Sandbox workspace/artifact：补 artifact 表、下载 API、容器默认执行和 workspace diff。
4. 前端 artifact 与 E2E：让用户能看到产物，并用 Playwright 覆盖核心链路。
5. 平台认证与权限：从 `demo-user` 迁移到 `AUTH_MODE=demo|oidc` 和 `ActorContext`。
6. Redis queue/event fanout：在单实例路径稳定后，再把 run dispatch 和 SSE fanout 推向多副本。

## 文档约定

- 面向人的说明使用中文。
- 代码路径、API path、JSON 字段、环境变量和协议字段保持英文。
- 每份文档都区分“当前仓库现状”和“目标技术方案”，避免把尚未完成的能力写成已完成。
- 参考资料优先使用官方或一手来源。
