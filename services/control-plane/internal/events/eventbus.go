package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"sync"

	"niceagent/common/protocol"
	"niceagent/control-plane/internal/app"

	redis "github.com/redis/go-redis/v9"
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

type EventNudge struct {
	RunID   string `json:"run_id"`
	Seq     int64  `json:"seq"`
	EventID string `json:"event_id,omitempty"`
}

type NudgeBus interface {
	Publish(ctx context.Context, event protocol.RunEvent) error
	Subscribe(ctx context.Context, runID string) (<-chan EventNudge, func(), error)
}

type RedisNudgeBus struct {
	client redis.UniversalClient
	prefix string
}

func NewRedisNudgeBus(addr, prefix string) *RedisNudgeBus {
	if addr == "" {
		addr = "127.0.0.1:6379"
	}
	if prefix == "" {
		prefix = "niceagent:run-events"
	}
	return &RedisNudgeBus{
		client: redis.NewClient(&redis.Options{Addr: addr}),
		prefix: prefix,
	}
}

func (b *RedisNudgeBus) Publish(ctx context.Context, event protocol.RunEvent) error {
	payload, err := json.Marshal(EventNudge{RunID: event.RunID, Seq: event.Seq, EventID: event.ID})
	if err != nil {
		return err
	}
	return b.client.Publish(ctx, b.channel(event.RunID), payload).Err()
}

func (b *RedisNudgeBus) Subscribe(ctx context.Context, runID string) (<-chan EventNudge, func(), error) {
	pubsub := b.client.Subscribe(ctx, b.channel(runID))
	if _, err := pubsub.Receive(ctx); err != nil {
		_ = pubsub.Close()
		return nil, nil, err
	}
	out := make(chan EventNudge, 16)
	done := make(chan struct{})
	go func() {
		defer close(out)
		defer pubsub.Close()
		ch := pubsub.Channel()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}
				var nudge EventNudge
				if err := json.Unmarshal([]byte(msg.Payload), &nudge); err != nil {
					continue
				}
				if nudge.RunID == "" {
					nudge.RunID = runID
				}
				select {
				case out <- nudge:
				case <-ctx.Done():
					return
				case <-done:
					return
				}
			}
		}
	}()
	cancel := func() {
		close(done)
		_ = pubsub.Close()
	}
	return out, cancel, nil
}

func (b *RedisNudgeBus) channel(runID string) string {
	return b.prefix + ":" + runID
}

type FanoutRepository struct {
	app.Repository
	bus NudgeBus
	log *slog.Logger
}

func NewFanoutRepository(repo app.Repository, bus NudgeBus, log *slog.Logger) *FanoutRepository {
	return &FanoutRepository{Repository: repo, bus: bus, log: log}
}

func (r *FanoutRepository) AddEvent(runID string, typ protocol.RunEventType, message string, payload any) (protocol.RunEvent, error) {
	event, err := r.Repository.AddEvent(runID, typ, message, payload)
	if err != nil || r.bus == nil {
		return event, err
	}
	if publishErr := r.bus.Publish(context.Background(), event); publishErr != nil && r.log != nil {
		r.log.Warn("publish run event nudge failed", "run_id", runID, "seq", event.Seq, "error", publishErr)
	}
	return event, nil
}

func (r *FanoutRepository) Subscribe(runID string) (<-chan protocol.RunEvent, func()) {
	localEvents, cancelLocal := r.Repository.Subscribe(runID)
	if r.bus == nil {
		return localEvents, cancelLocal
	}
	ctx, cancelContext := context.WithCancel(context.Background())
	nudges, cancelNudges, err := r.bus.Subscribe(ctx, runID)
	if err != nil {
		cancelContext()
		if r.log != nil {
			r.log.Warn("subscribe run event nudges failed", "run_id", runID, "error", err)
		}
		return localEvents, cancelLocal
	}

	out := make(chan protocol.RunEvent, 16)
	lastSeq := maxEventSeq(r.Repository.ListEvents(runID, 0))
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			cancelContext()
			cancelLocal()
			if cancelNudges != nil {
				cancelNudges()
			}
		})
	}
	go func() {
		defer close(out)
		for localEvents != nil || nudges != nil {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-localEvents:
				if !ok {
					localEvents = nil
					continue
				}
				if event.Seq > lastSeq && forwardEvent(ctx, out, event) {
					lastSeq = event.Seq
				}
			case nudge, ok := <-nudges:
				if !ok {
					nudges = nil
					continue
				}
				if nudge.Seq <= lastSeq {
					continue
				}
				for _, event := range r.Repository.ListEvents(runID, lastSeq) {
					if event.Seq <= lastSeq {
						continue
					}
					if !forwardEvent(ctx, out, event) {
						return
					}
					lastSeq = event.Seq
				}
			}
		}
	}()
	return out, cancel
}

func maxEventSeq(events []protocol.RunEvent) int64 {
	var maxSeq int64
	for _, event := range events {
		if event.Seq > maxSeq {
			maxSeq = event.Seq
		}
	}
	return maxSeq
}

func forwardEvent(ctx context.Context, out chan<- protocol.RunEvent, event protocol.RunEvent) bool {
	select {
	case out <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

func DecodeEventNudge(values map[string]string) (EventNudge, error) {
	seq, err := strconv.ParseInt(values["seq"], 10, 64)
	if err != nil {
		return EventNudge{}, err
	}
	return EventNudge{
		RunID:   values["run_id"],
		Seq:     seq,
		EventID: values["event_id"],
	}, nil
}
