import { SectionTitle } from "../../components/SectionTitle";
import type {
  ProjectQuotaPolicy,
  ProjectUsageBucket,
  ProjectUsageResponse,
} from "../../domain/project";
import styles from "./CapacityPanel.module.scss";

interface CapacityPanelProps {
  error: string;
  loading: boolean;
  policy: ProjectQuotaPolicy;
  usage: ProjectUsageResponse | null;
  onRefresh: () => void;
}

interface CapacityMetric {
  key: string;
  label: string;
  used: number;
  limit: number;
  unit?: string;
}

export function CapacityPanel({ error, loading, policy, usage, onRefresh }: CapacityPanelProps) {
  const total = usage?.total;
  const metrics = buildMetrics(policy, total);
  const topBuckets = (usage?.buckets || []).slice(0, 3);

  return (
    <section className={styles.panel} aria-label="项目容量">
      <div className={styles.titleRow}>
        <SectionTitle text="项目容量" />
        <button
          className={styles.refreshButton}
          onClick={onRefresh}
          disabled={loading}
          type="button"
        >
          {loading ? "同步中" : "刷新"}
        </button>
      </div>

      {error && <p className={styles.error}>容量数据加载失败：{error}</p>}

      <div className={styles.metricList}>
        {metrics.map((metric) => (
          <CapacityBar key={metric.key} metric={metric} />
        ))}
      </div>

      <div className={styles.summaryGrid}>
        <SummaryCell label="24h Runs" value={formatNumber(total?.run_count || 0)} />
        <SummaryCell label="Tool Errors" value={formatNumber(total?.tool_errors || 0)} />
        <SummaryCell label="Artifacts" value={formatBytes(total?.artifact_bytes || 0)} />
      </div>

      {topBuckets.length > 0 && (
        <div className={styles.bucketList}>
          {topBuckets.map((bucket, index) => (
            <div className={styles.bucket} key={bucketKey(bucket, index)}>
              <strong>{bucket.model || bucket.provider || "unknown model"}</strong>
              <span>
                {formatNumber(bucket.total_tokens)} tokens / {formatNumber(bucket.run_count)} runs
              </span>
            </div>
          ))}
        </div>
      )}
    </section>
  );
}

function CapacityBar({ metric }: { metric: CapacityMetric }) {
  const ratio = metric.limit > 0 ? Math.min(metric.used / metric.limit, 1) : 0;
  const percent = metric.limit > 0 ? Math.round(ratio * 100) : 0;

  return (
    <div className={styles.metric}>
      <div className={styles.metricHead}>
        <span>{metric.label}</span>
        <strong>
          {formatNumber(metric.used)}
          {metric.limit > 0 ? ` / ${formatNumber(metric.limit)}` : " / 未限制"}
          {metric.unit ? ` ${metric.unit}` : ""}
        </strong>
      </div>
      <div className={styles.track} aria-hidden="true">
        <div className={styles.fill} style={{ width: `${percent}%` }} />
      </div>
    </div>
  );
}

function SummaryCell({ label, value }: { label: string; value: string }) {
  return (
    <div className={styles.summaryCell}>
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

function buildMetrics(
  policy: ProjectQuotaPolicy,
  total: ProjectUsageBucket | undefined,
): CapacityMetric[] {
  return [
    {
      key: "model_tokens",
      label: "模型 Token / 天",
      used: total?.total_tokens || 0,
      limit: policy.max_model_tokens_per_day || 0,
    },
    {
      key: "tool_calls",
      label: "Tool Calls / 天",
      used: total?.tool_calls || 0,
      limit: policy.max_tool_calls_per_day || 0,
    },
    {
      key: "sandbox_seconds",
      label: "Sandbox 秒数 / 天",
      used: Math.ceil((total?.sandbox_duration_millis || 0) / 1000),
      limit: policy.max_sandbox_seconds_per_day || 0,
      unit: "s",
    },
    {
      key: "runs_per_hour",
      label: "Runs / 小时上限",
      used: 0,
      limit: policy.max_runs_per_hour || 0,
    },
    {
      key: "concurrent_runs",
      label: "并发 Run 上限",
      used: 0,
      limit: policy.max_concurrent_runs || 0,
    },
  ];
}

function bucketKey(bucket: ProjectUsageBucket, index: number): string {
  return [bucket.provider, bucket.model, bucket.currency, bucket.estimated, index].join(":");
}

function formatNumber(value: number): string {
  return new Intl.NumberFormat("zh-CN").format(value);
}

function formatBytes(value: number): string {
  if (value <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB"];
  let current = value;
  let index = 0;
  while (current >= 1024 && index < units.length - 1) {
    current /= 1024;
    index += 1;
  }
  return `${current.toFixed(index === 0 ? 0 : 1)} ${units[index]}`;
}
