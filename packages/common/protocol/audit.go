package protocol

import "time"

type AuditDecision string

const (
	AuditDecisionAllow AuditDecision = "allow"
	AuditDecisionDeny  AuditDecision = "deny"
)

type AuditEvent struct {
	ID             string         `json:"id"`
	ActorUserID    string         `json:"actor_user_id,omitempty"`
	ActorProjectID string         `json:"actor_project_id,omitempty"`
	ActorOrgID     string         `json:"actor_org_id,omitempty"`
	Action         string         `json:"action"`
	ResourceType   string         `json:"resource_type"`
	ResourceID     string         `json:"resource_id,omitempty"`
	Decision       AuditDecision  `json:"decision"`
	Reason         string         `json:"reason,omitempty"`
	RequestID      string         `json:"request_id,omitempty"`
	TraceID        string         `json:"trace_id,omitempty"`
	RunID          string         `json:"run_id,omitempty"`
	IP             string         `json:"ip,omitempty"`
	UserAgent      string         `json:"user_agent,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
}

type AuditEventInput struct {
	ActorUserID    string
	ActorProjectID string
	ActorOrgID     string
	Action         string
	ResourceType   string
	ResourceID     string
	Decision       AuditDecision
	Reason         string
	RequestID      string
	TraceID        string
	RunID          string
	IP             string
	UserAgent      string
	Metadata       map[string]any
}

type AuditEventsResponse struct {
	Events []AuditEvent `json:"events"`
}
