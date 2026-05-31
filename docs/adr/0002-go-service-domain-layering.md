# ADR 0002：Go 服务按部署边界和领域边界分层

## 状态

Accepted

## 背景

NiceAgent 后端已经拆成 Control Plane、Agent Runtime 和 Sandbox Executor 三个 Go module，但部分入口和共享协议仍偏集中。后续会继续增加认证、多租户、Skill registry、ToolBridge 和 sandbox 能力，需要更明确的代码归属。

## 决策

- 三个服务继续独立 `go.mod`，只通过 `packages/common` 共享协议和基础工具。
- 服务入口 `cmd/main.go` 只做装配。
- 环境变量解析放 `internal/config`。
- HTTP handler、应用服务、repository、dispatcher、runtime tools 等按职责拆分。
- `packages/common/protocol` 按领域文件拆分，保持 package/import path 不变。

## 影响

- 后续新增 API 时优先落在 HTTP 边界包，再调用应用服务。
- 后续新增持久化逻辑时只改 repository 包，不把 SQL 写进 handler。
- 后续新增协议类型时按领域文件加入，避免恢复单个巨大 `types.go`。
