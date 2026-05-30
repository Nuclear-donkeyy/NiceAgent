package controlplane

import (
	"errors"
	"sort"
	"sync"
	"time"

	"niceagent/internal/platform"
	"niceagent/internal/protocol"
)

var (
	ErrNotFound = errors.New("not found")
	ErrClosed   = errors.New("closed")
)

type Store struct {
	mu          sync.RWMutex
	users       map[string]protocol.User
	chats       map[string]protocol.ChatSession
	messages    map[string][]protocol.Message
	runs        map[string]protocol.Run
	events      map[string][]protocol.RunEvent
	skills      map[string]protocol.Skill
	subscribers map[string]map[chan protocol.RunEvent]struct{}
	seq         map[string]int64
}

func NewStore() *Store {
	now := time.Now().UTC()
	store := &Store{
		users:       map[string]protocol.User{},
		chats:       map[string]protocol.ChatSession{},
		messages:    map[string][]protocol.Message{},
		runs:        map[string]protocol.Run{},
		events:      map[string][]protocol.RunEvent{},
		skills:      map[string]protocol.Skill{},
		subscribers: map[string]map[chan protocol.RunEvent]struct{}{},
		seq:         map[string]int64{},
	}
	store.users["demo-user"] = protocol.User{
		ID:        "demo-user",
		Email:     "demo@niceagent.local",
		Name:      "Demo User",
		Status:    "active",
		CreatedAt: now,
	}
	store.skills["cli.exec"] = protocol.Skill{
		ID:           "cli.exec",
		Name:         "Remote CLI",
		Version:      "0.1.0",
		Description:  "Execute approved commands inside a sandbox workspace.",
		Risk:         protocol.SkillRiskHigh,
		RequiresAuth: true,
		InputSchema:   `{"type":"object","required":["command"],"properties":{"command":{"type":"array","items":{"type":"string"}}}}`,
	}
	store.skills["workspace.read"] = protocol.Skill{
		ID:           "workspace.read",
		Name:         "Workspace Reader",
		Version:      "0.1.0",
		Description:  "Inspect files and artifacts attached to a run workspace.",
		Risk:         protocol.SkillRiskLow,
		RequiresAuth: false,
	}
	return store
}

func (s *Store) ListChats(userID string) []protocol.ChatSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	chats := make([]protocol.ChatSession, 0, len(s.chats))
	for _, chat := range s.chats {
		if chat.UserID == userID && !chat.Archived {
			chat.MessageCount = len(s.messages[chat.ID])
			chats = append(chats, chat)
		}
	}
	sort.Slice(chats, func(i, j int) bool {
		return chats[i].UpdatedAt.After(chats[j].UpdatedAt)
	})
	return chats
}

func (s *Store) CreateChat(userID, title string) protocol.ChatSession {
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
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chats[chat.ID] = chat
	return chat
}

func (s *Store) GetChat(chatID string) (protocol.ChatSession, []protocol.Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	chat, ok := s.chats[chatID]
	if !ok {
		return protocol.ChatSession{}, nil, ErrNotFound
	}
	msgs := append([]protocol.Message(nil), s.messages[chatID]...)
	chat.MessageCount = len(msgs)
	return chat, msgs, nil
}

func (s *Store) AddUserMessage(chatID, userID, content string) (protocol.Message, protocol.Run, error) {
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	chat, ok := s.chats[chatID]
	if !ok || chat.UserID != userID {
		return protocol.Message{}, protocol.Run{}, ErrNotFound
	}
	msg := protocol.Message{
		ID:        platform.NewID("msg"),
		ChatID:    chatID,
		Role:      protocol.RoleUser,
		Content:   content,
		CreatedAt: now,
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
	msg.RunID = run.ID
	chat.LastRunID = run.ID
	chat.UpdatedAt = now
	if chat.Title == "New chat" && content != "" {
		chat.Title = titleFromContent(content)
	}
	s.messages[chatID] = append(s.messages[chatID], msg)
	s.chats[chatID] = chat
	s.runs[run.ID] = run
	return msg, run, nil
}

func (s *Store) AddAssistantMessage(chatID, runID, content string) protocol.Message {
	msg := protocol.Message{
		ID:        platform.NewID("msg"),
		ChatID:    chatID,
		RunID:     runID,
		Role:      protocol.RoleAssistant,
		Content:   content,
		CreatedAt: time.Now().UTC(),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages[chatID] = append(s.messages[chatID], msg)
	chat := s.chats[chatID]
	chat.UpdatedAt = msg.CreatedAt
	s.chats[chatID] = chat
	return msg
}

func (s *Store) LatestUserMessage(chatID string) (protocol.Message, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	msgs := s.messages[chatID]
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == protocol.RoleUser {
			return msgs[i], true
		}
	}
	return protocol.Message{}, false
}

func (s *Store) GetRun(runID string) (protocol.Run, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	run, ok := s.runs[runID]
	if !ok {
		return protocol.Run{}, ErrNotFound
	}
	return run, nil
}

func (s *Store) UpdateRunStatus(runID string, status protocol.RunStatus, errMessage string) (protocol.Run, error) {
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[runID]
	if !ok {
		return protocol.Run{}, ErrNotFound
	}
	if status == protocol.RunRunning && run.StartedAt == nil {
		run.StartedAt = &now
	}
	if status == protocol.RunSucceeded || status == protocol.RunFailed || status == protocol.RunCanceled {
		run.FinishedAt = &now
	}
	run.Status = status
	run.Error = errMessage
	run.UpdatedAt = now
	s.runs[runID] = run
	return run, nil
}

func (s *Store) AddEvent(runID string, typ protocol.RunEventType, message string, payload any) (protocol.RunEvent, error) {
	s.mu.Lock()
	run, ok := s.runs[runID]
	if !ok {
		s.mu.Unlock()
		return protocol.RunEvent{}, ErrNotFound
	}
	s.seq[runID]++
	event := protocol.RunEvent{
		ID:        platform.NewID("evt"),
		RunID:     runID,
		ChatID:    run.ChatID,
		Seq:       s.seq[runID],
		Type:      typ,
		Message:   message,
		Payload:   payload,
		CreatedAt: time.Now().UTC(),
	}
	s.events[runID] = append(s.events[runID], event)
	subscribers := make([]chan protocol.RunEvent, 0, len(s.subscribers[runID]))
	for ch := range s.subscribers[runID] {
		subscribers = append(subscribers, ch)
	}
	s.mu.Unlock()

	for _, ch := range subscribers {
		select {
		case ch <- event:
		default:
		}
	}
	return event, nil
}

func (s *Store) ListEvents(runID string, afterSeq int64) []protocol.RunEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	source := s.events[runID]
	events := make([]protocol.RunEvent, 0, len(source))
	for _, event := range source {
		if event.Seq > afterSeq {
			events = append(events, event)
		}
	}
	return events
}

func (s *Store) Subscribe(runID string) (<-chan protocol.RunEvent, func()) {
	ch := make(chan protocol.RunEvent, 32)
	s.mu.Lock()
	if s.subscribers[runID] == nil {
		s.subscribers[runID] = map[chan protocol.RunEvent]struct{}{}
	}
	s.subscribers[runID][ch] = struct{}{}
	s.mu.Unlock()
	cancel := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.subscribers[runID][ch]; ok {
			delete(s.subscribers[runID], ch)
			close(ch)
		}
	}
	return ch, cancel
}

func (s *Store) ListSkills() []protocol.Skill {
	s.mu.RLock()
	defer s.mu.RUnlock()
	skills := make([]protocol.Skill, 0, len(s.skills))
	for _, skill := range s.skills {
		skills = append(skills, skill)
	}
	sort.Slice(skills, func(i, j int) bool {
		return skills[i].ID < skills[j].ID
	})
	return skills
}

func titleFromContent(content string) string {
	const max = 42
	if len(content) <= max {
		return content
	}
	return content[:max] + "..."
}

