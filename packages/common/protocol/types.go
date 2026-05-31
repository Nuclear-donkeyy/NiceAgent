package protocol

import "time"

type User struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type Organization struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type Project struct {
	ID             string    `json:"id"`
	OrganizationID string    `json:"organization_id"`
	Name           string    `json:"name"`
	CreatedAt      time.Time `json:"created_at"`
}

type ChatSession struct {
	ID           string    `json:"id"`
	UserID       string    `json:"user_id"`
	ProjectID    string    `json:"project_id"`
	Title        string    `json:"title"`
	Archived     bool      `json:"archived"`
	LastRunID    string    `json:"last_run_id,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	MessageCount int       `json:"message_count"`
}

type MessageRole string

const (
	RoleSystem    MessageRole = "system"
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleTool      MessageRole = "tool"
)

type Message struct {
	ID        string      `json:"id"`
	ChatID    string      `json:"chat_id"`
	RunID     string      `json:"run_id,omitempty"`
	Role      MessageRole `json:"role"`
	Content   string      `json:"content"`
	CreatedAt time.Time   `json:"created_at"`
}

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

type RunEventType string

const (
	EventRunQueued       RunEventType = "run.queued"
	EventRunStarted      RunEventType = "run.started"
	EventModelToken      RunEventType = "model.token"
	EventToolStarted     RunEventType = "tool.started"
	EventToolOutput      RunEventType = "tool.output"
	EventToolFinished    RunEventType = "tool.finished"
	EventApprovalNeeded  RunEventType = "approval.needed"
	EventArtifactCreated RunEventType = "artifact.created"
	EventRunSucceeded    RunEventType = "run.succeeded"
	EventRunFailed       RunEventType = "run.failed"
	EventRunCanceled     RunEventType = "run.canceled"
)

type RunEvent struct {
	ID        string       `json:"id"`
	RunID     string       `json:"run_id"`
	ChatID    string       `json:"chat_id"`
	Seq       int64        `json:"seq"`
	Type      RunEventType `json:"type"`
	Message   string       `json:"message,omitempty"`
	Payload   any          `json:"payload,omitempty"`
	CreatedAt time.Time    `json:"created_at"`
}

type SkillRisk string

const (
	SkillRiskLow    SkillRisk = "low"
	SkillRiskMedium SkillRisk = "medium"
	SkillRiskHigh   SkillRisk = "high"
)

type Skill struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Version      string    `json:"version"`
	Description  string    `json:"description"`
	Risk         SkillRisk `json:"risk"`
	RequiresAuth bool      `json:"requires_auth"`
	InputSchema  string    `json:"input_schema,omitempty"`
	OutputSchema string    `json:"output_schema,omitempty"`
}

type Workspace struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	RootPath  string    `json:"root_path"`
	CreatedAt time.Time `json:"created_at"`
}

type ModelProvider struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	BaseURL   string `json:"base_url,omitempty"`
	Model     string `json:"model"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at,omitempty"`
}

type RunRequest struct {
	RunID       string   `json:"run_id"`
	ChatID      string   `json:"chat_id"`
	UserID      string   `json:"user_id"`
	WorkspaceID string   `json:"workspace_id"`
	SkillIDs    []string `json:"skill_ids"`
	ModelPolicy string   `json:"model_policy"`
}

type RunExecutionRequest struct {
	Request         RunRequest `json:"request"`
	UserMessage     string     `json:"user_message"`
	ControlPlaneURL string     `json:"control_plane_url,omitempty"`
}

type RunEventWriteRequest struct {
	Type    RunEventType `json:"type"`
	Message string       `json:"message,omitempty"`
	Payload any          `json:"payload,omitempty"`
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

type SkillInvocation struct {
	ID             string     `json:"id"`
	RunID          string     `json:"run_id"`
	SkillID        string     `json:"skill_id"`
	SkillVersion   string     `json:"skill_version"`
	Input          any        `json:"input"`
	TimeoutSeconds int        `json:"timeout_seconds"`
	ApprovalID     string     `json:"approval_id,omitempty"`
	StartedAt      time.Time  `json:"started_at"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
	Status         string     `json:"status"`
	Output         any        `json:"output,omitempty"`
	Error          string     `json:"error,omitempty"`
}

type Artifact struct {
	ID        string    `json:"id"`
	RunID     string    `json:"run_id"`
	Path      string    `json:"path"`
	MimeType  string    `json:"mime_type"`
	SizeBytes int64     `json:"size_bytes"`
	CreatedAt time.Time `json:"created_at"`
}

type SandboxCommand struct {
	RunID          string            `json:"run_id"`
	WorkspaceID    string            `json:"workspace_id"`
	Command        []string          `json:"command"`
	Env            map[string]string `json:"env,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds"`
	Network        bool              `json:"network"`
	MaxOutputBytes int               `json:"max_output_bytes,omitempty"`
	WorkspaceRoot  string            `json:"workspace_root,omitempty"`
}

type SandboxResult struct {
	RunID            string   `json:"run_id"`
	ExitCode         int      `json:"exit_code"`
	Stdout           string   `json:"stdout"`
	Stderr           string   `json:"stderr"`
	Duration         string   `json:"duration"`
	Truncated        bool     `json:"truncated"`
	ApprovalRequired bool     `json:"approval_required"`
	Reason           string   `json:"reason,omitempty"`
	Policy           string   `json:"policy,omitempty"`
	Command          []string `json:"command,omitempty"`
	Error            string   `json:"error,omitempty"`
}
