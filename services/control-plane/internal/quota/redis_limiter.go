package quota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	redis "github.com/redis/go-redis/v9"

	"niceagent/common/protocol"
	"niceagent/control-plane/internal/app"
)

const (
	defaultPrefix    = "niceagent:quota"
	activeTTL        = 24 * time.Hour
	runMarkerTTL     = 48 * time.Hour
	hourWindowBuffer = 2 * time.Hour
	tokenWindowTTL   = 48 * time.Hour
)

type Denial struct {
	Message  string
	Metadata map[string]any
}

type Reservation interface {
	Commit(ctx context.Context, runID string) error
	Rollback(ctx context.Context) error
}

type Limiter interface {
	ReserveRun(ctx context.Context, actor app.ActorContext, policy protocol.ProjectQuotaPolicy) (Reservation, Denial, error)
	ReleaseRun(ctx context.Context, run protocol.Run, projectID string, usage protocol.RunUsage) error
}

type ReservationHint struct {
	ModelTokens int
}

type HintedLimiter interface {
	Limiter
	ReserveRunWithHint(ctx context.Context, actor app.ActorContext, policy protocol.ProjectQuotaPolicy, hint ReservationHint) (Reservation, Denial, error)
}

type CounterStore interface {
	Incr(ctx context.Context, key string) (int64, error)
	Decr(ctx context.Context, key string) (int64, error)
	IncrBy(ctx context.Context, key string, amount int64) (int64, error)
	DecrBy(ctx context.Context, key string, amount int64) (int64, error)
	Expire(ctx context.Context, key string, ttl time.Duration) error
	SetNX(ctx context.Context, key string, value string, ttl time.Duration) (bool, error)
	Get(ctx context.Context, key string) (string, bool, error)
	Del(ctx context.Context, key string) (int64, error)
	Close() error
}

type LimiterOptions struct {
	ModelTokenReservationPerRun int
}

type RedisLimiter struct {
	store                       CounterStore
	prefix                      string
	now                         func() time.Time
	modelTokenReservationPerRun int
}

func NewRedisLimiter(addr, prefix string) *RedisLimiter {
	return NewRedisLimiterWithOptions(addr, prefix, LimiterOptions{})
}

func NewRedisLimiterWithOptions(addr, prefix string, opts LimiterOptions) *RedisLimiter {
	return NewLimiterWithStoreAndOptions(NewRedisCounterStore(addr), prefix, opts)
}

func NewLimiterWithStore(store CounterStore, prefix string) *RedisLimiter {
	return NewLimiterWithStoreAndOptions(store, prefix, LimiterOptions{})
}

func NewLimiterWithStoreAndOptions(store CounterStore, prefix string, opts LimiterOptions) *RedisLimiter {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = defaultPrefix
	}
	return &RedisLimiter{
		store:                       store,
		prefix:                      prefix,
		now:                         func() time.Time { return time.Now().UTC() },
		modelTokenReservationPerRun: maxInt(opts.ModelTokenReservationPerRun, 0),
	}
}

func (l *RedisLimiter) ReserveRun(ctx context.Context, actor app.ActorContext, policy protocol.ProjectQuotaPolicy) (Reservation, Denial, error) {
	return l.ReserveRunWithHint(ctx, actor, policy, ReservationHint{})
}

func (l *RedisLimiter) ReserveRunWithHint(ctx context.Context, actor app.ActorContext, policy protocol.ProjectQuotaPolicy, hint ReservationHint) (Reservation, Denial, error) {
	if l == nil || l.store == nil {
		return noopReservation{}, Denial{}, nil
	}
	reservation := &redisReservation{
		limiter: l,
		actor:   actor,
		policy:  policy,
	}
	if policy.MaxConcurrentRuns > 0 {
		key := l.activeKey(actor)
		current, err := l.store.Incr(ctx, key)
		if err != nil {
			return nil, Denial{}, err
		}
		reservation.activeKey = key
		reservation.reservedActive = true
		if err := l.store.Expire(ctx, key, activeTTL); err != nil {
			_ = reservation.Rollback(ctx)
			return nil, Denial{}, err
		}
		if current > int64(policy.MaxConcurrentRuns) {
			_, _ = l.store.Decr(ctx, key)
			return nil, Denial{
				Message: "已达到当前项目并发任务上限，请稍后再试。",
				Metadata: map[string]any{
					"quota":   "concurrent_runs",
					"limit":   policy.MaxConcurrentRuns,
					"current": current - 1,
					"source":  "redis",
				},
			}, nil
		}
	}
	if policy.MaxRunsPerHour > 0 {
		key := l.hourKey(actor, l.now())
		current, err := l.store.Incr(ctx, key)
		if err != nil {
			_ = reservation.Rollback(ctx)
			return nil, Denial{}, err
		}
		reservation.hourKey = key
		reservation.reservedHour = true
		if err := l.store.Expire(ctx, key, hourWindowBuffer); err != nil {
			_ = reservation.Rollback(ctx)
			return nil, Denial{}, err
		}
		if current > int64(policy.MaxRunsPerHour) {
			_ = reservation.Rollback(ctx)
			return nil, Denial{
				Message: "已达到当前项目每小时任务数上限，请稍后再试。",
				Metadata: map[string]any{
					"quota":   "runs_per_hour",
					"limit":   policy.MaxRunsPerHour,
					"current": current - 1,
					"source":  "redis",
				},
			}, nil
		}
	}
	if policy.MaxModelTokensPerDay > 0 && l.modelTokenReservation(hint) > 0 {
		key := l.tokenKey(actor, l.now())
		reserved := int64(l.modelTokenReservation(hint))
		current, err := l.store.IncrBy(ctx, key, reserved)
		if err != nil {
			_ = reservation.Rollback(ctx)
			return nil, Denial{}, err
		}
		reservation.tokenKey = key
		reservation.reservedTokens = reserved
		if err := l.store.Expire(ctx, key, tokenWindowTTL); err != nil {
			_ = reservation.Rollback(ctx)
			return nil, Denial{}, err
		}
		if current > int64(policy.MaxModelTokensPerDay) {
			_ = reservation.Rollback(ctx)
			return nil, Denial{
				Message: "已达到当前项目每日模型 token 上限，请明天再试。",
				Metadata: map[string]any{
					"quota":    "model_tokens_per_day",
					"limit":    policy.MaxModelTokensPerDay,
					"current":  current - reserved,
					"reserved": reserved,
					"source":   "redis",
				},
			}, nil
		}
	}
	return reservation, Denial{}, nil
}

func (l *RedisLimiter) modelTokenReservation(hint ReservationHint) int {
	if hint.ModelTokens > 0 {
		return hint.ModelTokens
	}
	return l.modelTokenReservationPerRun
}

func (l *RedisLimiter) ReleaseRun(ctx context.Context, run protocol.Run, projectID string, usage protocol.RunUsage) error {
	if l == nil || l.store == nil || strings.TrimSpace(run.ID) == "" {
		return nil
	}
	markerKey := l.runMarkerKey(run.ID)
	rawMarker, ok, err := l.store.Get(ctx, markerKey)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	marker := parseRunMarker(rawMarker)
	removed, err := l.store.Del(ctx, markerKey)
	if err != nil {
		return err
	}
	if removed == 0 {
		return nil
	}
	var firstErr error
	if marker.ActiveReserved {
		if _, err := l.store.Decr(ctx, l.activeKey(app.ActorContext{UserID: run.UserID, ProjectID: projectID})); err != nil {
			firstErr = err
		}
	}
	if marker.TokenReserved > 0 && marker.TokenKey != "" {
		actual := int64(protocol.NormalizeRunUsage(usage).TotalTokens)
		delta := actual - marker.TokenReserved
		if delta > 0 {
			if _, err := l.store.IncrBy(ctx, marker.TokenKey, delta); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		if delta < 0 {
			if _, err := l.store.DecrBy(ctx, marker.TokenKey, -delta); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (l *RedisLimiter) activeKey(actor app.ActorContext) string {
	return l.prefix + ":active:" + safeKey(actor.ProjectID) + ":" + safeKey(actor.UserID)
}

func (l *RedisLimiter) hourKey(actor app.ActorContext, at time.Time) string {
	return l.prefix + ":runs_per_hour:" + safeKey(actor.ProjectID) + ":" + safeKey(actor.UserID) + ":" + at.UTC().Format("2006010215")
}

func (l *RedisLimiter) tokenKey(actor app.ActorContext, at time.Time) string {
	return l.prefix + ":model_tokens_per_day:" + safeKey(actor.ProjectID) + ":" + safeKey(actor.UserID) + ":" + at.UTC().Format("20060102")
}

func (l *RedisLimiter) runMarkerKey(runID string) string {
	return l.prefix + ":run:" + safeKey(runID)
}

func safeKey(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	replacer := strings.NewReplacer(":", "_", " ", "_", "\n", "_", "\r", "_", "\t", "_")
	return replacer.Replace(value)
}

type redisReservation struct {
	limiter        *RedisLimiter
	actor          app.ActorContext
	policy         protocol.ProjectQuotaPolicy
	activeKey      string
	hourKey        string
	tokenKey       string
	runMarkerKey   string
	reservedTokens int64
	reservedActive bool
	reservedHour   bool
	committed      bool
	mu             sync.Mutex
}

func (r *redisReservation) Commit(ctx context.Context, runID string) error {
	if r == nil || r.limiter == nil || r.limiter.store == nil {
		return nil
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return errors.New("run id is required to commit quota reservation")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.reservedActive && r.reservedTokens == 0 {
		r.committed = true
		return nil
	}
	key := r.limiter.runMarkerKey(runID)
	marker := runMarker{
		ActiveReserved: r.reservedActive,
		TokenKey:       r.tokenKey,
		TokenReserved:  r.reservedTokens,
	}
	rawMarker, err := marshalRunMarker(marker)
	if err != nil {
		return err
	}
	ok, err := r.limiter.store.SetNX(ctx, key, rawMarker, runMarkerTTL)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("quota reservation marker already exists for run %s", runID)
	}
	r.runMarkerKey = key
	r.committed = true
	return nil
}

func (r *redisReservation) Rollback(ctx context.Context) error {
	if r == nil || r.limiter == nil || r.limiter.store == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var firstErr error
	shouldRelease := true
	if r.committed && r.runMarkerKey != "" {
		removed, err := r.limiter.store.Del(ctx, r.runMarkerKey)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		shouldRelease = removed > 0
	}
	if r.reservedActive {
		if shouldRelease {
			if _, err := r.limiter.store.Decr(ctx, r.activeKey); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		r.reservedActive = false
	}
	if r.reservedHour {
		if _, err := r.limiter.store.Decr(ctx, r.hourKey); err != nil && firstErr == nil {
			firstErr = err
		}
		r.reservedHour = false
	}
	if r.reservedTokens > 0 {
		if shouldRelease {
			if _, err := r.limiter.store.DecrBy(ctx, r.tokenKey, r.reservedTokens); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		r.reservedTokens = 0
	}
	return firstErr
}

type runMarker struct {
	ActiveReserved bool   `json:"active_reserved,omitempty"`
	TokenKey       string `json:"token_key,omitempty"`
	TokenReserved  int64  `json:"token_reserved,omitempty"`
}

func marshalRunMarker(marker runMarker) (string, error) {
	body, err := json.Marshal(marker)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func parseRunMarker(raw string) runMarker {
	var marker runMarker
	if err := json.Unmarshal([]byte(raw), &marker); err == nil {
		return marker
	}
	if strings.TrimSpace(raw) == "1" {
		return runMarker{ActiveReserved: true}
	}
	parts := strings.Split(raw, ";")
	for _, part := range parts {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "active":
			marker.ActiveReserved = strings.TrimSpace(value) == "1" || strings.EqualFold(strings.TrimSpace(value), "true")
		case "token_key":
			marker.TokenKey = strings.TrimSpace(value)
		case "token_reserved":
			parsed, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
			marker.TokenReserved = parsed
		}
	}
	return marker
}

type noopReservation struct{}

func (noopReservation) Commit(context.Context, string) error { return nil }
func (noopReservation) Rollback(context.Context) error       { return nil }

type RedisCounterStore struct {
	client *redis.Client
}

func NewRedisCounterStore(addr string) *RedisCounterStore {
	return &RedisCounterStore{client: redis.NewClient(&redis.Options{Addr: addr})}
}

func (s *RedisCounterStore) Incr(ctx context.Context, key string) (int64, error) {
	return s.client.Incr(ctx, key).Result()
}

func (s *RedisCounterStore) Decr(ctx context.Context, key string) (int64, error) {
	return s.client.Decr(ctx, key).Result()
}

func (s *RedisCounterStore) IncrBy(ctx context.Context, key string, amount int64) (int64, error) {
	return s.client.IncrBy(ctx, key, amount).Result()
}

func (s *RedisCounterStore) DecrBy(ctx context.Context, key string, amount int64) (int64, error) {
	return s.client.DecrBy(ctx, key, amount).Result()
}

func (s *RedisCounterStore) Expire(ctx context.Context, key string, ttl time.Duration) error {
	return s.client.Expire(ctx, key, ttl).Err()
}

func (s *RedisCounterStore) SetNX(ctx context.Context, key string, value string, ttl time.Duration) (bool, error) {
	return s.client.SetNX(ctx, key, value, ttl).Result()
}

func (s *RedisCounterStore) Get(ctx context.Context, key string) (string, bool, error) {
	value, err := s.client.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

func (s *RedisCounterStore) Del(ctx context.Context, key string) (int64, error) {
	return s.client.Del(ctx, key).Result()
}

func (s *RedisCounterStore) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Close()
}

func maxInt(value, fallback int) int {
	if value < fallback {
		return fallback
	}
	return value
}
