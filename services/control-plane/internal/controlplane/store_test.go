package controlplane

import (
	"testing"

	"niceagent/common/protocol"
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

func mustCreateChat(t *testing.T, repo Repository, userID, title string) protocol.ChatSession {
	t.Helper()
	chat, err := repo.CreateChat(userID, title)
	if err != nil {
		t.Fatalf("create chat: %v", err)
	}
	return chat
}
