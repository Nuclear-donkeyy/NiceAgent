package controlplane

import "niceagent/common/protocol"

type ChatListOptions struct {
	Query           string
	IncludeArchived bool
}

type Repository interface {
	ListChats(userID string, opts ChatListOptions) []protocol.ChatSession
	CreateChat(userID, title string) (protocol.ChatSession, error)
	GetChat(chatID string) (protocol.ChatSession, []protocol.Message, error)
	SetChatArchived(chatID, userID string, archived bool) (protocol.ChatSession, error)
	AddUserMessage(chatID, userID, content string) (protocol.Message, protocol.Run, error)
	AddAssistantMessage(chatID, runID, content string) (protocol.Message, error)
	GetRun(runID string) (protocol.Run, error)
	UpdateRunStatus(runID string, status protocol.RunStatus, errMessage string) (protocol.Run, error)
	AddEvent(runID string, typ protocol.RunEventType, message string, payload any) (protocol.RunEvent, error)
	ListEvents(runID string, afterSeq int64) []protocol.RunEvent
	Subscribe(runID string) (<-chan protocol.RunEvent, func())
	ListSkillsForUser(userID, projectID string) []protocol.Skill
	ListRuntimeSkillsForUser(userID, projectID string) []protocol.RuntimeSkill
	CreateHTTPSkill(userID, projectID string, input protocol.HTTPSkillInput) (protocol.Skill, error)
	UpdateHTTPSkill(userID, skillID string, input protocol.HTTPSkillInput) (protocol.Skill, error)
	SetSkillEnabled(userID, skillID string, enabled bool) (protocol.Skill, error)
}
