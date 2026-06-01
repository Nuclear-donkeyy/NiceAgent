package dispatch

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestRedisStreamsRunQueueEnqueueWritesStreamFields(t *testing.T) {
	client := &fakeRedisRunQueueClient{}
	queue := newRedisStreamsRunQueueWithClient(client, "niceagent:runs:test", "workers", "consumer-a")
	queue.MaxLen = 1000
	enqueuedAt := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)

	err := queue.Enqueue(context.Background(), QueuedRun{
		RunID:      "run_1",
		AttemptID:  "attempt_1",
		EnqueuedAt: enqueuedAt,
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if len(client.adds) != 1 {
		t.Fatalf("xadd calls = %d, want 1", len(client.adds))
	}
	add := client.adds[0]
	if add.stream != "niceagent:runs:test" {
		t.Fatalf("stream = %q, want niceagent:runs:test", add.stream)
	}
	if add.maxLen != 1000 {
		t.Fatalf("max len = %d, want 1000", add.maxLen)
	}
	if add.values["run_id"] != "run_1" || add.values["attempt_id"] != "attempt_1" {
		t.Fatalf("identity fields = %#v", add.values)
	}
	for _, removed := range []string{"chat_id", "user_id", "workspace_id", "user_message", "skill_ids", "model_policy"} {
		if _, ok := add.values[removed]; ok {
			t.Fatalf("queue payload contains execution context field %q: %#v", removed, add.values)
		}
	}
	if add.values["enqueued_at"] != enqueuedAt.Format(time.RFC3339Nano) {
		t.Fatalf("enqueued_at = %#v", add.values["enqueued_at"])
	}
}

func TestRedisStreamsRunQueueDequeueCreatesGroupDecodesRunAndAcksOnSuccess(t *testing.T) {
	client := &fakeRedisRunQueueClient{
		messages: []redisStreamMessage{{
			ID: "1700000000000-0",
			Values: map[string]any{
				"run_id":      "run_1",
				"attempt_id":  "attempt_1",
				"enqueued_at": "2026-05-31T10:00:00Z",
			},
		}},
	}
	queue := newRedisStreamsRunQueueWithClient(client, "niceagent:runs:test", "workers", "consumer-a")

	run, ack, err := queue.Dequeue(context.Background())
	if err != nil {
		t.Fatalf("dequeue: %v", err)
	}
	if run.RunID != "run_1" || run.AttemptID != "attempt_1" {
		t.Fatalf("run = %#v", run)
	}
	if len(client.groups) != 1 || client.groups[0].group != "workers" {
		t.Fatalf("groups = %#v", client.groups)
	}
	if err := ack(context.Background(), nil); err != nil {
		t.Fatalf("ack: %v", err)
	}
	if !reflect.DeepEqual(client.acked, []string{"1700000000000-0"}) {
		t.Fatalf("acked = %#v", client.acked)
	}
}

func TestRedisStreamsRunQueueKeepsPendingOnProcessingError(t *testing.T) {
	client := &fakeRedisRunQueueClient{
		messages: []redisStreamMessage{{
			ID: "1700000000000-0",
			Values: map[string]any{
				"run_id": "run_1",
			},
		}},
	}
	queue := newRedisStreamsRunQueueWithClient(client, "niceagent:runs:test", "workers", "consumer-a")

	_, ack, err := queue.Dequeue(context.Background())
	if err != nil {
		t.Fatalf("dequeue: %v", err)
	}
	if err := ack(context.Background(), errors.New("runtime failed")); err != nil {
		t.Fatalf("ack with processing error: %v", err)
	}
	if len(client.acked) != 0 {
		t.Fatalf("acked = %#v, want no ack on processing error", client.acked)
	}
}

type fakeRedisRunQueueClient struct {
	adds     []fakeXAdd
	groups   []fakeXGroup
	messages []redisStreamMessage
	acked    []string
}

type fakeXAdd struct {
	stream string
	values map[string]any
	maxLen int64
}

type fakeXGroup struct {
	stream string
	group  string
	start  string
}

func (c *fakeRedisRunQueueClient) XAdd(_ context.Context, stream string, values map[string]any, maxLen int64) (string, error) {
	c.adds = append(c.adds, fakeXAdd{stream: stream, values: values, maxLen: maxLen})
	return "1700000000000-0", nil
}

func (c *fakeRedisRunQueueClient) XGroupCreateMkStream(_ context.Context, stream, group, start string) error {
	c.groups = append(c.groups, fakeXGroup{stream: stream, group: group, start: start})
	return nil
}

func (c *fakeRedisRunQueueClient) XReadGroup(_ context.Context, _, _, _ string, _ int64, _ time.Duration) (redisStreamMessage, error) {
	if len(c.messages) == 0 {
		return redisStreamMessage{}, errQueueEmpty
	}
	message := c.messages[0]
	c.messages = c.messages[1:]
	return message, nil
}

func (c *fakeRedisRunQueueClient) XAck(_ context.Context, _ string, _ string, ids ...string) (int64, error) {
	c.acked = append(c.acked, ids...)
	return int64(len(ids)), nil
}

func (c *fakeRedisRunQueueClient) Close() error {
	return nil
}
