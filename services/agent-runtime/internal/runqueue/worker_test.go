package runqueue

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"niceagent/agent-runtime/internal/tools"
	"niceagent/common/platform"
	"niceagent/common/protocol"
)

func TestRedisWorkerFetchesExecutionContextExecutesAndAcks(t *testing.T) {
	engine := &fakeEngine{}
	var sawClaim bool
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer internal-secret" {
			t.Fatalf("authorization header = %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/internal/runs/run_1/execution-context":
			if r.URL.Query().Get("attempt_id") != "attempt_1" {
				t.Fatalf("attempt query = %q", r.URL.RawQuery)
			}
			writeJSON(t, w, protocol.RunExecutionRequest{
				Request: protocol.RunRequest{
					RunID:       "run_1",
					ChatID:      "chat_1",
					UserID:      "demo-user",
					WorkspaceID: "ws_1",
					SkillIDs:    []string{"cli.exec"},
					Skills: []protocol.RuntimeSkill{{
						Skill: protocol.Skill{ID: "cli.exec", Kind: protocol.SkillKindBuiltin, Scope: protocol.SkillScopeSystem, Enabled: true},
					}},
					ModelPolicy: "mock-default",
				},
				UserMessage:     "/cli echo hello",
				ControlPlaneURL: "http://" + r.Host,
			})
		case "/internal/runs/run_1/claim":
			var request protocol.RunClaimRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode claim: %v", err)
			}
			if request.AttemptID != "attempt_1" || request.ClaimedBy != "runtime-a" {
				t.Fatalf("claim request = %#v", request)
			}
			sawClaim = true
			writeJSON(t, w, protocol.RunClaimResponse{Run: protocol.Run{ID: "run_1", Status: protocol.RunRunning}})
		default:
			t.Fatalf("path = %q", r.URL.Path)
		}
	}))
	defer controlPlane.Close()

	client := &fakeRedisQueueClient{
		pendingCount: 3,
		streamLengths: map[string]int64{
			"niceagent:runs:test:dlq": 2,
		},
		messages: []redisStreamMessage{{
			ID: "1700000000000-0",
			Values: map[string]any{
				"run_id":      "run_1",
				"attempt_id":  "attempt_1",
				"enqueued_at": "2026-05-31T10:00:00Z",
			},
		}},
	}
	metrics := platform.NewMetrics("agent_runtime_test")
	worker := newRedisWorkerWithClient(Config{
		RedisAddr:       "redis:6379",
		Stream:          "niceagent:runs:test",
		Group:           "workers",
		Consumer:        "runtime-a",
		ControlPlaneURL: controlPlane.URL,
		InternalToken:   "internal-secret",
		Metrics:         metrics,
	}, engine, client, controlPlane.Client(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err := worker.ProcessNext(context.Background()); err != nil {
		t.Fatalf("process next: %v", err)
	}
	if len(client.groups) != 1 || client.groups[0].group != "workers" {
		t.Fatalf("groups = %#v", client.groups)
	}
	if !reflect.DeepEqual(client.acked, []string{"1700000000000-0"}) {
		t.Fatalf("acked = %#v", client.acked)
	}
	if !sawClaim {
		t.Fatal("worker did not claim run")
	}
	if engine.request.RunID != "run_1" || engine.request.AttemptID != "attempt_1" {
		t.Fatalf("engine request = %#v", engine.request)
	}
	if engine.userMessage != "/cli echo hello" {
		t.Fatalf("user message = %q", engine.userMessage)
	}
	renderedMetrics := metrics.Render()
	for _, want := range []string{"niceagent_redis_queue_messages_total", "niceagent_redis_queue_acked_total", "niceagent_redis_queue_pending_entries", "niceagent_redis_queue_dlq_length"} {
		if !strings.Contains(renderedMetrics, want) {
			t.Fatalf("metrics missing %s:\n%s", want, renderedMetrics)
		}
	}
	if !strings.Contains(renderedMetrics, `niceagent_redis_queue_pending_entries{service="agent_runtime_test",consumer="runtime-a",group="workers",stream="niceagent:runs:test"} 3.000000`) {
		t.Fatalf("pending gauge missing expected value:\n%s", renderedMetrics)
	}
	if !strings.Contains(renderedMetrics, `niceagent_redis_queue_dlq_length{service="agent_runtime_test",consumer="runtime-a",dlq_stream="niceagent:runs:test:dlq",group="workers",stream="niceagent:runs:test"} 2.000000`) {
		t.Fatalf("dlq gauge missing expected value:\n%s", renderedMetrics)
	}
}

func TestRedisWorkerKeepsPendingWhenExecutionContextFetchFails(t *testing.T) {
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "temporary outage", http.StatusServiceUnavailable)
	}))
	defer controlPlane.Close()
	client := &fakeRedisQueueClient{
		messages: []redisStreamMessage{{
			ID:     "1700000000000-0",
			Values: map[string]any{"run_id": "run_1"},
		}},
	}
	worker := newRedisWorkerWithClient(Config{
		RedisAddr:       "redis:6379",
		Stream:          "niceagent:runs:test",
		Group:           "workers",
		Consumer:        "runtime-a",
		ControlPlaneURL: controlPlane.URL,
	}, &fakeEngine{}, client, controlPlane.Client(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	err := worker.ProcessNext(context.Background())
	if err == nil {
		t.Fatalf("process next error = nil, want fetch error")
	}
	if len(client.acked) != 0 {
		t.Fatalf("acked = %#v, want no ack on fetch failure", client.acked)
	}
}

func TestRedisWorkerAcksMalformedQueueMessage(t *testing.T) {
	client := &fakeRedisQueueClient{
		messages: []redisStreamMessage{{
			ID:     "1700000000000-0",
			Values: map[string]any{},
		}},
	}
	worker := newRedisWorkerWithClient(Config{
		RedisAddr:       "redis:6379",
		Stream:          "niceagent:runs:test",
		Group:           "workers",
		Consumer:        "runtime-a",
		ControlPlaneURL: "http://control-plane",
	}, &fakeEngine{}, client, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	err := worker.ProcessNext(context.Background())
	if err == nil {
		t.Fatalf("process next error = nil, want decode error")
	}
	if !reflect.DeepEqual(client.acked, []string{"1700000000000-0"}) {
		t.Fatalf("acked = %#v", client.acked)
	}
	if len(client.adds) != 1 {
		t.Fatalf("dlq adds = %d, want 1", len(client.adds))
	}
	if client.adds[0].values["reason"] != "decode_failed" {
		t.Fatalf("dlq reason = %#v", client.adds[0].values)
	}
}

func TestRedisWorkerAutoClaimsPendingRunWithFreshAttempt(t *testing.T) {
	engine := &fakeEngine{}
	var sawExecutionAttempt string
	var sawClaimAttempt string
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/internal/runs/run_1/execution-context":
			sawExecutionAttempt = r.URL.Query().Get("attempt_id")
			if sawExecutionAttempt == "" || sawExecutionAttempt == "attempt_old" {
				t.Fatalf("execution attempt = %q, want fresh attempt", sawExecutionAttempt)
			}
			writeJSON(t, w, protocol.RunExecutionRequest{
				Request: protocol.RunRequest{
					RunID:       "run_1",
					ChatID:      "chat_1",
					UserID:      "demo-user",
					WorkspaceID: "ws_1",
					AttemptID:   sawExecutionAttempt,
					SkillIDs:    []string{"cli.exec"},
					Skills: []protocol.RuntimeSkill{{
						Skill: protocol.Skill{ID: "cli.exec", Kind: protocol.SkillKindBuiltin, Scope: protocol.SkillScopeSystem, Enabled: true},
					}},
				},
				UserMessage:     "/cli echo hello",
				ControlPlaneURL: "http://" + r.Host,
			})
		case "/internal/runs/run_1/claim":
			var request protocol.RunClaimRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode claim: %v", err)
			}
			sawClaimAttempt = request.AttemptID
			if request.AttemptID != sawExecutionAttempt {
				t.Fatalf("claim attempt = %q, execution attempt = %q", request.AttemptID, sawExecutionAttempt)
			}
			writeJSON(t, w, protocol.RunClaimResponse{Run: protocol.Run{ID: "run_1", Status: protocol.RunRunning}})
		default:
			t.Fatalf("path = %q", r.URL.Path)
		}
	}))
	defer controlPlane.Close()

	client := &fakeRedisQueueClient{
		autoClaimMessages: []redisStreamMessage{{
			ID: "1700000000000-0",
			Values: map[string]any{
				"run_id":      "run_1",
				"attempt_id":  "attempt_old",
				"enqueued_at": "2026-05-31T10:00:00Z",
			},
		}},
		pending: map[string]int64{"1700000000000-0": 2},
	}
	worker := newRedisWorkerWithClient(Config{
		RedisAddr:       "redis:6379",
		Stream:          "niceagent:runs:test",
		Group:           "workers",
		Consumer:        "runtime-b",
		RuntimeID:       "runtime-b",
		ControlPlaneURL: controlPlane.URL,
		ReclaimMinIdle:  time.Millisecond,
		MaxDeliveries:   5,
	}, engine, client, controlPlane.Client(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err := worker.ProcessNext(context.Background()); err != nil {
		t.Fatalf("process next: %v", err)
	}
	if sawClaimAttempt == "" {
		t.Fatal("worker did not claim reclaimed run")
	}
	if engine.request.AttemptID != sawClaimAttempt {
		t.Fatalf("engine attempt = %q, claim attempt = %q", engine.request.AttemptID, sawClaimAttempt)
	}
	if !reflect.DeepEqual(client.acked, []string{"1700000000000-0"}) {
		t.Fatalf("acked = %#v", client.acked)
	}
}

func TestRedisWorkerMovesOverDeliveredPendingMessageToDLQ(t *testing.T) {
	engine := &fakeEngine{}
	client := &fakeRedisQueueClient{
		autoClaimMessages: []redisStreamMessage{{
			ID: "1700000000000-0",
			Values: map[string]any{
				"run_id":     "run_1",
				"attempt_id": "attempt_old",
			},
		}},
		pending: map[string]int64{"1700000000000-0": 5},
	}
	metrics := platform.NewMetrics("agent_runtime_test")
	worker := newRedisWorkerWithClient(Config{
		RedisAddr:        "redis:6379",
		Stream:           "niceagent:runs:test",
		Group:            "workers",
		Consumer:         "runtime-b",
		ControlPlaneURL:  "http://control-plane",
		ReclaimMinIdle:   time.Millisecond,
		MaxDeliveries:    5,
		DeadLetterStream: "niceagent:runs:test:dlq",
		DeadLetterMaxLen: 100,
		Metrics:          metrics,
	}, engine, client, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err := worker.ProcessNext(context.Background()); err != nil {
		t.Fatalf("process next: %v", err)
	}
	if engine.request.RunID != "" {
		t.Fatalf("engine executed run = %#v, want skipped", engine.request)
	}
	if !reflect.DeepEqual(client.acked, []string{"1700000000000-0"}) {
		t.Fatalf("acked = %#v", client.acked)
	}
	if len(client.adds) != 1 {
		t.Fatalf("dlq adds = %d, want 1", len(client.adds))
	}
	add := client.adds[0]
	if add.stream != "niceagent:runs:test:dlq" {
		t.Fatalf("dlq stream = %q", add.stream)
	}
	if add.maxLen != 100 {
		t.Fatalf("dlq max len = %d, want 100", add.maxLen)
	}
	if add.values["reason"] != "max_deliveries_exceeded" || add.values["run_id"] != "run_1" {
		t.Fatalf("dlq values = %#v", add.values)
	}
	renderedMetrics := metrics.Render()
	for _, want := range []string{"niceagent_redis_queue_reclaimed_total", "niceagent_redis_queue_dlq_messages_total", "niceagent_redis_queue_acked_total"} {
		if !strings.Contains(renderedMetrics, want) {
			t.Fatalf("metrics missing %s:\n%s", want, renderedMetrics)
		}
	}
}

type fakeEngine struct {
	request     protocol.RunRequest
	userMessage string
}

func (e *fakeEngine) Execute(_ context.Context, req protocol.RunRequest, userMessage string, _ tools.EventSink) protocol.RunResult {
	e.request = req
	e.userMessage = userMessage
	return protocol.RunResult{RunID: req.RunID, Status: protocol.RunSucceeded}
}

type fakeRedisQueueClient struct {
	groups            []fakeXGroup
	messages          []redisStreamMessage
	autoClaimMessages []redisStreamMessage
	pending           map[string]int64
	pendingCount      int64
	streamLengths     map[string]int64
	adds              []fakeXAdd
	acked             []string
}

type fakeXGroup struct {
	stream string
	group  string
	start  string
}

type fakeXAdd struct {
	stream string
	values map[string]any
	maxLen int64
}

func (c *fakeRedisQueueClient) XGroupCreateMkStream(_ context.Context, stream, group, start string) error {
	c.groups = append(c.groups, fakeXGroup{stream: stream, group: group, start: start})
	return nil
}

func (c *fakeRedisQueueClient) XReadGroup(_ context.Context, _, _, _ string, _ int64, _ time.Duration) (redisStreamMessage, error) {
	if len(c.messages) == 0 {
		return redisStreamMessage{}, errQueueEmpty
	}
	message := c.messages[0]
	c.messages = c.messages[1:]
	return message, nil
}

func (c *fakeRedisQueueClient) XAutoClaim(_ context.Context, _, _, _ string, _ time.Duration, _ string, _ int64) ([]redisStreamMessage, string, error) {
	messages := c.autoClaimMessages
	c.autoClaimMessages = nil
	return messages, "0-0", nil
}

func (c *fakeRedisQueueClient) XPendingExt(_ context.Context, _, _, start, _ string, _ int64) ([]redisPendingEntry, error) {
	if c.pending == nil {
		return nil, nil
	}
	count, ok := c.pending[start]
	if !ok {
		return nil, nil
	}
	return []redisPendingEntry{{ID: start, RetryCount: count}}, nil
}

func (c *fakeRedisQueueClient) XPendingCount(_ context.Context, _, _ string) (int64, error) {
	return c.pendingCount, nil
}

func (c *fakeRedisQueueClient) XLen(_ context.Context, stream string) (int64, error) {
	if c.streamLengths == nil {
		return 0, nil
	}
	return c.streamLengths[stream], nil
}

func (c *fakeRedisQueueClient) XAdd(_ context.Context, stream string, values map[string]any, maxLen int64) (string, error) {
	c.adds = append(c.adds, fakeXAdd{stream: stream, values: values, maxLen: maxLen})
	return "1700000000001-0", nil
}

func (c *fakeRedisQueueClient) XAck(_ context.Context, _ string, _ string, ids ...string) (int64, error) {
	c.acked = append(c.acked, ids...)
	return int64(len(ids)), nil
}

func (c *fakeRedisQueueClient) Close() error {
	return nil
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}
