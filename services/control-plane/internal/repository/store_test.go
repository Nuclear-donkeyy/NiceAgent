package repository

import (
	"strings"
	"testing"
	"time"

	"niceagent/common/protocol"
	"niceagent/control-plane/internal/app"
)

func TestStoreCreatesChatMessageRunAndEvents(t *testing.T) {
	store := NewStore()
	chat := mustCreateChat(t, store, "demo-user", "")
	if chat.ID == "" {
		t.Fatal("expected chat id")
	}

	message, run, err := store.AddUserMessage(chat.ID, "demo-user", "hello agent")
	if err != nil {
		t.Fatalf("add user message: %v", err)
	}
	if message.RunID != run.ID {
		t.Fatalf("message run id = %q, want %q", message.RunID, run.ID)
	}
	if run.Status != protocol.RunQueued {
		t.Fatalf("run status = %q, want queued", run.Status)
	}

	event, err := store.AddEvent(run.ID, protocol.EventRunQueued, "queued", nil)
	if err != nil {
		t.Fatalf("add event: %v", err)
	}
	if event.Seq != 1 {
		t.Fatalf("event seq = %d, want 1", event.Seq)
	}

	events := store.ListEvents(run.ID, 0)
	if len(events) != 1 {
		t.Fatalf("events len = %d, want 1", len(events))
	}
}

func TestStoreRegistersWorkspaceAndArtifactsForRun(t *testing.T) {
	store := NewStore()
	chat := mustCreateChat(t, store, "demo-user", "artifact store")
	_, run, err := store.AddUserMessage(chat.ID, "demo-user", "make file")
	if err != nil {
		t.Fatalf("add user message: %v", err)
	}

	workspace, err := store.GetWorkspace(run.WorkspaceID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	if workspace.RunID != run.ID || workspace.ChatID != chat.ID || workspace.UserID != "demo-user" || workspace.ProjectID != chat.ProjectID {
		t.Fatalf("workspace = %#v", workspace)
	}

	artifact, err := store.AddArtifact(protocol.Artifact{
		RunID:     run.ID,
		Path:      "output/report.txt",
		Name:      "report.txt",
		MimeType:  "text/plain",
		SizeBytes: 12,
	})
	if err != nil {
		t.Fatalf("add artifact: %v", err)
	}
	if artifact.ID == "" || artifact.ChatID != chat.ID || artifact.UserID != "demo-user" || artifact.WorkspaceID != run.WorkspaceID || artifact.ProjectID != chat.ProjectID {
		t.Fatalf("artifact = %#v", artifact)
	}
	listed := store.ListArtifacts(run.ID)
	if len(listed) != 1 || listed[0].ID != artifact.ID {
		t.Fatalf("listed artifacts = %#v, want %s", listed, artifact.ID)
	}
}

func TestStoreListsSearchesArchivesAndRestoresChats(t *testing.T) {
	store := NewStore()
	alpha := mustCreateChat(t, store, "demo-user", "Alpha project")
	beta := mustCreateChat(t, store, "demo-user", "Beta project")

	list := store.ListChats("demo-user", app.DemoProjectID, app.ChatListOptions{})
	if len(list) != 2 {
		t.Fatalf("active chats len = %d, want 2", len(list))
	}
	matches := store.ListChats("demo-user", app.DemoProjectID, app.ChatListOptions{Query: "alpha"})
	if len(matches) != 1 || matches[0].ID != alpha.ID {
		t.Fatalf("search matches = %#v, want alpha chat", matches)
	}

	archived, err := store.SetChatArchived(beta.ID, "demo-user", true)
	if err != nil {
		t.Fatalf("archive chat: %v", err)
	}
	if !archived.Archived {
		t.Fatal("expected archived chat")
	}
	list = store.ListChats("demo-user", app.DemoProjectID, app.ChatListOptions{})
	if len(list) != 1 || list[0].ID != alpha.ID {
		t.Fatalf("active chats after archive = %#v, want alpha only", list)
	}
	list = store.ListChats("demo-user", app.DemoProjectID, app.ChatListOptions{IncludeArchived: true})
	if len(list) != 2 {
		t.Fatalf("all chats after archive = %d, want 2", len(list))
	}

	restored, err := store.SetChatArchived(beta.ID, "demo-user", false)
	if err != nil {
		t.Fatalf("restore chat: %v", err)
	}
	if restored.Archived {
		t.Fatal("expected restored chat")
	}
}

func TestStoreListsSkillsForUserWithoutCLIApproval(t *testing.T) {
	store := NewStore()
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
			if skill.Risk != protocol.SkillRiskMedium {
				t.Fatalf("cli.exec risk = %q, want medium", skill.Risk)
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

func TestStoreCreatesUserHTTPSkillWithoutExposingSecret(t *testing.T) {
	store := NewStore()
	skill, err := store.CreateHTTPSkill("demo-user", "demo-project", protocol.HTTPSkillInput{
		Name:        "Weather API",
		Description: "Fetch weather information",
		Method:      "POST",
		URL:         "https://example.com/weather",
		AuthType:    "bearer",
		BearerToken: "secret-token",
	})
	if err != nil {
		t.Fatalf("create http skill: %v", err)
	}
	if skill.Scope != protocol.SkillScopeUser || skill.Kind != protocol.SkillKindHTTP {
		t.Fatalf("skill scope/kind = %q/%q, want user/http", skill.Scope, skill.Kind)
	}
	if strings.Contains(skill.RuntimeConfig, "secret-token") {
		t.Fatalf("runtime config leaked token: %s", skill.RuntimeConfig)
	}

	skills := store.ListSkillsForUser("demo-user", "demo-project")
	if !containsSkill(skills, skill.ID) {
		t.Fatalf("skills = %#v, want created skill", skills)
	}
	runtimeSkills := store.ListRuntimeSkillsForUser("demo-user", "demo-project")
	var found bool
	for _, runtimeSkill := range runtimeSkills {
		if runtimeSkill.Skill.ID == skill.ID {
			found = true
			if runtimeSkill.Secrets["bearer_token"] != "secret-token" {
				t.Fatalf("runtime secret = %#v, want bearer token", runtimeSkill.Secrets)
			}
		}
	}
	if !found {
		t.Fatalf("runtime skills = %#v, want created skill", runtimeSkills)
	}
}

func TestStoreMaterializesHTTPSkillSecretRef(t *testing.T) {
	store := NewStore()
	skill, err := store.CreateHTTPSkill("demo-user", "demo-project", protocol.HTTPSkillInput{
		Name:                 "Secret Ref API",
		Method:               "POST",
		URL:                  "https://example.com/hook",
		AuthType:             "bearer",
		BearerTokenSecretRef: "vault://niceagent/weather",
	})
	if err != nil {
		t.Fatalf("create http skill: %v", err)
	}

	runtimeSkills := store.ListRuntimeSkillsForUser("demo-user", "demo-project")
	for _, runtimeSkill := range runtimeSkills {
		if runtimeSkill.Skill.ID != skill.ID {
			continue
		}
		if runtimeSkill.Secrets["bearer_token"] != "" {
			t.Fatalf("legacy runtime secret = %#v, want empty for secret_ref", runtimeSkill.Secrets)
		}
		material := runtimeSkill.SecretMaterial("bearer_token")
		if material.SecretRef != "vault://niceagent/weather" {
			t.Fatalf("secret material = %#v, want secret_ref", material)
		}
		return
	}
	t.Fatalf("runtime skills = %#v, want created skill", runtimeSkills)
}

func TestStoreUpdatesRunStatus(t *testing.T) {
	store := NewStore()
	chat := mustCreateChat(t, store, "demo-user", "status")
	_, run, err := store.AddUserMessage(chat.ID, "demo-user", "go")
	if err != nil {
		t.Fatalf("add user message: %v", err)
	}

	updated, err := store.UpdateRunStatus(run.ID, protocol.RunRunning, "")
	if err != nil {
		t.Fatalf("update run status: %v", err)
	}
	if updated.StartedAt == nil {
		t.Fatal("expected started_at to be set")
	}

	updated, err = store.UpdateRunStatus(run.ID, protocol.RunSucceeded, "")
	if err != nil {
		t.Fatalf("finish run: %v", err)
	}
	if updated.FinishedAt == nil {
		t.Fatal("expected finished_at to be set")
	}
}

func TestStoreSumsRunUsageTokensSince(t *testing.T) {
	store := NewStore()
	chat := mustCreateChat(t, store, "demo-user", "usage quota")
	_, run, err := store.AddUserMessage(chat.ID, "demo-user", "hello")
	if err != nil {
		t.Fatalf("add user message: %v", err)
	}
	if _, err := store.SaveRunUsage(run.ID, protocol.RunUsage{
		Provider:              "openai-compatible",
		Model:                 "deepseek-chat",
		InputTokens:           7,
		OutputTokens:          5,
		Estimated:             true,
		TokenEstimator:        "heuristic_rune_div4",
		Cost:                  0.0012,
		Currency:              "USD",
		ToolCalls:             2,
		ToolErrors:            1,
		SandboxCommands:       1,
		SandboxDurationMillis: 123,
		SandboxOutputBytes:    456,
		SandboxCPUMillis:      78,
		SandboxMemoryMaxBytes: 1024,
		ArtifactCount:         3,
		ArtifactBytes:         4096,
	}); err != nil {
		t.Fatalf("save usage: %v", err)
	}
	usage, err := store.GetRunUsage(run.ID)
	if err != nil {
		t.Fatalf("get usage: %v", err)
	}
	if usage.ToolCalls != 2 || usage.ToolErrors != 1 || usage.SandboxCommands != 1 ||
		usage.SandboxDurationMillis != 123 || usage.SandboxOutputBytes != 456 ||
		usage.SandboxCPUMillis != 78 || usage.SandboxMemoryMaxBytes != 1024 ||
		usage.ArtifactCount != 3 || usage.ArtifactBytes != 4096 {
		t.Fatalf("usage = %#v, want tool and sandbox usage", usage)
	}
	if !usage.Estimated || usage.TokenEstimator != "heuristic_rune_div4" {
		t.Fatalf("usage estimator = estimated:%v estimator:%q, want heuristic estimate metadata", usage.Estimated, usage.TokenEstimator)
	}

	total := store.SumRunUsageTokensSince("demo-user", app.DemoProjectID, time.Now().UTC().Add(-time.Hour))
	if total != 12 {
		t.Fatalf("usage token total = %d, want 12", total)
	}
	if total := store.SumRunUsageTokensSince("demo-user", "other-project", time.Now().UTC().Add(-time.Hour)); total != 0 {
		t.Fatalf("other project token total = %d, want 0", total)
	}
	totals := store.SumRunUsageSince("demo-user", app.DemoProjectID, time.Now().UTC().Add(-time.Hour))
	if totals.TotalTokens != 12 || totals.ToolCalls != 2 || totals.SandboxCommands != 1 || totals.ArtifactBytes != 4096 {
		t.Fatalf("usage totals = %#v, want token/tool/sandbox totals", totals)
	}
	buckets := store.ListRunUsageBucketsSince(app.DemoProjectID, time.Now().UTC().Add(-time.Hour))
	if len(buckets) != 1 {
		t.Fatalf("usage bucket count = %d, want 1", len(buckets))
	}
	if bucket := buckets[0]; bucket.Provider != "openai-compatible" || bucket.Model != "deepseek-chat" ||
		bucket.Currency != "USD" || !bucket.Estimated || bucket.TokenEstimator != "heuristic_rune_div4" ||
		bucket.RunCount != 1 || bucket.TotalTokens != 12 || bucket.ToolCalls != 2 || bucket.ArtifactBytes != 4096 {
		t.Fatalf("usage bucket = %#v, want provider/model billing bucket", bucket)
	}
}

func TestStoreListsProjectRoles(t *testing.T) {
	store := NewStore()
	if roles := store.ListProjectRoles("demo-user", app.DemoProjectID); len(roles) != 1 || roles[0] != "owner" {
		t.Fatalf("demo roles = %#v, want owner", roles)
	}
	store.SetProjectRole("user-a", "project-a", "viewer")
	if roles := store.ListProjectRoles("user-a", "project-a"); len(roles) != 1 || roles[0] != "viewer" {
		t.Fatalf("custom roles = %#v, want viewer", roles)
	}
}

func TestStoreListsOrganizationRoles(t *testing.T) {
	store := NewStore()
	if roles := store.ListOrganizationRoles("demo-user", app.DemoOrgID); len(roles) != 1 || roles[0] != "owner" {
		t.Fatalf("demo organization roles = %#v, want owner", roles)
	}
	store.SetOrganizationRole("user-a", "org-a", "admin")
	if roles := store.ListOrganizationRoles("user-a", "org-a"); len(roles) != 1 || roles[0] != "admin" {
		t.Fatalf("custom organization roles = %#v, want admin", roles)
	}
}

func TestStoreChecksProjectOrganization(t *testing.T) {
	store := NewStore()
	if !store.ProjectBelongsToOrganization(app.DemoProjectID, app.DemoOrgID) {
		t.Fatal("demo project should belong to demo org")
	}
	if store.ProjectBelongsToOrganization(app.DemoProjectID, "other-org") {
		t.Fatal("demo project unexpectedly belongs to other org")
	}
	store.SetProjectOrganization("project-a", "org-a")
	if !store.ProjectBelongsToOrganization("project-a", "org-a") {
		t.Fatal("project-a should belong to org-a")
	}
}

func TestStoreBindsUserIdentity(t *testing.T) {
	store := NewStore()
	identity, err := store.BindUserIdentity(protocol.UserIdentity{
		UserID:   "user-a",
		Provider: "oidc",
		Issuer:   "https://issuer.example.test",
		Subject:  "subject-a",
		Email:    "user-a@example.test",
		Name:     "User A",
	})
	if err != nil {
		t.Fatalf("bind identity: %v", err)
	}
	if identity.ID == "" || identity.UserID != "user-a" || identity.Email != "user-a@example.test" {
		t.Fatalf("identity = %#v", identity)
	}
	updated, err := store.BindUserIdentity(protocol.UserIdentity{
		UserID:   "user-a",
		Provider: "oidc",
		Issuer:   "https://issuer.example.test",
		Subject:  "subject-a",
		Email:    "user-a-new@example.test",
	})
	if err != nil {
		t.Fatalf("update same identity: %v", err)
	}
	if updated.ID != identity.ID || updated.Email != "user-a-new@example.test" {
		t.Fatalf("updated identity = %#v", updated)
	}
	if _, err := store.BindUserIdentity(protocol.UserIdentity{
		UserID:   "user-b",
		Provider: "oidc",
		Issuer:   "https://issuer.example.test",
		Subject:  "subject-a",
		Email:    "user-b@example.test",
	}); err != app.ErrIdentityConflict {
		t.Fatalf("same external identity err = %v, want ErrIdentityConflict", err)
	}
	if _, err := store.BindUserIdentity(protocol.UserIdentity{
		UserID:   "user-a",
		Provider: "oidc",
		Issuer:   "https://issuer.example.test",
		Subject:  "subject-other",
		Email:    "user-a@example.test",
	}); err != app.ErrIdentityConflict {
		t.Fatalf("same user different identity err = %v, want ErrIdentityConflict", err)
	}
}

func TestStoreManagesProjectMembers(t *testing.T) {
	store := NewStore()
	member, err := store.UpsertProjectMember(app.DemoProjectID, protocol.ProjectMemberInput{
		UserID: "user-a",
		Email:  "user-a@example.test",
		Name:   "User A",
		Role:   "viewer",
	})
	if err != nil {
		t.Fatalf("upsert project member: %v", err)
	}
	if member.UserID != "user-a" || member.Role != "viewer" || member.Email != "user-a@example.test" {
		t.Fatalf("member = %#v", member)
	}
	if roles := store.ListProjectRoles("user-a", app.DemoProjectID); len(roles) != 1 || roles[0] != "viewer" {
		t.Fatalf("roles = %#v, want viewer", roles)
	}
	updated, err := store.UpsertProjectMember(app.DemoProjectID, protocol.ProjectMemberInput{UserID: "user-a", Role: "member"})
	if err != nil {
		t.Fatalf("update project member: %v", err)
	}
	if updated.Role != "member" {
		t.Fatalf("updated member = %#v, want role member", updated)
	}
	members := store.ListProjectMembers(app.DemoProjectID)
	if len(members) < 2 {
		t.Fatalf("members = %#v, want demo owner and user-a", members)
	}
	removed, err := store.RemoveProjectMember(app.DemoProjectID, "user-a")
	if err != nil {
		t.Fatalf("remove project member: %v", err)
	}
	if removed.UserID != "user-a" || removed.Role != "member" {
		t.Fatalf("removed member = %#v", removed)
	}
	if roles := store.ListProjectRoles("user-a", app.DemoProjectID); len(roles) != 0 {
		t.Fatalf("roles after remove = %#v, want empty", roles)
	}
	if _, err := store.UpsertProjectMember(app.DemoProjectID, protocol.ProjectMemberInput{UserID: "bad", Role: "superuser"}); err != app.ErrInvalidInput {
		t.Fatalf("invalid role err = %v, want ErrInvalidInput", err)
	}
}

func TestStoreManagesOrganizationMembers(t *testing.T) {
	store := NewStore()
	member, err := store.UpsertOrganizationMember(app.DemoOrgID, protocol.OrganizationMemberInput{
		UserID: "user-a",
		Email:  "user-a@example.test",
		Name:   "User A",
		Role:   "admin",
	})
	if err != nil {
		t.Fatalf("upsert organization member: %v", err)
	}
	if member.UserID != "user-a" || member.Role != "admin" || member.Email != "user-a@example.test" {
		t.Fatalf("member = %#v", member)
	}
	members := store.ListOrganizationMembers(app.DemoOrgID)
	if len(members) < 2 {
		t.Fatalf("organization members = %#v, want demo owner and user-a", members)
	}
	updated, err := store.UpsertOrganizationMember(app.DemoOrgID, protocol.OrganizationMemberInput{UserID: "user-a", Role: "viewer"})
	if err != nil {
		t.Fatalf("update organization member: %v", err)
	}
	if updated.Role != "viewer" {
		t.Fatalf("updated member = %#v, want role viewer", updated)
	}
	removed, err := store.RemoveOrganizationMember(app.DemoOrgID, "user-a")
	if err != nil {
		t.Fatalf("remove organization member: %v", err)
	}
	if removed.UserID != "user-a" || removed.Role != "viewer" {
		t.Fatalf("removed member = %#v", removed)
	}
	if members := store.ListOrganizationMembers(app.DemoOrgID); len(members) != 1 || members[0].UserID != app.DemoUserID {
		t.Fatalf("members after remove = %#v, want demo owner only", members)
	}
	if _, err := store.UpsertOrganizationMember(app.DemoOrgID, protocol.OrganizationMemberInput{UserID: "bad", Role: "superuser"}); err != app.ErrInvalidInput {
		t.Fatalf("invalid role err = %v, want ErrInvalidInput", err)
	}
}

func TestStoreManagesInvitations(t *testing.T) {
	store := NewStore()
	orgInvitation, err := store.CreateInvitation(app.DemoOrgID, app.DemoUserID, protocol.InvitationInput{
		Email: "invited@example.test",
		Role:  "admin",
	})
	if err != nil {
		t.Fatalf("create org invitation: %v", err)
	}
	if orgInvitation.Token == "" || orgInvitation.Status != protocol.InvitationPending {
		t.Fatalf("org invitation = %#v", orgInvitation)
	}
	delivery, err := store.EnqueueInvitationEmail(orgInvitation, 2)
	if err != nil {
		t.Fatalf("enqueue invitation email: %v", err)
	}
	claimed := store.ClaimDueInvitationEmails(1, "worker-a", time.Now().UTC().Add(time.Minute))
	if len(claimed) != 1 || claimed[0].ID != delivery.ID || claimed[0].Attempts != 1 || claimed[0].Invitation.Token != orgInvitation.Token {
		t.Fatalf("claimed deliveries = %#v", claimed)
	}
	nextAttempt := time.Now().UTC()
	if err := store.MarkInvitationEmailFailed(delivery.ID, "temporary smtp failure", &nextAttempt, false); err != nil {
		t.Fatalf("mark invitation email failed: %v", err)
	}
	claimed = store.ClaimDueInvitationEmails(1, "worker-b", time.Now().UTC().Add(time.Minute))
	if len(claimed) != 1 || claimed[0].Attempts != 2 || claimed[0].LockedBy != "worker-b" {
		t.Fatalf("reclaimed deliveries = %#v", claimed)
	}
	if err := store.MarkInvitationEmailSent(delivery.ID); err != nil {
		t.Fatalf("mark invitation email sent: %v", err)
	}
	if claimed = store.ClaimDueInvitationEmails(1, "worker-c", time.Now().UTC().Add(time.Minute)); len(claimed) != 0 {
		t.Fatalf("sent delivery was claimed again: %#v", claimed)
	}
	invitations := store.ListInvitations(app.DemoOrgID)
	if len(invitations) != 1 || invitations[0].Token != "" {
		t.Fatalf("listed invitations = %#v, want redacted token", invitations)
	}
	accepted, err := store.AcceptInvitation(orgInvitation.Token, "invited-user", "invited@example.test", "Invited User")
	if err != nil {
		t.Fatalf("accept org invitation: %v", err)
	}
	if accepted.Status != protocol.InvitationAccepted || accepted.AcceptedByUserID != "invited-user" || accepted.Token != "" {
		t.Fatalf("accepted org invitation = %#v", accepted)
	}
	if roles := store.ListOrganizationRoles("invited-user", app.DemoOrgID); len(roles) != 1 || roles[0] != "admin" {
		t.Fatalf("organization roles after accept = %#v, want admin", roles)
	}

	projectInvitation, err := store.CreateInvitation(app.DemoOrgID, app.DemoUserID, protocol.InvitationInput{
		Email:     "project-invited@example.test",
		Role:      "viewer",
		ProjectID: app.DemoProjectID,
	})
	if err != nil {
		t.Fatalf("create project invitation: %v", err)
	}
	if _, err := store.AcceptInvitation(projectInvitation.Token, "project-invited", "project-invited@example.test", "Project Invited"); err != nil {
		t.Fatalf("accept project invitation: %v", err)
	}
	if roles := store.ListProjectRoles("project-invited", app.DemoProjectID); len(roles) != 1 || roles[0] != "viewer" {
		t.Fatalf("project roles after accept = %#v, want viewer", roles)
	}
	if _, err := store.AcceptInvitation(projectInvitation.Token, "project-invited-again", "project-invited@example.test", "Again"); err != app.ErrInvalidInput {
		t.Fatalf("second accept err = %v, want ErrInvalidInput", err)
	}
	mismatchInvitation, err := store.CreateInvitation(app.DemoOrgID, app.DemoUserID, protocol.InvitationInput{
		Email: "mismatch@example.test",
		Role:  "viewer",
	})
	if err != nil {
		t.Fatalf("create mismatch invitation: %v", err)
	}
	if _, err := store.AcceptInvitation(mismatchInvitation.Token, "wrong-email", "other@example.test", "Wrong Email"); err != app.ErrInvalidInput {
		t.Fatalf("wrong email err = %v, want ErrInvalidInput", err)
	}
	if _, err := store.CreateInvitation(app.DemoOrgID, app.DemoUserID, protocol.InvitationInput{Email: "bad", Role: "viewer"}); err != app.ErrInvalidInput {
		t.Fatalf("invalid email err = %v, want ErrInvalidInput", err)
	}
	if _, err := store.CreateInvitation(app.DemoOrgID, app.DemoUserID, protocol.InvitationInput{
		Email:     "wrong-project@example.test",
		Role:      "viewer",
		ProjectID: "missing-project",
	}); err != app.ErrInvalidInput {
		t.Fatalf("wrong project err = %v, want ErrInvalidInput", err)
	}
}

func TestStoreManagesProjectQuotaPolicy(t *testing.T) {
	store := NewStore()
	if _, ok := store.GetProjectQuotaPolicy(app.DemoProjectID); ok {
		t.Fatal("unexpected default project quota policy")
	}
	policy, err := store.SetProjectQuotaPolicy(app.DemoProjectID, protocol.ProjectQuotaPolicyInput{
		MaxConcurrentRuns:       2,
		MaxRunsPerHour:          10,
		MaxModelTokensPerDay:    1000,
		MaxToolCallsPerDay:      50,
		MaxSandboxSecondsPerDay: 60,
	})
	if err != nil {
		t.Fatalf("set quota policy: %v", err)
	}
	if policy.ProjectID != app.DemoProjectID || policy.MaxConcurrentRuns != 2 || policy.MaxRunsPerHour != 10 ||
		policy.MaxModelTokensPerDay != 1000 || policy.MaxToolCallsPerDay != 50 || policy.MaxSandboxSecondsPerDay != 60 {
		t.Fatalf("policy = %#v", policy)
	}
	got, ok := store.GetProjectQuotaPolicy(app.DemoProjectID)
	if !ok || got.MaxConcurrentRuns != 2 {
		t.Fatalf("got policy = %#v ok=%v", got, ok)
	}
	if _, err := store.SetProjectQuotaPolicy(app.DemoProjectID, protocol.ProjectQuotaPolicyInput{MaxConcurrentRuns: -1}); err != app.ErrInvalidInput {
		t.Fatalf("invalid quota err = %v, want ErrInvalidInput", err)
	}
	if _, err := store.SetProjectQuotaPolicy(app.DemoProjectID, protocol.ProjectQuotaPolicyInput{MaxToolCallsPerDay: -1}); err != app.ErrInvalidInput {
		t.Fatalf("invalid tool quota err = %v, want ErrInvalidInput", err)
	}
}

func TestStoreCountsRunsForQuota(t *testing.T) {
	store := NewStore()
	chat := mustCreateChat(t, store, "demo-user", "quota")
	_, run, err := store.AddUserMessage(chat.ID, "demo-user", "first")
	if err != nil {
		t.Fatalf("add first message: %v", err)
	}
	if active := store.CountActiveRuns("demo-user", app.DemoProjectID); active != 1 {
		t.Fatalf("active runs = %d, want 1", active)
	}
	if recent := store.CountRunsCreatedSince("demo-user", app.DemoProjectID, time.Now().Add(-time.Hour)); recent != 1 {
		t.Fatalf("recent runs = %d, want 1", recent)
	}
	if _, err := store.UpdateRunStatus(run.ID, protocol.RunSucceeded, ""); err != nil {
		t.Fatalf("complete run: %v", err)
	}
	if active := store.CountActiveRuns("demo-user", app.DemoProjectID); active != 0 {
		t.Fatalf("active runs after completion = %d, want 0", active)
	}
	if recent := store.CountRunsCreatedSince("demo-user", app.DemoProjectID, time.Now().Add(time.Minute)); recent != 0 {
		t.Fatalf("future recent runs = %d, want 0", recent)
	}
}

func TestStoreClaimsAndChecksRunAttempts(t *testing.T) {
	store := NewStore()
	chat := mustCreateChat(t, store, "demo-user", "attempt")
	_, run, err := store.AddUserMessage(chat.ID, "demo-user", "go")
	if err != nil {
		t.Fatalf("add user message: %v", err)
	}

	lease := time.Now().UTC().Add(time.Minute)
	claimed, err := store.ClaimRunAttempt(run.ID, "attempt-1", "runtime-a", lease)
	if err != nil {
		t.Fatalf("claim attempt: %v", err)
	}
	if claimed.AttemptID != "attempt-1" || claimed.ClaimedBy != "runtime-a" || claimed.AttemptCount != 1 || claimed.LeaseExpiresAt == nil {
		t.Fatalf("claimed run = %#v", claimed)
	}
	if _, err := store.CheckRunAttempt(run.ID, "attempt-1"); err != nil {
		t.Fatalf("check active attempt: %v", err)
	}
	if _, err := store.CheckRunAttempt(run.ID, "attempt-2"); err != app.ErrAttemptMismatch {
		t.Fatalf("check stale attempt err = %v, want ErrAttemptMismatch", err)
	}
	if _, err := store.ClaimRunAttempt(run.ID, "attempt-2", "runtime-b", time.Now().UTC().Add(time.Minute)); err != app.ErrAttemptMismatch {
		t.Fatalf("claim competing attempt err = %v, want ErrAttemptMismatch", err)
	}

	if _, err := store.ClaimRunAttempt(run.ID, "attempt-1", "runtime-a", time.Now().UTC().Add(-time.Minute)); err != nil {
		t.Fatalf("renew active attempt with expired lease: %v", err)
	}
	claimed, err = store.ClaimRunAttempt(run.ID, "attempt-2", "runtime-b", time.Now().UTC().Add(time.Minute))
	if err != nil {
		t.Fatalf("claim after expired lease: %v", err)
	}
	if claimed.AttemptID != "attempt-2" || claimed.AttemptCount != 2 {
		t.Fatalf("claimed after lease expiry = %#v", claimed)
	}
}

func TestStoreKeepsTerminalRunStatus(t *testing.T) {
	store := NewStore()
	chat := mustCreateChat(t, store, "demo-user", "terminal")
	_, run, err := store.AddUserMessage(chat.ID, "demo-user", "cancel me")
	if err != nil {
		t.Fatalf("add user message: %v", err)
	}

	canceled, err := store.UpdateRunStatus(run.ID, protocol.RunCanceled, "")
	if err != nil {
		t.Fatalf("cancel run: %v", err)
	}
	if canceled.Status != protocol.RunCanceled {
		t.Fatalf("status = %q, want canceled", canceled.Status)
	}

	for _, status := range []protocol.RunStatus{protocol.RunRunning, protocol.RunSucceeded, protocol.RunFailed} {
		updated, err := store.UpdateRunStatus(run.ID, status, "late update")
		if err != nil {
			t.Fatalf("late update to %s: %v", status, err)
		}
		if updated.Status != protocol.RunCanceled {
			t.Fatalf("late update to %s overwrote status to %q", status, updated.Status)
		}
		if updated.Error != "" {
			t.Fatalf("late update to %s overwrote error to %q", status, updated.Error)
		}
	}
}

func TestStoreEventSeqAndReplayContract(t *testing.T) {
	store := NewStore()
	chat := mustCreateChat(t, store, "demo-user", "events")
	_, run, err := store.AddUserMessage(chat.ID, "demo-user", "emit")
	if err != nil {
		t.Fatalf("add user message: %v", err)
	}

	for i, typ := range []protocol.RunEventType{
		protocol.EventRunQueued,
		protocol.EventRunStarted,
		protocol.EventRunSucceeded,
	} {
		event, err := store.AddEvent(run.ID, typ, string(typ), nil)
		if err != nil {
			t.Fatalf("add event %d: %v", i, err)
		}
		wantSeq := int64(i + 1)
		if event.Seq != wantSeq {
			t.Fatalf("event seq = %d, want %d", event.Seq, wantSeq)
		}
	}

	events := store.ListEvents(run.ID, 1)
	if len(events) != 2 {
		t.Fatalf("replayed events len = %d, want 2", len(events))
	}
	if events[0].Seq != 2 || events[1].Seq != 3 {
		t.Fatalf("replayed seqs = [%d %d], want [2 3]", events[0].Seq, events[1].Seq)
	}
}

func TestStoreSubscribeReceivesNewEvents(t *testing.T) {
	store := NewStore()
	chat := mustCreateChat(t, store, "demo-user", "subscribe")
	_, run, err := store.AddUserMessage(chat.ID, "demo-user", "emit")
	if err != nil {
		t.Fatalf("add user message: %v", err)
	}
	ch, cancel := store.Subscribe(run.ID)
	defer cancel()

	want, err := store.AddEvent(run.ID, protocol.EventRunQueued, "queued", nil)
	if err != nil {
		t.Fatalf("add event: %v", err)
	}

	got := <-ch
	if got.ID != want.ID || got.Seq != want.Seq {
		t.Fatalf("subscriber got event id/seq = %s/%d, want %s/%d", got.ID, got.Seq, want.ID, want.Seq)
	}
}

func mustCreateChat(t *testing.T, repo app.Repository, userID, title string) protocol.ChatSession {
	t.Helper()
	chat, err := repo.CreateChat(userID, app.DemoProjectID, title)
	if err != nil {
		t.Fatalf("create chat: %v", err)
	}
	return chat
}

func containsSkill(skills []protocol.Skill, id string) bool {
	for _, skill := range skills {
		if skill.ID == id {
			return true
		}
	}
	return false
}
