# NiceAgent Frontend 导览

本目录是 NiceAgent 的 Web 应用，负责聊天工作台、会话管理、run 状态折叠、系统能力/用户能力展示和 HTTP Skill 管理。

## 技术栈

- React + Rspack + TypeScript。
- 样式使用 SCSS Modules，文件命名为 `*.module.scss`。
- `src/styles/global.scss` 只放 reset、CSS variables、字体和页面基础背景。
- Prettier 负责 TS/TSX/SCSS/JSON/MD 格式检查。

## 目录结构

```text
src/app
  应用装配、跨 feature 状态编排、SSE 事件折叠。

src/api
  API client 和 chats/runs/skills 请求函数。

src/domain
  Chat、Run、Skill、RunEvent 等前端领域类型和展示文案。

src/features
  chats、conversation、skills 等业务组件。

src/components
  跨 feature 复用的小组件。

src/styles
  全局样式入口和设计变量。
```

## 修改约定

- 新组件必须使用 TypeScript 和同名 SCSS Module。
- 不要把业务逻辑塞回 `src/app/App.tsx`；跨 feature 状态放在 `useNiceAgentWorkspace.ts` 或进一步拆 hook。
- 不要新增全局业务 class；组件样式放在组件旁边。
- API 字段和路径保持英文，用户可见文案默认中文。

## 检查命令

```bash
npm run format:check
npm run typecheck
npm run lint
npm run build
```
