package runqueue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	redis "github.com/redis/go-redis/v9"

	"niceagent/agent-runtime/internal/engine"
	"niceagent/agent-runtime/internal/sink"
	"niceagent/common/platform"
	"niceagent/common/protocol"
)

const (
	defaultStream            = "niceagent:runs"
	defaultGroup             = "agent-runtimes"
	defaultBlockTimeout      = 5 * time.Second
	defaultLeaseSeconds      = 600
	defaultHeartbeatInterval = 60 * time.Second
	defaultReclaimCount      = 1
	defaultMaxDeliveries     = 5
)

var errQueueEmpty = errors.New("run queue is empty")

type Config struct {
	RedisAddr         string
	Stream            string
	Group             string
	Consumer          string
	ControlPlaneURL   string
	InternalToken     string
	RuntimeID         string
	BlockTimeout      time.Duration
	ReclaimMinIdle    time.Duration
	ReclaimCount      int64
	MaxDeliveries     int64
	DeadLetterStream  string
	DeadLetterMaxLen  int64
	LeaseSeconds      int
	HeartbeatInterval time.Duration
	Metrics           *platform.Metrics
}

type RedisWorker struct {
	cfg        Config
	engine     engine.AgentEngine
	client     redisQueueClient
	httpClient *http.Client
	log        *slog.Logger
	metrics    *platform.Metrics
}

func NewRedisWorker(cfg Config, agent engine.AgentEngine, log *slog.Logger) *RedisWorker {
	cfg = normalizeConfig(cfg)
	return &RedisWorker{
		cfg:        cfg,
		engine:     agent,
		client:     newGoRedisQueueClient(cfg.RedisAddr),
		httpClient: &http.Client{Timeout: 30 * time.Second},
		log:        log,
		metrics:    cfg.Metrics,
	}
}

func newRedisWorkerWithClient(cfg Config, agent engine.AgentEngine, client redisQueueClient, httpClient *http.Client, log *slog.Logger) *RedisWorker {
	cfg = normalizeConfig(cfg)
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &RedisWorker{
		cfg:        cfg,
		engine:     agent,
		client:     client,
		httpClient: httpClient,
		log:        log,
		metrics:    cfg.Metrics,
	}
}

func (w *RedisWorker) Run(ctx context.Context) {
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		if err := w.ProcessNext(ctx); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return
			}
			if w.log != nil {
				w.log.Warn("redis run queue worker iteration failed", "error", err)
			}
			time.Sleep(500 * time.Millisecond)
		}
	}
}

func (w *RedisWorker) ProcessNext(ctx context.Context) (err error) {
	ctx, endSpan := platform.StartSpan(ctx, "niceagent/agent_runtime", "run_queue.process_next", platform.Labels{
		"stream":   w.cfg.Stream,
		"group":    w.cfg.Group,
		"consumer": w.cfg.Consumer,
	})
	defer func() { endSpan(err, nil) }()
	if err := w.ensureGroup(ctx); err != nil {
		w.incQueueError("ensure_group")
		return err
	}
	w.sampleQueueDepths(ctx)
	message, reclaimed, err := w.readNextMessage(ctx)
	if errors.Is(err, errQueueEmpty) {
		return nil
	}
	if err != nil {
		w.incQueueError("read")
		return err
	}
	w.incQueueMessage(reclaimed)
	queued, err := decodeQueuedRun(message.Values)
	if err != nil {
		_ = w.deadLetter(ctx, message, "decode_failed", 0, err)
		_, _ = w.client.XAck(ctx, w.cfg.Stream, w.cfg.Group, message.ID)
		w.incQueueError("decode")
		return err
	}
	queued.MessageID = message.ID
	queued.Reclaimed = reclaimed
	if reclaimed {
		queued.PreviousAttemptID = queued.AttemptID
		queued.AttemptID = platform.NewID("attempt")
	}
	if err := w.executeQueuedRun(ctx, queued); err != nil {
		w.incQueueError("execute")
		return err
	}
	_, err = w.client.XAck(ctx, w.cfg.Stream, w.cfg.Group, message.ID)
	if err != nil {
		w.incQueueError("ack")
	} else {
		w.incQueueAck()
	}
	return err
}

func (w *RedisWorker) Close() error {
	if w.client == nil {
		return nil
	}
	return w.client.Close()
}

func (w *RedisWorker) ensureGroup(ctx context.Context) error {
	err := w.client.XGroupCreateMkStream(ctx, w.cfg.Stream, w.cfg.Group, "0")
	if err == nil || strings.Contains(err.Error(), "BUSYGROUP") {
		return nil
	}
	return err
}

func (w *RedisWorker) readNextMessage(ctx context.Context) (redisStreamMessage, bool, error) {
	message, err := w.client.XReadGroup(ctx, w.cfg.Stream, w.cfg.Group, w.cfg.Consumer, 1, w.cfg.BlockTimeout)
	if !errors.Is(err, errQueueEmpty) {
		return message, false, err
	}
	if w.cfg.ReclaimMinIdle <= 0 {
		return redisStreamMessage{}, false, errQueueEmpty
	}
	return w.autoClaimPending(ctx)
}

func (w *RedisWorker) autoClaimPending(ctx context.Context) (redisStreamMessage, bool, error) {
	messages, _, err := w.client.XAutoClaim(ctx, w.cfg.Stream, w.cfg.Group, w.cfg.Consumer, w.cfg.ReclaimMinIdle, "0-0", w.cfg.ReclaimCount)
	if err != nil {
		w.incQueueError("autoclaim")
		return redisStreamMessage{}, false, err
	}
	for _, message := range messages {
		w.incQueueReclaim()
		deliveryCount := w.pendingRetryCount(ctx, message.ID)
		if w.cfg.MaxDeliveries > 0 && deliveryCount >= w.cfg.MaxDeliveries {
			if err := w.deadLetter(ctx, message, "max_deliveries_exceeded", deliveryCount, nil); err != nil {
				w.incQueueError("dead_letter")
				return redisStreamMessage{}, true, err
			}
			if _, err := w.client.XAck(ctx, w.cfg.Stream, w.cfg.Group, message.ID); err != nil {
				w.incQueueError("ack_dead_letter")
				return redisStreamMessage{}, true, err
			}
			w.incQueueAck()
			continue
		}
		message.DeliveryCount = deliveryCount
		return message, true, nil
	}
	return redisStreamMessage{}, false, errQueueEmpty
}

func (w *RedisWorker) pendingRetryCount(ctx context.Context, messageID string) int64 {
	entries, err := w.client.XPendingExt(ctx, w.cfg.Stream, w.cfg.Group, messageID, messageID, 1)
	if err != nil || len(entries) == 0 {
		if err != nil {
			w.incQueueError("pending")
		}
		return 0
	}
	return entries[0].RetryCount
}

func (w *RedisWorker) sampleQueueDepths(ctx context.Context) {
	if w.metrics == nil {
		return
	}
	pendingCount, err := w.client.XPendingCount(ctx, w.cfg.Stream, w.cfg.Group)
	if err != nil {
		w.incQueueError("pending_sample")
	} else {
		w.metrics.SetGauge("niceagent_redis_queue_pending_entries", w.baseMetricLabels(nil), float64(pendingCount))
	}
	dlqStream := strings.TrimSpace(w.cfg.DeadLetterStream)
	if dlqStream == "" {
		return
	}
	dlqLength, err := w.client.XLen(ctx, dlqStream)
	if err != nil {
		w.incQueueError("dlq_len")
		return
	}
	w.metrics.SetGauge("niceagent_redis_queue_dlq_length", w.baseMetricLabels(platform.Labels{
		"dlq_stream": dlqStream,
	}), float64(dlqLength))
}

func (w *RedisWorker) deadLetter(ctx context.Context, message redisStreamMessage, reason string, deliveryCount int64, cause error) error {
	stream := strings.TrimSpace(w.cfg.DeadLetterStream)
	if stream == "" {
		return nil
	}
	values := map[string]any{
		"schema_version":   "1",
		"original_stream":  w.cfg.Stream,
		"original_id":      message.ID,
		"reason":           reason,
		"delivery_count":   deliveryCount,
		"dead_lettered_at": time.Now().UTC().Format(time.RFC3339Nano),
	}
	if runID := redisValueString(message.Values["run_id"]); runID != "" {
		values["run_id"] = runID
	}
	if attemptID := redisValueString(message.Values["attempt_id"]); attemptID != "" {
		values["attempt_id"] = attemptID
	}
	if cause != nil {
		values["error"] = cause.Error()
	}
	payload, err := json.Marshal(message.Values)
	if err != nil {
		values["payload_error"] = err.Error()
	} else {
		values["payload"] = string(payload)
	}
	_, err = w.client.XAdd(ctx, stream, values, w.cfg.DeadLetterMaxLen)
	if err == nil {
		w.incDeadLetter(reason, stream)
	}
	return err
}

func (w *RedisWorker) executeQueuedRun(ctx context.Context, queued queuedRun) error {
	if w.engine == nil {
		return errors.New("agent engine is nil")
	}
	execution, err := w.fetchExecutionContext(ctx, queued)
	if err != nil {
		return err
	}
	controlPlaneURL := strings.TrimRight(execution.ControlPlaneURL, "/")
	if controlPlaneURL == "" {
		controlPlaneURL = w.cfg.ControlPlaneURL
	}
	if controlPlaneURL == "" {
		return errors.New("control plane url is required")
	}
	controlSink := sink.NewControlPlaneSink(controlPlaneURL, w.cfg.InternalToken).
		WithAttempt(execution.Request.AttemptID).
		WithTraceContext(ctx)
	if execution.Request.AttemptID != "" {
		claimed, err := controlSink.Claim(execution.Request.RunID, execution.Request.AttemptID, w.claimedBy(), w.cfg.LeaseSeconds)
		if err != nil {
			return err
		}
		if isTerminalRunStatus(claimed.Status) {
			return nil
		}
	}
	executeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopHeartbeat := w.startLeaseHeartbeat(executeCtx, cancel, controlSink, execution.Request.RunID, execution.Request.AttemptID)
	defer stopHeartbeat()
	w.engine.Execute(executeCtx, execution.Request, execution.UserMessage, controlSink)
	return nil
}

func (w *RedisWorker) startLeaseHeartbeat(ctx context.Context, cancel context.CancelFunc, controlSink *sink.ControlPlaneSink, runID, attemptID string) func() {
	if attemptID == "" || w.cfg.HeartbeatInterval <= 0 {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(w.cfg.HeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				claimed, err := controlSink.Claim(runID, attemptID, w.claimedBy(), w.cfg.LeaseSeconds)
				if err != nil {
					if w.log != nil {
						w.log.Warn("run lease heartbeat failed", "run_id", runID, "attempt_id", attemptID, "error", err)
					}
					cancel()
					return
				}
				if isTerminalRunStatus(claimed.Status) {
					cancel()
					return
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

func (w *RedisWorker) fetchExecutionContext(ctx context.Context, queued queuedRun) (protocol.RunExecutionRequest, error) {
	ctx, endSpan := platform.StartSpan(ctx, "niceagent/agent_runtime", "control_plane.execution_context.get", platform.Labels{
		"run_id":     queued.RunID,
		"attempt_id": queued.AttemptID,
	})
	var spanErr error
	defer func() { endSpan(spanErr, nil) }()
	baseURL := strings.TrimRight(w.cfg.ControlPlaneURL, "/")
	if baseURL == "" {
		spanErr = errors.New("CONTROL_PLANE_URL is required for redis worker")
		return protocol.RunExecutionRequest{}, spanErr
	}
	endpoint, err := url.Parse(baseURL + "/internal/runs/" + url.PathEscape(queued.RunID) + "/execution-context")
	if err != nil {
		spanErr = err
		return protocol.RunExecutionRequest{}, err
	}
	query := endpoint.Query()
	if queued.AttemptID != "" {
		query.Set("attempt_id", queued.AttemptID)
	}
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		spanErr = err
		return protocol.RunExecutionRequest{}, err
	}
	platform.InjectTraceHeaders(ctx, request.Header)
	if w.cfg.InternalToken != "" {
		request.Header.Set("Authorization", "Bearer "+w.cfg.InternalToken)
	}
	response, err := w.httpClient.Do(request)
	if err != nil {
		spanErr = err
		return protocol.RunExecutionRequest{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		spanErr = fmt.Errorf("control plane execution context returned %s: %s", response.Status, strings.TrimSpace(string(body)))
		endSpan(spanErr, platform.Labels{"http_status": fmt.Sprint(response.StatusCode)})
		endSpan = func(error, platform.Labels) {}
		return protocol.RunExecutionRequest{}, spanErr
	}
	var execution protocol.RunExecutionRequest
	if err := json.NewDecoder(response.Body).Decode(&execution); err != nil {
		spanErr = err
		return protocol.RunExecutionRequest{}, err
	}
	if execution.Request.RunID == "" {
		execution.Request.RunID = queued.RunID
	}
	if execution.Request.AttemptID == "" {
		execution.Request.AttemptID = queued.AttemptID
	}
	if execution.ControlPlaneURL == "" {
		execution.ControlPlaneURL = baseURL
	}
	endSpan(nil, platform.Labels{"http_status": fmt.Sprint(response.StatusCode)})
	endSpan = func(error, platform.Labels) {}
	return execution, nil
}

func normalizeConfig(cfg Config) Config {
	cfg.RedisAddr = strings.TrimSpace(cfg.RedisAddr)
	cfg.Stream = strings.TrimSpace(cfg.Stream)
	cfg.Group = strings.TrimSpace(cfg.Group)
	cfg.Consumer = strings.TrimSpace(cfg.Consumer)
	cfg.RuntimeID = strings.TrimSpace(cfg.RuntimeID)
	cfg.ControlPlaneURL = strings.TrimRight(strings.TrimSpace(cfg.ControlPlaneURL), "/")
	if cfg.Stream == "" {
		cfg.Stream = defaultStream
	}
	if cfg.Group == "" {
		cfg.Group = defaultGroup
	}
	if cfg.Consumer == "" {
		cfg.Consumer = "agent-runtime"
	}
	if cfg.RuntimeID == "" {
		cfg.RuntimeID = cfg.Consumer
	}
	if cfg.BlockTimeout <= 0 {
		cfg.BlockTimeout = defaultBlockTimeout
	}
	if cfg.ReclaimCount <= 0 {
		cfg.ReclaimCount = defaultReclaimCount
	}
	if cfg.MaxDeliveries <= 0 {
		cfg.MaxDeliveries = defaultMaxDeliveries
	}
	if cfg.DeadLetterStream == "" {
		cfg.DeadLetterStream = cfg.Stream + ":dlq"
	}
	if cfg.LeaseSeconds <= 0 {
		cfg.LeaseSeconds = defaultLeaseSeconds
	}
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = defaultHeartbeatInterval
	}
	return cfg
}

func (w *RedisWorker) claimedBy() string {
	if w.cfg.RuntimeID != "" {
		return w.cfg.RuntimeID
	}
	return w.cfg.Consumer
}

func (w *RedisWorker) baseMetricLabels(extra platform.Labels) platform.Labels {
	labels := platform.Labels{
		"stream":   w.cfg.Stream,
		"group":    w.cfg.Group,
		"consumer": w.cfg.Consumer,
	}
	for key, value := range extra {
		labels[key] = value
	}
	return labels
}

func (w *RedisWorker) incQueueMessage(reclaimed bool) {
	w.metrics.IncCounter("niceagent_redis_queue_messages_total", w.baseMetricLabels(platform.Labels{
		"reclaimed": fmt.Sprint(reclaimed),
	}))
}

func (w *RedisWorker) incQueueReclaim() {
	w.metrics.IncCounter("niceagent_redis_queue_reclaimed_total", w.baseMetricLabels(nil))
}

func (w *RedisWorker) incQueueAck() {
	w.metrics.IncCounter("niceagent_redis_queue_acked_total", w.baseMetricLabels(nil))
}

func (w *RedisWorker) incQueueError(stage string) {
	w.metrics.IncCounter("niceagent_redis_queue_errors_total", w.baseMetricLabels(platform.Labels{
		"stage": stage,
	}))
}

func (w *RedisWorker) incDeadLetter(reason, dlqStream string) {
	w.metrics.IncCounter("niceagent_redis_queue_dlq_messages_total", w.baseMetricLabels(platform.Labels{
		"reason":     reason,
		"dlq_stream": dlqStream,
	}))
}

func isTerminalRunStatus(status protocol.RunStatus) bool {
	return status == protocol.RunSucceeded || status == protocol.RunFailed || status == protocol.RunCanceled
}

type queuedRun struct {
	RunID             string
	AttemptID         string
	EnqueuedAt        time.Time
	MessageID         string
	Reclaimed         bool
	PreviousAttemptID string
}

func decodeQueuedRun(values map[string]any) (queuedRun, error) {
	run := queuedRun{
		RunID:     redisValueString(values["run_id"]),
		AttemptID: redisValueString(values["attempt_id"]),
	}
	if run.RunID == "" {
		return queuedRun{}, errors.New("queued run is missing run_id")
	}
	if rawEnqueuedAt := redisValueString(values["enqueued_at"]); rawEnqueuedAt != "" {
		enqueuedAt, err := time.Parse(time.RFC3339Nano, rawEnqueuedAt)
		if err != nil {
			return queuedRun{}, fmt.Errorf("decode queued run enqueued_at: %w", err)
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

type redisStreamMessage struct {
	ID            string
	Values        map[string]any
	DeliveryCount int64
}

type redisPendingEntry struct {
	ID         string
	RetryCount int64
}

type redisQueueClient interface {
	XGroupCreateMkStream(ctx context.Context, stream, group, start string) error
	XReadGroup(ctx context.Context, stream, group, consumer string, count int64, block time.Duration) (redisStreamMessage, error)
	XAutoClaim(ctx context.Context, stream, group, consumer string, minIdle time.Duration, start string, count int64) ([]redisStreamMessage, string, error)
	XPendingCount(ctx context.Context, stream, group string) (int64, error)
	XPendingExt(ctx context.Context, stream, group, start, end string, count int64) ([]redisPendingEntry, error)
	XLen(ctx context.Context, stream string) (int64, error)
	XAdd(ctx context.Context, stream string, values map[string]any, maxLen int64) (string, error)
	XAck(ctx context.Context, stream, group string, ids ...string) (int64, error)
	Close() error
}

type goRedisQueueClient struct {
	client *redis.Client
}

func newGoRedisQueueClient(addr string) *goRedisQueueClient {
	return &goRedisQueueClient{client: redis.NewClient(&redis.Options{Addr: addr})}
}

func (c *goRedisQueueClient) XGroupCreateMkStream(ctx context.Context, stream, group, start string) error {
	return c.client.XGroupCreateMkStream(ctx, stream, group, start).Err()
}

func (c *goRedisQueueClient) XReadGroup(ctx context.Context, stream, group, consumer string, count int64, block time.Duration) (redisStreamMessage, error) {
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

func (c *goRedisQueueClient) XAck(ctx context.Context, stream, group string, ids ...string) (int64, error) {
	return c.client.XAck(ctx, stream, group, ids...).Result()
}

func (c *goRedisQueueClient) XAutoClaim(ctx context.Context, stream, group, consumer string, minIdle time.Duration, start string, count int64) ([]redisStreamMessage, string, error) {
	messages, nextStart, err := c.client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   stream,
		Group:    group,
		Consumer: consumer,
		MinIdle:  minIdle,
		Start:    start,
		Count:    count,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nextStart, nil
	}
	if err != nil {
		return nil, nextStart, err
	}
	result := make([]redisStreamMessage, 0, len(messages))
	for _, message := range messages {
		result = append(result, redisStreamMessage{ID: message.ID, Values: message.Values})
	}
	return result, nextStart, nil
}

func (c *goRedisQueueClient) XPendingExt(ctx context.Context, stream, group, start, end string, count int64) ([]redisPendingEntry, error) {
	entries, err := c.client.XPendingExt(ctx, &redis.XPendingExtArgs{
		Stream: stream,
		Group:  group,
		Start:  start,
		End:    end,
		Count:  count,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	result := make([]redisPendingEntry, 0, len(entries))
	for _, entry := range entries {
		result = append(result, redisPendingEntry{ID: entry.ID, RetryCount: entry.RetryCount})
	}
	return result, nil
}

func (c *goRedisQueueClient) XPendingCount(ctx context.Context, stream, group string) (int64, error) {
	pending, err := c.client.XPending(ctx, stream, group).Result()
	if errors.Is(err, redis.Nil) || pending == nil {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return pending.Count, nil
}

func (c *goRedisQueueClient) XLen(ctx context.Context, stream string) (int64, error) {
	length, err := c.client.XLen(ctx, stream).Result()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return length, err
}

func (c *goRedisQueueClient) XAdd(ctx context.Context, stream string, values map[string]any, maxLen int64) (string, error) {
	args := &redis.XAddArgs{
		Stream: stream,
		Values: values,
	}
	if maxLen > 0 {
		args.MaxLen = maxLen
		args.Approx = true
	}
	return c.client.XAdd(ctx, args).Result()
}

func (c *goRedisQueueClient) Close() error {
	return c.client.Close()
}
