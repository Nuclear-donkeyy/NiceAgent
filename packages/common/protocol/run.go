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
	Artifacts  []Artifact `json:"artifacts,omitempty"`
}

type RunFailRequest struct {
	Error string `json:"error"`
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
	Error      string     `json:"error,omitempty"`
}

type TokenUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}
