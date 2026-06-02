export type SkillRiskPolicy = "allow" | "block-high" | "block-destructive" | "read-only";

export interface ProjectQuotaPolicy {
  project_id: string;
  max_concurrent_runs: number;
  max_runs_per_hour: number;
  max_model_tokens_per_day: number;
  max_tool_calls_per_day: number;
  max_sandbox_seconds_per_day: number;
  created_at?: string;
  updated_at?: string;
}

export interface ProjectQuotaPolicyResponse {
  policy: ProjectQuotaPolicy;
}

export interface ProjectRuntimePolicy {
  project_id: string;
  skill_risk_policy: SkillRiskPolicy;
  created_at?: string;
  updated_at?: string;
}

export interface ProjectRuntimePolicyInput {
  skill_risk_policy: SkillRiskPolicy;
}

export interface ProjectRuntimePolicyResponse {
  policy: ProjectRuntimePolicy;
}

export interface ProjectUsageBucket {
  provider?: string;
  model?: string;
  currency?: string;
  estimated?: boolean;
  token_estimator?: string;
  run_count: number;
  input_tokens: number;
  output_tokens: number;
  reasoning_tokens: number;
  cached_tokens: number;
  total_tokens: number;
  cost: number;
  latency_millis: number;
  retry_count: number;
  tool_calls: number;
  tool_errors: number;
  sandbox_commands: number;
  sandbox_duration_millis: number;
  sandbox_output_bytes: number;
  sandbox_cpu_millis: number;
  sandbox_memory_max_bytes: number;
  artifact_count: number;
  artifact_bytes: number;
}

export interface ProjectUsageResponse {
  project_id: string;
  window: string;
  since: string;
  buckets: ProjectUsageBucket[];
  total: ProjectUsageBucket;
}
