# 多实例队列与事件流

## 产品功能

NiceAgent 的目标是让用户请求可以命中任意 Control Plane，run 可以被任意 Agent Runtime 消费，同时前端仍能稳定看到同一条会话、同一个 run 和连续事件流。

目标能力：

- 多个 Control Plane 副本同时服务 Web API 和 SSE。
- 多个 Agent Runtime 副本并发消费 run。
- 单个 run 只有一个有效 attempt 写入最终状态。
- Runtime 崩溃后 run 可重试或进入 DLQ。
- SSE 断线后按 event `seq` 补齐缺失事件。
- Postgres 保持权威状态，Redis 只做调度、短期 pending/retry、实时 fanout 或唤醒。

## 成熟方案调研

Redis Streams 适合作为 run queue。Control Plane 用 `XADD` 写入 run stream，Agent Runtime 使用同一个 consumer group 通过 `XREADGROUP` 分摊任务，成功后 `XACK`。consumer group 语义是至少一次交付，因此需要业务幂等。参考：[Redis Streams](https://redis.io/docs/latest/develop/data-types/streams/)。

重试和恢复可以基于 pending entries。`XPENDING` 可观察未确认消息，`XAUTOCLAIM` 可把 idle 时间过长的 pending message 转给新的 consumer，超过最大投递次数后应进入 DLQ。参考：[XREADGROUP](https://redis.io/docs/latest/commands/xreadgroup/)、[XPENDING](https://redis.io/docs/latest/commands/xpending/)、[XAUTOCLAIM](https://redis.io/docs/latest/commands/xautoclaim/)。

Postgres `LISTEN/NOTIFY` 可以做轻量跨副本事件唤醒，但它不是持久队列。更稳妥的模式是：权威事件写 `run_events`，通知只发布 `{run_id, seq}`，收到通知的 Control Plane 再从 Postgres replay。参考：[PostgreSQL LISTEN](https://www.postgresql.org/docs/current/sql-listen.html)、[PostgreSQL NOTIFY](https://www.postgresql.org/docs/current/sql-notify.html)。

SSE replay 应使用事件 ID。服务端可以发送 `id: <seq>`，前端保留最后处理的 `seq`，重连时通过 `Last-Event-ID` 或 `?after=` 补齐缺口，避免模型 token 重复拼接。

## 当前仓库现状

当前创建消息时，Control Plane 已经持久化 user message、run 和 `run.queued` event，然后调用 dispatcher。外部 API 会立刻返回 message/run，符合异步 run 生命周期。

默认 dispatch 主路径仍是 HTTP dispatcher：后台 goroutine 调 `AGENT_RUNTIME_URL` 的 `/internal/runs/execute`。如果 HTTP 调用失败，Control Plane 直接写 `run.failed`。这条路径简单可用，适合作为本地和最小部署路径。

Redis queue 路径已经具备最小可执行闭环：`RunQueue`、`MemoryRunQueue`、`RedisStreamsRunQueue` 边界已经存在；`RedisStreamsRunQueue` 已提供 `XADD`、`XGROUP CREATE MKSTREAM`、`XREADGROUP`、成功 `XACK` adapter，并通过 `DISPATCH_MODE=redis` 接入 Control Plane 入队路径。当前 queue payload 已收敛为 `run_id`、`attempt_id` 和 `enqueued_at`，Agent Runtime 也已有 `RUNTIME_QUEUE_MODE=redis` worker，可通过 Control Plane `/internal/runs/{run_id}/execution-context` 拉取用户消息、workspace 和 `RuntimeSkill` 后执行。Runtime 执行前会 claim attempt，Control Plane 内部回写 API 会校验 `attempt_id`。因此 Redis 模式已经从“只入队”推进到多 runtime consumer 可运行路径。

`EventBus`、`RepositoryEventBus`、`RedisStreamsEventBus` 边界也已存在，但 HTTP API 当前主要还是直接通过 repository replay/subscribe。`RedisStreamsEventBus` 还是占位实现。当前新增了 `FanoutRepository + RedisNudgeBus`：repository 写入事件后发布轻量 nudge，其他 Control Plane 副本收到 nudge 后按 `seq` 从 repository 补读权威事件。

Postgres event 写入已能保证单 run 内 `seq` 递增，memory/Postgres store 都有进程内 fanout。`EVENT_FANOUT_MODE=redis` 可补齐多副本 live nudge；Redis 只负责唤醒，不存放权威事件正文。

SSE endpoint 支持 `?after=`、`Last-Event-ID` replay、SSE `id: <seq>` 和 ping；前端记录每个 run 的 last seq，打开连接时带 `after`，并在应用事件前丢弃重复 seq。服务端订阅前后都会按 `seq` replay 一次，降低 replay 与 subscribe 之间的竞态窗口。

终态保护已有基础：`Complete`/`Fail` 会跳过 terminal run，store 也阻止 terminal 被不同状态覆盖。`runs` 已补上 `active_attempt_id`、`claimed_by`、`lease_expires_at`、`attempt_count`，旧 attempt 的 event/complete/fail 会被拒绝。Redis worker 已支持 heartbeat 续租、idle pending `XAUTOCLAIM` 和 DLQ；跨 Control Plane 副本 live fanout 已有 Redis nudge 路径。`make smoke-three-services-redis` 已覆盖两个 Agent Runtime consumer 的最小冒烟，`make smoke-control-plane-fanout` 已覆盖两个 Control Plane 进程间的 event fanout 冒烟。Redis worker 已采样 pending count、最老 pending idle 秒数和 DLQ length，并提供基础 Prometheus 告警。Control Plane 已暴露 SSE run event 订阅连接打开/关闭次数、当前活跃连接数和连接持续时间。生产级 Redis 高可用和容量压测仍待补齐。

已落地能力：

- HTTP dispatcher 仍是默认简单路径，Redis dispatcher 可通过 `DISPATCH_MODE=redis` 入队。
- Redis Streams queue adapter 已有 `XADD`、consumer group、`XREADGROUP`、`XACK`、payload 最小化、Runtime worker、execution context 拉取和多 Runtime consumer smoke。
- attempt/lease/fencing 已落到 `runs.active_attempt_id`、`claimed_by`、`lease_expires_at`、`attempt_count`，旧 attempt 回写会被拒绝。
- Runtime worker 已支持 heartbeat 续租、idle pending `XAUTOCLAIM`、最大投递次数和 DLQ。
- SSE replay 已支持 `id`、`?after=`、`Last-Event-ID`、前端 seq 去重和跨 Control Plane Redis nudge fanout。
- CI 已包含 Redis queue/fanout 相关 smoke，Redis worker 也暴露 message、reclaim、ack、error、DLQ、pending count、oldest pending idle 等指标；Control Plane 暴露 SSE active subscriber 和连接持续时间指标。

仍待落地能力：

- 生产级 Redis HA/备份/故障演练、容量压测、queue lag、更多 fanout 延迟指标和外部告警。
- 可选独立 dispatcher worker 服务、独立 `run_attempts` 审计表和更完整的多副本回放压测。

## 扩展点

- `DISPATCH_MODE=redis`、`RUNTIME_QUEUE_MODE=redis`、stream/group/consumer/DLQ/idle timeout/max delivery 已有最小配置面，后续要补生产部署模板和容量建议。
- `runs` 已具备 `active_attempt_id`、`claimed_by`、`lease_expires_at`、`attempt_count`，后续如需更完整审计可新增独立 `run_attempts` 表。
- Runtime Redis worker loop 已落地，后续可评估是否拆成独立 dispatcher worker 服务。
- Control Plane 内部回写 API 已增加 `attempt_id` 校验，旧 attempt 不能写 event 或终态。
- Event 写入后可发布 Redis fanout nudge，Control Plane 副本收到 nudge 后从 Postgres 按 seq 补发给本机 SSE。
- SSE 已写入 `id: seq`，前端记录 last seq，重连时带 `after` 并去重。
- queue message/reclaim/ack/error/DLQ counters、pending entries、oldest pending idle、DLQ length gauges、SSE subscriber count 和连接持续时间已有最小指标，后续补 queue lag、外部告警和压测。

## 技术架构

推荐链路：

```text
Browser -> 任意 Control Plane
  -> Postgres 事务写 message/run/run.queued
  -> Redis XADD niceagent:runs {run_id, attempt_seed}
  -> Agent Runtime XREADGROUP 消费
  -> Runtime 向 Control Plane claim run
  -> 执行 Eino agentic loop
  -> 回写 event/complete/fail + attempt_id
  -> Control Plane 事务写 run_events/message/status
  -> fanout nudge {run_id, seq}
  -> 各 Control Plane 副本 replay seq > last_seq
  -> SSE 推给本机浏览器连接
```

幂等边界：

- Redis queue 只保证至少一次投递。
- Postgres attempt/lease 是真正的执行权威。
- `complete/fail/cancel` 都必须检查 run 当前状态和 attempt。
- `run_events` 继续以 `(run_id, seq)` 保证顺序。

队列 payload 建议只放最小信息：

- `run_id`
- `attempt_seed` 或 `dispatch_id`
- `created_at`

执行上下文、用户消息、skills 和 secrets 由 Runtime 在 claim 成功后从 Control Plane 内部 API 拉取，避免 queue 里保存过期授权或 secret。

## 技术方案

第一步先修 SSE replay，因为它独立且能立即提升前端可靠性：

- `WriteSSE` 支持可选 event id。
- `/api/runs/{id}/events` 同时读取 `Last-Event-ID` 和 `?after=`。
- 前端维护 `lastSeqByRun`，应用事件前去重。
- 断线重连时显式带 `?after=<lastSeq>`。

第二步做跨副本 fanout：

- event 仍写 Postgres。
- 写入 event 后发布 Postgres `NOTIFY` 或 Redis pub/sub/stream nudge。
- 每个 Control Plane 只根据本机 subscriber 拉取缺失事件。

第三步做 Redis run queue：

- `DISPATCH_MODE=redis` 下 Control Plane 只入队，不直连单个 runtime。
- Runtime 启动 consumer group worker。
- claim 成功才执行，claim 失败就 `XACK` 当前消息。
- 失败根据错误类型决定 retry、DLQ 或直接 fail。

第四步补 attempt/lease：

- 长任务 Runtime 定期 heartbeat。
- 超过 lease 的 pending message 可 `XAUTOCLAIM`。
- 旧 attempt 的 event/complete/fail 被拒绝或记录为 ignored audit。

## 分阶段落地

1. SSE replay 收口：`id:`、`Last-Event-ID`、前端 last seq、去重测试。
2. 跨副本 event fanout：Redis nudge 最小路径和多 Control Plane smoke 已落地，权威仍是 Postgres/repository；下一步补容量测试和外部告警。
3. Redis Streams run queue：`XADD`、`XREADGROUP`、`XACK` 基础路径已落地，并有多 Runtime consumer 冒烟。
4. Attempt/lease/retry：claim、heartbeat、`XAUTOCLAIM`、DLQ 已有最小闭环；`make smoke-three-services-redis` 已能启动临时 Redis 和两个 Agent Runtime consumer 做最小多 runtime 冒烟；`make smoke-control-plane-fanout` 已覆盖跨 Control Plane event fanout。主 queue stream 和 DLQ stream 已支持可配置近似裁剪，Runtime 已暴露 message/reclaim/ack/error/DLQ counters，并采样 pending entries 与 DLQ length gauges，后续继续补外部告警和压测。
5. 压测与观测：覆盖 100/500/1000 并发 run、queue lag、SSE replay gap。

## 风险与验收

风险：

- Redis Streams 至少一次投递导致重复执行。
- Runtime 长任务被误判 idle 后重复 claim。
- 迟到 complete 覆盖 canceled 或 succeeded。
- SSE 重连重复 token。
- Redis stream trim 删除未 ack message。
- fanout 通知丢失导致 live SSE 暂时不更新。

验收：

- 两个 Runtime 并发消费 100 个 run，无重复终态。
- 杀死 Runtime 后 run 可重试或进入 DLQ。
- 两个 Control Plane 副本下 SSE 不丢 event。
- 刷新或断网重连只补缺失 seq，不重复 assistant token。
- 取消 run 后迟到 complete 不写 assistant message。
- Postgres 中 run/message/event 状态一致。

## 参考资料

- [Redis Streams](https://redis.io/docs/latest/develop/data-types/streams/)
- [XREADGROUP](https://redis.io/docs/latest/commands/xreadgroup/)
- [XACK](https://redis.io/docs/latest/commands/xack/)
- [XPENDING](https://redis.io/docs/latest/commands/xpending/)
- [XAUTOCLAIM](https://redis.io/docs/latest/commands/xautoclaim/)
- [PostgreSQL LISTEN](https://www.postgresql.org/docs/current/sql-listen.html)
- [PostgreSQL NOTIFY](https://www.postgresql.org/docs/current/sql-notify.html)
