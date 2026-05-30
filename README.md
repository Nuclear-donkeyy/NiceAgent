# NiceAgent

NiceAgent 是一个网页端远端 agent 服务框架的早期骨架。当前重点是把“前置控制服务、远端 agent 实例、sandbox 执行器、React 前端、共享协议”拆成可以独立部署和独立演进的模块。

## 当前结构

根目录是协调层，使用 `go.work` 管理多个 Go module：

```text
services/control-plane       前置服务器，负责用户会话、run、events、skills 和 Web/API
services/agent-runtime       agent 实例服务，负责无状态 agentic loop 执行边界
services/sandbox-executor    CLI/sandbox 执行服务，负责命令执行边界
packages/common              公共 Go module，放共享协议、平台工具和可复用 sandbox 组件
frontend                     React + Rspack 前端应用
migrations                   Postgres schema
deployments                  Docker Compose 拓扑
docs                         中文架构、API、开发、路线图和运维文档
```

后端服务各自拥有独立 `go.mod`，可以分开构建和部署；公共代码只通过 `packages/common` 复用。

## 当前能力与限制

- Control Plane 默认使用 memory store，适合本地演示；Postgres repository 已有边界和基础实现。
- Agent Runtime 当前是 mock provider + 可替换 `AgentEngine` 边界，尚未真实接入 Eino ADK。
- Sandbox 当前提供 local executor 和 container executor 入口，但还不是生产级强隔离沙箱。
- Frontend 已改为 React + Rspack，风格为黑白微黄色、面性+线性、少圆角的简洁聊天工作台。
- Redis Streams、模型密钥管理、认证、多租户、配额和完整审批闭环仍在后续阶段。

## 本地运行

安装 Go 后启动 Control Plane：

```bash
make run-control
```

React 前端开发服务器：

```bash
cd frontend
npm install
npm run dev
```

Rspack dev server 默认运行在 `http://localhost:3000`，并把 `/api`、`/healthz` 代理到 `http://127.0.0.1:8080`。

构建前端静态产物：

```bash
make build-web
```

Control Plane 默认从 `../../frontend/dist` 托管构建后的静态文件，也可以通过 `WEB_DIST_DIR` 覆盖。

## 常用检查

```bash
make test
make check-js
make compose-config
git diff --check
```

如果本机 Go cache 权限受限，可以使用：

```bash
GOCACHE=/private/tmp/niceagent-go-cache make test
```

## 下一阶段计划

- Control Plane：把 memory store 切换为可配置 repository，完善 Postgres/Redis Streams 实际运行路径。
- Agent Runtime：接入 Eino ADK adapter、模型 provider、tool bridge 和 checkpoint/resume。
- Sandbox：把 container executor 做成默认远端 CLI 执行路径，补齐资源限制、网络策略、审计和审批。
- Frontend：继续完善 ChatGPT 风格交互，包括 run replay、artifact 展示、skill 授权流和错误恢复。
- 部署：为三个 Go 服务和前端分别补 Dockerfile、镜像构建和环境配置说明。

更多细节见 [架构说明](docs/ARCHITECTURE.md)、[API 契约](docs/API.md)、[开发指南](docs/DEVELOPMENT.md) 和 [路线图](docs/ROADMAP.md)。
