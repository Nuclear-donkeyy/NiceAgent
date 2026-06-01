package repository

import (
	"database/sql"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"niceagent/common/platform"
	"niceagent/common/protocol"
	"niceagent/control-plane/internal/app"
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

func (s *PostgresStore) ListChats(userID, projectID string, opts app.ChatListOptions) []protocol.ChatSession {
	query := "%" + strings.ToLower(strings.TrimSpace(opts.Query)) + "%"
	rows, err := s.db.Query(`
		SELECT c.id, c.user_id, c.project_id, c.title, c.archived, COALESCE(c.last_run_id, ''),
		       c.created_at, c.updated_at, COUNT(m.id)
		FROM chat_sessions c
		LEFT JOIN messages m ON m.chat_id = c.id
		WHERE c.user_id = $1
		  AND c.project_id = $2
		  AND ($3 OR c.archived = false)
		  AND ($4 = '%%' OR LOWER(c.title) LIKE $4)
		GROUP BY c.id
		ORDER BY c.updated_at DESC`, userID, projectID, opts.IncludeArchived, query)
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

func (s *PostgresStore) CreateChat(userID, projectID, title string) (protocol.ChatSession, error) {
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
			return protocol.ChatSession{}, nil, app.ErrNotFound
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
		return protocol.ChatSession{}, app.ErrNotFound
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
			return protocol.Message{}, protocol.Run{}, app.ErrNotFound
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
	if _, err := tx.Exec(`INSERT INTO workspaces (id, user_id, project_id, chat_id, run_id, root_path, created_at) VALUES ($1, $2, (SELECT project_id FROM chat_sessions WHERE id = $3), $3, $4, $5, $6)`,
		run.WorkspaceID, run.UserID, run.ChatID, run.ID, run.WorkspaceID, now); err != nil {
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
	var startedAt, finishedAt, leaseExpiresAt sql.NullTime
	var errText, attemptID, claimedBy sql.NullString
	if err := s.db.QueryRow(`
		SELECT id, chat_id, user_id, workspace_id, active_attempt_id, claimed_by, lease_expires_at, attempt_count,
		       status, error, created_at, updated_at, started_at, finished_at
		FROM runs WHERE id = $1`, runID).Scan(
		&run.ID, &run.ChatID, &run.UserID, &run.WorkspaceID, &attemptID, &claimedBy, &leaseExpiresAt, &run.AttemptCount,
		&run.Status, &errText, &run.CreatedAt, &run.UpdatedAt, &startedAt, &finishedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return protocol.Run{}, app.ErrNotFound
		}
		return protocol.Run{}, err
	}
	if errText.Valid {
		run.Error = errText.String
	}
	if attemptID.Valid {
		run.AttemptID = attemptID.String
	}
	if claimedBy.Valid {
		run.ClaimedBy = claimedBy.String
	}
	if leaseExpiresAt.Valid {
		run.LeaseExpiresAt = &leaseExpiresAt.Time
	}
	if startedAt.Valid {
		run.StartedAt = &startedAt.Time
	}
	if finishedAt.Valid {
		run.FinishedAt = &finishedAt.Time
	}
	usage, err := s.getRunUsage(runID)
	if err != nil {
		return protocol.Run{}, err
	}
	run.Usage = usage
	return run, nil
}

func (s *PostgresStore) CountActiveRuns(userID, projectID string) int {
	var count int
	err := s.db.QueryRow(`
		SELECT COUNT(*)
		FROM runs r
		JOIN chat_sessions c ON c.id = r.chat_id
		WHERE r.user_id = $1
		  AND c.project_id = $2
		  AND r.status IN ($3, $4, $5)`,
		userID, projectID, protocol.RunQueued, protocol.RunRunning, protocol.RunWaitingForApproval,
	).Scan(&count)
	if err != nil {
		return 0
	}
	return count
}

func (s *PostgresStore) CountRunsCreatedSince(userID, projectID string, since time.Time) int {
	var count int
	err := s.db.QueryRow(`
		SELECT COUNT(*)
		FROM runs r
		JOIN chat_sessions c ON c.id = r.chat_id
		WHERE r.user_id = $1
		  AND c.project_id = $2
		  AND r.created_at >= $3`,
		userID, projectID, since,
	).Scan(&count)
	if err != nil {
		return 0
	}
	return count
}

func (s *PostgresStore) SumRunUsageTokensSince(userID, projectID string, since time.Time) int {
	return s.SumRunUsageSince(userID, projectID, since).TotalTokens
}

func (s *PostgresStore) SumRunUsageSince(userID, projectID string, since time.Time) protocol.RunUsage {
	var usage protocol.RunUsage
	err := s.db.QueryRow(`
		SELECT
			COALESCE(SUM(ru.input_tokens), 0),
			COALESCE(SUM(ru.output_tokens), 0),
			COALESCE(SUM(ru.reasoning_tokens), 0),
			COALESCE(SUM(ru.cached_tokens), 0),
			COALESCE(SUM(
				CASE
					WHEN ru.total_tokens > 0 THEN ru.total_tokens
					ELSE ru.input_tokens + ru.output_tokens
				END
			), 0),
			COALESCE(SUM(ru.cost), 0),
			COALESCE(SUM(ru.latency_millis), 0),
			COALESCE(SUM(ru.retry_count), 0),
			COALESCE(SUM(ru.tool_calls), 0),
			COALESCE(SUM(ru.tool_errors), 0),
			COALESCE(SUM(ru.sandbox_commands), 0),
			COALESCE(SUM(ru.sandbox_duration_millis), 0),
			COALESCE(SUM(ru.sandbox_output_bytes), 0),
			COALESCE(SUM(ru.sandbox_cpu_millis), 0),
			COALESCE(MAX(ru.sandbox_memory_max_bytes), 0),
			COALESCE(SUM(ru.artifact_count), 0),
			COALESCE(SUM(ru.artifact_bytes), 0)
		FROM run_usage ru
		JOIN runs r ON r.id = ru.run_id
		JOIN chat_sessions c ON c.id = r.chat_id
		WHERE r.user_id = $1
		  AND c.project_id = $2
		  AND r.created_at >= $3`,
		userID, projectID, since,
	).Scan(
		&usage.InputTokens,
		&usage.OutputTokens,
		&usage.ReasoningTokens,
		&usage.CachedTokens,
		&usage.TotalTokens,
		&usage.Cost,
		&usage.LatencyMillis,
		&usage.RetryCount,
		&usage.ToolCalls,
		&usage.ToolErrors,
		&usage.SandboxCommands,
		&usage.SandboxDurationMillis,
		&usage.SandboxOutputBytes,
		&usage.SandboxCPUMillis,
		&usage.SandboxMemoryMaxBytes,
		&usage.ArtifactCount,
		&usage.ArtifactBytes,
	)
	if err != nil {
		return protocol.RunUsage{}
	}
	return protocol.NormalizeRunUsage(usage)
}

func (s *PostgresStore) ListRunUsageBucketsSince(projectID string, since time.Time) []protocol.RunUsageBucket {
	rows, err := s.db.Query(`
		SELECT
			ru.provider,
			ru.model,
			ru.currency,
			ru.estimated,
			COALESCE(ru.token_estimator, ''),
			COUNT(*),
			COALESCE(SUM(ru.input_tokens), 0),
			COALESCE(SUM(ru.output_tokens), 0),
			COALESCE(SUM(ru.reasoning_tokens), 0),
			COALESCE(SUM(ru.cached_tokens), 0),
			COALESCE(SUM(
				CASE
					WHEN ru.total_tokens > 0 THEN ru.total_tokens
					ELSE ru.input_tokens + ru.output_tokens
				END
			), 0),
			COALESCE(SUM(ru.cost), 0),
			COALESCE(SUM(ru.latency_millis), 0),
			COALESCE(SUM(ru.retry_count), 0),
			COALESCE(SUM(ru.tool_calls), 0),
			COALESCE(SUM(ru.tool_errors), 0),
			COALESCE(SUM(ru.sandbox_commands), 0),
			COALESCE(SUM(ru.sandbox_duration_millis), 0),
			COALESCE(SUM(ru.sandbox_output_bytes), 0),
			COALESCE(SUM(ru.sandbox_cpu_millis), 0),
			COALESCE(MAX(ru.sandbox_memory_max_bytes), 0),
			COALESCE(SUM(ru.artifact_count), 0),
			COALESCE(SUM(ru.artifact_bytes), 0)
		FROM run_usage ru
		JOIN runs r ON r.id = ru.run_id
		JOIN chat_sessions c ON c.id = r.chat_id
		WHERE c.project_id = $1
		  AND r.created_at >= $2
		GROUP BY ru.provider, ru.model, ru.currency, ru.estimated, COALESCE(ru.token_estimator, '')
		ORDER BY ru.provider, ru.model, ru.currency, ru.estimated, COALESCE(ru.token_estimator, '')`,
		projectID, since,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()
	buckets := []protocol.RunUsageBucket{}
	for rows.Next() {
		var bucket protocol.RunUsageBucket
		if err := rows.Scan(
			&bucket.Provider,
			&bucket.Model,
			&bucket.Currency,
			&bucket.Estimated,
			&bucket.TokenEstimator,
			&bucket.RunCount,
			&bucket.InputTokens,
			&bucket.OutputTokens,
			&bucket.ReasoningTokens,
			&bucket.CachedTokens,
			&bucket.TotalTokens,
			&bucket.Cost,
			&bucket.LatencyMillis,
			&bucket.RetryCount,
			&bucket.ToolCalls,
			&bucket.ToolErrors,
			&bucket.SandboxCommands,
			&bucket.SandboxDurationMillis,
			&bucket.SandboxOutputBytes,
			&bucket.SandboxCPUMillis,
			&bucket.SandboxMemoryMaxBytes,
			&bucket.ArtifactCount,
			&bucket.ArtifactBytes,
		); err == nil {
			buckets = append(buckets, bucket)
		}
	}
	return buckets
}

func (s *PostgresStore) ProjectBelongsToOrganization(projectID, orgID string) bool {
	var exists bool
	err := s.db.QueryRow(`
		SELECT EXISTS (
			SELECT 1
			FROM projects
			WHERE id = $1 AND organization_id = $2
		)`, projectID, orgID).Scan(&exists)
	return err == nil && exists
}

func (s *PostgresStore) BindUserIdentity(identity protocol.UserIdentity) (protocol.UserIdentity, error) {
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
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.UserIdentity{}, err
	}
	defer tx.Rollback()
	var existing protocol.UserIdentity
	err = tx.QueryRow(`
		SELECT id, user_id, provider, issuer, subject, COALESCE(email, ''), COALESCE(name, ''), created_at, updated_at
		FROM user_identities
		WHERE provider = $1 AND issuer = $2 AND subject = $3
		FOR UPDATE`, identity.Provider, identity.Issuer, identity.Subject).Scan(
		&existing.ID, &existing.UserID, &existing.Provider, &existing.Issuer, &existing.Subject,
		&existing.Email, &existing.Name, &existing.CreatedAt, &existing.UpdatedAt,
	)
	if err == nil {
		if existing.UserID != identity.UserID {
			return protocol.UserIdentity{}, app.ErrIdentityConflict
		}
		existing.Email = firstNonEmpty(identity.Email, existing.Email)
		existing.Name = firstNonEmpty(identity.Name, existing.Name)
		existing.UpdatedAt = now
		if _, err := tx.Exec(`
			UPDATE user_identities
			SET email = NULLIF($1, ''), name = NULLIF($2, ''), updated_at = $3
			WHERE id = $4`, existing.Email, existing.Name, existing.UpdatedAt, existing.ID); err != nil {
			return protocol.UserIdentity{}, err
		}
		if err := tx.Commit(); err != nil {
			return protocol.UserIdentity{}, err
		}
		return existing, nil
	}
	if err != sql.ErrNoRows {
		return protocol.UserIdentity{}, err
	}
	var conflictingID string
	err = tx.QueryRow(`
		SELECT id
		FROM user_identities
		WHERE provider = $1 AND user_id = $2
		FOR UPDATE`, identity.Provider, identity.UserID).Scan(&conflictingID)
	if err == nil {
		return protocol.UserIdentity{}, app.ErrIdentityConflict
	}
	if err != sql.ErrNoRows {
		return protocol.UserIdentity{}, err
	}
	email := firstNonEmpty(identity.Email, identity.UserID+"@niceagent.local")
	name := firstNonEmpty(identity.Name, identity.UserID)
	if _, err := tx.Exec(`
		INSERT INTO users (id, email, name, status, created_at)
		VALUES ($1, $2, $3, 'active', $4)
		ON CONFLICT (id)
		DO UPDATE SET email = EXCLUDED.email, name = EXCLUDED.name, status = 'active'`,
		identity.UserID, email, name, now); err != nil {
		return protocol.UserIdentity{}, err
	}
	identity.ID = platform.NewID("uid")
	identity.CreatedAt = now
	identity.UpdatedAt = now
	if _, err := tx.Exec(`
		INSERT INTO user_identities (
			id, user_id, provider, issuer, subject, email, name, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), NULLIF($7, ''), $8, $9)`,
		identity.ID, identity.UserID, identity.Provider, identity.Issuer, identity.Subject,
		identity.Email, identity.Name, identity.CreatedAt, identity.UpdatedAt); err != nil {
		return protocol.UserIdentity{}, err
	}
	if err := tx.Commit(); err != nil {
		return protocol.UserIdentity{}, err
	}
	return identity, nil
}

func (s *PostgresStore) ListOrganizationRoles(userID, orgID string) []string {
	rows, err := s.db.Query(`
		SELECT role
		FROM organization_members
		WHERE user_id = $1 AND organization_id = $2
		ORDER BY role`, userID, orgID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var roles []string
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err == nil && strings.TrimSpace(role) != "" {
			roles = append(roles, role)
		}
	}
	return roles
}

func (s *PostgresStore) ListProjectRoles(userID, projectID string) []string {
	rows, err := s.db.Query(`
		SELECT role
		FROM project_members
		WHERE user_id = $1 AND project_id = $2
		ORDER BY role`, userID, projectID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var roles []string
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err == nil && strings.TrimSpace(role) != "" {
			roles = append(roles, role)
		}
	}
	return roles
}

func (s *PostgresStore) ListOrganizationMembers(orgID string) []protocol.OrganizationMember {
	rows, err := s.db.Query(`
		SELECT om.id, om.user_id, om.organization_id, om.role, u.email, u.name, om.created_at, om.updated_at
		FROM organization_members om
		JOIN users u ON u.id = om.user_id
		WHERE om.organization_id = $1
		ORDER BY om.role, u.name, om.user_id`, orgID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	members := []protocol.OrganizationMember{}
	for rows.Next() {
		var member protocol.OrganizationMember
		if err := rows.Scan(&member.ID, &member.UserID, &member.OrganizationID, &member.Role, &member.Email, &member.Name, &member.CreatedAt, &member.UpdatedAt); err == nil {
			members = append(members, member)
		}
	}
	return members
}

func (s *PostgresStore) UpsertOrganizationMember(orgID string, input protocol.OrganizationMemberInput) (protocol.OrganizationMember, error) {
	userID := strings.TrimSpace(input.UserID)
	role := normalizeMemberRole(input.Role)
	if userID == "" || role == "" {
		return protocol.OrganizationMember{}, app.ErrInvalidInput
	}
	now := time.Now().UTC()
	email := strings.TrimSpace(input.Email)
	if email == "" {
		email = userID + "@niceagent.local"
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = userID
	}
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.OrganizationMember{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
		INSERT INTO users (id, email, name, status, created_at)
		VALUES ($1, $2, $3, 'active', $4)
		ON CONFLICT (id)
		DO UPDATE SET email = EXCLUDED.email, name = EXCLUDED.name, status = 'active'`,
		userID, email, name, now); err != nil {
		return protocol.OrganizationMember{}, err
	}
	var memberID string
	if err := tx.QueryRow(`
		INSERT INTO organization_members (id, user_id, organization_id, role, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id, organization_id)
		DO UPDATE SET role = EXCLUDED.role, updated_at = EXCLUDED.updated_at
		RETURNING id`,
		platform.NewID("orgmem"), userID, orgID, role, now, now).Scan(&memberID); err != nil {
		return protocol.OrganizationMember{}, err
	}
	if err := tx.Commit(); err != nil {
		return protocol.OrganizationMember{}, err
	}
	return s.getOrganizationMember(orgID, userID)
}

func (s *PostgresStore) RemoveOrganizationMember(orgID, userID string) (protocol.OrganizationMember, error) {
	member, err := s.getOrganizationMember(orgID, userID)
	if err != nil {
		return protocol.OrganizationMember{}, err
	}
	result, err := s.db.Exec(`DELETE FROM organization_members WHERE organization_id = $1 AND user_id = $2`, orgID, userID)
	if err != nil {
		return protocol.OrganizationMember{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return protocol.OrganizationMember{}, err
	}
	if affected == 0 {
		return protocol.OrganizationMember{}, app.ErrNotFound
	}
	return member, nil
}

func (s *PostgresStore) ListInvitations(orgID string) []protocol.Invitation {
	rows, err := s.db.Query(`
		SELECT id, COALESCE(project_id, ''), email, role,
		       CASE WHEN status = 'pending' AND expires_at <= now() THEN 'expired' ELSE status END,
		       invited_by_user_id, COALESCE(accepted_by_user_id, ''), created_at, expires_at, accepted_at
		FROM invitations
		WHERE organization_id = $1
		ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	invitations := []protocol.Invitation{}
	for rows.Next() {
		var invitation protocol.Invitation
		var status string
		var acceptedAt sql.NullTime
		if err := rows.Scan(
			&invitation.ID,
			&invitation.ProjectID,
			&invitation.Email,
			&invitation.Role,
			&status,
			&invitation.InvitedByUserID,
			&invitation.AcceptedByUserID,
			&invitation.CreatedAt,
			&invitation.ExpiresAt,
			&acceptedAt,
		); err == nil {
			invitation.OrganizationID = orgID
			invitation.Status = protocol.InvitationStatus(status)
			if acceptedAt.Valid {
				invitation.AcceptedAt = &acceptedAt.Time
			}
			invitations = append(invitations, invitation)
		}
	}
	return invitations
}

func (s *PostgresStore) CreateInvitation(orgID, invitedByUserID string, input protocol.InvitationInput) (protocol.Invitation, error) {
	email := strings.ToLower(strings.TrimSpace(input.Email))
	role := normalizeMemberRole(input.Role)
	projectID := strings.TrimSpace(input.ProjectID)
	if email == "" || !strings.Contains(email, "@") || role == "" {
		return protocol.Invitation{}, app.ErrInvalidInput
	}
	if projectID != "" && !s.ProjectBelongsToOrganization(projectID, orgID) {
		return protocol.Invitation{}, app.ErrInvalidInput
	}
	expiresInHours := input.ExpiresInHours
	if expiresInHours <= 0 {
		expiresInHours = 168
	}
	if expiresInHours > 24*30 {
		return protocol.Invitation{}, app.ErrInvalidInput
	}
	now := time.Now().UTC()
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
	var nullableProjectID any
	if projectID != "" {
		nullableProjectID = projectID
	}
	if _, err := s.db.Exec(`
		INSERT INTO invitations (
			id, token, organization_id, project_id, email, role, status,
			invited_by_user_id, created_at, expires_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		invitation.ID, invitation.Token, orgID, nullableProjectID, email, role,
		invitation.Status, invitedByUserID, invitation.CreatedAt, invitation.ExpiresAt,
	); err != nil {
		return protocol.Invitation{}, err
	}
	return invitation, nil
}

func (s *PostgresStore) AcceptInvitation(token, userID, email, name string) (protocol.Invitation, error) {
	token = strings.TrimSpace(token)
	userID = strings.TrimSpace(userID)
	email = strings.ToLower(strings.TrimSpace(email))
	if token == "" || userID == "" || email == "" {
		return protocol.Invitation{}, app.ErrInvalidInput
	}
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.Invitation{}, err
	}
	defer tx.Rollback()
	var invitation protocol.Invitation
	var projectID sql.NullString
	var status string
	if err := tx.QueryRow(`
		SELECT id, organization_id, project_id, email, role, status, invited_by_user_id, created_at, expires_at
		FROM invitations
		WHERE token = $1
		FOR UPDATE`, token).Scan(
		&invitation.ID,
		&invitation.OrganizationID,
		&projectID,
		&invitation.Email,
		&invitation.Role,
		&status,
		&invitation.InvitedByUserID,
		&invitation.CreatedAt,
		&invitation.ExpiresAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return protocol.Invitation{}, app.ErrNotFound
		}
		return protocol.Invitation{}, err
	}
	if projectID.Valid {
		invitation.ProjectID = projectID.String
	}
	if protocol.InvitationStatus(status) != protocol.InvitationPending {
		return protocol.Invitation{}, app.ErrInvalidInput
	}
	if !invitation.ExpiresAt.After(now) {
		_, _ = tx.Exec(`UPDATE invitations SET status = 'expired' WHERE id = $1`, invitation.ID)
		if err := tx.Commit(); err != nil {
			return protocol.Invitation{}, err
		}
		return protocol.Invitation{}, app.ErrInvalidInput
	}
	if email != invitation.Email {
		return protocol.Invitation{}, app.ErrInvalidInput
	}
	displayName := strings.TrimSpace(name)
	if displayName == "" {
		displayName = userID
	}
	if _, err := tx.Exec(`
		INSERT INTO users (id, email, name, status, created_at)
		VALUES ($1, $2, $3, 'active', $4)
		ON CONFLICT (id)
		DO UPDATE SET email = EXCLUDED.email, name = EXCLUDED.name, status = 'active'`,
		userID, invitation.Email, displayName, now); err != nil {
		return protocol.Invitation{}, err
	}
	if invitation.ProjectID != "" {
		if _, err := tx.Exec(`
			INSERT INTO project_members (id, user_id, project_id, role, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (user_id, project_id)
			DO UPDATE SET role = EXCLUDED.role, updated_at = EXCLUDED.updated_at`,
			platform.NewID("prjmem"), userID, invitation.ProjectID, invitation.Role, now, now); err != nil {
			return protocol.Invitation{}, err
		}
	} else {
		if _, err := tx.Exec(`
			INSERT INTO organization_members (id, user_id, organization_id, role, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (user_id, organization_id)
			DO UPDATE SET role = EXCLUDED.role, updated_at = EXCLUDED.updated_at`,
			platform.NewID("orgmem"), userID, invitation.OrganizationID, invitation.Role, now, now); err != nil {
			return protocol.Invitation{}, err
		}
	}
	if _, err := tx.Exec(`
		UPDATE invitations
		SET status = 'accepted', accepted_by_user_id = $1, accepted_at = $2
		WHERE id = $3`, userID, now, invitation.ID); err != nil {
		return protocol.Invitation{}, err
	}
	if err := tx.Commit(); err != nil {
		return protocol.Invitation{}, err
	}
	invitation.Status = protocol.InvitationAccepted
	invitation.AcceptedByUserID = userID
	invitation.AcceptedAt = &now
	return invitation, nil
}

func (s *PostgresStore) ListProjectMembers(projectID string) []protocol.ProjectMember {
	rows, err := s.db.Query(`
		SELECT pm.id, pm.user_id, pm.project_id, pm.role, u.email, u.name, pm.created_at, pm.updated_at
		FROM project_members pm
		JOIN users u ON u.id = pm.user_id
		WHERE pm.project_id = $1
		ORDER BY pm.role, u.name, pm.user_id`, projectID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	members := []protocol.ProjectMember{}
	for rows.Next() {
		var member protocol.ProjectMember
		if err := rows.Scan(&member.ID, &member.UserID, &member.ProjectID, &member.Role, &member.Email, &member.Name, &member.CreatedAt, &member.UpdatedAt); err == nil {
			members = append(members, member)
		}
	}
	return members
}

func (s *PostgresStore) UpsertProjectMember(projectID string, input protocol.ProjectMemberInput) (protocol.ProjectMember, error) {
	userID := strings.TrimSpace(input.UserID)
	role := normalizeMemberRole(input.Role)
	if userID == "" || role == "" {
		return protocol.ProjectMember{}, app.ErrInvalidInput
	}
	now := time.Now().UTC()
	email := strings.TrimSpace(input.Email)
	if email == "" {
		email = userID + "@niceagent.local"
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = userID
	}
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.ProjectMember{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
		INSERT INTO users (id, email, name, status, created_at)
		VALUES ($1, $2, $3, 'active', $4)
		ON CONFLICT (id)
		DO UPDATE SET email = EXCLUDED.email, name = EXCLUDED.name, status = 'active'`,
		userID, email, name, now); err != nil {
		return protocol.ProjectMember{}, err
	}
	var memberID string
	if err := tx.QueryRow(`
		INSERT INTO project_members (id, user_id, project_id, role, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id, project_id)
		DO UPDATE SET role = EXCLUDED.role, updated_at = EXCLUDED.updated_at
		RETURNING id`,
		platform.NewID("prjmem"), userID, projectID, role, now, now).Scan(&memberID); err != nil {
		return protocol.ProjectMember{}, err
	}
	if err := tx.Commit(); err != nil {
		return protocol.ProjectMember{}, err
	}
	return s.getProjectMember(projectID, userID)
}

func (s *PostgresStore) RemoveProjectMember(projectID, userID string) (protocol.ProjectMember, error) {
	member, err := s.getProjectMember(projectID, userID)
	if err != nil {
		return protocol.ProjectMember{}, err
	}
	result, err := s.db.Exec(`DELETE FROM project_members WHERE project_id = $1 AND user_id = $2`, projectID, userID)
	if err != nil {
		return protocol.ProjectMember{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return protocol.ProjectMember{}, err
	}
	if affected == 0 {
		return protocol.ProjectMember{}, app.ErrNotFound
	}
	return member, nil
}

func (s *PostgresStore) GetProjectQuotaPolicy(projectID string) (protocol.ProjectQuotaPolicy, bool) {
	var policy protocol.ProjectQuotaPolicy
	err := s.db.QueryRow(`
		SELECT project_id, max_concurrent_runs, max_runs_per_hour, max_model_tokens_per_day,
		       max_tool_calls_per_day, max_sandbox_seconds_per_day, created_at, updated_at
		FROM project_quota_policies
		WHERE project_id = $1`, projectID).Scan(
		&policy.ProjectID, &policy.MaxConcurrentRuns, &policy.MaxRunsPerHour, &policy.MaxModelTokensPerDay,
		&policy.MaxToolCallsPerDay, &policy.MaxSandboxSecondsPerDay, &policy.CreatedAt, &policy.UpdatedAt,
	)
	if err != nil {
		return protocol.ProjectQuotaPolicy{}, false
	}
	return policy, true
}

func (s *PostgresStore) SetProjectQuotaPolicy(projectID string, input protocol.ProjectQuotaPolicyInput) (protocol.ProjectQuotaPolicy, error) {
	if input.MaxConcurrentRuns < 0 || input.MaxRunsPerHour < 0 || input.MaxModelTokensPerDay < 0 ||
		input.MaxToolCallsPerDay < 0 || input.MaxSandboxSecondsPerDay < 0 {
		return protocol.ProjectQuotaPolicy{}, app.ErrInvalidInput
	}
	now := time.Now().UTC()
	var policy protocol.ProjectQuotaPolicy
	err := s.db.QueryRow(`
		INSERT INTO project_quota_policies (
			project_id, max_concurrent_runs, max_runs_per_hour, max_model_tokens_per_day,
			max_tool_calls_per_day, max_sandbox_seconds_per_day, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (project_id)
		DO UPDATE SET
			max_concurrent_runs = EXCLUDED.max_concurrent_runs,
			max_runs_per_hour = EXCLUDED.max_runs_per_hour,
			max_model_tokens_per_day = EXCLUDED.max_model_tokens_per_day,
			max_tool_calls_per_day = EXCLUDED.max_tool_calls_per_day,
			max_sandbox_seconds_per_day = EXCLUDED.max_sandbox_seconds_per_day,
			updated_at = EXCLUDED.updated_at
		RETURNING project_id, max_concurrent_runs, max_runs_per_hour, max_model_tokens_per_day,
		          max_tool_calls_per_day, max_sandbox_seconds_per_day, created_at, updated_at`,
		projectID, input.MaxConcurrentRuns, input.MaxRunsPerHour, input.MaxModelTokensPerDay,
		input.MaxToolCallsPerDay, input.MaxSandboxSecondsPerDay, now, now).Scan(
		&policy.ProjectID, &policy.MaxConcurrentRuns, &policy.MaxRunsPerHour, &policy.MaxModelTokensPerDay,
		&policy.MaxToolCallsPerDay, &policy.MaxSandboxSecondsPerDay, &policy.CreatedAt, &policy.UpdatedAt,
	)
	if err != nil {
		return protocol.ProjectQuotaPolicy{}, err
	}
	return policy, nil
}

func (s *PostgresStore) ClaimRunAttempt(runID, attemptID, claimedBy string, leaseExpiresAt time.Time) (protocol.Run, error) {
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.Run{}, err
	}
	defer tx.Rollback()
	var status protocol.RunStatus
	var currentAttempt sql.NullString
	var currentLease sql.NullTime
	if err := tx.QueryRow(`SELECT status, active_attempt_id, lease_expires_at FROM runs WHERE id = $1 FOR UPDATE`, runID).Scan(&status, &currentAttempt, &currentLease); err != nil {
		if err == sql.ErrNoRows {
			return protocol.Run{}, app.ErrNotFound
		}
		return protocol.Run{}, err
	}
	if app.IsTerminalRunStatus(status) {
		if err := tx.Commit(); err != nil {
			return protocol.Run{}, err
		}
		return s.GetRun(runID)
	}
	if attemptID == "" {
		return protocol.Run{}, app.ErrAttemptMismatch
	}
	if currentAttempt.Valid && currentAttempt.String != attemptID && (!currentLease.Valid || currentLease.Time.After(now)) {
		return protocol.Run{}, app.ErrAttemptMismatch
	}
	if _, err := tx.Exec(`
		UPDATE runs
		SET active_attempt_id = $1,
		    claimed_by = NULLIF($2, ''),
		    lease_expires_at = $3,
		    attempt_count = attempt_count + CASE WHEN active_attempt_id IS DISTINCT FROM $1 THEN 1 ELSE 0 END,
		    status = $4,
		    started_at = COALESCE(started_at, $5),
		    updated_at = $5
		WHERE id = $6`,
		attemptID, claimedBy, leaseExpiresAt, protocol.RunRunning, now, runID); err != nil {
		return protocol.Run{}, err
	}
	if err := tx.Commit(); err != nil {
		return protocol.Run{}, err
	}
	return s.GetRun(runID)
}

func (s *PostgresStore) CheckRunAttempt(runID, attemptID string) (protocol.Run, error) {
	run, err := s.GetRun(runID)
	if err != nil {
		return protocol.Run{}, err
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

func (s *PostgresStore) UpdateRunStatus(runID string, status protocol.RunStatus, errMessage string) (protocol.Run, error) {
	run, err := s.GetRun(runID)
	if err != nil {
		return protocol.Run{}, err
	}
	if app.IsTerminalRunStatus(run.Status) && run.Status != status {
		return run, nil
	}
	now := time.Now().UTC()
	startedAt := run.StartedAt
	finishedAt := run.FinishedAt
	if status == protocol.RunRunning && startedAt == nil {
		startedAt = &now
	}
	if app.IsTerminalRunStatus(status) && finishedAt == nil {
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

func (s *PostgresStore) SaveRunUsage(runID string, usage protocol.RunUsage) (protocol.RunUsage, error) {
	if _, err := s.GetRun(runID); err != nil {
		return protocol.RunUsage{}, err
	}
	usage = protocol.NormalizeRunUsage(usage)
	_, err := s.db.Exec(`
		INSERT INTO run_usage (
			run_id, provider, model, input_tokens, output_tokens, reasoning_tokens, cached_tokens,
			total_tokens, estimated, token_estimator, cost, currency, latency_millis, retry_count, fallback_from,
			fallback_to, error_class, tool_calls, tool_errors, sandbox_commands, sandbox_duration_millis,
			sandbox_output_bytes, sandbox_cpu_millis, sandbox_memory_max_bytes, artifact_count,
			artifact_bytes, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16,
			$17, $18, $19, $20, $21, $22, $23, $24, $25, $26, now(), now())
		ON CONFLICT (run_id) DO UPDATE SET
			provider = EXCLUDED.provider,
			model = EXCLUDED.model,
			input_tokens = EXCLUDED.input_tokens,
			output_tokens = EXCLUDED.output_tokens,
			reasoning_tokens = EXCLUDED.reasoning_tokens,
			cached_tokens = EXCLUDED.cached_tokens,
			total_tokens = EXCLUDED.total_tokens,
			estimated = EXCLUDED.estimated,
			token_estimator = EXCLUDED.token_estimator,
			cost = EXCLUDED.cost,
			currency = EXCLUDED.currency,
			latency_millis = EXCLUDED.latency_millis,
			retry_count = EXCLUDED.retry_count,
			fallback_from = EXCLUDED.fallback_from,
			fallback_to = EXCLUDED.fallback_to,
			error_class = EXCLUDED.error_class,
			tool_calls = EXCLUDED.tool_calls,
			tool_errors = EXCLUDED.tool_errors,
			sandbox_commands = EXCLUDED.sandbox_commands,
			sandbox_duration_millis = EXCLUDED.sandbox_duration_millis,
			sandbox_output_bytes = EXCLUDED.sandbox_output_bytes,
			sandbox_cpu_millis = EXCLUDED.sandbox_cpu_millis,
			sandbox_memory_max_bytes = EXCLUDED.sandbox_memory_max_bytes,
			artifact_count = EXCLUDED.artifact_count,
			artifact_bytes = EXCLUDED.artifact_bytes,
			updated_at = now()`,
		runID, usage.Provider, usage.Model, usage.InputTokens, usage.OutputTokens, usage.ReasoningTokens, usage.CachedTokens,
		usage.TotalTokens, usage.Estimated, usage.TokenEstimator, usage.Cost, usage.Currency, usage.LatencyMillis, usage.RetryCount, usage.FallbackFrom,
		usage.FallbackTo, usage.ErrorClass, usage.ToolCalls, usage.ToolErrors, usage.SandboxCommands, usage.SandboxDurationMillis,
		usage.SandboxOutputBytes, usage.SandboxCPUMillis, usage.SandboxMemoryMaxBytes, usage.ArtifactCount, usage.ArtifactBytes)
	if err != nil {
		return protocol.RunUsage{}, err
	}
	return usage, nil
}

func (s *PostgresStore) GetRunUsage(runID string) (protocol.RunUsage, error) {
	var exists bool
	if err := s.db.QueryRow(`SELECT EXISTS (SELECT 1 FROM runs WHERE id = $1)`, runID).Scan(&exists); err != nil {
		return protocol.RunUsage{}, err
	}
	if !exists {
		return protocol.RunUsage{}, app.ErrNotFound
	}
	return s.getRunUsage(runID)
}

func (s *PostgresStore) getRunUsage(runID string) (protocol.RunUsage, error) {
	var usage protocol.RunUsage
	if err := s.db.QueryRow(`
		SELECT provider, model, input_tokens, output_tokens, reasoning_tokens, cached_tokens,
		       total_tokens, estimated, token_estimator, cost, currency, latency_millis, retry_count,
		       fallback_from, fallback_to, error_class, tool_calls, tool_errors,
		       sandbox_commands, sandbox_duration_millis, sandbox_output_bytes,
		       sandbox_cpu_millis, sandbox_memory_max_bytes, artifact_count, artifact_bytes
		FROM run_usage
		WHERE run_id = $1`, runID).Scan(
		&usage.Provider, &usage.Model, &usage.InputTokens, &usage.OutputTokens, &usage.ReasoningTokens, &usage.CachedTokens,
		&usage.TotalTokens, &usage.Estimated, &usage.TokenEstimator, &usage.Cost, &usage.Currency, &usage.LatencyMillis, &usage.RetryCount,
		&usage.FallbackFrom, &usage.FallbackTo, &usage.ErrorClass, &usage.ToolCalls, &usage.ToolErrors,
		&usage.SandboxCommands, &usage.SandboxDurationMillis, &usage.SandboxOutputBytes,
		&usage.SandboxCPUMillis, &usage.SandboxMemoryMaxBytes, &usage.ArtifactCount, &usage.ArtifactBytes,
	); err != nil {
		if err == sql.ErrNoRows {
			return protocol.RunUsage{}, nil
		}
		return protocol.RunUsage{}, err
	}
	return protocol.NormalizeRunUsage(usage), nil
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
			return protocol.RunEvent{}, app.ErrNotFound
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

func (s *PostgresStore) AddWorkspace(workspace protocol.Workspace) (protocol.Workspace, error) {
	if workspace.ID == "" {
		workspace.ID = platform.NewID("ws")
	}
	if workspace.CreatedAt.IsZero() {
		workspace.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.Exec(`
		INSERT INTO workspaces (id, user_id, project_id, chat_id, run_id, root_path, created_at)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''), $6, $7)
		ON CONFLICT (id)
		DO UPDATE SET user_id = EXCLUDED.user_id,
		              project_id = EXCLUDED.project_id,
		              chat_id = EXCLUDED.chat_id,
		              run_id = EXCLUDED.run_id,
		              root_path = EXCLUDED.root_path`,
		workspace.ID, workspace.UserID, workspace.ProjectID, workspace.ChatID, workspace.RunID, workspace.RootPath, workspace.CreatedAt)
	if err != nil {
		return protocol.Workspace{}, err
	}
	return workspace, nil
}

func (s *PostgresStore) GetWorkspace(workspaceID string) (protocol.Workspace, error) {
	var workspace protocol.Workspace
	if err := s.db.QueryRow(`
		SELECT id, user_id, COALESCE(project_id, ''), COALESCE(chat_id, ''), COALESCE(run_id, ''), root_path, created_at
		FROM workspaces
		WHERE id = $1`, workspaceID).Scan(
		&workspace.ID, &workspace.UserID, &workspace.ProjectID, &workspace.ChatID, &workspace.RunID, &workspace.RootPath, &workspace.CreatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return protocol.Workspace{}, app.ErrNotFound
		}
		return protocol.Workspace{}, err
	}
	return workspace, nil
}

func (s *PostgresStore) AddArtifact(artifact protocol.Artifact) (protocol.Artifact, error) {
	if artifact.ID == "" {
		artifact.ID = platform.NewID("art")
	}
	if artifact.CreatedAt.IsZero() {
		artifact.CreatedAt = time.Now().UTC()
	}
	if artifact.RunID != "" && (artifact.ChatID == "" || artifact.UserID == "" || artifact.WorkspaceID == "" || artifact.ProjectID == "") {
		run, err := s.GetRun(artifact.RunID)
		if err == nil {
			artifact.ChatID = firstNonEmpty(artifact.ChatID, run.ChatID)
			artifact.UserID = firstNonEmpty(artifact.UserID, run.UserID)
			artifact.WorkspaceID = firstNonEmpty(artifact.WorkspaceID, run.WorkspaceID)
			if chat, _, err := s.GetChat(run.ChatID); err == nil {
				artifact.ProjectID = firstNonEmpty(artifact.ProjectID, chat.ProjectID)
			}
		}
	}
	_, err := s.db.Exec(`
		INSERT INTO artifacts (id, run_id, chat_id, user_id, project_id, workspace_id, path, name, mime_type, size_bytes, sha256, storage_backend, storage_key, created_at, deleted_at)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, ''), $7, NULLIF($8, ''), $9, $10, NULLIF($11, ''), NULLIF($12, ''), NULLIF($13, ''), $14, $15)
		ON CONFLICT (id)
		DO UPDATE SET path = EXCLUDED.path,
		              name = EXCLUDED.name,
		              mime_type = EXCLUDED.mime_type,
		              size_bytes = EXCLUDED.size_bytes,
		              sha256 = EXCLUDED.sha256,
		              storage_backend = EXCLUDED.storage_backend,
		              storage_key = EXCLUDED.storage_key,
		              deleted_at = EXCLUDED.deleted_at`,
		artifact.ID, artifact.RunID, artifact.ChatID, artifact.UserID, artifact.ProjectID, artifact.WorkspaceID,
		artifact.Path, artifact.Name, artifact.MimeType, artifact.SizeBytes, artifact.SHA256, artifact.StorageBackend, artifact.StorageKey, artifact.CreatedAt, artifact.DeletedAt)
	if err != nil {
		return protocol.Artifact{}, err
	}
	return artifact, nil
}

func (s *PostgresStore) ListArtifacts(runID string) []protocol.Artifact {
	rows, err := s.db.Query(`
		SELECT id, run_id, COALESCE(chat_id, ''), COALESCE(user_id, ''), COALESCE(project_id, ''), COALESCE(workspace_id, ''),
		       path, COALESCE(name, ''), mime_type, size_bytes, COALESCE(sha256, ''), COALESCE(storage_backend, ''), COALESCE(storage_key, ''), created_at, deleted_at
		FROM artifacts
		WHERE run_id = $1 AND deleted_at IS NULL
		ORDER BY created_at`, runID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var artifacts []protocol.Artifact
	for rows.Next() {
		var artifact protocol.Artifact
		var deletedAt sql.NullTime
		if err := rows.Scan(
			&artifact.ID, &artifact.RunID, &artifact.ChatID, &artifact.UserID, &artifact.ProjectID, &artifact.WorkspaceID,
			&artifact.Path, &artifact.Name, &artifact.MimeType, &artifact.SizeBytes, &artifact.SHA256, &artifact.StorageBackend, &artifact.StorageKey, &artifact.CreatedAt, &deletedAt,
		); err == nil {
			if deletedAt.Valid {
				artifact.DeletedAt = &deletedAt.Time
			}
			artifacts = append(artifacts, artifact)
		}
	}
	return artifacts
}

func (s *PostgresStore) GetArtifact(artifactID string) (protocol.Artifact, error) {
	var artifact protocol.Artifact
	var deletedAt sql.NullTime
	if err := s.db.QueryRow(`
		SELECT id, run_id, COALESCE(chat_id, ''), COALESCE(user_id, ''), COALESCE(project_id, ''), COALESCE(workspace_id, ''),
		       path, COALESCE(name, ''), mime_type, size_bytes, COALESCE(sha256, ''), COALESCE(storage_backend, ''), COALESCE(storage_key, ''), created_at, deleted_at
		FROM artifacts
		WHERE id = $1 AND deleted_at IS NULL`, artifactID).Scan(
		&artifact.ID, &artifact.RunID, &artifact.ChatID, &artifact.UserID, &artifact.ProjectID, &artifact.WorkspaceID,
		&artifact.Path, &artifact.Name, &artifact.MimeType, &artifact.SizeBytes, &artifact.SHA256, &artifact.StorageBackend, &artifact.StorageKey, &artifact.CreatedAt, &deletedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return protocol.Artifact{}, app.ErrNotFound
		}
		return protocol.Artifact{}, err
	}
	if deletedAt.Valid {
		artifact.DeletedAt = &deletedAt.Time
	}
	return artifact, nil
}

func (s *PostgresStore) ListSkillsForUser(userID, projectID string) []protocol.Skill {
	skills := s.listSkillsForUser(userID, projectID, false)
	for i := range skills {
		skills[i] = redactSkill(skills[i])
	}
	return skills
}

func (s *PostgresStore) ListRuntimeSkillsForUser(userID, projectID string) []protocol.RuntimeSkill {
	skills := s.listSkillsForUser(userID, projectID, true)
	runtimeSkills := make([]protocol.RuntimeSkill, 0, len(skills))
	for _, skill := range skills {
		secrets := map[string]string{}
		secretMaterials := map[string]protocol.RuntimeSecret{}
		rows, err := s.db.Query(`SELECT secret_key, COALESCE(encrypted_value, ''), COALESCE(secret_ref, '') FROM skill_secrets WHERE skill_id = $1`, skill.ID)
		if err == nil {
			for rows.Next() {
				var key, encryptedValue, secretRef string
				if err := rows.Scan(&key, &encryptedValue, &secretRef); err == nil {
					material := protocol.RuntimeSecret{EncryptedValue: encryptedValue, SecretRef: secretRef}
					if material.EncryptedValue != "" || material.SecretRef != "" {
						secretMaterials[key] = material
					}
					if encryptedValue != "" {
						secrets[key] = encryptedValue
					}
				}
			}
			rows.Close()
		}
		runtimeSkills = append(runtimeSkills, protocol.RuntimeSkill{Skill: skill, Secrets: secrets, SecretMaterials: secretMaterials})
	}
	return runtimeSkills
}

func (s *PostgresStore) listSkillsForUser(userID, projectID string, runtimeOnly bool) []protocol.Skill {
	statusFilter := ""
	if runtimeOnly {
		statusFilter = "AND s.status = 'enabled'"
	}
	rows, err := s.db.Query(`
		SELECT s.id, s.slug, s.scope, s.kind, COALESCE(s.owner_user_id, ''),
		       COALESCE(s.project_id, ''), s.status, COALESCE(s.current_version_id, ''),
		       v.name, v.version, v.description, v.risk, false,
		       COALESCE(v.input_schema::text, ''), COALESCE(v.output_schema::text, ''),
		       COALESCE(v.annotations::text, ''), COALESCE(v.runtime_config::text, ''),
		       g.enabled
		FROM skills s
		JOIN skill_versions v ON v.id = s.current_version_id
		JOIN skill_grants g ON g.skill_id = s.id
		WHERE g.user_id = $1
		  AND (g.project_id = $2 OR g.project_id IS NULL)
		  AND g.enabled = true
		  AND s.status <> 'archived'
		  `+statusFilter+`
		ORDER BY s.id`, userID, projectID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var skills []protocol.Skill
	for rows.Next() {
		var skill protocol.Skill
		var scope, kind, status, risk string
		if err := rows.Scan(
			&skill.ID,
			&skill.Slug,
			&scope,
			&kind,
			&skill.OwnerUserID,
			&skill.ProjectID,
			&status,
			&skill.CurrentVersionID,
			&skill.Name,
			&skill.Version,
			&skill.Description,
			&risk,
			&skill.RequiresAuth,
			&skill.InputSchema,
			&skill.OutputSchema,
			&skill.Annotations,
			&skill.RuntimeConfig,
			&skill.Enabled,
		); err == nil {
			skill.Scope = protocol.SkillScope(scope)
			skill.Kind = protocol.SkillKind(kind)
			skill.Status = protocol.SkillStatus(status)
			skill.Risk = protocol.SkillRisk(risk)
			skills = append(skills, skill)
		}
	}
	return skills
}

func (s *PostgresStore) CreateHTTPSkill(userID, projectID string, input protocol.HTTPSkillInput) (protocol.Skill, error) {
	skillID := platform.NewID("skill")
	versionID := platform.NewID("skv")
	skill, secret, hasSecret, err := httpSkillFromInput(skillID, versionID, userID, projectID, "1.0.0", input)
	if err != nil {
		return protocol.Skill{}, err
	}
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.Skill{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
		INSERT INTO skills (id, slug, scope, kind, owner_user_id, project_id, status, current_version_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		skill.ID, skill.Slug, skill.Scope, skill.Kind, skill.OwnerUserID, skill.ProjectID, skill.Status, skill.CurrentVersionID, now, now); err != nil {
		return protocol.Skill{}, err
	}
	if _, err := tx.Exec(`
		INSERT INTO skill_versions (id, skill_id, version, name, description, risk, input_schema, output_schema, annotations, runtime_config, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, '')::jsonb, NULLIF($8, '')::jsonb, NULLIF($9, '')::jsonb, NULLIF($10, '')::jsonb, $11)`,
		skill.CurrentVersionID, skill.ID, skill.Version, skill.Name, skill.Description, skill.Risk, skill.InputSchema, skill.OutputSchema, skill.Annotations, skill.RuntimeConfig, now); err != nil {
		return protocol.Skill{}, err
	}
	if _, err := tx.Exec(`
		INSERT INTO skill_grants (id, user_id, project_id, skill_id, enabled, created_at)
		VALUES ($1, $2, $3, $4, true, $5)`,
		platform.NewID("grant"), userID, projectID, skill.ID, now); err != nil {
		return protocol.Skill{}, err
	}
	if hasSecret {
		if _, err := tx.Exec(`
			INSERT INTO skill_secrets (id, skill_id, secret_key, secret_ref, encrypted_value, created_at, updated_at)
			VALUES ($1, $2, 'bearer_token', NULLIF($3, ''), NULLIF($4, ''), $5, $6)`,
			platform.NewID("secret"), skill.ID, secret.SecretRef, secret.EncryptedValue, now, now); err != nil {
			return protocol.Skill{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return protocol.Skill{}, err
	}
	return redactSkill(skill), nil
}

func (s *PostgresStore) UpdateHTTPSkill(userID, skillID string, input protocol.HTTPSkillInput) (protocol.Skill, error) {
	var current protocol.Skill
	var scope, kind, status, projectID string
	if err := s.db.QueryRow(`
		SELECT id, slug, scope, kind, COALESCE(project_id, ''), status
		FROM skills
		WHERE id = $1 AND owner_user_id = $2`, skillID, userID).Scan(
		&current.ID, &current.Slug, &scope, &kind, &projectID, &status,
	); err != nil {
		if err == sql.ErrNoRows {
			return protocol.Skill{}, app.ErrNotFound
		}
		return protocol.Skill{}, err
	}
	if protocol.SkillKind(kind) != protocol.SkillKindHTTP {
		return protocol.Skill{}, app.ErrNotFound
	}
	versionID := platform.NewID("skv")
	skill, secret, hasSecret, err := httpSkillFromInput(skillID, versionID, userID, projectID, "1.0.0", input)
	if err != nil {
		return protocol.Skill{}, err
	}
	skill.Slug = current.Slug
	skill.Scope = protocol.SkillScope(scope)
	skill.Kind = protocol.SkillKind(kind)
	skill.Status = protocol.SkillStatus(status)
	skill.Enabled = skill.Status == protocol.SkillStatusEnabled
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.Skill{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
		INSERT INTO skill_versions (id, skill_id, version, name, description, risk, input_schema, output_schema, annotations, runtime_config, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, '')::jsonb, NULLIF($8, '')::jsonb, NULLIF($9, '')::jsonb, NULLIF($10, '')::jsonb, $11)`,
		skill.CurrentVersionID, skill.ID, skill.Version, skill.Name, skill.Description, skill.Risk, skill.InputSchema, skill.OutputSchema, skill.Annotations, skill.RuntimeConfig, now); err != nil {
		return protocol.Skill{}, err
	}
	if _, err := tx.Exec(`UPDATE skills SET current_version_id = $1, updated_at = $2 WHERE id = $3`, skill.CurrentVersionID, now, skill.ID); err != nil {
		return protocol.Skill{}, err
	}
	if hasSecret {
		if _, err := tx.Exec(`
			INSERT INTO skill_secrets (id, skill_id, secret_key, secret_ref, encrypted_value, created_at, updated_at)
			VALUES ($1, $2, 'bearer_token', NULLIF($3, ''), NULLIF($4, ''), $5, $6)
			ON CONFLICT (skill_id, secret_key)
			DO UPDATE SET secret_ref = EXCLUDED.secret_ref, encrypted_value = EXCLUDED.encrypted_value, updated_at = EXCLUDED.updated_at`,
			platform.NewID("secret"), skill.ID, secret.SecretRef, secret.EncryptedValue, now, now); err != nil {
			return protocol.Skill{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return protocol.Skill{}, err
	}
	return redactSkill(skill), nil
}

func (s *PostgresStore) SetSkillEnabled(userID, skillID string, enabled bool) (protocol.Skill, error) {
	status := protocol.SkillStatusDisabled
	if enabled {
		status = protocol.SkillStatusEnabled
	}
	result, err := s.db.Exec(`
		UPDATE skills
		SET status = $1, updated_at = $2
		WHERE id = $3 AND owner_user_id = $4 AND scope = 'user'`,
		status, time.Now().UTC(), skillID, userID)
	if err != nil {
		return protocol.Skill{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return protocol.Skill{}, err
	}
	if affected == 0 {
		return protocol.Skill{}, app.ErrNotFound
	}
	return s.getOwnedHTTPSkill(userID, skillID)
}

func (s *PostgresStore) AddAuditEvent(input protocol.AuditEventInput) (protocol.AuditEvent, error) {
	event := auditEventFromInput(input)
	metadataJSON, err := json.Marshal(event.Metadata)
	if err != nil {
		return protocol.AuditEvent{}, err
	}
	_, err = s.db.Exec(`
		INSERT INTO audit_events (
			id, actor_user_id, actor_project_id, actor_org_id, action, resource_type,
			resource_id, decision, reason, request_id, trace_id, run_id, ip, user_agent,
			metadata, created_at
		)
		VALUES ($1, NULLIF($2, ''), NULLIF($3, ''), NULLIF($4, ''), $5, $6,
		        NULLIF($7, ''), $8, NULLIF($9, ''), NULLIF($10, ''), NULLIF($11, ''),
		        NULLIF($12, ''), NULLIF($13, ''), NULLIF($14, ''), $15::jsonb, $16)`,
		event.ID, event.ActorUserID, event.ActorProjectID, event.ActorOrgID, event.Action, event.ResourceType,
		event.ResourceID, event.Decision, event.Reason, event.RequestID, event.TraceID, event.RunID, event.IP,
		event.UserAgent, string(metadataJSON), event.CreatedAt)
	if err != nil {
		return protocol.AuditEvent{}, err
	}
	return event, nil
}

func (s *PostgresStore) ListAuditEvents(actor app.ActorContext, opts app.AuditEventListOptions) []protocol.AuditEvent {
	limit := opts.Limit
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := s.db.Query(`
		SELECT id, COALESCE(actor_user_id, ''), COALESCE(actor_project_id, ''), COALESCE(actor_org_id, ''),
		       action, resource_type, COALESCE(resource_id, ''), decision, COALESCE(reason, ''),
		       COALESCE(request_id, ''), COALESCE(trace_id, ''), COALESCE(run_id, ''),
		       COALESCE(ip, ''), COALESCE(user_agent, ''), metadata, created_at
		FROM audit_events
		WHERE actor_user_id = $1
		  AND actor_project_id = $2
		  AND ($3 = '' OR request_id = $3)
		  AND ($4 = '' OR run_id = $4)
		  AND ($5 = '' OR action = $5)
		  AND ($6 = '' OR resource_id = $6)
		ORDER BY created_at DESC
		LIMIT $7`,
		actor.UserID, actor.ProjectID, opts.RequestID, opts.RunID, opts.Action, opts.ResourceID, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	events := make([]protocol.AuditEvent, 0)
	for rows.Next() {
		var event protocol.AuditEvent
		var decision string
		var rawMetadata []byte
		if err := rows.Scan(
			&event.ID, &event.ActorUserID, &event.ActorProjectID, &event.ActorOrgID,
			&event.Action, &event.ResourceType, &event.ResourceID, &decision, &event.Reason,
			&event.RequestID, &event.TraceID, &event.RunID, &event.IP, &event.UserAgent,
			&rawMetadata, &event.CreatedAt,
		); err != nil {
			return events
		}
		event.Decision = protocol.AuditDecision(decision)
		if len(rawMetadata) > 0 && string(rawMetadata) != "null" {
			var metadata map[string]any
			if err := json.Unmarshal(rawMetadata, &metadata); err == nil {
				event.Metadata = metadata
			}
		}
		events = append(events, event)
	}
	return events
}

func (s *PostgresStore) getProjectMember(projectID, userID string) (protocol.ProjectMember, error) {
	var member protocol.ProjectMember
	err := s.db.QueryRow(`
		SELECT pm.id, pm.user_id, pm.project_id, pm.role, u.email, u.name, pm.created_at, pm.updated_at
		FROM project_members pm
		JOIN users u ON u.id = pm.user_id
		WHERE pm.project_id = $1 AND pm.user_id = $2`, projectID, userID).Scan(
		&member.ID, &member.UserID, &member.ProjectID, &member.Role, &member.Email, &member.Name, &member.CreatedAt, &member.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return protocol.ProjectMember{}, app.ErrNotFound
		}
		return protocol.ProjectMember{}, err
	}
	return member, nil
}

func (s *PostgresStore) getOrganizationMember(orgID, userID string) (protocol.OrganizationMember, error) {
	var member protocol.OrganizationMember
	err := s.db.QueryRow(`
		SELECT om.id, om.user_id, om.organization_id, om.role, u.email, u.name, om.created_at, om.updated_at
		FROM organization_members om
		JOIN users u ON u.id = om.user_id
		WHERE om.organization_id = $1 AND om.user_id = $2`, orgID, userID).Scan(
		&member.ID, &member.UserID, &member.OrganizationID, &member.Role, &member.Email, &member.Name, &member.CreatedAt, &member.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return protocol.OrganizationMember{}, app.ErrNotFound
		}
		return protocol.OrganizationMember{}, err
	}
	return member, nil
}

func (s *PostgresStore) getOwnedHTTPSkill(userID, skillID string) (protocol.Skill, error) {
	var skill protocol.Skill
	var scope, kind, status, risk string
	if err := s.db.QueryRow(`
		SELECT s.id, s.slug, s.scope, s.kind, COALESCE(s.owner_user_id, ''),
		       COALESCE(s.project_id, ''), s.status, COALESCE(s.current_version_id, ''),
		       v.name, v.version, v.description, v.risk, false,
		       COALESCE(v.input_schema::text, ''), COALESCE(v.output_schema::text, ''),
		       COALESCE(v.annotations::text, ''), COALESCE(v.runtime_config::text, '')
		FROM skills s
		JOIN skill_versions v ON v.id = s.current_version_id
		WHERE s.id = $1 AND s.owner_user_id = $2 AND s.kind = 'http'`, skillID, userID).Scan(
		&skill.ID, &skill.Slug, &scope, &kind, &skill.OwnerUserID, &skill.ProjectID,
		&status, &skill.CurrentVersionID, &skill.Name, &skill.Version, &skill.Description,
		&risk, &skill.RequiresAuth, &skill.InputSchema, &skill.OutputSchema, &skill.Annotations,
		&skill.RuntimeConfig,
	); err != nil {
		if err == sql.ErrNoRows {
			return protocol.Skill{}, app.ErrNotFound
		}
		return protocol.Skill{}, err
	}
	skill.Scope = protocol.SkillScope(scope)
	skill.Kind = protocol.SkillKind(kind)
	skill.Status = protocol.SkillStatus(status)
	skill.Risk = protocol.SkillRisk(risk)
	skill.Enabled = skill.Status == protocol.SkillStatusEnabled
	return redactSkill(skill), nil
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
