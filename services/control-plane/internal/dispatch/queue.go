package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"niceagent/common/platform"
	"niceagent/common/protocol"

	redis "github.com/redis/go-redis/v9"
)

var ErrQueueClosed = errors.New("run queue is closed")

const (
	defaultRunQueueStream   = "niceagent:runs"
	defaultRunQueueGroup    = "agent-runtimes"
	defaultRunQueueConsumer = "control-plane"
	defaultRedisBlock       = 5 * time.Second
)

var errQueueEmpty = errors.New("run queue is empty")

type QueuedRun struct {
	RunID       string    `json:"run_id"`
	ChatID      string    `json:"chat_id"`
	UserID      string    `json:"user_id"`
	WorkspaceID string    `json:"workspace_id"`
	UserMessage string    `json:"user_message"`
	AttemptID   string    `json:"attempt_id,omitempty"`
	SkillIDs    []string  `json:"skill_ids"`
	ModelPolicy string    `json:"model_policy"`
	EnqueuedAt  time.Time `json:"enqueued_at,omitempty"`
}

func (q QueuedRun) Request() protocol.RunRequest {
	return protocol.RunRequest{
		RunID:       q.RunID,
		ChatID:      q.ChatID,
		UserID:      q.UserID,
		WorkspaceID: q.WorkspaceID,
		AttemptID:   q.AttemptID,
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
	Addr         string
	Stream       string
	Group        string
	Consumer     string
	BlockTimeout time.Duration

	client redisRunQueueClient
}

func NewRedisStreamsRunQueue(addr, stream, group, consumer string) *RedisStreamsRunQueue {
	q := &RedisStreamsRunQueue{
		Addr:     addr,
		Stream:   stream,
		Group:    group,
		Consumer: consumer,
	}
	q.setDefaults()
	q.client = newGoRedisRunQueueClient(q.Addr)
	return q
}

func newRedisStreamsRunQueueWithClient(client redisRunQueueClient, stream, group, consumer string) *RedisStreamsRunQueue {
	q := &RedisStreamsRunQueue{
		Stream:   stream,
		Group:    group,
		Consumer: consumer,
		client:   client,
	}
	q.setDefaults()
	return q
}

func (q *RedisStreamsRunQueue) Enqueue(ctx context.Context, run QueuedRun) error {
	q.ensureClient()
	run = normalizeQueuedRun(run)
	values, err := encodeQueuedRun(run)
	if err != nil {
		return err
	}
	_, err = q.client.XAdd(ctx, q.Stream, values)
	return err
}

func (q *RedisStreamsRunQueue) Dequeue(ctx context.Context) (QueuedRun, AckFunc, error) {
	q.ensureClient()
	if err := q.ensureGroup(ctx); err != nil {
		return QueuedRun{}, nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return QueuedRun{}, nil, err
		}
		message, err := q.client.XReadGroup(ctx, q.Stream, q.Group, q.Consumer, 1, q.BlockTimeout)
		if errors.Is(err, errQueueEmpty) {
			continue
		}
		if err != nil {
			return QueuedRun{}, nil, err
		}
		run, err := decodeQueuedRun(message.Values)
		if err != nil {
			_, _ = q.client.XAck(ctx, q.Stream, q.Group, message.ID)
			return QueuedRun{}, nil, err
		}
		ack := func(ctx context.Context, processErr error) error {
			if processErr != nil {
				return nil
			}
			_, err := q.client.XAck(ctx, q.Stream, q.Group, message.ID)
			return err
		}
		return run, ack, nil
	}
}

func (q *RedisStreamsRunQueue) Close() error {
	if q.client == nil {
		return nil
	}
	return q.client.Close()
}

func (q *RedisStreamsRunQueue) ensureClient() {
	q.setDefaults()
	if q.client == nil {
		q.client = newGoRedisRunQueueClient(q.Addr)
	}
}

func (q *RedisStreamsRunQueue) setDefaults() {
	if q.Addr == "" {
		q.Addr = "127.0.0.1:6379"
	}
	if q.Stream == "" {
		q.Stream = defaultRunQueueStream
	}
	if q.Group == "" {
		q.Group = defaultRunQueueGroup
	}
	if q.Consumer == "" {
		q.Consumer = defaultRunQueueConsumer
	}
	if q.BlockTimeout <= 0 {
		q.BlockTimeout = defaultRedisBlock
	}
}

func (q *RedisStreamsRunQueue) ensureGroup(ctx context.Context) error {
	err := q.client.XGroupCreateMkStream(ctx, q.Stream, q.Group, "0")
	if err == nil || isRedisBusyGroup(err) {
		return nil
	}
	return err
}

func normalizeQueuedRun(run QueuedRun) QueuedRun {
	if run.AttemptID == "" {
		run.AttemptID = platform.NewID("attempt")
	}
	if run.EnqueuedAt.IsZero() {
		run.EnqueuedAt = time.Now().UTC()
	}
	return run
}

func encodeQueuedRun(run QueuedRun) (map[string]any, error) {
	skillIDs, err := json.Marshal(run.SkillIDs)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"schema_version": "1",
		"run_id":         run.RunID,
		"chat_id":        run.ChatID,
		"user_id":        run.UserID,
		"workspace_id":   run.WorkspaceID,
		"user_message":   run.UserMessage,
		"attempt_id":     run.AttemptID,
		"skill_ids":      string(skillIDs),
		"model_policy":   run.ModelPolicy,
		"enqueued_at":    run.EnqueuedAt.Format(time.RFC3339Nano),
	}, nil
}

func decodeQueuedRun(values map[string]any) (QueuedRun, error) {
	run := QueuedRun{
		RunID:       redisValueString(values["run_id"]),
		ChatID:      redisValueString(values["chat_id"]),
		UserID:      redisValueString(values["user_id"]),
		WorkspaceID: redisValueString(values["workspace_id"]),
		UserMessage: redisValueString(values["user_message"]),
		AttemptID:   redisValueString(values["attempt_id"]),
		ModelPolicy: redisValueString(values["model_policy"]),
	}
	if run.RunID == "" {
		return QueuedRun{}, errors.New("queued run is missing run_id")
	}
	if rawSkillIDs := redisValueString(values["skill_ids"]); rawSkillIDs != "" {
		if err := json.Unmarshal([]byte(rawSkillIDs), &run.SkillIDs); err != nil {
			return QueuedRun{}, fmt.Errorf("decode queued run skill_ids: %w", err)
		}
	}
	if rawEnqueuedAt := redisValueString(values["enqueued_at"]); rawEnqueuedAt != "" {
		enqueuedAt, err := time.Parse(time.RFC3339Nano, rawEnqueuedAt)
		if err != nil {
			return QueuedRun{}, fmt.Errorf("decode queued run enqueued_at: %w", err)
		}
		run.EnqueuedAt = enqueuedAt
	}
	return run, nil
}

func redisValueString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return fmt.Sprint(typed)
	}
}

func isRedisBusyGroup(err error) bool {
	return strings.Contains(err.Error(), "BUSYGROUP")
}

type redisStreamMessage struct {
	ID     string
	Values map[string]any
}

type redisRunQueueClient interface {
	XAdd(ctx context.Context, stream string, values map[string]any) (string, error)
	XGroupCreateMkStream(ctx context.Context, stream, group, start string) error
	XReadGroup(ctx context.Context, stream, group, consumer string, count int64, block time.Duration) (redisStreamMessage, error)
	XAck(ctx context.Context, stream, group string, ids ...string) (int64, error)
	Close() error
}

type goRedisRunQueueClient struct {
	client *redis.Client
}

func newGoRedisRunQueueClient(addr string) *goRedisRunQueueClient {
	return &goRedisRunQueueClient{client: redis.NewClient(&redis.Options{Addr: addr})}
}

func (c *goRedisRunQueueClient) XAdd(ctx context.Context, stream string, values map[string]any) (string, error) {
	return c.client.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: values}).Result()
}

func (c *goRedisRunQueueClient) XGroupCreateMkStream(ctx context.Context, stream, group, start string) error {
	return c.client.XGroupCreateMkStream(ctx, stream, group, start).Err()
}

func (c *goRedisRunQueueClient) XReadGroup(ctx context.Context, stream, group, consumer string, count int64, block time.Duration) (redisStreamMessage, error) {
	streams, err := c.client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    group,
		Consumer: consumer,
		Streams:  []string{stream, ">"},
		Count:    count,
		Block:    block,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return redisStreamMessage{}, errQueueEmpty
	}
	if err != nil {
		return redisStreamMessage{}, err
	}
	for _, streamResult := range streams {
		for _, message := range streamResult.Messages {
			return redisStreamMessage{ID: message.ID, Values: message.Values}, nil
		}
	}
	return redisStreamMessage{}, errQueueEmpty
}

func (c *goRedisRunQueueClient) XAck(ctx context.Context, stream, group string, ids ...string) (int64, error) {
	return c.client.XAck(ctx, stream, group, ids...).Result()
}

func (c *goRedisRunQueueClient) Close() error {
	return c.client.Close()
}
