package app

import (
	"context"

	"niceagent/common/protocol"
)

const (
	DemoUserID    = "demo-user"
	DemoProjectID = "demo-project"
	DemoOrgID     = "demo-org"
)

type ActorContext struct {
	UserID    string
	ProjectID string
	OrgID     string
	Roles     []string
}

func DemoActor() ActorContext {
	return ActorContext{
		UserID:    DemoUserID,
		ProjectID: DemoProjectID,
		OrgID:     DemoOrgID,
		Roles:     []string{"owner"},
	}
}

type ChatListOptions struct {
	Query           string
	IncludeArchived bool
}

type AuditEventListOptions struct {
	Limit      int
	RequestID  string
	RunID      string
	Action     string
	ResourceID string
}

type Repository interface {
	ListChats(userID, projectID string, opts ChatListOptions) []protocol.ChatSession
	CreateChat(userID, projectID, title string) (protocol.ChatSession, error)
	GetChat(chatID string) (protocol.ChatSession, []protocol.Message, error)
	SetChatArchived(chatID, userID string, archived bool) (protocol.ChatSession, error)
	AddUserMessage(chatID, userID, content string) (protocol.Message, protocol.Run, error)
	AddAssistantMessage(chatID, runID, content string) (protocol.Message, error)
	GetRun(runID string) (protocol.Run, error)
	UpdateRunStatus(runID string, status protocol.RunStatus, errMessage string) (protocol.Run, error)
	SaveRunUsage(runID string, usage protocol.RunUsage) (protocol.RunUsage, error)
	GetRunUsage(runID string) (protocol.RunUsage, error)
	AddEvent(runID string, typ protocol.RunEventType, message string, payload any) (protocol.RunEvent, error)
	ListEvents(runID string, afterSeq int64) []protocol.RunEvent
	Subscribe(runID string) (<-chan protocol.RunEvent, func())
	AddWorkspace(workspace protocol.Workspace) (protocol.Workspace, error)
	GetWorkspace(workspaceID string) (protocol.Workspace, error)
	AddArtifact(artifact protocol.Artifact) (protocol.Artifact, error)
	ListArtifacts(runID string) []protocol.Artifact
	GetArtifact(artifactID string) (protocol.Artifact, error)
	ListSkillsForUser(userID, projectID string) []protocol.Skill
	ListRuntimeSkillsForUser(userID, projectID string) []protocol.RuntimeSkill
	CreateHTTPSkill(userID, projectID string, input protocol.HTTPSkillInput) (protocol.Skill, error)
	UpdateHTTPSkill(userID, skillID string, input protocol.HTTPSkillInput) (protocol.Skill, error)
	SetSkillEnabled(userID, skillID string, enabled bool) (protocol.Skill, error)
	AddAuditEvent(input protocol.AuditEventInput) (protocol.AuditEvent, error)
	ListAuditEvents(actor ActorContext, opts AuditEventListOptions) []protocol.AuditEvent
}

type RunDispatcher interface {
	Dispatch(ctx context.Context, run protocol.Run, userMessage string) error
}
