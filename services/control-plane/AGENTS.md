# Control Plane 服务导览

Control Plane 是 NiceAgent 的前置服务器，负责用户可见状态和调度入口：聊天、消息、run、events、skills、SSE、持久化和 Runtime 调度。

## 分层结构

```text
cmd
  服务装配入口，只负责读取配置、组装依赖、启动 HTTP server。

internal/config
  环境变量解析和启动配置。

internal/httpapi
  外部 Web API、内部 Runtime 回写 API、SSE、鉴权、request/response 解析。

internal/app
  Repository、RunDispatcher 等端口接口，以及 run 完成/失败和 skill 下发相关的小型用例逻辑。

internal/repository
  memory store 与 Postgres store。这里是会话、消息、run、event、skill 的权威状态实现。

internal/dispatch
  local/http dispatcher 和 run queue 边界。

internal/events
  event bus/replay/fanout 边界；当前以 repository 为权威。
```

## 修改约定

- `httpapi` 不直接依赖具体 store 或 dispatcher 实现，只依赖 `app` 端口。
- `repository` 不处理 HTTP request/response。
- `dispatch` 不做用户会话管理，只负责把 run 交给 Runtime 或 fallback。
- 不要让其他服务 import 本服务的 `internal` 包；跨服务协议放到 `packages/common`。

## 检查命令

```bash
GOCACHE=/private/tmp/niceagent-go-cache go test ./...
```
