# NiceAgent 运维说明

## 运行模式

当前仓库支持两种运行思路：

- memory demo：直接启动 `services/control-plane`，无需 Postgres/Redis，适合本地验证 UI、API 和事件流。
- Compose 拓扑：通过 `deployments/docker-compose.yml` 启动服务和依赖，Control Plane 使用 `STORE_DRIVER=postgres` 验证持久化路径。
- ACK 拓扑：通过 `deployments/k8s` 将三服务发布到阿里云 ACK，适合验证镜像发布和服务解耦链路。
- 前端开发：启动 `frontend` 的 Rspack dev server，并通过代理访问 Control Plane API。

## 状态与数据

memory 模式的权威状态在进程内存中，进程重启会丢失数据。Postgres 模式下，以下数据以数据库为准：

- 用户、组织、项目。
- 聊天会话和消息。
- runs、run events、artifacts。
- skills、审批、配额、审计记录。

当前 Postgres 模式支持单 Control Plane 进程内 SSE fanout 和基于数据库的 `RunEvent` replay。Redis Streams 后续用于多实例 run dispatch 和 event fanout，但不应替代 Postgres 的权威持久化。

## 模型 Provider

Agent Runtime 默认使用 `MODEL_PROVIDER=mock`，适合本地演示和 CI。切到真实 OpenAI-compatible provider 时，需要配置：

- `MODEL_PROVIDER=openai-compatible`
- `MODEL_BASE_URL`：兼容服务根地址，不包含 `/v1/chat/completions`。
- `MODEL_API_KEY`：模型服务密钥，只能通过环境变量或 Kubernetes Secret 注入，不写入仓库。
- `MODEL_NAME`：请求体中的 `model`。
- `MODEL_TIMEOUT_SECONDS`：模型 HTTP 请求超时，默认 120 秒。

模型流式输出统一写成 `model.token` run event。非 2xx、流式 JSON 解析失败、网络超时都会让 runtime 通过 Control Plane 写入 `run.failed`。

## Postgres 模式排查

启动 Compose：

```bash
docker compose -f deployments/docker-compose.yml up
```

只重启 Control Plane：

```bash
docker compose -f deployments/docker-compose.yml restart control-plane
```

重启后访问 `GET /api/chats` 和 `GET /api/runs/{run_id}/events?after=0`，确认会话、消息、run 和 events 仍可恢复。

清理本地持久化数据：

```bash
docker compose -f deployments/docker-compose.yml down -v
```

这会删除本地 Postgres volume，适合重新初始化 schema。

## 日志与排查

排查问题时优先关注：

- `run_id`：贯穿一次用户请求的执行链路。
- `chat_id`：定位用户会话。
- event `seq`：确认 SSE replay 和事件顺序。
- run terminal state：确认 `succeeded`、`failed`、`canceled` 是否被迟到事件覆盖。
- `MODEL_PROVIDER` 和模型 HTTP 状态：定位真实模型调用失败。

后续需要接入结构化日志、metrics 和 trace，便于观测队列延迟、模型延迟、sandbox 启动耗时和失败率。

## Sandbox 安全边界

当前 local executor 只适合本地演示，不是生产沙箱。生产化前至少需要：

- 容器或更强隔离边界。
- CPU、内存、磁盘、进程数和超时限制。
- workspace 只读/读写挂载策略。
- 网络默认关闭或按策略开启。
- 输出大小限制和敏感信息过滤。
- 高风险命令审批和完整审计。

## CLI 策略排查

当前 CLI policy 分三类：

- allowlist 命令：例如 `echo`、`pwd`、`ls`、`date`，会正常执行并产生 `tool.output`。
- dangerous 命令：例如 `rm`、`sudo`、`chmod`、`curl`，不会执行，会产生 `approval.needed`，run 进入 `waiting_for_approval`。
- 非 allowlist 命令：直接策略拒绝，作为普通 tool error 暴露，不进入审批。

`approval.needed` 目前只表示“已进入等待授权状态”，审批后恢复执行仍在后续阶段实现。

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

- memory store 无法支撑多实例共享状态；Postgres 模式当前先服务单 Control Plane 副本。
- OpenAI-compatible provider 已支持真实流式模型输出，但 runtime 还不具备模型工具规划和长任务恢复能力。
- local executor 不提供生产级命令隔离。
- sandbox、认证、租户配额、审批和审计仍需继续完善。
