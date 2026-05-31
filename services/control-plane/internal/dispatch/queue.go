package controlplane

import (
	"context"
	"errors"
	"sync"

	"niceagent/common/protocol"
)

var ErrQueueClosed = errors.New("run queue is closed")

type QueuedRun struct {
	RunID       string   `json:"run_id"`
	ChatID      string   `json:"chat_id"`
	UserID      string   `json:"user_id"`
	WorkspaceID string   `json:"workspace_id"`
	UserMessage string   `json:"user_message"`
	SkillIDs    []string `json:"skill_ids"`
	ModelPolicy string   `json:"model_policy"`
}

func (q QueuedRun) Request() protocol.RunRequest {
	return protocol.RunRequest{
		RunID:       q.RunID,
		ChatID:      q.ChatID,
		UserID:      q.UserID,
		WorkspaceID: q.WorkspaceID,
		SkillIDs:    append([]string(nil), q.SkillIDs...),
		ModelPolicy: q.ModelPolicy,
	}
}

type RunQueue interface {
	Enqueue(ctx context.Context, run QueuedRun) error
	Dequeue(ctx context.Context) (QueuedRun, AckFunc, error)
}

type AckFunc func(ctx context.Context, err error) error

type MemoryRunQueue struct {
	ch     chan QueuedRun
	mu     sync.RWMutex
	closed bool
}

func NewMemoryRunQueue(size int) *MemoryRunQueue {
	if size <= 0 {
		size = 128
	}
	return &MemoryRunQueue{ch: make(chan QueuedRun, size)}
}

func (q *MemoryRunQueue) Enqueue(ctx context.Context, run QueuedRun) error {
	q.mu.RLock()
	closed := q.closed
	q.mu.RUnlock()
	if closed {
		return ErrQueueClosed
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case q.ch <- run:
		return nil
	}
}

func (q *MemoryRunQueue) Dequeue(ctx context.Context) (QueuedRun, AckFunc, error) {
	select {
	case <-ctx.Done():
		return QueuedRun{}, nil, ctx.Err()
	case run, ok := <-q.ch:
		if !ok {
			return QueuedRun{}, nil, ErrQueueClosed
		}
		return run, func(context.Context, error) error { return nil }, nil
	}
}

func (q *MemoryRunQueue) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.closed {
		q.closed = true
		close(q.ch)
	}
}

type RedisStreamsRunQueue struct {
	Addr     string
	Stream   string
	Group    string
	Consumer string
}

func (q RedisStreamsRunQueue) Enqueue(context.Context, QueuedRun) error {
	return errors.New("redis streams run queue adapter is configured but not implemented in the stdlib scaffold")
}

func (q RedisStreamsRunQueue) Dequeue(context.Context) (QueuedRun, AckFunc, error) {
	return QueuedRun{}, nil, errors.New("redis streams run queue adapter is configured but not implemented in the stdlib scaffold")
}
