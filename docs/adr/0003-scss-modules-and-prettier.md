# ADR 0003：前端使用 SCSS Modules 与 Prettier

## 状态

Accepted

## 背景

前端已经迁移到 React + Rspack + TypeScript，并按 app/api/domain/features/components 拆分。继续使用普通 CSS Modules 可以满足隔离需求，但后续组件样式会需要更清晰的嵌套结构、变量组合和格式一致性。

## 决策

- 组件样式使用 SCSS Modules，文件命名为 `*.module.scss`。
- 全局样式入口使用 `src/styles/global.scss`，只放 reset、CSS variables 和页面基础背景。
- 引入 Prettier，作为 TS/TSX/SCSS/JSON/MD 的格式检查。
- `npm run check` 包含 typecheck、ESLint 和 Prettier check。

## 影响

- 新组件必须使用同名 SCSS Module。
- 不引入 Tailwind、CSS-in-JS 或组件库。
- Go 代码继续使用 `gofmt`，Prettier 不负责后端格式化。
