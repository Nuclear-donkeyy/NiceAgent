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
	chat, err := store.CreateChat("demo-user", "postgres")
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
		Provider:     "openai-compatible",
		Model:        "deepseek-v4-flash",
		InputTokens:  9,
		OutputTokens: 4,
		TotalTokens:  13,
		Estimated:    false,
	}); err != nil {
		t.Fatalf("save run usage: %v", err)
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
	chat, err := store.CreateChat("demo-user", title)
	if err != nil {
		t.Fatalf("create chat: %v", err)
	}
	matches := store.ListChats("demo-user", app.ChatListOptions{Query: title})
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
	matches = store.ListChats("demo-user", app.ChatListOptions{Query: title})
	if len(matches) != 0 {
		t.Fatalf("active search matches after archive = %#v, want none", matches)
	}
	matches = store.ListChats("demo-user", app.ChatListOptions{Query: title, IncludeArchived: true})
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
	var foundCLI bool
	for _, skill := range skills {
		if skill.ID == "cli.exec" {
			foundCLI = true
			if skill.RequiresAuth {
				t.Fatalf("cli.exec requires auth = true, want false")
			}
		}
	}
	if !foundCLI {
		t.Fatalf("skills = %#v, want cli.exec", skills)
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
