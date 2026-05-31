package protocol

import "time"

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

type RunEventWriteRequest struct {
	Type    RunEventType `json:"type"`
	Message string       `json:"message,omitempty"`
	Payload any          `json:"payload,omitempty"`
}
