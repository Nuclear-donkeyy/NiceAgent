# Skill Rate Limit 容量与告警 Runbook

本文用于排查 `NiceAgentSkillRateLimitDenials` 告警，并给早期生产部署提供 Skill rate limit 容量建议。当前能力覆盖 HTTP Skill 和 MCP Skill；系统内置 builtin skill 的执行配额仍以 Control Plane 的 tool/sandbox quota 为主。

## 告警语义

Prometheus 规则：

```promql
sum by (kind, mode) (rate(niceagent_skill_rate_limit_denials_total[5m])) > 0.1
```

标签说明：

- `kind`：被限流的 Skill 类型，当前主要是 `http` 或 `mcp`。
- `mode`：限流器模式，`local` 表示单个 Agent Runtime 进程内固定窗口，`redis` 表示多个 Runtime 副本共享 Redis 分钟窗口，`custom` 表示后续自定义限流器。

触发含义：

- 模型正在频繁调用同一类外部能力。
- 某个导入的 HTTP/MCP Skill 的 `rate_limit.requests_per_minute` 配置可能过紧。
- 第三方 API 实际容量不足，限流保护正在阻止继续打请求。
- `SKILL_RATE_LIMIT_MODE=redis` 下，所有 Runtime 副本共享同一窗口，因此告警更接近项目真实外部 API 压力。

## 快速排查

1. 先确认告警维度：

   ```promql
   sum by (kind, mode) (rate(niceagent_skill_rate_limit_denials_total[5m]))
   ```

2. 看是否伴随风险策略拒绝：

   ```promql
   sum by (policy, reason, risk, kind) (rate(niceagent_skill_policy_denials_total[5m]))
   ```

   如果两者同时升高，通常说明模型反复尝试调用不可用或不合规的能力，需要检查 prompt、Skill manifest 风险标注和项目运行策略。

3. 看 tool 调用是否同时触发项目级 quota：

   ```promql
   sum by (quota) (rate(niceagent_quota_denials_total[5m]))
   ```

   如果 `tool_calls_per_day` 或 `sandbox_seconds_per_day` 同时升高，优先处理项目 quota，而不是单个 Skill 的 rate limit。

4. 在 Control Plane 查询项目用量：

   ```bash
   curl -H "Authorization: Bearer $TOKEN" \
     "$CONTROL_PLANE_URL/api/projects/$PROJECT_ID/usage?window=24h"
   ```

   重点看 `tool_calls`、`tool_errors`、`sandbox_duration_millis`、`provider/model` 分布。

5. 在前端左侧“项目容量”区域查看 24h 容量视图，确认模型 token、tool calls 和 sandbox 秒数是否已经接近项目 quota。

## 常见原因

### 导入 Skill 配置过紧

OpenAPI/MCP 导入时，如果把 `rate_limit.requests_per_minute` 设置得很小，例如 1 或 2，模型的一次任务循环就可能触发限流。

处理方式：

- 对只读查询型 API，可以把 `requests_per_minute` 调高到供应商允许范围内。
- 对付费或有严格配额的 API，保留较低值，同时在系统提示词中要求 agent 汇总多个问题后再调用。

### 模型反复调用同一个工具

当 tool observation 不够清晰、HTTP Skill 返回错误、或者模型没有拿到足够信息时，agentic loop 可能重复调用同一 Skill。

处理方式：

- 检查最近 run 的 `tool.output` observation 是否清楚说明失败原因。
- 检查 HTTP/MCP Skill 的 `description` 是否足够明确，是否写清楚适用场景和输入字段。
- 如果是 API 返回非 2xx，优先修复第三方服务或参数 schema。

### 多 Runtime 副本放大请求量

`SKILL_RATE_LIMIT_MODE=local` 时，每个 Runtime 副本都有独立窗口。假设单个 Skill 配置 `requests_per_minute=60`，有 4 个 Runtime 副本，则外部 API 实际可能收到约 240 rpm。

处理方式：

- 生产推荐使用 `SKILL_RATE_LIMIT_MODE=redis`。
- 只有本地开发、单 Runtime 或无 Redis 环境才使用 `local`。

### Redis rate limit fail closed

Redis 模式下，如果 Runtime 访问 Redis 失败，当前策略是 fail closed，优先保护第三方 API。这可能导致 `rate_limited` observation 增多。

处理方式：

- 检查 Agent Runtime 到 Redis 的网络连通性。
- 检查 `REDIS_ADDR`、`SKILL_RATE_LIMIT_PREFIX` 和 Redis 连接数。
- 对 Redis 本身配置监控和高可用，不要让 rate limit Redis 与 run queue Redis 互相挤占容量。

## 容量建议

### requests_per_minute 初始值

| Skill 类型 | 建议初始值 | 说明 |
| --- | ---: | --- |
| 系统内部只读查询 | 120 | 只适合低成本、低风险、响应稳定的内部服务 |
| 普通外部 HTTP API | 30 | 适合作为默认导入值，避免快速打爆第三方 |
| 付费 API 或搜索 API | 10 | 适合按次计费或供应商限制较紧的服务 |
| 高成本或敏感 API | 1-5 | 应搭配更明确的 prompt 和人工运营观察 |

### local 与 redis 模式换算

如果必须使用 `local` 模式，按副本数折算：

```text
单副本配置值 = 目标全局 rpm / Agent Runtime 副本数
```

例如第三方 API 允许全局 120 rpm，当前有 3 个 Runtime：

```text
requests_per_minute = 120 / 3 = 40
```

如果使用 `redis` 模式，`requests_per_minute` 就是全局共享窗口值。

### 与项目 quota 的关系

Skill rate limit 控制“单个 Skill 的外部 API 请求速度”，项目 quota 控制“用户/项目的整体资源消耗”。两者不要混用：

- 第三方 API 被打爆：调 Skill rate limit。
- 用户整体成本过高：调项目 quota。
- agent 反复调用工具：先修 prompt、Skill description 和 observation，再调限流。

## 推荐看板

最小 Grafana/Prometheus 面板可以包含：

```promql
sum by (kind, mode) (rate(niceagent_skill_rate_limit_denials_total[5m]))
sum by (policy, reason, risk, kind) (rate(niceagent_skill_policy_denials_total[5m]))
sum by (quota) (rate(niceagent_quota_denials_total[5m]))
sum by (provider, model, error_class) (rate(niceagent_model_runs_total{status="failed",error_class!="none"}[5m]))
sum by (stream, group) (niceagent_redis_queue_pending_entries)
```

前端“项目容量”区域适合给产品/运营快速判断项目级用量；Prometheus/Grafana 看板适合值班和平台工程排障。

## 验收清单

- `NiceAgentSkillRateLimitDenials` 告警能按 `kind/mode` 聚合，不包含 `skill_id` 这类高基数字段。
- Alertmanager 能把 `component="skill-rate-limit"` 路由到 quota/platform 相关接收组。
- 前端项目容量视图能显示 24h `tool_calls` 和项目 quota。
- 命中 rate limit 的 tool observation 为 `rate_limited`，且不会发起真实 HTTP/MCP 请求。
- `SKILL_RATE_LIMIT_MODE=redis` 在多 Runtime 部署中使用同一 Redis prefix。
