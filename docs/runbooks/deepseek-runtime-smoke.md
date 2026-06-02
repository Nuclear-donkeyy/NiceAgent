# DeepSeek Runtime 冒烟 Runbook

本文记录如何用真实 DeepSeek API key 验证 NiceAgent Agent Runtime 的 OpenAI-compatible + Eino `ToolCallingChatModel` 路径。该流程不会提交密钥，默认只把脱敏后的结果写入 `.local/deepseek-smoke/`。

## 适用场景

- 首次接入 DeepSeek API key。
- 更换 `MODEL_NAME`、provider profile、价格配置或限流配置后做上线前检查。
- 排查 Runtime `/healthz` 中 `model_provider` 状态异常。

## 前置条件

- 本机 Go 工具链可用。
- 已从 DeepSeek 控制台拿到 API key。
- 已确认当前要使用的模型名，以 DeepSeek 官方文档或控制台为准，不在仓库中硬编码。

## 执行命令

```bash
export DEEPSEEK_API_KEY="sk-..."
export DEEPSEEK_MODEL="<以 DeepSeek 官方文档为准的模型名>"

make smoke-deepseek-runtime
```

也可以用通用模型环境变量：

```bash
export MODEL_API_KEY="sk-..."
export MODEL_NAME="<以 DeepSeek 官方文档为准的模型名>"

python3 scripts/smoke_deepseek_runtime.py
```

没有设置 `DEEPSEEK_API_KEY`/`MODEL_API_KEY` 或 `DEEPSEEK_MODEL`/`MODEL_NAME` 时，脚本会输出 `SKIP` 并返回成功，避免 CI 或本地检查误触发真实模型调用。如果希望在缺少 key 时失败，可加：

```bash
python3 scripts/smoke_deepseek_runtime.py --require-key
```

## 验证内容

脚本会启动一个临时 Agent Runtime，并注入：

- `MODEL_PROVIDER=openai-compatible`
- `MODEL_PROVIDER_PROFILE=deepseek`
- `MODEL_HEALTH_PROBE_ENABLED=true`
- `MODEL_HEALTH_PROBE_INITIAL_DELAY_SECONDS=0`
- `MODEL_HEALTH_PROBE_INTERVAL_SECONDS=3600`

然后轮询 Runtime `GET /healthz`，等待 `model_provider.probe_count > 0`。成功时应看到：

- `provider=deepseek`
- `model=<当前模型名>`
- `status=healthy`
- `probe_status=healthy`
- `probe_success >= 1`
- `last_error_class` 为空或 `none`

## 结果记录

默认报告路径：

```text
.local/deepseek-smoke/<timestamp>.json
```

`.local/` 已被 `.gitignore` 忽略。报告只包含 provider、model、健康状态、probe 次数、错误分类、延迟和机器可读验收字段，不包含 API key、Authorization header、prompt 或 completion。

报告核心字段：

- `schema_version`：当前为 `1`。
- `kind`：固定为 `deepseek_runtime_smoke`。
- `result`：`passed`、`failed` 或 `skipped`。
- `checks.provider_is_deepseek`：确认 Runtime 使用 DeepSeek profile。
- `checks.status_is_healthy`：确认 `/healthz model_provider.status=healthy`。
- `checks.probe_status_is_healthy`：确认主动探针健康。
- `checks.probe_succeeded`：确认至少一次探针成功。
- `checks.api_key_redacted`：脚本已对报告和失败日志尾部做密钥脱敏。

当 Runtime 已启动但探针返回非健康状态时，脚本会写入 `result=failed` 报告。当 Runtime 无法启动、超时或其他异常发生时，脚本也会写入失败报告，并只保留脱敏后的日志尾部。未设置 key 或模型名时默认只输出 `SKIP`；如果显式传入 `--report`，会写入 `result=skipped` 报告。

可以显式指定报告路径：

```bash
python3 scripts/smoke_deepseek_runtime.py --report .local/deepseek-smoke/manual.json
```

## 失败排查

- `auth_error`：检查 API key 是否有效，是否使用了错误的环境变量。
- `billing_error`：检查 DeepSeek 账户余额或套餐状态。
- `request_error`：检查 `DEEPSEEK_MODEL`/`MODEL_NAME` 是否是当前官方支持的模型名。
- `rate_limited`：降低探针频率，或检查账号级限流。
- `provider_unavailable` / `network_error`：检查网络、代理、防火墙和 DeepSeek 服务状态。

脚本会对运行日志做 API key 替换脱敏；如果需要贴日志排查，仍建议先人工确认没有密钥、token、cookie 或完整业务 prompt。

## 后续改进

- 把真实 smoke 结果纳入发布 checklist，而不是默认 CI。
- 接入外部告警系统后，把 `niceagent_model_health_probe_total{status="error"}` 和 `/healthz model_provider` 状态接入值班路由。
- 补跨 Runtime/provider 账号级容量协调和更细的多 provider 路由策略。
