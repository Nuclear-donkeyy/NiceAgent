package events

import (
	"context"

	"niceagent/common/protocol"
	"niceagent/control-plane/internal/app"
)

type EventBus interface {
	Publish(ctx context.Context, event protocol.RunEvent) error
	Replay(ctx context.Context, runID string, afterSeq int64) ([]protocol.RunEvent, error)
	Subscribe(ctx context.Context, runID string) (<-chan protocol.RunEvent, func(), error)
}

type RepositoryEventBus struct {
	repo app.Repository
}

func NewRepositoryEventBus(repo app.Repository) *RepositoryEventBus {
	return &RepositoryEventBus{repo: repo}
}

func (b *RepositoryEventBus) Publish(_ context.Context, event protocol.RunEvent) error {
	_, err := b.repo.AddEvent(event.RunID, event.Type, event.Message, event.Payload)
	return err
}

func (b *RepositoryEventBus) Replay(_ context.Context, runID string, afterSeq int64) ([]protocol.RunEvent, error) {
	return b.repo.ListEvents(runID, afterSeq), nil
}

func (b *RepositoryEventBus) Subscribe(_ context.Context, runID string) (<-chan protocol.RunEvent, func(), error) {
	ch, cancel := b.repo.Subscribe(runID)
	return ch, cancel, nil
}

type RedisStreamsEventBus struct {
	Addr   string
	Stream string
}

func (b RedisStreamsEventBus) Publish(context.Context, protocol.RunEvent) error {
	return app.ErrExternalAdapterNotImplemented
}

func (b RedisStreamsEventBus) Replay(context.Context, string, int64) ([]protocol.RunEvent, error) {
	return nil, app.ErrExternalAdapterNotImplemented
}

func (b RedisStreamsEventBus) Subscribe(context.Context, string) (<-chan protocol.RunEvent, func(), error) {
	return nil, nil, app.ErrExternalAdapterNotImplemented
}
