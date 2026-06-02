# 模型运营与 DeepSeek 接入

## 产品功能

模型层要从“能连 OpenAI-compatible provider”推进到“可运营的模型服务”。用户感知是 agent 回复稳定、工具调用可靠、失败可读；平台侧需要知道 token、成本、延迟、错误、限流、fallback 和日志安全。

近期重点：

- 接入真实 DeepSeek API key。
- 验证 Eino `ToolCallingChatModel` + DeepSeek/OpenAI-compatible 的 tool calling 链路。
- 记录真实 token usage 和成本。
- 对 429/5xx/超时做重试和 fallback。
- 模型日志、tool output、event payload 默认脱敏。

## 成熟方案调研

Eino 已提供 ChatModel 和 ADK `ChatModelAgent` 抽象，当前仓库使用 `ToolCallingChatModel` 是正确方向。Eino ChatModel 支持 `Generate`、`Stream`、tool calling 和 callback/trace 扩展。参考：[Eino ChatModel 指南](https://www.cloudwego.io/zh/docs/eino/core_modules/components/chat_model_guide/)、[Eino ADK ChatModelAgent](https://www.cloudwego.io/docs/eino/core_modules/eino_adk/agent_implementation/chat_model/)。

DeepSeek 官方 API 兼容 OpenAI/Anthropic API。按官方文档，OpenAI-compatible `base_url` 可用 `https://api.deepseek.com`，请求仍走 chat completions 风格；`MODEL_BASE_URL` 不应包含最终的 `/chat/completions` endpoint。参考：[DeepSeek API Docs](https://api-docs.deepseek.com/)。

DeepSeek 模型名会随官方发布节奏变化，仓库文档不再把某个模型名写成长期默认值。接入时应查看 DeepSeek 官方文档的当前模型列表，并把 `MODEL_NAME` 显式配置为当时可用的模型。

DeepSeek 官方错误码包括 400、401、402、422、429、500、503。401/402/422 属于配置或请求问题，不应重试；429、500、503 和网络瞬时错误可退避重试。参考：[DeepSeek Error Codes](https://api-docs.deepseek.com/quick_start/error_codes)。

DeepSeek 有账号级并发限制和 `user_id` isolation。应把 NiceAgent 内部 user/project 映射为 provider 侧 `user_id` 或 metadata，但不要包含隐私信息。参考：[DeepSeek Rate Limit & Isolation](https://api-docs.deepseek.com/quick_start/rate_limit)。

日志脱敏参考 OWASP Logging Cheat Sheet，不记录 access token、API key、密码、session id、连接串等敏感材料。参考：[OWASP Logging Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html)。

## 当前仓库现状

Agent Runtime 已有配置：

- `MODEL_PROVIDER=mock|openai-compatible`
- `MODEL_BASE_URL`
- `MODEL_API_KEY`
- `MODEL_NAME`
- `MODEL_TIMEOUT_SECONDS`
- `MODEL_HEALTH_PROBE_ENABLED`
- `MODEL_HEALTH_PROBE_INTERVAL_SECONDS`
- `MODEL_HEALTH_PROBE_TIMEOUT_SECONDS`
- `MODEL_HEALTH_PROBE_INITIAL_DELAY_SECONDS`
- `MODEL_REQUESTS_PER_MINUTE`
- `MODEL_MAX_CONCURRENT_REQUESTS`

`openai-compatible` provider 会校验 base URL、API key、model 非空，并通过 `github.com/cloudwego/eino-ext/components/model/openai` 创建 Eino OpenAI ChatModel。

Runtime engine 已使用 `model.ToolCallingChatModel`，按 run 下发 skills 构造 Eino tools，并用 `guardedToolModel` 阻止模型调用未授权 tool。`/cli ...` 仍保留为开发测试入口。

模型层已经新增运营包装：`OperationalChatModel`、`RetryTransport`、`FallbackChatModel`、错误分类、usage tracker、pricing policy 和 redactor。Runtime 会优先从 Eino message metadata 收集真实 token usage，缺失时回退估算，并通过 `CompleteWithUsage` 回写 Control Plane。Control Plane 已有 `run_usage` 表、repository 和 complete handler 持久化。估算 usage 会设置 `estimated=true`，并记录 `token_estimator`，可为 `tiktoken_o200k_base`、`tiktoken_cl100k_base` 或 `heuristic_rune_div4`，用于区分真实 provider usage 和当前估算。`run_usage` 不只记录模型 token，也会记录 Eino tool observation 聚合出的 tool/sandbox/artifact 用量，包括 tool 调用数、tool 错误数、sandbox 命令数、sandbox 耗时/输出/资源摘要和 artifact 数量/大小。费用计算通过 `MODEL_*_PRICE_PER_1M_TOKENS` 环境变量配置，不在仓库中硬编码实时模型价格；fallback 已支持单一后备 provider，可在 `rate_limited`、`provider_unavailable`、`network_error` 时切换到 `mock` 或另一个 OpenAI-compatible provider。Agent Runtime 的 `/healthz` 已返回 `model_provider` 健康快照，`/metrics` 已暴露 `niceagent_model_runs_total`、`niceagent_model_latency_seconds_*`、`niceagent_model_health_probe_total` 和 `niceagent_model_health_probe_duration_seconds_*`，用于观察 provider/model/status/error_class/fallback/probe。

日志方面，主入口记录 provider/base_url/model，不记录 API key。`modelprovider` 已有 redactor，provider 错误、OpenAI style key、Authorization/Bearer/token/secret/password/cookie 等会被掩码。HTTP Skill observation 也会对 secret 和敏感 key 做 redaction。主动 provider 探针已通过 `MODEL_HEALTH_PROBE_ENABLED` 和间隔/超时配置落地，默认关闭；探针结果会进入 `/healthz` 和 `/metrics`，运维文档给出了基础告警建议。fake DeepSeek/OpenAI-compatible server 已覆盖普通回复、tool calling 响应解析、usage 采集和 401/402/429/503 错误分类。仍待补的是统一覆盖所有 run event、audit、tool raw output 的策略开关、真实 DeepSeek 生产 key 下的 smoke 记录和外部告警系统接入。

K8s 部署已经把 `MODEL_API_KEY` 从 `niceagent-model-provider` Secret 注入，`.env.example` 只放空值，方向正确。

已落地能力：

- Eino 原生 `model.ToolCallingChatModel` 主路径，OpenAI-compatible 通过 `eino-ext` OpenAI adapter 创建。
- `MODEL_PROVIDER_PROFILE=deepseek` 配置预设、API key Secret 注入、mock provider 和 fake DeepSeek/OpenAI-compatible 回归测试。
- provider 错误分类、retry transport、单一 fallback、usage tracker、pricing/cost、redactor、health snapshot、metrics 和可选主动探针。
- Runtime 进程内模型请求速率限制和并发保护。
- `run_usage` 持久化模型 token/cost、估算标记、token estimator、latency 和 tool/sandbox/artifact 聚合用量。

本轮新增了 `scripts/smoke_deepseek_runtime.py` 和 `make smoke-deepseek-runtime`：未设置真实 key 时安全 `SKIP`，设置 `DEEPSEEK_API_KEY`/`DEEPSEEK_MODEL` 后会启动临时 Agent Runtime，使用 `MODEL_PROVIDER_PROFILE=deepseek` 触发一次 `/healthz model_provider` 主动 probe，并把脱敏结果写入 `.local/deepseek-smoke/`。报告已经升级为机器可读验收证据，包含 `schema_version`、`kind=deepseek_runtime_smoke`、`result=passed|failed|skipped`、provider/model、probe 状态、错误分类、延迟和 `checks`；失败路径也会写入脱敏后的日志尾部。详细流程见 `docs/runbooks/deepseek-runtime-smoke.md`。

仍待落地能力：

- 使用真实 DeepSeek API key 执行一次 smoke，并把脱敏结果作为发布验收记录保存在本地或运维系统。
- 更完整 tokenizer 覆盖、跨 Runtime/provider 账号级容量协调、复杂多 provider 路由和外部 SLO 告警系统。
- 覆盖所有 run event、audit、tool raw output 的集中 redaction 策略开关。

## 扩展点

- `modelprovider` 已有 provider wrapper：统一 retry、fallback、usage callback、pricing/cost、health probe、错误分类、redaction、Runtime 进程内请求限流和并发保护。后续继续补跨 Runtime 的 provider 容量协调、供应商账号级限流联动和多 provider 路由。
- `RunCompleteRequest` 和 Control Plane repository 落地 token usage 持久化。
- `run_usage` 表已记录 provider、model、input/output/reasoning/cached tokens、`estimated`、`token_estimator`、latency、cost、currency，以及 run 级 tool/sandbox/artifact 聚合用量。
- 当前先通过环境变量提供轻量 pricing policy；后续如需多模型、多租户成本核算，再增加 `model_pricing` 配置或表，按生效日期维护不同 provider/model 价格。
- 日志和 event 已有局部 `Redactor`，后续继续统一覆盖 prompt、completion、tool raw output、headers、secret key 和 audit payload。
- provider health、错误分类、fallback 记录、主动探针和基础指标已有最小闭环；后续补外部 SLO 告警系统、多 provider 路由指标和真实生产 key 下的 smoke 记录。

## 技术架构

推荐链路：

```text
EinoAgentEngine
  -> ModelProviderWrapper
       -> RateLimiter
       -> RetryPolicy
       -> Eino ToolCallingChatModel
       -> UsageCollector
       -> Redactor
       -> FallbackPolicy
  -> ToolBridge
  -> ControlPlaneSink
  -> RunUsage / RunEvent / Audit
```

错误分类建议：

- `config_error`：缺 API key、base URL、model，不重试。
- `auth_error`：401，不重试。
- `billing_error`：402，不重试。
- `request_error`：400、422，不重试。
- `rate_limited`：429，指数退避，可 fallback。
- `provider_unavailable`：500、503，可退避重试，可 fallback。
- `network_error`：timeout、connection reset，可退避重试。
- `tool_schema_error`：工具 schema 或 tool call 参数错误，不 fallback 到其他模型，先修 skill/model prompt。

## 技术方案

DeepSeek 本地接入示例：

```bash
MODEL_PROVIDER=openai-compatible
MODEL_PROVIDER_PROFILE=deepseek
MODEL_BASE_URL=https://api.deepseek.com
MODEL_API_KEY=sk-...
MODEL_NAME=<以 DeepSeek 官方文档为准>
MODEL_TIMEOUT_SECONDS=120
MODEL_HEALTH_PROBE_ENABLED=true
MODEL_HEALTH_PROBE_INTERVAL_SECONDS=60
MODEL_HEALTH_PROBE_TIMEOUT_SECONDS=10
```

本地不要把 API key 写入仓库。可以放在未提交的 `.env`、shell 环境变量或 Docker Compose override；K8s 使用 `niceagent-model-provider` Secret。

接入检查清单：

- 在 DeepSeek 控制台创建 API key。
- 确认账户余额充足，避免 402。
- 设置 `MODEL_PROVIDER_PROFILE=deepseek`；如果未显式传 `MODEL_BASE_URL`，Runtime 会默认使用 `https://api.deepseek.com`，且不要包含最终 endpoint。
- 使用 DeepSeek 官方文档中的当前模型名，不把旧模型名当作长期默认值。
- 启动三服务后测试普通消息。
- 测试一次 tool calling，例如 `/cli echo hello` 或模型自主调用 `cli.exec`。
- 测试错误 key，确认 401 日志脱敏。
- 测试超时/取消路径，确认不会写成功终态。
- 检查日志、event、前端响应不出现 API key 或 Authorization header。

usage/cost 方案：

- 优先从 Eino callback 或 provider response usage 读取真实 usage。
- mock provider 和缺失 usage 时保留估算 token。
- cost 计算不要写死在 engine，使用 provider/model pricing 配置。
- usage 写入 run-level 聚合，包含模型 token/cost 和 tool/sandbox/artifact 用量；后续用于 quota、账单和排障。

retry/fallback 方案：

- 尊重 `Retry-After`。
- 指数退避 + jitter。
- 限制最大尝试次数和总耗时。
- 只对 429、500、503、network timeout、connection reset 重试。
- 401、402、400、422 不重试。
- fallback 只对 provider 瞬时问题生效，记录 `fallback_from`、`fallback_to`、error_class、retry_count；当前已支持单一后备 provider，复杂多 provider 路由仍后续实现。

日志脱敏方案：

- 默认不记录完整 prompt、completion、tool raw output。
- headers 中的 `Authorization`、`Cookie`、`Set-Cookie` 永远不写日志。
- 对 key 名包含 `api_key`、`authorization`、`bearer`、`token`、`secret`、`password` 的字段做掩码。
- `tool.output` 根据 skill 配置决定是否允许持久化 raw output；默认保存摘要和大小。

## 分阶段落地

1. DeepSeek 冒烟：fake DeepSeek/OpenAI-compatible server 已覆盖普通回复、tool calling、usage 和典型错误分类；真实 key smoke 脚本、机器可读报告和 runbook 已落地，下一步执行真实 key 并沉淀脱敏记录。
2. Usage 持久化：从 Eino callback/provider response 收集 token usage，写入 Control Plane。
3. Retry/rate limit：provider wrapper 已处理 429/5xx/timeout retry，并支持 Runtime 进程内请求限流和并发保护；后续补跨副本容量协调。
4. Fallback：支持多 provider/model 策略和错误分类。
5. 日志脱敏：统一 redactor，覆盖 model、tool、event、audit。
6. 模型运营面板：后端已有 provider health 和基础 metrics；前端/后台仍需展示 latency、token、cost、错误和 fallback。

## 风险与验收

风险：

- API key 被提交到仓库、镜像、日志或前端。
- `MODEL_BASE_URL` 配到最终 endpoint 导致 Eino adapter 拼接错误。
- provider 不完全兼容 OpenAI tool calling。
- DeepSeek reasoning/thinking 参数和 tool calling 组合有 provider-specific 行为。
- token usage 缺失导致 quota/cost 不准。
- 过度 retry 造成成本放大。

验收：

- DeepSeek 普通消息可成功回复。
- DeepSeek tool calling 能执行已授权 `cli.exec` 或用户 HTTP Skill。
- 错误 key 返回 401，日志和 event 不泄露 key。
- 402 不重试，429/503 按策略退避。
- 取消 run 后不写成功终态。
- usage/cost 能按 run 查询，缺失 usage 时明确标记为 estimate。
- 日志默认不包含 prompt、completion、Authorization header、API key、tool secret。

## 参考资料

- [Eino ChatModel 指南](https://www.cloudwego.io/zh/docs/eino/core_modules/components/chat_model_guide/)
- [Eino ADK ChatModelAgent](https://www.cloudwego.io/docs/eino/core_modules/eino_adk/agent_implementation/chat_model/)
- [DeepSeek API Docs](https://api-docs.deepseek.com/)
- [DeepSeek Function Calling](https://api-docs.deepseek.com/guides/function_calling)
- [DeepSeek Rate Limit & Isolation](https://api-docs.deepseek.com/quick_start/rate_limit)
- [DeepSeek Error Codes](https://api-docs.deepseek.com/quick_start/error_codes)
- [OWASP Logging Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html)
