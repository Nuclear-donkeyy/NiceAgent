package repository

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"niceagent/common/protocol"
	"niceagent/control-plane/internal/app"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresStorePersistsEventsAndKeepsTerminalStatusWhenConfigured(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run postgres repository tests")
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	defer db.Close()
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("ping postgres: %v", err)
	}
	applyTestMigration(t, db)

	store := NewPostgresStore(db)
	chat, err := store.CreateChat("demo-user", app.DemoProjectID, "postgres")
	if err != nil {
		t.Fatalf("create chat: %v", err)
	}
	message, run, err := store.AddUserMessage(chat.ID, "demo-user", "hello postgres")
	if err != nil {
		t.Fatalf("add user message: %v", err)
	}
	if message.RunID != run.ID {
		t.Fatalf("message run id = %q, want %q", message.RunID, run.ID)
	}
	claimed, err := store.ClaimRunAttempt(run.ID, "attempt-postgres", "runtime-pg", time.Now().UTC().Add(time.Minute))
	if err != nil {
		t.Fatalf("claim run attempt: %v", err)
	}
	if claimed.AttemptID != "attempt-postgres" || claimed.ClaimedBy != "runtime-pg" || claimed.AttemptCount != 1 || claimed.LeaseExpiresAt == nil {
		t.Fatalf("claimed run = %#v", claimed)
	}
	if _, err := store.CheckRunAttempt(run.ID, "attempt-stale"); err != app.ErrAttemptMismatch {
		t.Fatalf("check stale attempt err = %v, want ErrAttemptMismatch", err)
	}

	events, cancel := store.Subscribe(run.ID)
	defer cancel()
	first, err := store.AddEvent(run.ID, protocol.EventRunQueued, "queued", nil)
	if err != nil {
		t.Fatalf("add first event: %v", err)
	}
	second, err := store.AddEvent(run.ID, protocol.EventRunStarted, "started", map[string]any{"source": "postgres-test"})
	if err != nil {
		t.Fatalf("add second event: %v", err)
	}
	if first.Seq != 1 || second.Seq != 2 {
		t.Fatalf("event seqs = %d/%d, want 1/2", first.Seq, second.Seq)
	}
	select {
	case event := <-events:
		if event.ID != first.ID {
			t.Fatalf("subscriber got %s, want %s", event.ID, first.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive postgres event")
	}

	replayed := store.ListEvents(run.ID, 1)
	if len(replayed) != 1 || replayed[0].ID != second.ID {
		t.Fatalf("replayed events = %#v, want only second event", replayed)
	}
	if _, err := store.SaveRunUsage(run.ID, protocol.RunUsage{
		Provider:              "openai-compatible",
		Model:                 "deepseek-v4-flash",
		InputTokens:           9,
		OutputTokens:          4,
		TotalTokens:           13,
		Estimated:             true,
		TokenEstimator:        "heuristic_rune_div4",
		ToolCalls:             2,
		ToolErrors:            1,
		SandboxCommands:       1,
		SandboxDurationMillis: 345,
		SandboxOutputBytes:    678,
		SandboxCPUMillis:      90,
		SandboxMemoryMaxBytes: 2048,
		ArtifactCount:         4,
		ArtifactBytes:         8192,
	}); err != nil {
		t.Fatalf("save run usage: %v", err)
	}
	if total := store.SumRunUsageTokensSince("demo-user", app.DemoProjectID, time.Now().UTC().Add(-time.Hour)); total < 13 {
		t.Fatalf("usage token total = %d, want at least 13", total)
	}
	totals := store.SumRunUsageSince("demo-user", app.DemoProjectID, time.Now().UTC().Add(-time.Hour))
	if totals.TotalTokens < 13 || totals.ToolCalls != 2 || totals.SandboxCommands != 1 || totals.ArtifactBytes != 8192 {
		t.Fatalf("usage totals = %#v, want token/tool/sandbox totals", totals)
	}
	buckets := store.ListRunUsageBucketsSince(app.DemoProjectID, time.Now().UTC().Add(-time.Hour))
	if len(buckets) != 1 {
		t.Fatalf("usage bucket count = %d, want 1", len(buckets))
	}
	if bucket := buckets[0]; bucket.Provider != "openai-compatible" || bucket.Model != "deepseek-v4-flash" ||
		bucket.RunCount != 1 || bucket.TotalTokens < 13 || !bucket.Estimated ||
		bucket.TokenEstimator != "heuristic_rune_div4" || bucket.ToolCalls != 2 || bucket.ArtifactBytes != 8192 {
		t.Fatalf("usage bucket = %#v, want provider/model usage bucket", bucket)
	}
	if err := (app.RepositorySink{Repo: store}).Complete(run.ID, "persisted assistant"); err != nil {
		t.Fatalf("complete run: %v", err)
	}

	reloaded := NewPostgresStore(db)
	gotChat, messages, err := reloaded.GetChat(chat.ID)
	if err != nil {
		t.Fatalf("reload chat: %v", err)
	}
	if gotChat.LastRunID != run.ID || len(messages) != 2 || messages[1].Content != "persisted assistant" {
		t.Fatalf("reloaded chat/messages = %#v %#v", gotChat, messages)
	}
	gotRun, err := reloaded.GetRun(run.ID)
	if err != nil {
		t.Fatalf("reload run: %v", err)
	}
	if gotRun.Status != protocol.RunSucceeded || gotRun.FinishedAt == nil {
		t.Fatalf("reloaded run = %#v, want succeeded with finished_at", gotRun)
	}
	if gotRun.Usage.Provider != "openai-compatible" || gotRun.Usage.InputTokens != 9 || gotRun.Usage.OutputTokens != 4 {
		t.Fatalf("reloaded run usage = %#v", gotRun.Usage)
	}
	if !gotRun.Usage.Estimated || gotRun.Usage.TokenEstimator != "heuristic_rune_div4" {
		t.Fatalf("reloaded usage estimator = estimated:%v estimator:%q, want heuristic metadata", gotRun.Usage.Estimated, gotRun.Usage.TokenEstimator)
	}
	if gotRun.Usage.ToolCalls != 2 || gotRun.Usage.ToolErrors != 1 || gotRun.Usage.SandboxCommands != 1 ||
		gotRun.Usage.SandboxDurationMillis != 345 || gotRun.Usage.SandboxOutputBytes != 678 ||
		gotRun.Usage.SandboxCPUMillis != 90 || gotRun.Usage.SandboxMemoryMaxBytes != 2048 ||
		gotRun.Usage.ArtifactCount != 4 || gotRun.Usage.ArtifactBytes != 8192 {
		t.Fatalf("reloaded tool/sandbox usage = %#v", gotRun.Usage)
	}
	if roles := reloaded.ListProjectRoles("demo-user", app.DemoProjectID); len(roles) != 1 || roles[0] != "owner" {
		t.Fatalf("demo project roles = %#v, want owner", roles)
	}
	if roles := reloaded.ListOrganizationRoles("demo-user", app.DemoOrgID); len(roles) != 1 || roles[0] != "owner" {
		t.Fatalf("demo organization roles = %#v, want owner", roles)
	}
	if !reloaded.ProjectBelongsToOrganization(app.DemoProjectID, app.DemoOrgID) {
		t.Fatal("demo project should belong to demo org")
	}
	if reloaded.ProjectBelongsToOrganization(app.DemoProjectID, "other-org") {
		t.Fatal("demo project unexpectedly belongs to other org")
	}
	identity, err := reloaded.BindUserIdentity(protocol.UserIdentity{
		UserID:   "postgres-identity-user",
		Provider: "oidc",
		Issuer:   "https://issuer.example.test",
		Subject:  "postgres-subject",
		Email:    "postgres-identity@example.test",
		Name:     "Postgres Identity",
	})
	if err != nil {
		t.Fatalf("bind user identity: %v", err)
	}
	if identity.ID == "" || identity.UserID != "postgres-identity-user" || identity.Email != "postgres-identity@example.test" {
		t.Fatalf("identity = %#v", identity)
	}
	if _, err := reloaded.BindUserIdentity(protocol.UserIdentity{
		UserID:   "postgres-other-user",
		Provider: "oidc",
		Issuer:   "https://issuer.example.test",
		Subject:  "postgres-subject",
		Email:    "postgres-other@example.test",
	}); err != app.ErrIdentityConflict {
		t.Fatalf("same external identity err = %v, want ErrIdentityConflict", err)
	}
	if _, err := reloaded.BindUserIdentity(protocol.UserIdentity{
		UserID:   "postgres-identity-user",
		Provider: "oidc",
		Issuer:   "https://issuer.example.test",
		Subject:  "postgres-other-subject",
		Email:    "postgres-identity@example.test",
	}); err != app.ErrIdentityConflict {
		t.Fatalf("same user different identity err = %v, want ErrIdentityConflict", err)
	}
	orgInvitation, err := reloaded.CreateInvitation(app.DemoOrgID, app.DemoUserID, protocol.InvitationInput{
		Email: "postgres-invited@example.test",
		Role:  "admin",
	})
	if err != nil {
		t.Fatalf("create organization invitation: %v", err)
	}
	if orgInvitation.Token == "" || orgInvitation.Status != protocol.InvitationPending {
		t.Fatalf("organization invitation = %#v", orgInvitation)
	}
	delivery, err := reloaded.EnqueueInvitationEmail(orgInvitation, 2)
	if err != nil {
		t.Fatalf("enqueue invitation email: %v", err)
	}
	claimedDeliveries := reloaded.ClaimDueInvitationEmails(1, "postgres-worker-a", time.Now().UTC().Add(time.Minute))
	if len(claimedDeliveries) != 1 || claimedDeliveries[0].ID != delivery.ID || claimedDeliveries[0].Attempts != 1 || claimedDeliveries[0].Invitation.Token != orgInvitation.Token {
		t.Fatalf("claimed invitation email deliveries = %#v", claimedDeliveries)
	}
	nextAttempt := time.Now().UTC()
	if err := reloaded.MarkInvitationEmailFailed(delivery.ID, "temporary smtp failure", &nextAttempt, false); err != nil {
		t.Fatalf("mark invitation email failed: %v", err)
	}
	claimedDeliveries = reloaded.ClaimDueInvitationEmails(1, "postgres-worker-b", time.Now().UTC().Add(time.Minute))
	if len(claimedDeliveries) != 1 || claimedDeliveries[0].Attempts != 2 || claimedDeliveries[0].LockedBy != "postgres-worker-b" {
		t.Fatalf("reclaimed invitation email deliveries = %#v", claimedDeliveries)
	}
	if err := reloaded.MarkInvitationEmailSent(delivery.ID); err != nil {
		t.Fatalf("mark invitation email sent: %v", err)
	}
	if claimedDeliveries = reloaded.ClaimDueInvitationEmails(1, "postgres-worker-c", time.Now().UTC().Add(time.Minute)); len(claimedDeliveries) != 0 {
		t.Fatalf("sent delivery was claimed again: %#v", claimedDeliveries)
	}
	emailEvent, err := reloaded.RecordInvitationEmailEvent(protocol.InvitationEmailEventInput{
		InvitationID:      orgInvitation.ID,
		DeliveryID:        delivery.ID,
		Provider:          "smtp-test",
		ProviderMessageID: "postgres-message-a",
		Type:              string(protocol.InvitationEmailEventComplaint),
		Reason:            "recipient complained",
		Payload:           map[string]any{"provider_event_id": "evt-a"},
	})
	if err != nil {
		t.Fatalf("record invitation email event: %v", err)
	}
	if emailEvent.Type != protocol.InvitationEmailEventComplaint || emailEvent.ProviderMessageID != "postgres-message-a" {
		t.Fatalf("email event = %#v, want complaint event", emailEvent)
	}
	emailEvents := reloaded.ListInvitationEmailEvents(app.DemoOrgID, app.InvitationEmailEventListOptions{InvitationID: orgInvitation.ID})
	if len(emailEvents) != 1 || emailEvents[0].Reason != "recipient complained" {
		t.Fatalf("listed email events = %#v, want recorded complaint", emailEvents)
	}
	if !reloaded.IsInvitationEmailSuppressed(app.DemoOrgID, orgInvitation.Email) {
		t.Fatalf("invitation email should be suppressed after complaint")
	}
	suppressions := reloaded.ListInvitationEmailSuppressions(app.DemoOrgID, 10)
	if len(suppressions) != 1 || suppressions[0].Email != orgInvitation.Email || suppressions[0].Reason != "recipient complained" {
		t.Fatalf("suppressions = %#v, want complaint invitation suppression", suppressions)
	}
	if _, err := reloaded.RequeueInvitationEmail(app.DemoOrgID, orgInvitation.ID, 3); err != app.ErrInvalidInput {
		t.Fatalf("requeue suppressed invitation email err = %v, want ErrInvalidInput", err)
	}
	if _, err := reloaded.EnqueueInvitationEmail(orgInvitation, 3); err != app.ErrInvalidInput {
		t.Fatalf("enqueue suppressed invitation email err = %v, want ErrInvalidInput", err)
	}
	claimedDeliveries = reloaded.ClaimDueInvitationEmails(1, "postgres-worker-resend", time.Now().UTC().Add(time.Minute))
	if len(claimedDeliveries) != 0 {
		t.Fatalf("suppressed delivery was claimed again: %#v", claimedDeliveries)
	}
	deletedSuppression, err := reloaded.DeleteInvitationEmailSuppression(app.DemoOrgID, suppressions[0].ID)
	if err != nil {
		t.Fatalf("delete invitation email suppression: %v", err)
	}
	if deletedSuppression.Email != orgInvitation.Email || reloaded.IsInvitationEmailSuppressed(app.DemoOrgID, orgInvitation.Email) {
		t.Fatalf("deleted suppression = %#v, suppressed = %v", deletedSuppression, reloaded.IsInvitationEmailSuppressed(app.DemoOrgID, orgInvitation.Email))
	}
	requeued, err := reloaded.RequeueInvitationEmail(app.DemoOrgID, orgInvitation.ID, 3)
	if err != nil {
		t.Fatalf("requeue unsuppressed invitation email: %v", err)
	}
	if requeued.ID != delivery.ID || requeued.Status != protocol.InvitationEmailPending || requeued.Attempts != 0 || requeued.MaxAttempts != 3 || requeued.LastError != "" || requeued.Invitation.Token != orgInvitation.Token {
		t.Fatalf("requeued delivery = %#v", requeued)
	}
	invitations := reloaded.ListInvitations(app.DemoOrgID)
	if len(invitations) == 0 || invitations[0].Token != "" {
		t.Fatalf("listed invitations = %#v, want redacted token", invitations)
	}
	acceptedInvitation, err := reloaded.AcceptInvitation(orgInvitation.Token, "postgres-invited-user", "postgres-invited@example.test", "Postgres Invited")
	if err != nil {
		t.Fatalf("accept organization invitation: %v", err)
	}
	if acceptedInvitation.Status != protocol.InvitationAccepted || acceptedInvitation.AcceptedByUserID != "postgres-invited-user" {
		t.Fatalf("accepted organization invitation = %#v", acceptedInvitation)
	}
	if roles := reloaded.ListOrganizationRoles("postgres-invited-user", app.DemoOrgID); len(roles) != 1 || roles[0] != "admin" {
		t.Fatalf("organization roles after invitation = %#v, want admin", roles)
	}
	projectInvitation, err := reloaded.CreateInvitation(app.DemoOrgID, app.DemoUserID, protocol.InvitationInput{
		Email:     "postgres-project-invited@example.test",
		Role:      "viewer",
		ProjectID: app.DemoProjectID,
	})
	if err != nil {
		t.Fatalf("create project invitation: %v", err)
	}
	if _, err := reloaded.AcceptInvitation(projectInvitation.Token, "postgres-project-invited", "postgres-project-invited@example.test", "Postgres Project Invited"); err != nil {
		t.Fatalf("accept project invitation: %v", err)
	}
	if roles := reloaded.ListProjectRoles("postgres-project-invited", app.DemoProjectID); len(roles) != 1 || roles[0] != "viewer" {
		t.Fatalf("project roles after invitation = %#v, want viewer", roles)
	}
	orgMember, err := reloaded.UpsertOrganizationMember(app.DemoOrgID, protocol.OrganizationMemberInput{
		UserID: "postgres-org-member",
		Email:  "postgres-org-member@example.test",
		Name:   "Postgres Org Member",
		Role:   "admin",
	})
	if err != nil {
		t.Fatalf("upsert organization member: %v", err)
	}
	if orgMember.UserID != "postgres-org-member" || orgMember.Role != "admin" || orgMember.Email != "postgres-org-member@example.test" {
		t.Fatalf("organization member = %#v", orgMember)
	}
	orgMembers := reloaded.ListOrganizationMembers(app.DemoOrgID)
	if len(orgMembers) < 2 {
		t.Fatalf("organization members = %#v, want demo owner and postgres org member", orgMembers)
	}
	updatedOrgMember, err := reloaded.UpsertOrganizationMember(app.DemoOrgID, protocol.OrganizationMemberInput{UserID: "postgres-org-member", Role: "viewer"})
	if err != nil {
		t.Fatalf("update organization member: %v", err)
	}
	if updatedOrgMember.Role != "viewer" {
		t.Fatalf("updated organization member = %#v, want viewer", updatedOrgMember)
	}
	removedOrgMember, err := reloaded.RemoveOrganizationMember(app.DemoOrgID, "postgres-org-member")
	if err != nil {
		t.Fatalf("remove organization member: %v", err)
	}
	if removedOrgMember.UserID != "postgres-org-member" || removedOrgMember.Role != "viewer" {
		t.Fatalf("removed organization member = %#v", removedOrgMember)
	}
	member, err := reloaded.UpsertProjectMember(app.DemoProjectID, protocol.ProjectMemberInput{
		UserID: "postgres-member",
		Email:  "postgres-member@example.test",
		Name:   "Postgres Member",
		Role:   "viewer",
	})
	if err != nil {
		t.Fatalf("upsert project member: %v", err)
	}
	if member.UserID != "postgres-member" || member.Role != "viewer" || member.Email != "postgres-member@example.test" {
		t.Fatalf("member = %#v", member)
	}
	members := reloaded.ListProjectMembers(app.DemoProjectID)
	if len(members) < 2 {
		t.Fatalf("project members = %#v, want demo owner and postgres member", members)
	}
	updated, err := reloaded.UpsertProjectMember(app.DemoProjectID, protocol.ProjectMemberInput{UserID: "postgres-member", Role: "member"})
	if err != nil {
		t.Fatalf("update project member: %v", err)
	}
	if updated.Role != "member" {
		t.Fatalf("updated member = %#v, want member", updated)
	}
	removed, err := reloaded.RemoveProjectMember(app.DemoProjectID, "postgres-member")
	if err != nil {
		t.Fatalf("remove project member: %v", err)
	}
	if removed.UserID != "postgres-member" || removed.Role != "member" {
		t.Fatalf("removed member = %#v", removed)
	}
	policy, err := reloaded.SetProjectQuotaPolicy(app.DemoProjectID, protocol.ProjectQuotaPolicyInput{
		MaxConcurrentRuns:       3,
		MaxRunsPerHour:          12,
		MaxModelTokensPerDay:    1200,
		MaxToolCallsPerDay:      33,
		MaxSandboxSecondsPerDay: 44,
	})
	if err != nil {
		t.Fatalf("set project quota policy: %v", err)
	}
	if policy.MaxConcurrentRuns != 3 || policy.MaxRunsPerHour != 12 || policy.MaxModelTokensPerDay != 1200 ||
		policy.MaxToolCallsPerDay != 33 || policy.MaxSandboxSecondsPerDay != 44 {
		t.Fatalf("quota policy = %#v", policy)
	}
	gotPolicy, ok := reloaded.GetProjectQuotaPolicy(app.DemoProjectID)
	if !ok || gotPolicy.MaxConcurrentRuns != 3 || gotPolicy.MaxToolCallsPerDay != 33 || gotPolicy.MaxSandboxSecondsPerDay != 44 {
		t.Fatalf("got quota policy = %#v ok=%v", gotPolicy, ok)
	}
	runtimePolicy, err := reloaded.SetProjectRuntimePolicy(app.DemoProjectID, protocol.ProjectRuntimePolicyInput{
		SkillRiskPolicy: protocol.SkillRiskPolicyReadOnly,
	})
	if err != nil {
		t.Fatalf("set project runtime policy: %v", err)
	}
	if runtimePolicy.SkillRiskPolicy != protocol.SkillRiskPolicyReadOnly {
		t.Fatalf("runtime policy = %#v", runtimePolicy)
	}
	gotRuntimePolicy, ok := reloaded.GetProjectRuntimePolicy(app.DemoProjectID)
	if !ok || gotRuntimePolicy.SkillRiskPolicy != protocol.SkillRiskPolicyReadOnly {
		t.Fatalf("got runtime policy = %#v ok=%v", gotRuntimePolicy, ok)
	}

	_, canceledRun, err := reloaded.AddUserMessage(chat.ID, "demo-user", "cancel me")
	if err != nil {
		t.Fatalf("add cancel run: %v", err)
	}
	if _, err := reloaded.UpdateRunStatus(canceledRun.ID, protocol.RunCanceled, ""); err != nil {
		t.Fatalf("cancel run: %v", err)
	}
	if err := (app.RepositorySink{Repo: reloaded}).Complete(canceledRun.ID, "late completion"); err != nil {
		t.Fatalf("late complete: %v", err)
	}
	gotRun, err = reloaded.GetRun(canceledRun.ID)
	if err != nil {
		t.Fatalf("reload canceled run: %v", err)
	}
	if gotRun.Status != protocol.RunCanceled {
		t.Fatalf("late completion overwrote status to %q", gotRun.Status)
	}
}

func TestPostgresStoreSearchesArchivesAndRestoresChatsWhenConfigured(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run postgres repository tests")
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	defer db.Close()
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("ping postgres: %v", err)
	}
	applyTestMigration(t, db)

	store := NewPostgresStore(db)
	title := "searchable chat " + time.Now().Format("20060102150405.000000000")
	chat, err := store.CreateChat("demo-user", app.DemoProjectID, title)
	if err != nil {
		t.Fatalf("create chat: %v", err)
	}
	matches := store.ListChats("demo-user", app.DemoProjectID, app.ChatListOptions{Query: title})
	if len(matches) != 1 || matches[0].ID != chat.ID {
		t.Fatalf("search matches = %#v, want created chat", matches)
	}

	archived, err := store.SetChatArchived(chat.ID, "demo-user", true)
	if err != nil {
		t.Fatalf("archive chat: %v", err)
	}
	if !archived.Archived {
		t.Fatalf("archived chat = %#v, want archived", archived)
	}
	matches = store.ListChats("demo-user", app.DemoProjectID, app.ChatListOptions{Query: title})
	if len(matches) != 0 {
		t.Fatalf("active search matches after archive = %#v, want none", matches)
	}
	matches = store.ListChats("demo-user", app.DemoProjectID, app.ChatListOptions{Query: title, IncludeArchived: true})
	if len(matches) != 1 || !matches[0].Archived {
		t.Fatalf("archived search matches = %#v, want archived chat", matches)
	}

	restored, err := store.SetChatArchived(chat.ID, "demo-user", false)
	if err != nil {
		t.Fatalf("restore chat: %v", err)
	}
	if restored.Archived {
		t.Fatalf("restored chat = %#v, want active", restored)
	}
}

func TestPostgresStoreListsUserSkillGrantsWhenConfigured(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run postgres repository tests")
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	defer db.Close()
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("ping postgres: %v", err)
	}
	applyTestMigration(t, db)

	store := NewPostgresStore(db)
	skills := store.ListSkillsForUser("demo-user", "demo-project")
	if len(skills) == 0 {
		t.Fatal("expected demo user skills")
	}
	var foundCLI, foundWorkspace bool
	for _, skill := range skills {
		if skill.ID == "cli.exec" {
			foundCLI = true
			if skill.RequiresAuth {
				t.Fatalf("cli.exec requires auth = true, want false")
			}
		}
		if skill.ID == "workspace.read" {
			foundWorkspace = true
			if !strings.Contains(skill.InputSchema, `"summary"`) {
				t.Fatalf("workspace.read input schema = %s, want summary action", skill.InputSchema)
			}
		}
	}
	if !foundCLI {
		t.Fatalf("skills = %#v, want cli.exec", skills)
	}
	if !foundWorkspace {
		t.Fatalf("skills = %#v, want workspace.read", skills)
	}
}

func applyTestMigration(t *testing.T, db *sql.DB) {
	t.Helper()
	entries, err := os.ReadDir("../../../../migrations")
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		raw, err := os.ReadFile("../../../../migrations/" + entry.Name())
		if err != nil {
			t.Fatalf("read migration %s: %v", entry.Name(), err)
		}
		for _, stmt := range strings.Split(string(raw), ";") {
			stmt = strings.TrimSpace(stmt)
			if stmt == "" {
				continue
			}
			if _, err := db.Exec(stmt); err != nil {
				if strings.Contains(err.Error(), "already exists") {
					continue
				}
				t.Fatalf("apply migration %s statement %q: %v", entry.Name(), stmt, err)
			}
		}
	}
}
