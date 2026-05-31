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
- skills、skill_versions、用户/项目 skill_grants、skill_secrets、配额、审计记录。

当前 Postgres 模式支持单 Control Plane 进程内 SSE fanout 和基于数据库的 `RunEvent` replay。Redis Streams 后续用于多实例 run dispatch 和 event fanout，但不应替代 Postgres 的权威持久化。

## 模型 Provider

Agent Runtime 默认使用 `MODEL_PROVIDER=mock`，适合本地演示和 CI。切到真实 OpenAI-compatible provider 时，需要配置：

- `MODEL_PROVIDER=openai-compatible`
- `MODEL_BASE_URL`：兼容服务根地址，不包含 `/v1/chat/completions`。
- `MODEL_API_KEY`：模型服务密钥，只能通过环境变量或 Kubernetes Secret 注入，不写入仓库。
- `MODEL_NAME`：请求体中的 `model`。
- `MODEL_TIMEOUT_SECONDS`：模型 HTTP 请求超时，默认 120 秒。

Runtime 当前通过 Eino ADK `ChatModelAgent + Runner` 和 Eino 原生 `ToolCallingChatModel` 执行 agentic loop。模型输出统一写成 `model.token` run event，tool 调用统一写成 `tool.started`、`tool.output`、`tool.finished`。OpenAI-compatible provider 通过 `eino-ext` OpenAI ChatModel 接入，模型 HTTP 错误、tool calling 错误和网络超时都会让 runtime 通过 Control Plane 写入 `run.failed`。

## Skill 与 Secret 排查

Skill 存储分为四层：

- `skills`：稳定身份、scope、kind、owner、status。
- `skill_versions`：name、description、JSON schema、MCP 风格 annotations、runtime_config。
- `skill_grants`：用户/项目可用性。
- `skill_secrets`：secret 引用或本地开发密文。

前端 `GET /api/skills` 不返回 secret。Runtime 通过 Control Plane 下发的内部 `RuntimeSkill` 获取执行所需 secret。当前本地开发允许把 bearer token 存入 `encrypted_value`；生产环境应替换为阿里云 KMS、Vault 或 External Secrets。

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
- 网络按只读外部信息获取策略开启。
- 输出大小限制和敏感信息过滤。
- 高风险命令策略拒绝和完整审计。

## CLI 策略排查

当前 CLI 是系统级 agent 工具，不需要用户逐次授权。CLI policy 分三类：

- allowlist 命令：例如 `curl`、`wget`、`dig`、`nslookup`、`echo`、`pwd`、`ls`、`date`，会正常执行并产生 `tool.output`。
- dangerous 命令：例如 `rm`、`sudo`、`chmod`、`chown`、shell 启动和文件写入类命令，不会执行，会作为普通 tool error 返回给 Agent Runtime。
- 非 allowlist 命令：直接策略拒绝，作为普通 tool error 暴露。

`approval.needed` 事件保留给未来真正需要用户确认的非 CLI skill。系统 CLI 不再使用该事件，也不会让 run 进入 `waiting_for_approval`。

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
- OpenAI-compatible provider 已切到 Eino 原生 ChatModel；不同 OpenAI-compatible 服务的非标准参数兼容性仍需在后续按 provider-specific option 补强。
- local executor 不提供生产级命令隔离。
- sandbox、认证、租户配额、审批和审计仍需继续完善。
