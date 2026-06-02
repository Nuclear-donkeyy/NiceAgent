package app

import (
	"context"
	"time"

	"niceagent/common/protocol"
)

const (
	DemoUserID    = "demo-user"
	DemoProjectID = "demo-project"
	DemoOrgID     = "demo-org"
)

type ActorContext struct {
	UserID           string
	Email            string
	Name             string
	IdentityProvider string
	IdentityIssuer   string
	IdentitySubject  string
	ProjectID        string
	OrgID            string
	Roles            []string
}

func DemoActor() ActorContext {
	return ActorContext{
		UserID:    DemoUserID,
		Email:     "demo@niceagent.local",
		Name:      "Demo User",
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

type SkillInvocationListOptions struct {
	Limit   int
	RunID   string
	SkillID string
	Status  string
}

type OIDCBrowserSession struct {
	ID               string
	UserID           string
	ProjectID        string
	RefreshTokenHash string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	ExpiresAt        time.Time
	RevokedAt        *time.Time
}

type InvitationEmailEventListOptions struct {
	InvitationID string
	DeliveryID   string
	Limit        int
}

type Repository interface {
	ListChats(userID, projectID string, opts ChatListOptions) []protocol.ChatSession
	CreateChat(userID, projectID, title string) (protocol.ChatSession, error)
	GetChat(chatID string) (protocol.ChatSession, []protocol.Message, error)
	SetChatArchived(chatID, userID string, archived bool) (protocol.ChatSession, error)
	AddUserMessage(chatID, userID, content string) (protocol.Message, protocol.Run, error)
	AddAssistantMessage(chatID, runID, content string) (protocol.Message, error)
	GetRun(runID string) (protocol.Run, error)
	CountActiveRuns(userID, projectID string) int
	CountRunsCreatedSince(userID, projectID string, since time.Time) int
	SumRunUsageTokensSince(userID, projectID string, since time.Time) int
	SumRunUsageSince(userID, projectID string, since time.Time) protocol.RunUsage
	ListRunUsageBucketsSince(projectID string, since time.Time) []protocol.RunUsageBucket
	ProjectBelongsToOrganization(projectID, orgID string) bool
	BindUserIdentity(identity protocol.UserIdentity) (protocol.UserIdentity, error)
	UpsertOIDCBrowserSession(session OIDCBrowserSession) error
	RevokeOIDCBrowserSession(sessionID string, revokedAt time.Time) error
	IsOIDCBrowserSessionActive(sessionID, userID string, now time.Time) bool
	ListOrganizationRoles(userID, orgID string) []string
	ListProjectRoles(userID, projectID string) []string
	ListOrganizationMembers(orgID string) []protocol.OrganizationMember
	UpsertOrganizationMember(orgID string, input protocol.OrganizationMemberInput) (protocol.OrganizationMember, error)
	RemoveOrganizationMember(orgID, userID string) (protocol.OrganizationMember, error)
	ListInvitations(orgID string) []protocol.Invitation
	CreateInvitation(orgID, invitedByUserID string, input protocol.InvitationInput) (protocol.Invitation, error)
	AcceptInvitation(token, userID, email, name string) (protocol.Invitation, error)
	EnqueueInvitationEmail(invitation protocol.Invitation, maxAttempts int) (protocol.InvitationEmailDelivery, error)
	RequeueInvitationEmail(orgID, invitationID string, maxAttempts int) (protocol.InvitationEmailDelivery, error)
	ClaimDueInvitationEmails(limit int, lockedBy string, lockUntil time.Time) []protocol.InvitationEmailDelivery
	MarkInvitationEmailSent(deliveryID string) error
	MarkInvitationEmailFailed(deliveryID, lastError string, nextAttemptAt *time.Time, terminal bool) error
	RecordInvitationEmailEvent(input protocol.InvitationEmailEventInput) (protocol.InvitationEmailEvent, error)
	ListInvitationEmailEvents(orgID string, opts InvitationEmailEventListOptions) []protocol.InvitationEmailEvent
	IsInvitationEmailSuppressed(orgID, email string) bool
	ListInvitationEmailSuppressions(orgID string, limit int) []protocol.InvitationEmailSuppression
	DeleteInvitationEmailSuppression(orgID, suppressionID string) (protocol.InvitationEmailSuppression, error)
	ListProjectMembers(projectID string) []protocol.ProjectMember
	UpsertProjectMember(projectID string, input protocol.ProjectMemberInput) (protocol.ProjectMember, error)
	RemoveProjectMember(projectID, userID string) (protocol.ProjectMember, error)
	GetProjectQuotaPolicy(projectID string) (protocol.ProjectQuotaPolicy, bool)
	SetProjectQuotaPolicy(projectID string, input protocol.ProjectQuotaPolicyInput) (protocol.ProjectQuotaPolicy, error)
	GetProjectRuntimePolicy(projectID string) (protocol.ProjectRuntimePolicy, bool)
	SetProjectRuntimePolicy(projectID string, input protocol.ProjectRuntimePolicyInput) (protocol.ProjectRuntimePolicy, error)
	ClaimRunAttempt(runID, attemptID, claimedBy string, leaseExpiresAt time.Time) (protocol.Run, error)
	CheckRunAttempt(runID, attemptID string) (protocol.Run, error)
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
	DeleteExpiredArtifacts(now time.Time, limit int) ([]protocol.Artifact, error)
	ListSkillsForUser(userID, projectID string) []protocol.Skill
	ListRuntimeSkillsForUser(userID, projectID string) []protocol.RuntimeSkill
	CreateHTTPSkill(userID, projectID string, input protocol.HTTPSkillInput) (protocol.Skill, error)
	CreateMCPSkill(userID, projectID string, input protocol.MCPImportCreateInput) (protocol.Skill, error)
	UpdateHTTPSkill(userID, skillID string, input protocol.HTTPSkillInput) (protocol.Skill, error)
	SetSkillEnabled(userID, skillID string, enabled bool) (protocol.Skill, error)
	RecordSkillInvocation(input protocol.SkillInvocationRecordInput) (protocol.SkillInvocationRecord, error)
	ListSkillInvocations(actor ActorContext, opts SkillInvocationListOptions) []protocol.SkillInvocationRecord
	AddAuditEvent(input protocol.AuditEventInput) (protocol.AuditEvent, error)
	ListAuditEvents(actor ActorContext, opts AuditEventListOptions) []protocol.AuditEvent
}

type RunDispatcher interface {
	Dispatch(ctx context.Context, run protocol.Run, userMessage string) error
}

type InvitationMailer interface {
	SendInvitation(ctx context.Context, invitation protocol.Invitation) error
}
