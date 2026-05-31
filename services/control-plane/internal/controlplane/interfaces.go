package controlplane

import "niceagent/common/protocol"

type Repository interface {
	ListChats(userID string) []protocol.ChatSession
	CreateChat(userID, title string) (protocol.ChatSession, error)
	GetChat(chatID string) (protocol.ChatSession, []protocol.Message, error)
	AddUserMessage(chatID, userID, content string) (protocol.Message, protocol.Run, error)
	AddAssistantMessage(chatID, runID, content string) (protocol.Message, error)
	GetRun(runID string) (protocol.Run, error)
	UpdateRunStatus(runID string, status protocol.RunStatus, errMessage string) (protocol.Run, error)
	AddEvent(runID string, typ protocol.RunEventType, message string, payload any) (protocol.RunEvent, error)
	ListEvents(runID string, afterSeq int64) []protocol.RunEvent
	Subscribe(runID string) (<-chan protocol.RunEvent, func())
	ListSkills() []protocol.Skill
}
