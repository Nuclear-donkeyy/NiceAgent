package events

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"niceagent/common/protocol"
	"niceagent/control-plane/internal/app"
	"niceagent/control-plane/internal/repository"
)

func TestFanoutRepositoryPublishesNudgeAndDeduplicatesLocalEvents(t *testing.T) {
	store := repository.NewStore()
	bus := newMemoryNudgeBus()
	repo := NewFanoutRepository(store, bus, slog.New(slog.NewTextHandler(io.Discard, nil)))
	chat, err := repo.CreateChat(app.DemoUserID, app.DemoProjectID, "fanout")
	if err != nil {
		t.Fatalf("create chat: %v", err)
	}
	_, run, err := repo.AddUserMessage(chat.ID, app.DemoUserID, "hello")
	if err != nil {
		t.Fatalf("add user message: %v", err)
	}

	ch, cancel := repo.Subscribe(run.ID)
	defer cancel()
	event, err := repo.AddEvent(run.ID, protocol.EventRunQueued, "queued", nil)
	if err != nil {
		t.Fatalf("add event: %v", err)
	}

	select {
	case got := <-ch:
		if got.ID != event.ID || got.Seq != 1 {
			t.Fatalf("event = %#v, want %#v", got, event)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive fanout event")
	}
	select {
	case got := <-ch:
		t.Fatalf("unexpected duplicate event: %#v", got)
	case <-time.After(50 * time.Millisecond):
	}
}

type memoryNudgeBus struct {
	mu          sync.Mutex
	subscribers map[string][]chan EventNudge
}

func newMemoryNudgeBus() *memoryNudgeBus {
	return &memoryNudgeBus{subscribers: map[string][]chan EventNudge{}}
}

func (b *memoryNudgeBus) Publish(_ context.Context, event protocol.RunEvent) error {
	b.mu.Lock()
	subscribers := append([]chan EventNudge(nil), b.subscribers[event.RunID]...)
	b.mu.Unlock()
	for _, ch := range subscribers {
		select {
		case ch <- EventNudge{RunID: event.RunID, Seq: event.Seq, EventID: event.ID}:
		default:
		}
	}
	return nil
}

func (b *memoryNudgeBus) Subscribe(_ context.Context, runID string) (<-chan EventNudge, func(), error) {
	ch := make(chan EventNudge, 16)
	b.mu.Lock()
	b.subscribers[runID] = append(b.subscribers[runID], ch)
	b.mu.Unlock()
	cancel := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		subscribers := b.subscribers[runID]
		for i, candidate := range subscribers {
			if candidate == ch {
				b.subscribers[runID] = append(subscribers[:i], subscribers[i+1:]...)
				break
			}
		}
		close(ch)
	}
	return ch, cancel, nil
}
