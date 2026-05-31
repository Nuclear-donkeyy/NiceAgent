# Sandbox Executor 服务导览

Sandbox Executor 是系统 CLI 和文件类能力的隔离执行边界。当前默认执行只读网络型命令，并保留 workspace、超时、环境变量过滤、输出截断和审计结果。

## 分层结构

```text
cmd
  服务装配入口，只负责读取配置、组装 executor、启动 HTTP server。

internal/config
  环境变量解析。

internal/httpapi
  `/healthz` 和 `/internal/sandbox/exec`，负责内部鉴权和 DTO 解析。

internal/executor
  服务内 executor 装配入口，当前封装 common local executor。

internal/policy
  CLI 策略边界说明和后续扩展点。
```

## 修改约定

- CLI 不做用户逐次授权，但必须受系统策略和 sandbox 限制。
- 高风险、写磁盘、改权限、长期驻留类命令继续拒绝。
- HTTP API 不直接扩散到 common 包；服务内行为先放在本服务 `internal`。
- 生产级容器/微虚拟机隔离属于后续阶段，不要把 local executor 写成强安全承诺。

## 检查命令

```bash
GOCACHE=/private/tmp/niceagent-go-cache go test ./...
```
