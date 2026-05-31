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
