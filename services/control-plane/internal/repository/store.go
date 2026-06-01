package repository

import (
	"sort"
	"strings"
	"sync"
	"time"

	"niceagent/common/platform"
	"niceagent/common/protocol"
	"niceagent/common/skillmanifest"
	"niceagent/control-plane/internal/app"
)

type Store struct {
	mu             sync.RWMutex
	users          map[string]protocol.User
	identities     map[string]protocol.UserIdentity
	userIdentities map[string]string
	projects       map[string]protocol.Project
	chats          map[string]protocol.ChatSession
	messages       map[string][]protocol.Message
	runs           map[string]protocol.Run
	runUsage       map[string]protocol.RunUsage
	events         map[string][]protocol.RunEvent
	auditEvents    []protocol.AuditEvent
	workspaces     map[string]protocol.Workspace
	artifacts      map[string]protocol.Artifact
	invitations    map[string]protocol.Invitation
	skills         map[string]protocol.Skill
	skillGrants    map[string][]string
	skillSecrets   map[string]map[string]protocol.RuntimeSecret
	orgRoles       map[string][]string
	projectRoles   map[string][]string
	quotaPolicies  map[string]protocol.ProjectQuotaPolicy
	subscribers    map[string]map[chan protocol.RunEvent]struct{}
	seq            map[string]int64
}

func NewStore() *Store {
	now := time.Now().UTC()
	store := &Store{
		users:          map[string]protocol.User{},
		identities:     map[string]protocol.UserIdentity{},
		userIdentities: map[string]string{},
		projects:       map[string]protocol.Project{},
		chats:          map[string]protocol.ChatSession{},
		messages:       map[string][]protocol.Message{},
		runs:           map[string]protocol.Run{},
		runUsage:       map[string]protocol.RunUsage{},
		events:         map[string][]protocol.RunEvent{},
		auditEvents:    []protocol.AuditEvent{},
		workspaces:     map[string]protocol.Workspace{},
		artifacts:      map[string]protocol.Artifact{},
		invitations:    map[string]protocol.Invitation{},
		skills:         map[string]protocol.Skill{},
		skillGrants:    map[string][]string{},
		skillSecrets:   map[string]map[string]protocol.RuntimeSecret{},
		orgRoles:       map[string][]string{},
		projectRoles:   map[string][]string{},
		quotaPolicies:  map[string]protocol.ProjectQuotaPolicy{},
		subscribers:    map[string]map[chan protocol.RunEvent]struct{}{},
		seq:            map[string]int64{},
	}
	store.users["demo-user"] = protocol.User{
		ID:        "demo-user",
		Email:     "demo@niceagent.local",
		Name:      "Demo User",
		Status:    "active",
		CreatedAt: now,
	}
	store.projects["demo-project"] = protocol.Project{
		ID:             "demo-project",
		OrganizationID: "demo-org",
		Name:           "Demo Project",
		CreatedAt:      now,
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
	store.orgRoles[orgRoleKey("demo-user", "demo-org")] = []string{"owner"}
	store.projectRoles[projectRoleKey("demo-user", "demo-project")] = []string{"owner"}
	return store
}

func (s *Store) ListChats(userID, projectID string, opts app.ChatListOptions) []protocol.ChatSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	query := strings.ToLower(strings.TrimSpace(opts.Query))
	chats := make([]protocol.ChatSession, 0, len(s.chats))
	for _, chat := range s.chats {
		if chat.UserID != userID || chat.ProjectID != projectID {
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

func (s *Store) CreateChat(userID, projectID, title string) (protocol.ChatSession, error) {
	now := time.Now().UTC()
	if title == "" {
		title = "New chat"
	}
	chat := protocol.ChatSession{
		ID:        platform.NewID("chat"),
		UserID:    userID,
		ProjectID: projectID,
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
		return protocol.ChatSession{}, nil, app.ErrNotFound
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
		return protocol.ChatSession{}, app.ErrNotFound
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
		return protocol.Message{}, protocol.Run{}, app.ErrNotFound
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
	s.workspaces[run.WorkspaceID] = protocol.Workspace{
		ID:        run.WorkspaceID,
		UserID:    userID,
		ProjectID: chat.ProjectID,
		ChatID:    chatID,
		RunID:     run.ID,
		RootPath:  run.WorkspaceID,
		CreatedAt: now,
	}
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
		return protocol.Message{}, app.ErrNotFound
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
		return protocol.Run{}, app.ErrNotFound
	}
	run.Usage = s.runUsage[runID]
	return run, nil
}

func (s *Store) CountActiveRuns(userID, projectID string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := 0
	for _, run := range s.runs {
		if run.UserID != userID || !isActiveRunStatus(run.Status) {
			continue
		}
		chat, ok := s.chats[run.ChatID]
		if ok && chat.ProjectID == projectID {
			count++
		}
	}
	return count
}

func (s *Store) CountRunsCreatedSince(userID, projectID string, since time.Time) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := 0
	for _, run := range s.runs {
		if run.UserID != userID || run.CreatedAt.Before(since) {
			continue
		}
		chat, ok := s.chats[run.ChatID]
		if ok && chat.ProjectID == projectID {
			count++
		}
	}
	return count
}

func (s *Store) SumRunUsageTokensSince(userID, projectID string, since time.Time) int {
	return s.SumRunUsageSince(userID, projectID, since).TotalTokens
}

func (s *Store) SumRunUsageSince(userID, projectID string, since time.Time) protocol.RunUsage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	total := protocol.RunUsage{}
	for runID, usage := range s.runUsage {
		run, ok := s.runs[runID]
		if !ok || run.UserID != userID || run.CreatedAt.Before(since) {
			continue
		}
		chat, ok := s.chats[run.ChatID]
		if !ok || chat.ProjectID != projectID {
			continue
		}
		normalized := protocol.NormalizeRunUsage(usage)
		total.InputTokens += normalized.InputTokens
		total.OutputTokens += normalized.OutputTokens
		total.ReasoningTokens += normalized.ReasoningTokens
		total.CachedTokens += normalized.CachedTokens
		total.TotalTokens += normalized.TotalTokens
		total.Cost += normalized.Cost
		total.LatencyMillis += normalized.LatencyMillis
		total.RetryCount += normalized.RetryCount
		total.ToolCalls += normalized.ToolCalls
		total.ToolErrors += normalized.ToolErrors
		total.SandboxCommands += normalized.SandboxCommands
		total.SandboxDurationMillis += normalized.SandboxDurationMillis
		total.SandboxOutputBytes += normalized.SandboxOutputBytes
		total.SandboxCPUMillis += normalized.SandboxCPUMillis
		if normalized.SandboxMemoryMaxBytes > total.SandboxMemoryMaxBytes {
			total.SandboxMemoryMaxBytes = normalized.SandboxMemoryMaxBytes
		}
		total.ArtifactCount += normalized.ArtifactCount
		total.ArtifactBytes += normalized.ArtifactBytes
	}
	return total
}

func (s *Store) ListRunUsageBucketsSince(projectID string, since time.Time) []protocol.RunUsageBucket {
	s.mu.RLock()
	defer s.mu.RUnlock()
	bucketsByKey := map[string]*protocol.RunUsageBucket{}
	for runID, usage := range s.runUsage {
		run, ok := s.runs[runID]
		if !ok || run.CreatedAt.Before(since) {
			continue
		}
		chat, ok := s.chats[run.ChatID]
		if !ok || chat.ProjectID != projectID {
			continue
		}
		normalized := protocol.NormalizeRunUsage(usage)
		key := usageBucketKey(normalized)
		bucket := bucketsByKey[key]
		if bucket == nil {
			bucket = &protocol.RunUsageBucket{
				Provider:       normalized.Provider,
				Model:          normalized.Model,
				Currency:       normalized.Currency,
				Estimated:      normalized.Estimated,
				TokenEstimator: normalized.TokenEstimator,
			}
			bucketsByKey[key] = bucket
		}
		addUsageToBucket(bucket, normalized)
	}
	buckets := make([]protocol.RunUsageBucket, 0, len(bucketsByKey))
	for _, bucket := range bucketsByKey {
		buckets = append(buckets, *bucket)
	}
	sortRunUsageBuckets(buckets)
	return buckets
}

func (s *Store) ProjectBelongsToOrganization(projectID, orgID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	project, ok := s.projects[projectID]
	return ok && project.OrganizationID == orgID
}

func (s *Store) BindUserIdentity(identity protocol.UserIdentity) (protocol.UserIdentity, error) {
	identity.Provider = strings.ToLower(strings.TrimSpace(identity.Provider))
	identity.Issuer = strings.TrimSpace(identity.Issuer)
	identity.Subject = strings.TrimSpace(identity.Subject)
	identity.UserID = strings.TrimSpace(identity.UserID)
	identity.Email = strings.ToLower(strings.TrimSpace(identity.Email))
	identity.Name = strings.TrimSpace(identity.Name)
	if identity.Provider == "" || identity.Issuer == "" || identity.Subject == "" || identity.UserID == "" {
		return protocol.UserIdentity{}, app.ErrInvalidInput
	}
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	key := identityKey(identity.Provider, identity.Issuer, identity.Subject)
	userKey := userIdentityKey(identity.Provider, identity.UserID)
	if existing, ok := s.identities[key]; ok {
		if existing.UserID != identity.UserID {
			return protocol.UserIdentity{}, app.ErrIdentityConflict
		}
		existing.Email = firstNonEmpty(identity.Email, existing.Email)
		existing.Name = firstNonEmpty(identity.Name, existing.Name)
		existing.UpdatedAt = now
		s.identities[key] = existing
		return existing, nil
	}
	if existingKey, ok := s.userIdentities[userKey]; ok && existingKey != key {
		return protocol.UserIdentity{}, app.ErrIdentityConflict
	}
	user := s.users[identity.UserID]
	user.ID = identity.UserID
	user.Email = firstNonEmpty(identity.Email, user.Email, identity.UserID+"@niceagent.local")
	user.Name = firstNonEmpty(identity.Name, user.Name, identity.UserID)
	user.Status = firstNonEmpty(user.Status, "active")
	if user.CreatedAt.IsZero() {
		user.CreatedAt = now
	}
	s.users[identity.UserID] = user
	identity.ID = platform.NewID("uid")
	identity.CreatedAt = now
	identity.UpdatedAt = now
	s.identities[key] = identity
	s.userIdentities[userKey] = key
	return identity, nil
}

func (s *Store) ListOrganizationRoles(userID, orgID string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.orgRoles[orgRoleKey(userID, orgID)]...)
}

func (s *Store) ListProjectRoles(userID, projectID string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.projectRoles[projectRoleKey(userID, projectID)]...)
}

func (s *Store) ListOrganizationMembers(orgID string) []protocol.OrganizationMember {
	s.mu.RLock()
	defer s.mu.RUnlock()
	members := make([]protocol.OrganizationMember, 0)
	for key, roles := range s.orgRoles {
		userID, memberOrgID, ok := splitOrgRoleKey(key)
		if !ok || memberOrgID != orgID || len(roles) == 0 {
			continue
		}
		member := protocol.OrganizationMember{
			ID:             "orgmem_" + userID + "_" + orgID,
			UserID:         userID,
			OrganizationID: orgID,
			Role:           roles[0],
			CreatedAt:      time.Now().UTC(),
			UpdatedAt:      time.Now().UTC(),
		}
		if user, ok := s.users[userID]; ok {
			member.Email = user.Email
			member.Name = user.Name
		}
		members = append(members, member)
	}
	sort.Slice(members, func(i, j int) bool {
		if members[i].Role == members[j].Role {
			return members[i].UserID < members[j].UserID
		}
		return members[i].Role < members[j].Role
	})
	return members
}

func (s *Store) UpsertOrganizationMember(orgID string, input protocol.OrganizationMemberInput) (protocol.OrganizationMember, error) {
	userID := strings.TrimSpace(input.UserID)
	role := normalizeMemberRole(input.Role)
	if userID == "" || role == "" {
		return protocol.OrganizationMember{}, app.ErrInvalidInput
	}
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	user := s.users[userID]
	user.ID = userID
	user.Email = firstNonEmpty(strings.TrimSpace(input.Email), user.Email, userID+"@niceagent.local")
	user.Name = firstNonEmpty(strings.TrimSpace(input.Name), user.Name, userID)
	user.Status = firstNonEmpty(user.Status, "active")
	if user.CreatedAt.IsZero() {
		user.CreatedAt = now
	}
	s.users[userID] = user
	s.orgRoles[orgRoleKey(userID, orgID)] = []string{role}
	return protocol.OrganizationMember{
		ID:             "orgmem_" + userID + "_" + orgID,
		UserID:         userID,
		OrganizationID: orgID,
		Role:           role,
		Email:          user.Email,
		Name:           user.Name,
		CreatedAt:      now,
		UpdatedAt:      now,
	}, nil
}

func (s *Store) RemoveOrganizationMember(orgID, userID string) (protocol.OrganizationMember, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := orgRoleKey(userID, orgID)
	roles := s.orgRoles[key]
	if len(roles) == 0 {
		return protocol.OrganizationMember{}, app.ErrNotFound
	}
	now := time.Now().UTC()
	member := protocol.OrganizationMember{
		ID:             "orgmem_" + userID + "_" + orgID,
		UserID:         userID,
		OrganizationID: orgID,
		Role:           roles[0],
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if user, ok := s.users[userID]; ok {
		member.Email = user.Email
		member.Name = user.Name
	}
	delete(s.orgRoles, key)
	return member, nil
}

func (s *Store) ListInvitations(orgID string) []protocol.Invitation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	invitations := make([]protocol.Invitation, 0)
	now := time.Now().UTC()
	for _, invitation := range s.invitations {
		if invitation.OrganizationID != orgID {
			continue
		}
		invitation.Token = ""
		if invitation.Status == protocol.InvitationPending && !invitation.ExpiresAt.After(now) {
			invitation.Status = protocol.InvitationExpired
		}
		invitations = append(invitations, invitation)
	}
	sort.Slice(invitations, func(i, j int) bool {
		return invitations[i].CreatedAt.After(invitations[j].CreatedAt)
	})
	return invitations
}

func (s *Store) CreateInvitation(orgID, invitedByUserID string, input protocol.InvitationInput) (protocol.Invitation, error) {
	email := strings.ToLower(strings.TrimSpace(input.Email))
	role := normalizeMemberRole(input.Role)
	projectID := strings.TrimSpace(input.ProjectID)
	if email == "" || !strings.Contains(email, "@") || role == "" {
		return protocol.Invitation{}, app.ErrInvalidInput
	}
	now := time.Now().UTC()
	expiresInHours := input.ExpiresInHours
	if expiresInHours <= 0 {
		expiresInHours = 168
	}
	if expiresInHours > 24*30 {
		return protocol.Invitation{}, app.ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if projectID != "" {
		project, ok := s.projects[projectID]
		if !ok || project.OrganizationID != orgID {
			return protocol.Invitation{}, app.ErrInvalidInput
		}
	}
	invitation := protocol.Invitation{
		ID:              platform.NewID("inv"),
		Token:           platform.NewID("invite_token"),
		OrganizationID:  orgID,
		ProjectID:       projectID,
		Email:           email,
		Role:            role,
		Status:          protocol.InvitationPending,
		InvitedByUserID: invitedByUserID,
		CreatedAt:       now,
		ExpiresAt:       now.Add(time.Duration(expiresInHours) * time.Hour),
	}
	s.invitations[invitation.Token] = invitation
	return invitation, nil
}

func (s *Store) AcceptInvitation(token, userID, email, name string) (protocol.Invitation, error) {
	token = strings.TrimSpace(token)
	userID = strings.TrimSpace(userID)
	email = strings.ToLower(strings.TrimSpace(email))
	if token == "" || userID == "" || email == "" {
		return protocol.Invitation{}, app.ErrInvalidInput
	}
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	invitation, ok := s.invitations[token]
	if !ok {
		return protocol.Invitation{}, app.ErrNotFound
	}
	if invitation.Status != protocol.InvitationPending {
		return protocol.Invitation{}, app.ErrInvalidInput
	}
	if !invitation.ExpiresAt.After(now) {
		invitation.Status = protocol.InvitationExpired
		s.invitations[token] = invitation
		return protocol.Invitation{}, app.ErrInvalidInput
	}
	if email != invitation.Email {
		return protocol.Invitation{}, app.ErrInvalidInput
	}
	user := s.users[userID]
	user.ID = userID
	user.Email = invitation.Email
	user.Name = firstNonEmpty(strings.TrimSpace(name), user.Name, userID)
	user.Status = firstNonEmpty(user.Status, "active")
	if user.CreatedAt.IsZero() {
		user.CreatedAt = now
	}
	s.users[userID] = user
	if invitation.ProjectID != "" {
		s.projectRoles[projectRoleKey(userID, invitation.ProjectID)] = []string{invitation.Role}
	} else {
		s.orgRoles[orgRoleKey(userID, invitation.OrganizationID)] = []string{invitation.Role}
	}
	invitation.Status = protocol.InvitationAccepted
	invitation.AcceptedByUserID = userID
	invitation.AcceptedAt = &now
	s.invitations[token] = invitation
	invitation.Token = ""
	return invitation, nil
}

func (s *Store) ListProjectMembers(projectID string) []protocol.ProjectMember {
	s.mu.RLock()
	defer s.mu.RUnlock()
	members := make([]protocol.ProjectMember, 0)
	for key, roles := range s.projectRoles {
		userID, memberProjectID, ok := splitProjectRoleKey(key)
		if !ok || memberProjectID != projectID || len(roles) == 0 {
			continue
		}
		member := protocol.ProjectMember{
			ID:        "prjmem_" + userID + "_" + projectID,
			UserID:    userID,
			ProjectID: projectID,
			Role:      roles[0],
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}
		if user, ok := s.users[userID]; ok {
			member.Email = user.Email
			member.Name = user.Name
		}
		members = append(members, member)
	}
	sort.Slice(members, func(i, j int) bool {
		if members[i].Role == members[j].Role {
			return members[i].UserID < members[j].UserID
		}
		return members[i].Role < members[j].Role
	})
	return members
}

func (s *Store) UpsertProjectMember(projectID string, input protocol.ProjectMemberInput) (protocol.ProjectMember, error) {
	userID := strings.TrimSpace(input.UserID)
	role := normalizeMemberRole(input.Role)
	if userID == "" || role == "" {
		return protocol.ProjectMember{}, app.ErrInvalidInput
	}
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	user := s.users[userID]
	user.ID = userID
	user.Email = firstNonEmpty(strings.TrimSpace(input.Email), user.Email, userID+"@niceagent.local")
	user.Name = firstNonEmpty(strings.TrimSpace(input.Name), user.Name, userID)
	user.Status = firstNonEmpty(user.Status, "active")
	if user.CreatedAt.IsZero() {
		user.CreatedAt = now
	}
	s.users[userID] = user
	s.projectRoles[projectRoleKey(userID, projectID)] = []string{role}
	return protocol.ProjectMember{
		ID:        "prjmem_" + userID + "_" + projectID,
		UserID:    userID,
		ProjectID: projectID,
		Role:      role,
		Email:     user.Email,
		Name:      user.Name,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

func (s *Store) RemoveProjectMember(projectID, userID string) (protocol.ProjectMember, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := projectRoleKey(userID, projectID)
	roles := s.projectRoles[key]
	if len(roles) == 0 {
		return protocol.ProjectMember{}, app.ErrNotFound
	}
	now := time.Now().UTC()
	member := protocol.ProjectMember{
		ID:        "prjmem_" + userID + "_" + projectID,
		UserID:    userID,
		ProjectID: projectID,
		Role:      roles[0],
		CreatedAt: now,
		UpdatedAt: now,
	}
	if user, ok := s.users[userID]; ok {
		member.Email = user.Email
		member.Name = user.Name
	}
	delete(s.projectRoles, key)
	return member, nil
}

func (s *Store) GetProjectQuotaPolicy(projectID string) (protocol.ProjectQuotaPolicy, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	policy, ok := s.quotaPolicies[projectID]
	return policy, ok
}

func (s *Store) SetProjectQuotaPolicy(projectID string, input protocol.ProjectQuotaPolicyInput) (protocol.ProjectQuotaPolicy, error) {
	if input.MaxConcurrentRuns < 0 || input.MaxRunsPerHour < 0 || input.MaxModelTokensPerDay < 0 ||
		input.MaxToolCallsPerDay < 0 || input.MaxSandboxSecondsPerDay < 0 {
		return protocol.ProjectQuotaPolicy{}, app.ErrInvalidInput
	}
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	policy := s.quotaPolicies[projectID]
	if policy.CreatedAt.IsZero() {
		policy.CreatedAt = now
	}
	policy.ProjectID = projectID
	policy.MaxConcurrentRuns = input.MaxConcurrentRuns
	policy.MaxRunsPerHour = input.MaxRunsPerHour
	policy.MaxModelTokensPerDay = input.MaxModelTokensPerDay
	policy.MaxToolCallsPerDay = input.MaxToolCallsPerDay
	policy.MaxSandboxSecondsPerDay = input.MaxSandboxSecondsPerDay
	policy.UpdatedAt = now
	s.quotaPolicies[projectID] = policy
	return policy, nil
}

func (s *Store) SetProjectRole(userID, projectID, role string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.projectRoles[projectRoleKey(userID, projectID)] = []string{role}
}

func (s *Store) SetOrganizationRole(userID, orgID, role string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.orgRoles[orgRoleKey(userID, orgID)] = []string{role}
}

func (s *Store) SetProjectOrganization(projectID, orgID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	project := s.projects[projectID]
	project.ID = projectID
	project.OrganizationID = orgID
	project.Name = firstNonEmpty(project.Name, projectID)
	if project.CreatedAt.IsZero() {
		project.CreatedAt = time.Now().UTC()
	}
	s.projects[projectID] = project
}

func (s *Store) ClaimRunAttempt(runID, attemptID, claimedBy string, leaseExpiresAt time.Time) (protocol.Run, error) {
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[runID]
	if !ok {
		return protocol.Run{}, app.ErrNotFound
	}
	if app.IsTerminalRunStatus(run.Status) {
		return run, nil
	}
	if attemptID == "" {
		return protocol.Run{}, app.ErrAttemptMismatch
	}
	if run.AttemptID != "" && run.AttemptID != attemptID && (run.LeaseExpiresAt == nil || run.LeaseExpiresAt.After(now)) {
		return protocol.Run{}, app.ErrAttemptMismatch
	}
	if run.AttemptID != attemptID {
		run.AttemptCount++
	}
	run.AttemptID = attemptID
	run.ClaimedBy = claimedBy
	run.LeaseExpiresAt = &leaseExpiresAt
	run.Status = protocol.RunRunning
	if run.StartedAt == nil {
		run.StartedAt = &now
	}
	run.UpdatedAt = now
	s.runs[runID] = run
	return run, nil
}

func isActiveRunStatus(status protocol.RunStatus) bool {
	return status == protocol.RunQueued || status == protocol.RunRunning || status == protocol.RunWaitingForApproval
}

func (s *Store) CheckRunAttempt(runID, attemptID string) (protocol.Run, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	run, ok := s.runs[runID]
	if !ok {
		return protocol.Run{}, app.ErrNotFound
	}
	if run.AttemptID == "" {
		if attemptID == "" {
			return run, nil
		}
		return protocol.Run{}, app.ErrAttemptMismatch
	}
	if attemptID != run.AttemptID {
		return protocol.Run{}, app.ErrAttemptMismatch
	}
	return run, nil
}

func (s *Store) UpdateRunStatus(runID string, status protocol.RunStatus, errMessage string) (protocol.Run, error) {
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[runID]
	if !ok {
		return protocol.Run{}, app.ErrNotFound
	}
	if app.IsTerminalRunStatus(run.Status) && run.Status != status {
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

func (s *Store) SaveRunUsage(runID string, usage protocol.RunUsage) (protocol.RunUsage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[runID]
	if !ok {
		return protocol.RunUsage{}, app.ErrNotFound
	}
	usage = protocol.NormalizeRunUsage(usage)
	s.runUsage[runID] = usage
	run.Usage = usage
	s.runs[runID] = run
	return usage, nil
}

func (s *Store) GetRunUsage(runID string) (protocol.RunUsage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.runs[runID]; !ok {
		return protocol.RunUsage{}, app.ErrNotFound
	}
	return s.runUsage[runID], nil
}

func (s *Store) AddEvent(runID string, typ protocol.RunEventType, message string, payload any) (protocol.RunEvent, error) {
	s.mu.Lock()
	run, ok := s.runs[runID]
	if !ok {
		s.mu.Unlock()
		return protocol.RunEvent{}, app.ErrNotFound
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

func (s *Store) AddWorkspace(workspace protocol.Workspace) (protocol.Workspace, error) {
	if workspace.ID == "" {
		workspace.ID = platform.NewID("ws")
	}
	if workspace.CreatedAt.IsZero() {
		workspace.CreatedAt = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workspaces[workspace.ID] = workspace
	return workspace, nil
}

func (s *Store) GetWorkspace(workspaceID string) (protocol.Workspace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	workspace, ok := s.workspaces[workspaceID]
	if !ok {
		return protocol.Workspace{}, app.ErrNotFound
	}
	return workspace, nil
}

func (s *Store) AddArtifact(artifact protocol.Artifact) (protocol.Artifact, error) {
	if artifact.ID == "" {
		artifact.ID = platform.NewID("art")
	}
	if artifact.CreatedAt.IsZero() {
		artifact.CreatedAt = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if artifact.RunID != "" {
		if run, ok := s.runs[artifact.RunID]; ok {
			artifact.ChatID = firstNonEmpty(artifact.ChatID, run.ChatID)
			artifact.UserID = firstNonEmpty(artifact.UserID, run.UserID)
			artifact.WorkspaceID = firstNonEmpty(artifact.WorkspaceID, run.WorkspaceID)
			if chat, ok := s.chats[run.ChatID]; ok {
				artifact.ProjectID = firstNonEmpty(artifact.ProjectID, chat.ProjectID)
			}
		}
	}
	s.artifacts[artifact.ID] = artifact
	return artifact, nil
}

func (s *Store) ListArtifacts(runID string) []protocol.Artifact {
	s.mu.RLock()
	defer s.mu.RUnlock()
	artifacts := make([]protocol.Artifact, 0)
	for _, artifact := range s.artifacts {
		if artifact.RunID == runID && artifact.DeletedAt == nil {
			artifacts = append(artifacts, artifact)
		}
	}
	sort.Slice(artifacts, func(i, j int) bool {
		return artifacts[i].CreatedAt.Before(artifacts[j].CreatedAt)
	})
	return artifacts
}

func (s *Store) GetArtifact(artifactID string) (protocol.Artifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	artifact, ok := s.artifacts[artifactID]
	if !ok || artifact.DeletedAt != nil {
		return protocol.Artifact{}, app.ErrNotFound
	}
	return artifact, nil
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
		secretMaterials := map[string]protocol.RuntimeSecret{}
		for key, material := range s.skillSecrets[skill.ID] {
			secretMaterials[key] = material
			if material.EncryptedValue != "" {
				secrets[key] = material.EncryptedValue
			}
		}
		runtimeSkills = append(runtimeSkills, protocol.RuntimeSkill{Skill: skill, Secrets: secrets, SecretMaterials: secretMaterials})
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
	skill, secret, hasSecret, err := httpSkillFromInput(skillID, versionID, userID, projectID, "1.0.0", input)
	if err != nil {
		return protocol.Skill{}, err
	}
	skill.Enabled = true
	skill.Status = protocol.SkillStatusEnabled
	_ = now
	s.mu.Lock()
	defer s.mu.Unlock()
	s.skills[skill.ID] = skill
	if hasSecret {
		s.skillSecrets[skill.ID] = map[string]protocol.RuntimeSecret{"bearer_token": secret}
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
		return protocol.Skill{}, app.ErrNotFound
	}
	updated, secret, hasSecret, err := httpSkillFromInput(skillID, platform.NewID("skv"), userID, current.ProjectID, current.Version, input)
	if err != nil {
		return protocol.Skill{}, err
	}
	updated.Enabled = current.Enabled
	updated.Status = current.Status
	s.skills[skillID] = updated
	if hasSecret {
		if s.skillSecrets[skillID] == nil {
			s.skillSecrets[skillID] = map[string]protocol.RuntimeSecret{}
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
		return protocol.Skill{}, app.ErrNotFound
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

func (s *Store) AddAuditEvent(input protocol.AuditEventInput) (protocol.AuditEvent, error) {
	event := auditEventFromInput(input)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.auditEvents = append(s.auditEvents, event)
	return event, nil
}

func (s *Store) ListAuditEvents(actor app.ActorContext, opts app.AuditEventListOptions) []protocol.AuditEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	limit := opts.Limit
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	events := make([]protocol.AuditEvent, 0, limit)
	for i := len(s.auditEvents) - 1; i >= 0 && len(events) < limit; i-- {
		event := s.auditEvents[i]
		if event.ActorUserID != actor.UserID || event.ActorProjectID != actor.ProjectID {
			continue
		}
		if !auditEventMatches(event, opts) {
			continue
		}
		events = append(events, event)
	}
	return events
}

func skillGrantKey(userID, projectID string) string {
	return userID + "\x00" + projectID
}

func identityKey(provider, issuer, subject string) string {
	return provider + "\x00" + issuer + "\x00" + subject
}

func userIdentityKey(provider, userID string) string {
	return provider + "\x00" + userID
}

func orgRoleKey(userID, orgID string) string {
	return userID + "\x00" + orgID
}

func splitOrgRoleKey(key string) (string, string, bool) {
	parts := strings.Split(key, "\x00")
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func projectRoleKey(userID, projectID string) string {
	return userID + "\x00" + projectID
}

func splitProjectRoleKey(key string) (string, string, bool) {
	parts := strings.Split(key, "\x00")
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func usageBucketKey(usage protocol.RunUsage) string {
	return usage.Provider + "\x00" + usage.Model + "\x00" + usage.Currency + "\x00" + formatBool(usage.Estimated) + "\x00" + usage.TokenEstimator
}

func addUsageToBucket(bucket *protocol.RunUsageBucket, usage protocol.RunUsage) {
	bucket.RunCount++
	bucket.InputTokens += usage.InputTokens
	bucket.OutputTokens += usage.OutputTokens
	bucket.ReasoningTokens += usage.ReasoningTokens
	bucket.CachedTokens += usage.CachedTokens
	bucket.TotalTokens += usage.TotalTokens
	bucket.Cost += usage.Cost
	bucket.LatencyMillis += usage.LatencyMillis
	bucket.RetryCount += usage.RetryCount
	bucket.ToolCalls += usage.ToolCalls
	bucket.ToolErrors += usage.ToolErrors
	bucket.SandboxCommands += usage.SandboxCommands
	bucket.SandboxDurationMillis += usage.SandboxDurationMillis
	bucket.SandboxOutputBytes += usage.SandboxOutputBytes
	bucket.SandboxCPUMillis += usage.SandboxCPUMillis
	if usage.SandboxMemoryMaxBytes > bucket.SandboxMemoryMaxBytes {
		bucket.SandboxMemoryMaxBytes = usage.SandboxMemoryMaxBytes
	}
	bucket.ArtifactCount += usage.ArtifactCount
	bucket.ArtifactBytes += usage.ArtifactBytes
}

func sortRunUsageBuckets(buckets []protocol.RunUsageBucket) {
	sort.Slice(buckets, func(i, j int) bool {
		if buckets[i].Provider != buckets[j].Provider {
			return buckets[i].Provider < buckets[j].Provider
		}
		if buckets[i].Model != buckets[j].Model {
			return buckets[i].Model < buckets[j].Model
		}
		if buckets[i].Currency != buckets[j].Currency {
			return buckets[i].Currency < buckets[j].Currency
		}
		if buckets[i].Estimated != buckets[j].Estimated {
			return !buckets[i].Estimated
		}
		return buckets[i].TokenEstimator < buckets[j].TokenEstimator
	})
}

func formatBool(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func normalizeMemberRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "owner", "admin", "member", "editor", "writer", "viewer":
		return strings.ToLower(strings.TrimSpace(role))
	default:
		return ""
	}
}

func appendUnique(values []string, next string) []string {
	for _, value := range values {
		if value == next {
			return values
		}
	}
	return append(values, next)
}

func httpSkillFromInput(skillID, versionID, userID, projectID, version string, input protocol.HTTPSkillInput) (protocol.Skill, protocol.RuntimeSecret, bool, error) {
	if err := skillmanifest.ValidateHTTPSkillInput(input); err != nil {
		return protocol.Skill{}, protocol.RuntimeSecret{}, false, err
	}
	config, err := skillmanifest.NewHTTPSkillRuntimeConfig(input)
	if err != nil {
		return protocol.Skill{}, protocol.RuntimeSecret{}, false, err
	}
	annotations := `{"readOnlyHint":false,"destructiveHint":false,"idempotentHint":false,"openWorldHint":true}`
	inputSchema := strings.TrimSpace(input.InputSchema)
	if inputSchema == "" {
		inputSchema = `{"type":"object","additionalProperties":true}`
	}
	inputSchema, _ = skillmanifest.NormalizeJSON(inputSchema)
	outputSchema, _ := skillmanifest.NormalizeJSON(input.OutputSchema)
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
		OutputSchema:     outputSchema,
		Annotations:      annotations,
		RuntimeConfig:    config.JSONString(),
		Enabled:          true,
	}
	secret := protocol.RuntimeSecret{EncryptedValue: strings.TrimSpace(input.BearerToken)}
	if secret.EncryptedValue == "" {
		secret.SecretRef = strings.TrimSpace(input.BearerTokenSecretRef)
	}
	return skill, secret, secret.EncryptedValue != "" || secret.SecretRef != "", nil
}

func redactSkill(skill protocol.Skill) protocol.Skill {
	return skill
}

func auditEventFromInput(input protocol.AuditEventInput) protocol.AuditEvent {
	decision := input.Decision
	if decision == "" {
		decision = protocol.AuditDecisionAllow
	}
	return protocol.AuditEvent{
		ID:             platform.NewID("audit"),
		ActorUserID:    input.ActorUserID,
		ActorProjectID: input.ActorProjectID,
		ActorOrgID:     input.ActorOrgID,
		Action:         input.Action,
		ResourceType:   input.ResourceType,
		ResourceID:     input.ResourceID,
		Decision:       decision,
		Reason:         input.Reason,
		RequestID:      input.RequestID,
		TraceID:        input.TraceID,
		RunID:          input.RunID,
		IP:             input.IP,
		UserAgent:      input.UserAgent,
		Metadata:       platform.RedactMap(input.Metadata),
		CreatedAt:      time.Now().UTC(),
	}
}

func auditEventMatches(event protocol.AuditEvent, opts app.AuditEventListOptions) bool {
	if opts.RequestID != "" && event.RequestID != opts.RequestID {
		return false
	}
	if opts.RunID != "" && event.RunID != opts.RunID {
		return false
	}
	if opts.Action != "" && event.Action != opts.Action {
		return false
	}
	if opts.ResourceID != "" && event.ResourceID != opts.ResourceID {
		return false
	}
	return true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func titleFromContent(content string) string {
	const max = 42
	if len(content) <= max {
		return content
	}
	return content[:max] + "..."
}
