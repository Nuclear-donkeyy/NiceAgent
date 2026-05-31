package protocol

import "time"

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
