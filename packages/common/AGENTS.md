# Common Package 导览

`packages/common` 是跨服务共享 Go module，只放协议类型、平台基础工具和真正需要复用的执行客户端。

## 目录职责

```text
protocol
  跨服务 DTO、领域枚举和 API payload 类型。

platform
  日志、JSON response、SSE、ID 等通用基础工具。

sandbox
  local/container/http sandbox executor 客户端和共享执行结果结构。
```

## 修改约定

- 只有两个及以上服务都需要的类型或工具才放这里。
- 不要把某个服务的私有业务逻辑放进 common。
- 修改 `protocol` 必须同步更新 `docs/API.md` 和相关服务测试。
- common 不 import 任意服务的 `internal` 包。
