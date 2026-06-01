package protocol

import "time"

type SkillInvocationStatus string

const (
	SkillInvocationStarted   SkillInvocationStatus = "started"
	SkillInvocationSucceeded SkillInvocationStatus = "succeeded"
	SkillInvocationFailed    SkillInvocationStatus = "failed"
)

type SkillInvocationRecord struct {
	ID               string                `json:"id"`
	RunID            string                `json:"run_id"`
	ChatID           string                `json:"chat_id,omitempty"`
	UserID           string                `json:"user_id,omitempty"`
	ProjectID        string                `json:"project_id,omitempty"`
	SkillID          string                `json:"skill_id"`
	ToolName         string                `json:"tool_name,omitempty"`
	Status           SkillInvocationStatus `json:"status"`
	Decision         AuditDecision         `json:"decision"`
	Reason           string                `json:"reason,omitempty"`
	RequestID        string                `json:"request_id,omitempty"`
	TraceID          string                `json:"trace_id,omitempty"`
	StartedEventID   string                `json:"started_event_id,omitempty"`
	StartedEventSeq  int64                 `json:"started_event_seq,omitempty"`
	FinishedEventID  string                `json:"finished_event_id,omitempty"`
	FinishedEventSeq int64                 `json:"finished_event_seq,omitempty"`
	DurationMs       int64                 `json:"duration_ms,omitempty"`
	Metadata         map[string]any        `json:"metadata,omitempty"`
	StartedAt        time.Time             `json:"started_at"`
	FinishedAt       *time.Time            `json:"finished_at,omitempty"`
	UpdatedAt        time.Time             `json:"updated_at"`
}

type SkillInvocationRecordInput struct {
	RunID            string
	ChatID           string
	UserID           string
	ProjectID        string
	SkillID          string
	ToolName         string
	Status           SkillInvocationStatus
	Decision         AuditDecision
	Reason           string
	RequestID        string
	TraceID          string
	StartedEventID   string
	StartedEventSeq  int64
	FinishedEventID  string
	FinishedEventSeq int64
	DurationMs       int64
	Metadata         map[string]any
	EventCreatedAt   time.Time
}

type SkillInvocationsResponse struct {
	Invocations []SkillInvocationRecord `json:"invocations"`
}
