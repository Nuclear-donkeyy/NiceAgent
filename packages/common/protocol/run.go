package protocol

import "time"

type RunStatus string

const (
	RunQueued             RunStatus = "queued"
	RunRunning            RunStatus = "running"
	RunWaitingForApproval RunStatus = "waiting_for_approval"
	RunSucceeded          RunStatus = "succeeded"
	RunFailed             RunStatus = "failed"
	RunCanceled           RunStatus = "canceled"
)

type Run struct {
	ID          string     `json:"id"`
	ChatID      string     `json:"chat_id"`
	UserID      string     `json:"user_id"`
	WorkspaceID string     `json:"workspace_id"`
	AttemptID   string     `json:"attempt_id,omitempty"`
	Status      RunStatus  `json:"status"`
	Error       string     `json:"error,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
}

type RunRequest struct {
	RunID       string         `json:"run_id"`
	ChatID      string         `json:"chat_id"`
	UserID      string         `json:"user_id"`
	WorkspaceID string         `json:"workspace_id"`
	AttemptID   string         `json:"attempt_id,omitempty"`
	SkillIDs    []string       `json:"skill_ids"`
	Skills      []RuntimeSkill `json:"skills,omitempty"`
	ModelPolicy string         `json:"model_policy"`
}

type RunExecutionRequest struct {
	Request         RunRequest `json:"request"`
	UserMessage     string     `json:"user_message"`
	ControlPlaneURL string     `json:"control_plane_url,omitempty"`
}

type RunCompleteRequest struct {
	Content    string     `json:"content"`
	TokenUsage TokenUsage `json:"token_usage,omitempty"`
	Usage      RunUsage   `json:"usage,omitempty"`
	AttemptID  string     `json:"attempt_id,omitempty"`
	Artifacts  []Artifact `json:"artifacts,omitempty"`
}

type RunFailRequest struct {
	Error     string `json:"error"`
	AttemptID string `json:"attempt_id,omitempty"`
}

type RunStatusResponse struct {
	Run Run `json:"run"`
}

type RunResult struct {
	RunID      string     `json:"run_id"`
	Status     RunStatus  `json:"status"`
	MessageID  string     `json:"message_id,omitempty"`
	Artifacts  []Artifact `json:"artifacts,omitempty"`
	TokenUsage TokenUsage `json:"token_usage"`
	Usage      RunUsage   `json:"usage,omitempty"`
	Error      string     `json:"error,omitempty"`
}

type TokenUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type RunUsage struct {
	Provider        string  `json:"provider,omitempty"`
	Model           string  `json:"model,omitempty"`
	InputTokens     int     `json:"input_tokens,omitempty"`
	OutputTokens    int     `json:"output_tokens,omitempty"`
	ReasoningTokens int     `json:"reasoning_tokens,omitempty"`
	CachedTokens    int     `json:"cached_tokens,omitempty"`
	TotalTokens     int     `json:"total_tokens,omitempty"`
	Estimated       bool    `json:"estimated,omitempty"`
	Cost            float64 `json:"cost,omitempty"`
	Currency        string  `json:"currency,omitempty"`
	LatencyMillis   int64   `json:"latency_millis,omitempty"`
	RetryCount      int     `json:"retry_count,omitempty"`
	FallbackFrom    string  `json:"fallback_from,omitempty"`
	FallbackTo      string  `json:"fallback_to,omitempty"`
	ErrorClass      string  `json:"error_class,omitempty"`
}
