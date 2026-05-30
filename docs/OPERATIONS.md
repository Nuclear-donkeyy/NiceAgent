# NiceAgent 运维说明

## 运行模式

当前仓库支持两种运行思路：

- memory demo：直接启动 `services/control-plane`，无需 Postgres/Redis，适合本地验证 UI、API 和事件流。
- Compose 拓扑：通过 `deployments/docker-compose.yml` 启动服务和依赖，适合验证后续 Postgres/Redis 接入路径。
- ACK 拓扑：通过 `deployments/k8s` 将三服务发布到阿里云 ACK，适合验证镜像发布和服务解耦链路。
- 前端开发：启动 `frontend` 的 Rspack dev server，并通过代理访问 Control Plane API。

## 状态与数据

当前权威状态仍在 memory store 中，进程重启会丢失数据。后续 Postgres repository 完成后，以下数据应以数据库为准：

- 用户、组织、项目。
- 聊天会话和消息。
- runs、run events、artifacts。
- skills、审批、配额、审计记录。

Redis Streams 后续用于 run dispatch 和 event fanout，但不应替代 Postgres 的权威持久化。

## 日志与排查

排查问题时优先关注：

- `run_id`：贯穿一次用户请求的执行链路。
- `chat_id`：定位用户会话。
- event `seq`：确认 SSE replay 和事件顺序。
- run terminal state：确认 `succeeded`、`failed`、`canceled` 是否被迟到事件覆盖。

后续需要接入结构化日志、metrics 和 trace，便于观测队列延迟、模型延迟、sandbox 启动耗时和失败率。

## Sandbox 安全边界

当前 local executor 只适合本地演示，不是生产沙箱。生产化前至少需要：

- 容器或更强隔离边界。
- CPU、内存、磁盘、进程数和超时限制。
- workspace 只读/读写挂载策略。
- 网络默认关闭或按策略开启。
- 输出大小限制和敏感信息过滤。
- 高风险命令审批和完整审计。

## 常用运维检查

检查 Compose 配置：

```bash
make compose-config
```

检查前端脚本语法：

```bash
make check-js
```

构建 React/Rspack 前端：

```bash
make build-web
```

运行 Go 测试：

```bash
make test
```

检查未提交 diff 的空白问题：

```bash
git diff --check
```

构建三服务容器镜像：

```bash
make docker-build
```

## 当前限制

- memory store 无法支撑多实例共享状态。
- mock runtime 不具备真实模型调用、工具规划和长任务恢复能力。
- local executor 不提供生产级命令隔离。
- sandbox、认证、租户配额、审批和审计仍需继续完善。
