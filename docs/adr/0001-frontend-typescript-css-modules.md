# ADR 0001：前端使用 TypeScript 与 CSS Modules

## 状态

Accepted

## 背景

早期前端集中在 `App.jsx` 和全局 `styles.css` 中。随着会话管理、run replay、Skill 管理和 agent 状态折叠增加，单文件结构不利于人类和 AI agent 定位职责。

## 决策

- 前端使用 TypeScript，开启 `strict` 渐进模式。
- React 组件使用 `.tsx`，非组件逻辑使用 `.ts`。
- 样式默认使用 CSS Modules。
- `global.css` 只保留设计 token、reset 和基础页面背景。
- 目录按 `app`、`api`、`domain`、`features`、`components` 分层。

## 影响

- 新增 feature 需要同时维护类型、API 边界和组件样式边界。
- CI 通过 `npm run check` 和 `npm run build` 拦截类型、lint 和构建问题。
- 不引入 Tailwind、CSS-in-JS 或组件库，继续保持当前黑、白、微黄色的轻量视觉系统。
