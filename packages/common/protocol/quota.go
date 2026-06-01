package protocol

import "time"

type ProjectQuotaPolicy struct {
	ProjectID               string    `json:"project_id"`
	MaxConcurrentRuns       int       `json:"max_concurrent_runs"`
	MaxRunsPerHour          int       `json:"max_runs_per_hour"`
	MaxModelTokensPerDay    int       `json:"max_model_tokens_per_day"`
	MaxToolCallsPerDay      int       `json:"max_tool_calls_per_day"`
	MaxSandboxSecondsPerDay int       `json:"max_sandbox_seconds_per_day"`
	CreatedAt               time.Time `json:"created_at"`
	UpdatedAt               time.Time `json:"updated_at"`
}

type ProjectQuotaPolicyInput struct {
	MaxConcurrentRuns       int `json:"max_concurrent_runs"`
	MaxRunsPerHour          int `json:"max_runs_per_hour"`
	MaxModelTokensPerDay    int `json:"max_model_tokens_per_day"`
	MaxToolCallsPerDay      int `json:"max_tool_calls_per_day"`
	MaxSandboxSecondsPerDay int `json:"max_sandbox_seconds_per_day"`
}

type ProjectQuotaPolicyResponse struct {
	Policy ProjectQuotaPolicy `json:"policy"`
}

type RunUsageBucket struct {
	Provider              string  `json:"provider,omitempty"`
	Model                 string  `json:"model,omitempty"`
	Currency              string  `json:"currency,omitempty"`
	Estimated             bool    `json:"estimated,omitempty"`
	TokenEstimator        string  `json:"token_estimator,omitempty"`
	RunCount              int     `json:"run_count"`
	InputTokens           int     `json:"input_tokens"`
	OutputTokens          int     `json:"output_tokens"`
	ReasoningTokens       int     `json:"reasoning_tokens"`
	CachedTokens          int     `json:"cached_tokens"`
	TotalTokens           int     `json:"total_tokens"`
	Cost                  float64 `json:"cost"`
	LatencyMillis         int64   `json:"latency_millis"`
	RetryCount            int     `json:"retry_count"`
	ToolCalls             int     `json:"tool_calls"`
	ToolErrors            int     `json:"tool_errors"`
	SandboxCommands       int     `json:"sandbox_commands"`
	SandboxDurationMillis int64   `json:"sandbox_duration_millis"`
	SandboxOutputBytes    int     `json:"sandbox_output_bytes"`
	SandboxCPUMillis      int64   `json:"sandbox_cpu_millis"`
	SandboxMemoryMaxBytes int64   `json:"sandbox_memory_max_bytes"`
	ArtifactCount         int     `json:"artifact_count"`
	ArtifactBytes         int64   `json:"artifact_bytes"`
}

type ProjectUsageResponse struct {
	ProjectID string           `json:"project_id"`
	Window    string           `json:"window"`
	Since     time.Time        `json:"since"`
	Buckets   []RunUsageBucket `json:"buckets"`
	Total     RunUsageBucket   `json:"total"`
}

type ToolQuotaReserveRequest struct {
	AttemptID      string `json:"attempt_id,omitempty"`
	SkillID        string `json:"skill_id"`
	ToolCalls      int    `json:"tool_calls,omitempty"`
	SandboxSeconds int    `json:"sandbox_seconds,omitempty"`
}

type ToolQuotaReserveResponse struct {
	Allowed bool     `json:"allowed"`
	Message string   `json:"message,omitempty"`
	Quota   string   `json:"quota,omitempty"`
	Usage   RunUsage `json:"usage,omitempty"`
}
