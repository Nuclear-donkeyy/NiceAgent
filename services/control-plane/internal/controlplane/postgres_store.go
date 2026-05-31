package controlplane

import (
	"database/sql"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"niceagent/common/platform"
	"niceagent/common/protocol"
)

type PostgresStore struct {
	db          *sql.DB
	mu          sync.Mutex
	subscribers map[string]map[chan protocol.RunEvent]struct{}
}

func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{
		db:          db,
		subscribers: map[string]map[chan protocol.RunEvent]struct{}{},
	}
}

func (s *PostgresStore) ListChats(userID string, opts ChatListOptions) []protocol.ChatSession {
	query := "%" + strings.ToLower(strings.TrimSpace(opts.Query)) + "%"
	rows, err := s.db.Query(`
		SELECT c.id, c.user_id, c.project_id, c.title, c.archived, COALESCE(c.last_run_id, ''),
		       c.created_at, c.updated_at, COUNT(m.id)
		FROM chat_sessions c
		LEFT JOIN messages m ON m.chat_id = c.id
		WHERE c.user_id = $1
		  AND ($2 OR c.archived = false)
		  AND ($3 = '%%' OR LOWER(c.title) LIKE $3)
		GROUP BY c.id
		ORDER BY c.updated_at DESC`, userID, opts.IncludeArchived, query)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var chats []protocol.ChatSession
	for rows.Next() {
		var chat protocol.ChatSession
		if err := rows.Scan(&chat.ID, &chat.UserID, &chat.ProjectID, &chat.Title, &chat.Archived, &chat.LastRunID, &chat.CreatedAt, &chat.UpdatedAt, &chat.MessageCount); err == nil {
			chats = append(chats, chat)
		}
	}
	return chats
}

func (s *PostgresStore) CreateChat(userID, title string) (protocol.ChatSession, error) {
	now := time.Now().UTC()
	if title == "" {
		title = "New chat"
	}
	chat := protocol.ChatSession{
		ID:        platform.NewID("chat"),
		UserID:    userID,
		ProjectID: "demo-project",
		Title:     title,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_, err := s.db.Exec(`
		INSERT INTO chat_sessions (id, user_id, project_id, title, archived, created_at, updated_at)
		VALUES ($1, $2, $3, $4, false, $5, $6)`,
		chat.ID, chat.UserID, chat.ProjectID, chat.Title, chat.CreatedAt, chat.UpdatedAt)
	if err != nil {
		return protocol.ChatSession{}, err
	}
	return chat, nil
}

func (s *PostgresStore) GetChat(chatID string) (protocol.ChatSession, []protocol.Message, error) {
	var chat protocol.ChatSession
	if err := s.db.QueryRow(`
		SELECT id, user_id, project_id, title, archived, COALESCE(last_run_id, ''), created_at, updated_at
		FROM chat_sessions
		WHERE id = $1`, chatID).Scan(
		&chat.ID, &chat.UserID, &chat.ProjectID, &chat.Title, &chat.Archived, &chat.LastRunID, &chat.CreatedAt, &chat.UpdatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return protocol.ChatSession{}, nil, ErrNotFound
		}
		return protocol.ChatSession{}, nil, err
	}
	rows, err := s.db.Query(`
		SELECT id, chat_id, COALESCE(run_id, ''), role, content, created_at
		FROM messages
		WHERE chat_id = $1
		ORDER BY created_at`, chatID)
	if err != nil {
		return protocol.ChatSession{}, nil, err
	}
	defer rows.Close()
	var messages []protocol.Message
	for rows.Next() {
		var msg protocol.Message
		var role string
		if err := rows.Scan(&msg.ID, &msg.ChatID, &msg.RunID, &role, &msg.Content, &msg.CreatedAt); err != nil {
			return protocol.ChatSession{}, nil, err
		}
		msg.Role = protocol.MessageRole(role)
		messages = append(messages, msg)
	}
	chat.MessageCount = len(messages)
	return chat, messages, rows.Err()
}

func (s *PostgresStore) SetChatArchived(chatID, userID string, archived bool) (protocol.ChatSession, error) {
	now := time.Now().UTC()
	result, err := s.db.Exec(`
		UPDATE chat_sessions
		SET archived = $1, updated_at = $2
		WHERE id = $3 AND user_id = $4`, archived, now, chatID, userID)
	if err != nil {
		return protocol.ChatSession{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return protocol.ChatSession{}, err
	}
	if affected == 0 {
		return protocol.ChatSession{}, ErrNotFound
	}
	chat, _, err := s.GetChat(chatID)
	return chat, err
}

func (s *PostgresStore) AddUserMessage(chatID, userID, content string) (protocol.Message, protocol.Run, error) {
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.Message{}, protocol.Run{}, err
	}
	defer tx.Rollback()
	var currentTitle string
	if err := tx.QueryRow(`SELECT title FROM chat_sessions WHERE id = $1 AND user_id = $2`, chatID, userID).Scan(&currentTitle); err != nil {
		if err == sql.ErrNoRows {
			return protocol.Message{}, protocol.Run{}, ErrNotFound
		}
		return protocol.Message{}, protocol.Run{}, err
	}
	run := protocol.Run{
		ID:          platform.NewID("run"),
		ChatID:      chatID,
		UserID:      userID,
		WorkspaceID: platform.NewID("ws"),
		Status:      protocol.RunQueued,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	msg := protocol.Message{
		ID:        platform.NewID("msg"),
		ChatID:    chatID,
		RunID:     run.ID,
		Role:      protocol.RoleUser,
		Content:   content,
		CreatedAt: now,
	}
	if _, err := tx.Exec(`INSERT INTO runs (id, chat_id, user_id, workspace_id, status, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		run.ID, run.ChatID, run.UserID, run.WorkspaceID, run.Status, run.CreatedAt, run.UpdatedAt); err != nil {
		return protocol.Message{}, protocol.Run{}, err
	}
	if _, err := tx.Exec(`INSERT INTO messages (id, chat_id, run_id, role, content, created_at) VALUES ($1, $2, $3, $4, $5, $6)`,
		msg.ID, msg.ChatID, msg.RunID, msg.Role, msg.Content, msg.CreatedAt); err != nil {
		return protocol.Message{}, protocol.Run{}, err
	}
	title := currentTitle
	if title == "New chat" && content != "" {
		title = titleFromContent(content)
	}
	if _, err := tx.Exec(`UPDATE chat_sessions SET title = $1, last_run_id = $2, updated_at = $3 WHERE id = $4`, title, run.ID, now, chatID); err != nil {
		return protocol.Message{}, protocol.Run{}, err
	}
	if err := tx.Commit(); err != nil {
		return protocol.Message{}, protocol.Run{}, err
	}
	return msg, run, nil
}

func (s *PostgresStore) AddAssistantMessage(chatID, runID, content string) (protocol.Message, error) {
	msg := protocol.Message{
		ID:        platform.NewID("msg"),
		ChatID:    chatID,
		RunID:     runID,
		Role:      protocol.RoleAssistant,
		Content:   content,
		CreatedAt: time.Now().UTC(),
	}
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.Message{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO messages (id, chat_id, run_id, role, content, created_at) VALUES ($1, $2, $3, $4, $5, $6)`,
		msg.ID, msg.ChatID, msg.RunID, msg.Role, msg.Content, msg.CreatedAt); err != nil {
		return protocol.Message{}, err
	}
	if _, err := tx.Exec(`UPDATE chat_sessions SET updated_at = $1 WHERE id = $2`, msg.CreatedAt, chatID); err != nil {
		return protocol.Message{}, err
	}
	if err := tx.Commit(); err != nil {
		return protocol.Message{}, err
	}
	return msg, nil
}

func (s *PostgresStore) GetRun(runID string) (protocol.Run, error) {
	var run protocol.Run
	var startedAt, finishedAt sql.NullTime
	var errText sql.NullString
	if err := s.db.QueryRow(`
		SELECT id, chat_id, user_id, workspace_id, status, error, created_at, updated_at, started_at, finished_at
		FROM runs WHERE id = $1`, runID).Scan(
		&run.ID, &run.ChatID, &run.UserID, &run.WorkspaceID, &run.Status, &errText, &run.CreatedAt, &run.UpdatedAt, &startedAt, &finishedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return protocol.Run{}, ErrNotFound
		}
		return protocol.Run{}, err
	}
	if errText.Valid {
		run.Error = errText.String
	}
	if startedAt.Valid {
		run.StartedAt = &startedAt.Time
	}
	if finishedAt.Valid {
		run.FinishedAt = &finishedAt.Time
	}
	return run, nil
}

func (s *PostgresStore) UpdateRunStatus(runID string, status protocol.RunStatus, errMessage string) (protocol.Run, error) {
	run, err := s.GetRun(runID)
	if err != nil {
		return protocol.Run{}, err
	}
	if isTerminalRunStatus(run.Status) && run.Status != status {
		return run, nil
	}
	now := time.Now().UTC()
	startedAt := run.StartedAt
	finishedAt := run.FinishedAt
	if status == protocol.RunRunning && startedAt == nil {
		startedAt = &now
	}
	if isTerminalRunStatus(status) && finishedAt == nil {
		finishedAt = &now
	}
	_, err = s.db.Exec(`
		UPDATE runs
		SET status = $1, error = NULLIF($2, ''), updated_at = $3, started_at = $4, finished_at = $5
		WHERE id = $6`, status, errMessage, now, startedAt, finishedAt, runID)
	if err != nil {
		return protocol.Run{}, err
	}
	return s.GetRun(runID)
}

func (s *PostgresStore) AddEvent(runID string, typ protocol.RunEventType, message string, payload any) (protocol.RunEvent, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.RunEvent{}, err
	}
	defer tx.Rollback()
	var chatID string
	if err := tx.QueryRow(`SELECT chat_id FROM runs WHERE id = $1 FOR UPDATE`, runID).Scan(&chatID); err != nil {
		if err == sql.ErrNoRows {
			return protocol.RunEvent{}, ErrNotFound
		}
		return protocol.RunEvent{}, err
	}
	var seq int64
	if err := tx.QueryRow(`SELECT COALESCE(MAX(seq), 0) + 1 FROM run_events WHERE run_id = $1`, runID).Scan(&seq); err != nil {
		return protocol.RunEvent{}, err
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return protocol.RunEvent{}, err
	}
	event := protocol.RunEvent{
		ID:        platform.NewID("evt"),
		RunID:     runID,
		ChatID:    chatID,
		Seq:       seq,
		Type:      typ,
		Message:   message,
		Payload:   payload,
		CreatedAt: time.Now().UTC(),
	}
	if _, err := tx.Exec(`INSERT INTO run_events (id, run_id, chat_id, seq, type, message, payload, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		event.ID, event.RunID, event.ChatID, event.Seq, event.Type, event.Message, payloadJSON, event.CreatedAt); err != nil {
		return protocol.RunEvent{}, err
	}
	if err := tx.Commit(); err != nil {
		return protocol.RunEvent{}, err
	}
	s.publish(event)
	return event, nil
}

func (s *PostgresStore) ListEvents(runID string, afterSeq int64) []protocol.RunEvent {
	rows, err := s.db.Query(`
		SELECT id, run_id, chat_id, seq, type, COALESCE(message, ''), payload, created_at
		FROM run_events
		WHERE run_id = $1 AND seq > $2
		ORDER BY seq`, runID, afterSeq)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var events []protocol.RunEvent
	for rows.Next() {
		var event protocol.RunEvent
		var typ string
		var raw []byte
		if err := rows.Scan(&event.ID, &event.RunID, &event.ChatID, &event.Seq, &typ, &event.Message, &raw, &event.CreatedAt); err != nil {
			return events
		}
		event.Type = protocol.RunEventType(typ)
		if len(raw) > 0 && string(raw) != "null" {
			var payload any
			if err := json.Unmarshal(raw, &payload); err == nil {
				event.Payload = payload
			}
		}
		events = append(events, event)
	}
	return events
}

func (s *PostgresStore) Subscribe(runID string) (<-chan protocol.RunEvent, func()) {
	ch := make(chan protocol.RunEvent, 32)
	s.mu.Lock()
	if s.subscribers[runID] == nil {
		s.subscribers[runID] = map[chan protocol.RunEvent]struct{}{}
	}
	s.subscribers[runID][ch] = struct{}{}
	s.mu.Unlock()
	cancel := func() {
		s.mu.Lock()
		if subs, ok := s.subscribers[runID]; ok {
			delete(subs, ch)
			if len(subs) == 0 {
				delete(s.subscribers, runID)
			}
		}
		s.mu.Unlock()
	}
	return ch, cancel
}

func (s *PostgresStore) ListSkillsForUser(userID, projectID string) []protocol.Skill {
	rows, err := s.db.Query(`
		SELECT s.id, s.name, s.version, s.description, s.risk, s.requires_auth,
		       COALESCE(s.input_schema::text, ''), COALESCE(s.output_schema::text, '')
		FROM skills s
		JOIN skill_grants g ON g.skill_id = s.id
		WHERE g.user_id = $1 AND (g.project_id = $2 OR g.project_id IS NULL)
		ORDER BY s.id`, userID, projectID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var skills []protocol.Skill
	for rows.Next() {
		var skill protocol.Skill
		var risk string
		if err := rows.Scan(&skill.ID, &skill.Name, &skill.Version, &skill.Description, &risk, &skill.RequiresAuth, &skill.InputSchema, &skill.OutputSchema); err == nil {
			skill.Risk = protocol.SkillRisk(risk)
			skills = append(skills, skill)
		}
	}
	return skills
}

func (s *PostgresStore) publish(event protocol.RunEvent) {
	s.mu.Lock()
	subscribers := make([]chan protocol.RunEvent, 0, len(s.subscribers[event.RunID]))
	for ch := range s.subscribers[event.RunID] {
		subscribers = append(subscribers, ch)
	}
	s.mu.Unlock()
	for _, ch := range subscribers {
		select {
		case ch <- event:
		default:
		}
	}
}
