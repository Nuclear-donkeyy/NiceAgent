# NiceAgent 本地开发指南

## 准备环境

推荐安装：

- Go，用于运行和测试后端服务。
- Node.js，用于检查静态前端脚本语法。
- Docker 和 Docker Compose，用于启动 Postgres、Redis 和服务拓扑。

当前项目仍可在无 Postgres/Redis 的情况下运行 memory demo。

## 常用命令

启动 Control Plane：

```bash
go run ./cmd/control-plane
```

启动后访问：

```text
http://localhost:8080
```

测试受控 CLI skill：

```text
/cli echo hello
```

运行检查：

```bash
make check-js
make compose-config
make test
git diff --check
```

如果本机没有 Go 工具链，可以用 Docker 执行测试：

```bash
docker run --rm -v "$PWD":/workspace -w /workspace golang:1.22 go test ./...
```

## Docker Compose

检查 Compose 配置：

```bash
make compose-config
```

启动本地拓扑：

```bash
make compose-up
```

停止本地拓扑：

```bash
make compose-down
```

## 开发约定

- 面向人的说明文档和 UI 文案默认使用中文。
- 代码标识符、API path、JSON 字段、环境变量保持英文。
- 不要在文档中把尚未实现的能力写成已完成能力。
- 修改共享协议 `internal/protocol/types.go` 前需要和其他子 agent 协调。
- 并行开发时不要回滚他人变更，遇到冲突应优先顺应已有接口。

## 当前限制

- Control Plane 默认使用 memory store，数据不会持久化。
- Runtime 仍是 mock runtime，没有真实模型 provider 和 Eino loop。
- Sandbox 当前是 local executor，不是强隔离沙箱。
- 生产级认证、租户、配额、审批、审计和密钥管理仍待实现。
