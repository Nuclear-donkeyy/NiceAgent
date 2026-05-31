package controlplane

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"niceagent/common/platform"
	"niceagent/common/protocol"
)

var (
	ErrNotFound = errors.New("not found")
	ErrClosed   = errors.New("closed")
)

type Store struct {
	mu           sync.RWMutex
	users        map[string]protocol.User
	chats        map[string]protocol.ChatSession
	messages     map[string][]protocol.Message
	runs         map[string]protocol.Run
	events       map[string][]protocol.RunEvent
	skills       map[string]protocol.Skill
	skillGrants  map[string][]string
	skillSecrets map[string]map[string]string
	subscribers  map[string]map[chan protocol.RunEvent]struct{}
	seq          map[string]int64
}

func NewStore() *Store {
	now := time.Now().UTC()
	store := &Store{
		users:        map[string]protocol.User{},
		chats:        map[string]protocol.ChatSession{},
		messages:     map[string][]protocol.Message{},
		runs:         map[string]protocol.Run{},
		events:       map[string][]protocol.RunEvent{},
		skills:       map[string]protocol.Skill{},
		skillGrants:  map[string][]string{},
		skillSecrets: map[string]map[string]string{},
		subscribers:  map[string]map[chan protocol.RunEvent]struct{}{},
		seq:          map[string]int64{},
	}
	store.users["demo-user"] = protocol.User{
		ID:        "demo-user",
		Email:     "demo@niceagent.local",
		Name:      "Demo User",
		Status:    "active",
		CreatedAt: now,
	}
	store.skills["cli.exec"] = protocol.Skill{
		ID:            "cli.exec",
		Slug:          "cli.exec",
		Scope:         protocol.SkillScopeSystem,
		Kind:          protocol.SkillKindBuiltin,
		Status:        protocol.SkillStatusEnabled,
		Name:          "System CLI",
		Version:       "0.1.0",
		Description:   "Fetch external information through a read-only sandboxed CLI.",
		Risk:          protocol.SkillRiskMedium,
		RequiresAuth:  false,
		InputSchema:   `{"type":"object","required":["command"],"properties":{"command":{"type":"array","items":{"type":"string"}}}}`,
		Annotations:   `{"readOnlyHint":true,"destructiveHint":false,"idempotentHint":false,"openWorldHint":true}`,
		RuntimeConfig: `{"type":"builtin","executor":"sandbox"}`,
		Enabled:       true,
	}
	store.skills["workspace.read"] = protocol.Skill{
		ID:            "workspace.read",
		Slug:          "workspace.read",
		Scope:         protocol.SkillScopeSystem,
		Kind:          protocol.SkillKindBuiltin,
		Status:        protocol.SkillStatusEnabled,
		Name:          "Workspace Reader",
		Version:       "0.1.0",
		Description:   "Inspect files and artifacts attached to a run workspace.",
		Risk:          protocol.SkillRiskLow,
		RequiresAuth:  false,
		Annotations:   `{"readOnlyHint":true,"destructiveHint":false,"idempotentHint":true,"openWorldHint":false}`,
		RuntimeConfig: `{"type":"builtin"}`,
		Enabled:       true,
	}
	store.skillGrants[skillGrantKey("demo-user", "demo-project")] = []string{"cli.exec", "workspace.read"}
	return store
}

func (s *Store) ListChats(userID string, opts ChatListOptions) []protocol.ChatSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	query := strings.ToLower(strings.TrimSpace(opts.Query))
	chats := make([]protocol.ChatSession, 0, len(s.chats))
	for _, chat := range s.chats {
		if chat.UserID != userID {
			continue
		}
		if chat.Archived && !opts.IncludeArchived {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(chat.Title), query) {
			continue
		}
		chat.MessageCount = len(s.messages[chat.ID])
		chats = append(chats, chat)
	}
	sort.Slice(chats, func(i, j int) bool {
		return chats[i].UpdatedAt.After(chats[j].UpdatedAt)
	})
	return chats
}

func (s *Store) CreateChat(userID, title string) (protocol.ChatSession, error) {
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
	return chat, nil
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

func (s *Store) SetChatArchived(chatID, userID string, archived bool) (protocol.ChatSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	chat, ok := s.chats[chatID]
	if !ok || chat.UserID != userID {
		return protocol.ChatSession{}, ErrNotFound
	}
	chat.Archived = archived
	chat.UpdatedAt = time.Now().UTC()
	chat.MessageCount = len(s.messages[chat.ID])
	s.chats[chatID] = chat
	return chat, nil
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

func (s *Store) AddAssistantMessage(chatID, runID, content string) (protocol.Message, error) {
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
	if _, ok := s.chats[chatID]; !ok {
		return protocol.Message{}, ErrNotFound
	}
	s.messages[chatID] = append(s.messages[chatID], msg)
	chat := s.chats[chatID]
	chat.UpdatedAt = msg.CreatedAt
	s.chats[chatID] = chat
	return msg, nil
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
	if isTerminalRunStatus(run.Status) && run.Status != status {
		return run, nil
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

func isTerminalRunStatus(status protocol.RunStatus) bool {
	return status == protocol.RunSucceeded || status == protocol.RunFailed || status == protocol.RunCanceled
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
		}
	}
	return ch, cancel
}

func (s *Store) ListSkillsForUser(userID, projectID string) []protocol.Skill {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.listSkillsForUserLocked(userID, projectID)
}

func (s *Store) ListRuntimeSkillsForUser(userID, projectID string) []protocol.RuntimeSkill {
	s.mu.RLock()
	defer s.mu.RUnlock()
	skills := s.listSkillsForUserLocked(userID, projectID)
	runtimeSkills := make([]protocol.RuntimeSkill, 0, len(skills))
	for _, skill := range skills {
		if skill.Status != protocol.SkillStatusEnabled || !skill.Enabled {
			continue
		}
		secrets := map[string]string{}
		for key, value := range s.skillSecrets[skill.ID] {
			secrets[key] = value
		}
		runtimeSkills = append(runtimeSkills, protocol.RuntimeSkill{Skill: skill, Secrets: secrets})
	}
	return runtimeSkills
}

func (s *Store) listSkillsForUserLocked(userID, projectID string) []protocol.Skill {
	if userID == "" || projectID == "" {
		return nil
	}
	ids := s.skillGrants[skillGrantKey(userID, projectID)]
	skills := make([]protocol.Skill, 0, len(ids))
	for _, id := range ids {
		if skill, ok := s.skills[id]; ok {
			if skill.Status == protocol.SkillStatusArchived {
				continue
			}
			if skill.Scope == protocol.SkillScopeSystem && !skill.Enabled {
				continue
			}
			skill = redactSkill(skill)
			skills = append(skills, skill)
		}
	}
	sort.Slice(skills, func(i, j int) bool {
		return skills[i].ID < skills[j].ID
	})
	return skills
}

func (s *Store) CreateHTTPSkill(userID, projectID string, input protocol.HTTPSkillInput) (protocol.Skill, error) {
	now := time.Now().UTC()
	skillID := platform.NewID("skill")
	versionID := platform.NewID("skv")
	skill, secret := httpSkillFromInput(skillID, versionID, userID, projectID, "1.0.0", input)
	skill.Enabled = true
	skill.Status = protocol.SkillStatusEnabled
	_ = now
	s.mu.Lock()
	defer s.mu.Unlock()
	s.skills[skill.ID] = skill
	if secret != "" {
		s.skillSecrets[skill.ID] = map[string]string{"bearer_token": secret}
	}
	key := skillGrantKey(userID, projectID)
	s.skillGrants[key] = appendUnique(s.skillGrants[key], skill.ID)
	return redactSkill(skill), nil
}

func (s *Store) UpdateHTTPSkill(userID, skillID string, input protocol.HTTPSkillInput) (protocol.Skill, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.skills[skillID]
	if !ok || current.OwnerUserID != userID || current.Kind != protocol.SkillKindHTTP {
		return protocol.Skill{}, ErrNotFound
	}
	updated, secret := httpSkillFromInput(skillID, platform.NewID("skv"), userID, current.ProjectID, current.Version, input)
	updated.Enabled = current.Enabled
	updated.Status = current.Status
	s.skills[skillID] = updated
	if secret != "" {
		if s.skillSecrets[skillID] == nil {
			s.skillSecrets[skillID] = map[string]string{}
		}
		s.skillSecrets[skillID]["bearer_token"] = secret
	}
	return redactSkill(updated), nil
}

func (s *Store) SetSkillEnabled(userID, skillID string, enabled bool) (protocol.Skill, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	skill, ok := s.skills[skillID]
	if !ok || skill.OwnerUserID != userID || skill.Scope != protocol.SkillScopeUser {
		return protocol.Skill{}, ErrNotFound
	}
	skill.Enabled = enabled
	if enabled {
		skill.Status = protocol.SkillStatusEnabled
	} else {
		skill.Status = protocol.SkillStatusDisabled
	}
	s.skills[skillID] = skill
	return redactSkill(skill), nil
}

func skillGrantKey(userID, projectID string) string {
	return userID + "\x00" + projectID
}

func appendUnique(values []string, next string) []string {
	for _, value := range values {
		if value == next {
			return values
		}
	}
	return append(values, next)
}

func httpSkillFromInput(skillID, versionID, userID, projectID, version string, input protocol.HTTPSkillInput) (protocol.Skill, string) {
	method := strings.ToUpper(strings.TrimSpace(input.Method))
	if method == "" {
		method = "POST"
	}
	timeout := input.TimeoutSeconds
	if timeout <= 0 {
		timeout = 15
	}
	authType := strings.TrimSpace(input.AuthType)
	if authType == "" {
		authType = "none"
	}
	config := map[string]any{
		"type":            "http",
		"method":          method,
		"url":             strings.TrimSpace(input.URL),
		"timeout_seconds": timeout,
		"auth_type":       authType,
	}
	configBytes, _ := json.Marshal(config)
	annotations := `{"readOnlyHint":false,"destructiveHint":false,"idempotentHint":false,"openWorldHint":true}`
	inputSchema := strings.TrimSpace(input.InputSchema)
	if inputSchema == "" {
		inputSchema = `{"type":"object","additionalProperties":true}`
	}
	description := strings.TrimSpace(input.Description)
	if description == "" {
		description = "User-provided HTTP skill."
	}
	skill := protocol.Skill{
		ID:               skillID,
		Slug:             skillID,
		Scope:            protocol.SkillScopeUser,
		Kind:             protocol.SkillKindHTTP,
		OwnerUserID:      userID,
		ProjectID:        projectID,
		Status:           protocol.SkillStatusEnabled,
		CurrentVersionID: versionID,
		Name:             strings.TrimSpace(input.Name),
		Version:          version,
		Description:      description,
		Risk:             protocol.SkillRiskMedium,
		RequiresAuth:     false,
		InputSchema:      inputSchema,
		OutputSchema:     strings.TrimSpace(input.OutputSchema),
		Annotations:      annotations,
		RuntimeConfig:    string(configBytes),
		Enabled:          true,
	}
	return skill, strings.TrimSpace(input.BearerToken)
}

func redactSkill(skill protocol.Skill) protocol.Skill {
	return skill
}

func titleFromContent(content string) string {
	const max = 42
	if len(content) <= max {
		return content
	}
	return content[:max] + "..."
}
