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

默认 dispatch 主路径是 HTTP dispatcher：后台 goroutine 调 `AGENT_RUNTIME_URL` 的 `/internal/runs/execute`。如果 HTTP 调用失败，Control Plane 直接写 `run.failed`。这条路径简单可用，但只有单一 runtime target，没有 queue、ack、retry 和多 runtime consumer group。

`RunQueue`、`MemoryRunQueue`、`RedisStreamsRunQueue` 边界已经存在；`RedisStreamsRunQueue` 目前返回未实现错误。`QueuedRun` 目前只包含 `SkillIDs`，不包含完整 `RuntimeSkill` manifest 或 `attempt_id`。

`EventBus`、`RepositoryEventBus`、`RedisStreamsEventBus` 边界也已存在，但 HTTP API 当前主要还是直接通过 repository replay/subscribe。`RedisStreamsEventBus` 还是占位实现。

Postgres event 写入已能保证单 run 内 `seq` 递增，memory/Postgres store 都有进程内 fanout。问题是 fanout 只在当前 Control Plane 进程内生效，多副本下其他副本不会收到 live event，只能靠 replay。

SSE endpoint 支持 `?after=` replay 和 ping，但当前 `WriteSSE` 不写 SSE `id:`，前端 `EventSource` 也没有记录 last seq 或携带 `after` 重连，浏览器自动重连有重复 token 的风险。

终态保护已有基础：`Complete`/`Fail` 会跳过 terminal run，store 也阻止 terminal 被不同状态覆盖。但 assistant message、run status、terminal event 还没有统一 attempt fencing 和事务级幂等。

## 扩展点

- 增加 `DISPATCH_MODE=redis`，配置 `REDIS_ADDR`、stream、group、consumer、DLQ、idle timeout、max delivery。
- 增加 `run_attempts` 或在 `runs` 上增加 `attempt_id`、`claimed_by`、`lease_expires_at`、`attempt_count`。
- Runtime 增加 Redis worker loop，或单独新增 dispatcher worker 服务。
- Control Plane 内部回写 API 增加可选 `attempt_id`，旧 attempt 不能写终态。
- Event 写入后发布 fanout nudge，Control Plane 副本收到 nudge 后从 Postgres 按 seq 补发给本机 SSE。
- SSE 写入 `id: seq`，前端记录 last seq，重连时带 `after` 并去重。
- 补 queue lag、pending count、oldest idle、retry count、DLQ count、SSE subscriber count 指标。

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
2. 跨副本 event fanout：先用 Postgres `LISTEN/NOTIFY` 或 Redis nudge，权威仍是 Postgres。
3. Redis Streams run queue：实现 `XADD`、`XREADGROUP`、`XACK` 基础路径。
4. Attempt/lease/retry：增加 claim、heartbeat、`XPENDING/XAUTOCLAIM`、DLQ。
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
