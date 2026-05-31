package quota

import (
	"context"
	"errors"
	"testing"
	"time"

	"niceagent/common/protocol"
	"niceagent/control-plane/internal/app"
)

func TestRedisLimiterReservesAndReleasesConcurrentRun(t *testing.T) {
	store := newFakeCounterStore()
	limiter := NewLimiterWithStore(store, "test:quota")
	actor := app.ActorContext{UserID: "user-1", ProjectID: "project-1"}
	policy := protocol.ProjectQuotaPolicy{MaxConcurrentRuns: 1, MaxRunsPerHour: 10}

	reservation, denial, err := limiter.ReserveRun(context.Background(), actor, policy)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if denial.Message != "" {
		t.Fatalf("unexpected denial: %#v", denial)
	}
	if err := reservation.Commit(context.Background(), "run-1"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if got := store.value(limiter.activeKey(actor)); got != 1 {
		t.Fatalf("active counter = %d, want 1", got)
	}
	if err := limiter.ReleaseRun(context.Background(), protocol.Run{ID: "run-1", UserID: "user-1"}, "project-1", protocol.RunUsage{}); err != nil {
		t.Fatalf("release: %v", err)
	}
	if got := store.value(limiter.activeKey(actor)); got != 0 {
		t.Fatalf("active counter after release = %d, want 0", got)
	}
	if err := limiter.ReleaseRun(context.Background(), protocol.Run{ID: "run-1", UserID: "user-1"}, "project-1", protocol.RunUsage{}); err != nil {
		t.Fatalf("second release: %v", err)
	}
	if got := store.value(limiter.activeKey(actor)); got != 0 {
		t.Fatalf("active counter after second release = %d, want 0", got)
	}
}

func TestRedisLimiterReservesAndSettlesModelTokens(t *testing.T) {
	store := newFakeCounterStore()
	limiter := NewLimiterWithStoreAndOptions(store, "test:quota", LimiterOptions{ModelTokenReservationPerRun: 100})
	limiter.now = func() time.Time { return time.Date(2026, 5, 31, 10, 30, 0, 0, time.UTC) }
	actor := app.ActorContext{UserID: "user-1", ProjectID: "project-1"}
	policy := protocol.ProjectQuotaPolicy{MaxModelTokensPerDay: 200}

	reservation, denial, err := limiter.ReserveRun(context.Background(), actor, policy)
	if err != nil || denial.Message != "" {
		t.Fatalf("reserve err=%v denial=%#v", err, denial)
	}
	tokenKey := limiter.tokenKey(actor, limiter.now())
	if got := store.value(tokenKey); got != 100 {
		t.Fatalf("reserved tokens = %d, want 100", got)
	}
	if err := reservation.Commit(context.Background(), "run-token"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := limiter.ReleaseRun(context.Background(), protocol.Run{ID: "run-token", UserID: "user-1"}, "project-1", protocol.RunUsage{TotalTokens: 80}); err != nil {
		t.Fatalf("release: %v", err)
	}
	if got := store.value(tokenKey); got != 80 {
		t.Fatalf("settled tokens = %d, want actual usage 80", got)
	}
}

func TestRedisLimiterUsesDynamicTokenReservationHint(t *testing.T) {
	store := newFakeCounterStore()
	limiter := NewLimiterWithStoreAndOptions(store, "test:quota", LimiterOptions{ModelTokenReservationPerRun: 100})
	limiter.now = func() time.Time { return time.Date(2026, 5, 31, 10, 30, 0, 0, time.UTC) }
	actor := app.ActorContext{UserID: "user-1", ProjectID: "project-1"}
	policy := protocol.ProjectQuotaPolicy{MaxModelTokensPerDay: 200}

	reservation, denial, err := limiter.ReserveRunWithHint(context.Background(), actor, policy, ReservationHint{ModelTokens: 42})
	if err != nil || denial.Message != "" {
		t.Fatalf("reserve err=%v denial=%#v", err, denial)
	}
	tokenKey := limiter.tokenKey(actor, limiter.now())
	if got := store.value(tokenKey); got != 42 {
		t.Fatalf("reserved tokens = %d, want dynamic hint 42", got)
	}
	if err := reservation.Commit(context.Background(), "run-dynamic-token"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := limiter.ReleaseRun(context.Background(), protocol.Run{ID: "run-dynamic-token", UserID: "user-1"}, "project-1", protocol.RunUsage{TotalTokens: 60}); err != nil {
		t.Fatalf("release: %v", err)
	}
	if got := store.value(tokenKey); got != 60 {
		t.Fatalf("settled tokens = %d, want actual usage 60", got)
	}
}

func TestRedisLimiterDeniesWhenTokenReservationWouldExceedDailyLimit(t *testing.T) {
	store := newFakeCounterStore()
	limiter := NewLimiterWithStoreAndOptions(store, "test:quota", LimiterOptions{ModelTokenReservationPerRun: 100})
	limiter.now = func() time.Time { return time.Date(2026, 5, 31, 10, 30, 0, 0, time.UTC) }
	actor := app.ActorContext{UserID: "user-1", ProjectID: "project-1"}
	policy := protocol.ProjectQuotaPolicy{MaxModelTokensPerDay: 100}

	first, denial, err := limiter.ReserveRun(context.Background(), actor, policy)
	if err != nil || denial.Message != "" {
		t.Fatalf("first reserve err=%v denial=%#v", err, denial)
	}
	defer first.Rollback(context.Background())

	_, denial, err = limiter.ReserveRun(context.Background(), actor, policy)
	if err != nil {
		t.Fatalf("second reserve: %v", err)
	}
	if denial.Message == "" || denial.Metadata["quota"] != "model_tokens_per_day" || denial.Metadata["reserved"] != int64(100) {
		t.Fatalf("denial = %#v, want token reservation denial", denial)
	}
	if got := store.value(limiter.tokenKey(actor, limiter.now())); got != 100 {
		t.Fatalf("token counter after denial = %d, want 100", got)
	}
}

func TestRedisLimiterRollbackReleasesCommittedTokenOnlyReservation(t *testing.T) {
	store := newFakeCounterStore()
	limiter := NewLimiterWithStoreAndOptions(store, "test:quota", LimiterOptions{ModelTokenReservationPerRun: 50})
	limiter.now = func() time.Time { return time.Date(2026, 5, 31, 10, 30, 0, 0, time.UTC) }
	actor := app.ActorContext{UserID: "user-1", ProjectID: "project-1"}
	policy := protocol.ProjectQuotaPolicy{MaxModelTokensPerDay: 100}

	reservation, denial, err := limiter.ReserveRun(context.Background(), actor, policy)
	if err != nil || denial.Message != "" {
		t.Fatalf("reserve err=%v denial=%#v", err, denial)
	}
	if err := reservation.Commit(context.Background(), "run-token-rollback"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := reservation.Rollback(context.Background()); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if got := store.value(limiter.tokenKey(actor, limiter.now())); got != 0 {
		t.Fatalf("token counter after rollback = %d, want 0", got)
	}
	if _, ok := store.set[limiter.runMarkerKey("run-token-rollback")]; ok {
		t.Fatal("run marker still exists after rollback")
	}
}

func TestRedisLimiterDeniesOverConcurrentLimitAndRollsBack(t *testing.T) {
	store := newFakeCounterStore()
	limiter := NewLimiterWithStore(store, "test:quota")
	actor := app.ActorContext{UserID: "user-1", ProjectID: "project-1"}
	policy := protocol.ProjectQuotaPolicy{MaxConcurrentRuns: 1}

	first, denial, err := limiter.ReserveRun(context.Background(), actor, policy)
	if err != nil || denial.Message != "" {
		t.Fatalf("first reserve err=%v denial=%#v", err, denial)
	}
	defer first.Rollback(context.Background())

	_, denial, err = limiter.ReserveRun(context.Background(), actor, policy)
	if err != nil {
		t.Fatalf("second reserve: %v", err)
	}
	if denial.Message == "" || denial.Metadata["quota"] != "concurrent_runs" || denial.Metadata["source"] != "redis" {
		t.Fatalf("denial = %#v, want redis concurrent denial", denial)
	}
	if got := store.value(limiter.activeKey(actor)); got != 1 {
		t.Fatalf("active counter after denial = %d, want 1", got)
	}
}

func TestRedisLimiterRollbackReleasesHourAndActiveCounters(t *testing.T) {
	store := newFakeCounterStore()
	limiter := NewLimiterWithStore(store, "test:quota")
	limiter.now = func() time.Time { return time.Date(2026, 5, 31, 10, 30, 0, 0, time.UTC) }
	actor := app.ActorContext{UserID: "user-1", ProjectID: "project-1"}
	policy := protocol.ProjectQuotaPolicy{MaxConcurrentRuns: 2, MaxRunsPerHour: 2}

	reservation, denial, err := limiter.ReserveRun(context.Background(), actor, policy)
	if err != nil || denial.Message != "" {
		t.Fatalf("reserve err=%v denial=%#v", err, denial)
	}
	if err := reservation.Rollback(context.Background()); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if got := store.value(limiter.activeKey(actor)); got != 0 {
		t.Fatalf("active counter after rollback = %d, want 0", got)
	}
	if got := store.value(limiter.hourKey(actor, limiter.now())); got != 0 {
		t.Fatalf("hour counter after rollback = %d, want 0", got)
	}
}

func TestRedisLimiterRollsBackActiveWhenHourLimitDenies(t *testing.T) {
	store := newFakeCounterStore()
	limiter := NewLimiterWithStore(store, "test:quota")
	limiter.now = func() time.Time { return time.Date(2026, 5, 31, 10, 30, 0, 0, time.UTC) }
	actor := app.ActorContext{UserID: "user-1", ProjectID: "project-1"}
	policy := protocol.ProjectQuotaPolicy{MaxConcurrentRuns: 10, MaxRunsPerHour: 1}

	first, denial, err := limiter.ReserveRun(context.Background(), actor, policy)
	if err != nil || denial.Message != "" {
		t.Fatalf("first reserve err=%v denial=%#v", err, denial)
	}
	defer first.Rollback(context.Background())

	_, denial, err = limiter.ReserveRun(context.Background(), actor, policy)
	if err != nil {
		t.Fatalf("second reserve: %v", err)
	}
	if denial.Message == "" || denial.Metadata["quota"] != "runs_per_hour" {
		t.Fatalf("denial = %#v, want hour denial", denial)
	}
	if got := store.value(limiter.activeKey(actor)); got != 1 {
		t.Fatalf("active counter after hour denial = %d, want 1", got)
	}
}

type fakeCounterStore struct {
	values map[string]int64
	set    map[string]string
	err    error
}

func newFakeCounterStore() *fakeCounterStore {
	return &fakeCounterStore{values: map[string]int64{}, set: map[string]string{}}
}

func (s *fakeCounterStore) Incr(_ context.Context, key string) (int64, error) {
	if s.err != nil {
		return 0, s.err
	}
	s.values[key]++
	return s.values[key], nil
}

func (s *fakeCounterStore) Decr(_ context.Context, key string) (int64, error) {
	if s.err != nil {
		return 0, s.err
	}
	s.values[key]--
	return s.values[key], nil
}

func (s *fakeCounterStore) IncrBy(_ context.Context, key string, amount int64) (int64, error) {
	if s.err != nil {
		return 0, s.err
	}
	s.values[key] += amount
	return s.values[key], nil
}

func (s *fakeCounterStore) DecrBy(_ context.Context, key string, amount int64) (int64, error) {
	if s.err != nil {
		return 0, s.err
	}
	s.values[key] -= amount
	return s.values[key], nil
}

func (s *fakeCounterStore) Expire(context.Context, string, time.Duration) error {
	return s.err
}

func (s *fakeCounterStore) SetNX(_ context.Context, key string, value string, _ time.Duration) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	if _, ok := s.set[key]; ok {
		return false, nil
	}
	s.set[key] = value
	return true, nil
}

func (s *fakeCounterStore) Get(_ context.Context, key string) (string, bool, error) {
	if s.err != nil {
		return "", false, s.err
	}
	value, ok := s.set[key]
	return value, ok, nil
}

func (s *fakeCounterStore) Del(_ context.Context, key string) (int64, error) {
	if s.err != nil {
		return 0, s.err
	}
	if _, ok := s.set[key]; ok {
		delete(s.set, key)
		return 1, nil
	}
	return 0, nil
}

func (s *fakeCounterStore) Close() error {
	return nil
}

func (s *fakeCounterStore) value(key string) int64 {
	return s.values[key]
}

func TestFakeCounterStoreCanReturnErrors(t *testing.T) {
	store := newFakeCounterStore()
	store.err = errors.New("boom")
	limiter := NewLimiterWithStore(store, "test:quota")
	_, _, err := limiter.ReserveRun(context.Background(), app.ActorContext{UserID: "u", ProjectID: "p"}, protocol.ProjectQuotaPolicy{MaxConcurrentRuns: 1})
	if err == nil {
		t.Fatal("expected store error")
	}
}
