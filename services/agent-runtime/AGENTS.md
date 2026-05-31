# Agent Runtime 服务导览

Agent Runtime 是无状态 agent 实例服务，负责执行 agentic loop、模型调用、skill/tool 调用，并把事件和终态回写 Control Plane。它不保存用户会话状态。

## 分层结构

```text
cmd
  服务装配入口，只负责读取配置、组装依赖、启动 HTTP server。

internal/config
  Runtime、模型 provider、Sandbox、内部 token 的环境变量解析。

internal/httpapi
  `/healthz` 和 `/internal/runs/execute`，负责内部鉴权、DTO 解析和 Control Plane sink 装配。

internal/engine
  AgentEngine、Eino agentic loop、循环限制、模型和工具编排。

internal/modelprovider
  mock provider 与 OpenAI-compatible provider。

internal/tools
  ToolBridge、builtin skill、HTTP skill、SandboxExecutor 端口和工具事件输出。

internal/sink
  Control Plane event/status/complete/fail 回写客户端。
```

## 修改约定

- Runtime 只能使用 `RunExecutionRequest` 下发的 skills，不自行读取用户会话状态。
- 新模型 provider 放在 `internal/modelprovider`，不要写进 engine。
- 新 skill 执行器放在 `internal/tools`，所有调用都要产出 `tool.*` 事件。
- 回写 Control Plane 的 HTTP 细节只放在 `internal/sink`。

## 检查命令

```bash
GOCACHE=/private/tmp/niceagent-go-cache go test ./...
```
