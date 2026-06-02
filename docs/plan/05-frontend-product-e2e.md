# 前端产品体验与 E2E

## 产品功能

前端目标是“聊天优先的远端 agent 工作台”，而不是底层 run/event 调试台。用户应该在对话中看到 agent 当前状态、最终回答、可用能力和产物，而不需要理解 `RunEvent` 协议。

下一阶段产品能力：

- artifact 展示和下载。
- HTTP Skill 表单校验和错误提示。
- 更清晰的 agent 状态：排队、生成、调用 skill、获取外部信息、生成产物、失败、取消。
- SSE 断线重连和 run replay 的可见恢复。
- Playwright E2E 覆盖核心用户路径。

## 成熟方案调研

Playwright 适合作为本项目的 E2E 工具。它面向现代 Web 应用，支持 browser automation、web-first assertions、locator、trace 和 CI 报告。参考：[Playwright Introduction](https://playwright.dev/docs/intro)、[Playwright CI](https://playwright.dev/docs/ci)。

Locator 策略建议优先使用 `getByRole`、`getByLabel`、`getByText`，必要时补稳定 `data-testid`。这比 CSS selector 更贴近用户行为，也更抗 DOM 结构调整。

前端可访问性和错误状态应遵循 WCAG/WAI 思路：表单错误要能定位字段并给出修正建议；异步状态、保存结果、错误消息应能被辅助技术感知。参考：[WAI forms notifications](https://www.w3.org/WAI/tutorials/forms/notifications/)。

聊天类产品的事件折叠原则是：RunEvent 仍作为内部审计和 replay 协议存在，但界面只呈现用户可理解状态。例如 `tool.started` 显示“正在调用系统 CLI 获取外部信息”，`artifact.created` 显示“生成了 2 个文件”。

## 当前仓库现状

前端已经迁移到 React + Rspack + TypeScript + SCSS Modules，目录分为 `app`、`api`、`domain`、`features`、`components` 和 `styles`。`package.json` 有 `dev`、`build`、`typecheck`、`lint`、`format`、`check` 和 `smoke:e2e`。仓库已引入 Playwright，并新增 `frontend/e2e/smoke.spec.ts` 与 `frontend/playwright.config.ts`；当前已有 mock backend smoke。仓库也新增了 `make smoke-three-services-ui`，可构建前端、启动三服务真实进程，由 Control Plane 托管静态产物后跑真实浏览器 smoke。

Rspack dev server 已代理 `/api`、`/healthz` 到 Control Plane，本地 E2E 可以复用 dev server 和 8080 后端。

当前 UI 已支持：

- 会话列表、搜索、归档/恢复。
- 消息发送和 assistant 流式文本。
- run 状态徽标和取消按钮。
- 系统能力/我的能力分组。
- HTTP Skill 添加、启用、停用。
- artifact 展示与下载入口。

状态编排集中在 `frontend/src/app/useNiceAgentWorkspace.ts`。SSE 通过 `EventSource(/api/runs/{id}/events)` 订阅，`foldRunEvent` 把事件折叠成 assistant token 和 agent status。

协议类型已包含 `artifact.created`，前端已有 artifact domain、API、状态存储、列表组件和下载入口。`foldRunEvent` 会把 `artifact.created` 折叠成 agent status 和 artifact metadata。

HTTP Skill 表单已有字段级错误、URL 客户端校验、Bearer token 条件校验、保存中禁用和服务端错误展示。前端 URL 策略已与后端对齐：默认只允许 `https`，并拒绝带 credentials 的 URL。

已落地能力：

- React/Rspack/TypeScript/SCSS Modules 工程化结构、Prettier/ESLint/typecheck/build 检查。
- 聊天优先界面、会话搜索/归档/恢复、assistant 流式文本、run 状态、取消和刷新恢复。
- 系统能力/我的能力、HTTP Skill 表单字段校验、保存状态、启停和服务端错误展示。
- artifact domain/API/list/download、`artifact.created` 折叠、图片最小缩略预览和刷新后恢复。
- SSE `after`/seq 去重、断线/恢复状态折叠到 agent status，不展示底层事件调试台。
- Playwright mock smoke 与三服务真实 UI smoke 已覆盖核心聊天、CLI 状态、artifact list API 和刷新恢复。
- `make smoke-sse-replay` 已用真实 Control Plane、Agent Runtime 和 Sandbox Executor 覆盖 SSE 中途断开后按 `after=<last_seq>` replay，验证重连后不重复 seq 且能补到 `run.succeeded`。

仍待落地能力：

- 真实 HTTP Skill 后端流、浏览器级真实断线恢复、失败重试和更大样本的 E2E。
- 更多文件类型预览、skill 导入向导和面向多用户/项目的导航体验。

## 扩展点

- `frontend/src/domain/artifact.ts`：定义 artifact 类型和展示字段。
- `frontend/src/api/artifacts.ts`：封装 list/download/content API。
- `frontend/src/features/artifacts/*`：artifact 列表、卡片、下载入口和 CSV/TSV 表格预览。
- `frontend/src/features/skills/*`：拆出 HTTP Skill 表单状态、字段校验和错误展示。
- `frontend/src/app/runEvents.ts`：处理 `artifact.created`、tool failure、SSE reconnect 状态。
- `frontend/e2e/*`：Playwright 冒烟测试。
- `frontend/playwright.config.ts`：本地/CI E2E 配置。

## 技术架构

前端建议继续保持单页工作台：

```text
App
  -> ChatSidebar
  -> SkillPanel
  -> Conversation
       -> MessageList
       -> AgentStatus
       -> ArtifactList
       -> Composer
  -> useNiceAgentWorkspace
       -> chats/runs/skills/artifacts API
       -> SSE fold/replay
```

Artifact 不要做成底层事件面板，而是跟随 assistant 消息或当前 run 展示：

- assistant 消息下方：展示本轮生成文件。
- 右侧或底部轻量区域：展示当前 run 所有关联 artifact。
- 刷新后从后端 list API 恢复 artifact 元数据。

Skill 表单建议本地维护 `errors`：

- `name` 必填。
- `url` 必须是合法 URL，第一版建议只允许 `https`。
- `auth_type=bearer` 时 `bearer_token` 必填。
- 服务端 400/500 错误展示在表单顶部，并保留输入。

E2E 当前分三层：`frontend/e2e/smoke.spec.ts` 通过 fake backend 或网络 mock 验证前端行为；`make smoke-three-services-ui` 构建前端并启动三服务真实进程，验证 Control Plane 托管静态产物后的核心链路；`make smoke-sse-replay` 启动真实 Control Plane、Agent Runtime 和 Sandbox Executor，模拟 SSE 读到部分事件后断开，并用 `after=<last_seq>` 重连补齐，验证 replay 后没有重复 seq 且能收到 `run.succeeded`。后续再补真实 HTTP Skill 后端流、浏览器级真实断线恢复和更大样本测试。

## 技术方案

第一批前端改造建议：

- 增加 artifact domain 和展示组件。
- `foldRunEvent` 处理 `artifact.created`，返回 agentStatus 和 artifact metadata。
- `useNiceAgentWorkspace` 保存 current run artifacts，并在选择会话或终态后刷新。
- HTTP Skill 表单添加客户端校验和服务端错误区域。
- SSE 断线时显示“正在重连”，重连成功后显示“已恢复连接”，后续结合 `after` 做 replay。

第一批 E2E 场景：

- 创建会话并发送普通消息，看到 assistant 回复。
- 触发 CLI tool，看到“正在调用能力”和最终回复。
- 模拟 `artifact.created`，看到 artifact 卡片。
- 添加 HTTP Skill，启用/停用。
- 表单错误不丢输入。
- 刷新页面后恢复会话、最后 run 状态和 artifact。

## 分阶段落地

1. Artifact UI：补 domain/API/component，处理 `artifact.created`。
2. Skill 表单：客户端校验、字段级错误、服务端错误和保存状态。
3. SSE replay 体验：last seq、断线提示、去重和恢复状态。
4. Playwright smoke：本地 mock + CI Chromium。
5. 集成 E2E：`make smoke-three-services-ui` 已能构建前端、启动三服务，并由 Control Plane 托管静态产物，覆盖真实 `/cli echo hello`、artifact list API 和刷新恢复；`make smoke-sse-replay` 已覆盖真实服务 SSE 中途断开、按 seq replay 和去重的最小冒烟；后续继续补 HTTP Skill 真实后端流和浏览器级真实断线恢复。

## 风险与验收

风险：

- 重新暴露底层事件，破坏聊天优先体验。
- SSE 重连重复追加 token。
- artifact 下载权限已在 Control Plane 按 actor/run/artifact 校验，但仍需要在真实多用户/RBAC 下继续验证。
- 表单错误只出现在全局 notice，用户不知道怎么修。
- E2E 依赖真实模型导致不稳定。

验收：

- 用户不看原始事件，也能理解 agent 在排队、生成、调用 skill、生成 artifact、失败或取消。
- HTTP Skill 空名称、非法 URL、Bearer 未填、后端 4xx/5xx 都有中文错误且不丢输入。
- 刷新页面后恢复最近 run 状态和 artifact 元数据。
- Playwright mock smoke 覆盖会话、普通消息、CLI 状态、artifact、HTTP Skill 添加/启停、失败恢复；三服务 UI smoke 覆盖真实 `/cli echo`、artifact list API 和刷新恢复。
- `make check-js`、Rspack build、Playwright smoke 在本地和 CI 通过。

## 参考资料

- [Playwright Introduction](https://playwright.dev/docs/intro)
- [Playwright CI](https://playwright.dev/docs/ci)
- [WAI forms notifications](https://www.w3.org/WAI/tutorials/forms/notifications/)
