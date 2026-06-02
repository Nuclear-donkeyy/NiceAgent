# Redis Streams 容量冒烟 Runbook

本文用于给 Redis run queue 的早期容量验证留一个可重复入口。它不是正式压测，也不证明 Redis 高可用已经完成；它只验证当前环境下 Redis Streams 的 `XADD -> XREADGROUP -> XACK` 基础吞吐、pending 清零和机器可读报告。

## 适用场景

- 合入 Redis queue 相关改动后，想快速确认本机或 CI 节点能跑通 Streams 基础路径；该 smoke 已进入 GitHub `Test and build` 默认门禁。
- 在目标部署环境变更 Redis 规格、网络或参数前后，留一份轻量对比报告。
- 排查 `consumer group lag`、pending entries 或 DLQ 告警前，先确认基础 Redis 连接与 Streams 命令可用。

## 快速执行

默认入口会启动一个临时 `redis:7-alpine` 容器，写入独立的 `niceagent:capacity:runs:*` stream，消费并 ack 后清理 stream：

```bash
make smoke-redis-capacity
```

如果本机没有 Docker CLI 或 daemon，命令会输出 `SKIP` 并以成功状态退出，避免阻断普通开发检查。

GitHub Actions 默认会运行同一个入口。CI 中的报告直接打印到 job log，用于确认 `pending_count=0`、`lag=0` 和报告字段未漂移；生产发布前仍建议使用目标 Redis 运行一次 `--require-redis`。

## 使用已有 Redis

要对已有 Redis 运行同一个冒烟脚本：

```bash
REDIS_ADDR=127.0.0.1:6379 \
REDIS_CAPACITY_MESSAGES=1000 \
REDIS_CAPACITY_REPORT=.local/redis-capacity/report.json \
python3 scripts/smoke_redis_capacity.py
```

如果希望 Redis 不可达时直接失败，而不是 `SKIP`：

```bash
python3 scripts/smoke_redis_capacity.py \
  --redis-addr 127.0.0.1:6379 \
  --messages 1000 \
  --require-redis
```

## 报告字段

通过 `REDIS_CAPACITY_REPORT` 或 `--report` 可以写出 JSON 报告。通过时核心字段包括：

- `result`：`passed`、`failed` 或 `skipped`。
- `messages`：本次写入和消费的消息数。
- `batch_size`：`XREADGROUP COUNT` 批大小。
- `duration_ms`：完整冒烟耗时。
- `enqueue_rate_per_second`：`XADD` 近似写入速率。
- `consume_rate_per_second`：`XREADGROUP + XACK` 近似消费速率。
- `pending_count`：冒烟结束后 consumer group pending 数，应为 `0`。
- `lag`：Redis `XINFO GROUPS` 返回的 lag，Redis 版本不支持时可能为空。

## 解读建议

- `pending_count > 0`：说明有消息被读出但没有 ack，先检查脚本或 Redis 连接是否中断，再看 Runtime worker 的 ack/error 指标。
- `consume_rate_per_second` 明显低于预期：先确认 Redis 与运行节点之间网络延迟，再调整 `REDIS_CAPACITY_BATCH_SIZE` 做对比。
- `lag` 持续不为 0：说明 consumer group 视角仍有未消费消息，生产环境要结合 `niceagent_runtime_redis_queue_consumer_group_lag` 告警排查。
- 本脚本只使用单 consumer，不覆盖多 Runtime 竞争、lease 续租、`XAUTOCLAIM`、DLQ 或完整三服务执行成本；这些仍由 `make smoke-three-services-redis` 和后续正式压测覆盖。

## 生产化后续

正式生产容量规划仍需要：

- Redis 高可用拓扑、持久化、备份与故障演练。
- 多 consumer、多 Control Plane、多 Runtime 的长时间压测。
- queue lag、pending entries、oldest pending idle、DLQ length 和 Redis 内存的 Grafana 看板。
- 结合业务 run 的模型延迟、tool 延迟、sandbox 延迟一起做端到端容量评估。
