package controlplane

import (
	"context"

	"niceagent/common/protocol"
)

type EventBus interface {
	Publish(ctx context.Context, event protocol.RunEvent) error
	Replay(ctx context.Context, runID string, afterSeq int64) ([]protocol.RunEvent, error)
	Subscribe(ctx context.Context, runID string) (<-chan protocol.RunEvent, func(), error)
}

type RepositoryEventBus struct {
	repo Repository
}

func NewRepositoryEventBus(repo Repository) *RepositoryEventBus {
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
	return ErrExternalAdapterNotImplemented
}

func (b RedisStreamsEventBus) Replay(context.Context, string, int64) ([]protocol.RunEvent, error) {
	return nil, ErrExternalAdapterNotImplemented
}

func (b RedisStreamsEventBus) Subscribe(context.Context, string) (<-chan protocol.RunEvent, func(), error) {
	return nil, nil, ErrExternalAdapterNotImplemented
}
