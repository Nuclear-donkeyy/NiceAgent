# NiceAgent 本地开发指南

## 环境准备

推荐安装：

- Go：运行和测试三个后端服务。
- Node.js/npm：运行 React + Rspack 前端。
- Docker/Docker Compose：启动 Postgres、Redis 和服务拓扑。

## 后端服务

仓库根目录使用 `go.work` 组织多个 module：

```bash
make run-control
make run-runtime
make run-sandbox
```

也可以进入单个服务目录运行：

```bash
cd services/control-plane && go run ./cmd
cd services/agent-runtime && go run ./cmd
cd services/sandbox-executor && go run ./cmd
```

## 前端应用

```bash
cd frontend
npm install
npm run dev
```

前端开发服务默认在 `http://localhost:3000`，API 代理到 `http://127.0.0.1:8080`。

构建静态产物：

```bash
make build-web
```

Control Plane 默认托管 `../../frontend/dist`。如需指定其他目录：

```bash
WEB_DIST_DIR=/absolute/path/to/dist make run-control
```

## 检查命令

```bash
make test
make check-js
make compose-config
git diff --check
```

如果 Go cache 目录不可写：

```bash
GOCACHE=/private/tmp/niceagent-go-cache make test
```

如果不想在本机装 Go，可以用 Docker：

```bash
docker run --rm -v "$PWD":/workspace -w /workspace golang:1.22 \
  go test ./packages/common/... ./services/control-plane/... ./services/agent-runtime/... ./services/sandbox-executor/...
```

## 开发约定

- 面向人的文档和 UI 文案默认使用中文。
- 服务间共享代码只能放入 `packages/common`，不要跨 module import 其他服务的 `internal` 包。
- Control Plane、Agent Runtime、Sandbox Executor 应保持可独立部署。
- 代码标识符、API path、JSON 字段、环境变量保持英文。
- 不要在文档中把尚未完成的能力写成已完成能力。
