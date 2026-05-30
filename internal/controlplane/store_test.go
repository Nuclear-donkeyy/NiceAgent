package controlplane

import (
	"testing"

	"niceagent/internal/protocol"
)

func TestStoreCreatesChatMessageRunAndEvents(t *testing.T) {
	store := NewStore()
	chat := store.CreateChat("demo-user", "")
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

func TestStoreUpdatesRunStatus(t *testing.T) {
	store := NewStore()
	chat := store.CreateChat("demo-user", "status")
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

