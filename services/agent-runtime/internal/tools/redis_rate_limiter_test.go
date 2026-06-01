package tools

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRedisSkillRateLimiterRejectsOverLimit(t *testing.T) {
	client := &fakeRedisSkillRateLimitClient{}
	limiter := NewRedisSkillRateLimiterWithClient(client, "test:skill")
	limiter.now = func() time.Time { return time.Date(2026, 6, 2, 10, 30, 15, 0, time.UTC) }

	if !limiter.Allow(context.Background(), "skill_123", 1) {
		t.Fatal("first call denied, want allow")
	}
	if limiter.Allow(context.Background(), "skill_123", 1) {
		t.Fatal("second call allowed, want deny")
	}
	if len(client.expirations) != 1 {
		t.Fatalf("expire calls = %d, want 1", len(client.expirations))
	}
	for key := range client.counts {
		if key == "test:skill:skill_123:202606021030" {
			t.Fatalf("redis key leaked raw skill id: %s", key)
		}
	}
}

func TestRedisSkillRateLimiterResetsPerMinuteWindow(t *testing.T) {
	client := &fakeRedisSkillRateLimitClient{}
	current := time.Date(2026, 6, 2, 10, 30, 15, 0, time.UTC)
	limiter := NewRedisSkillRateLimiterWithClient(client, "test:skill")
	limiter.now = func() time.Time { return current }

	if !limiter.Allow(context.Background(), "skill_123", 1) {
		t.Fatal("first call denied, want allow")
	}
	current = current.Add(time.Minute)
	if !limiter.Allow(context.Background(), "skill_123", 1) {
		t.Fatal("next minute call denied, want allow")
	}
	if len(client.counts) != 2 {
		t.Fatalf("redis keys = %d, want separate per-minute keys", len(client.counts))
	}
}

func TestRedisSkillRateLimiterFailsClosedOnRedisError(t *testing.T) {
	client := &fakeRedisSkillRateLimitClient{err: errors.New("redis unavailable")}
	limiter := NewRedisSkillRateLimiterWithClient(client, "test:skill")

	if limiter.Allow(context.Background(), "skill_123", 10) {
		t.Fatal("redis error allowed request, want fail closed")
	}
}

type fakeRedisSkillRateLimitClient struct {
	counts      map[string]int64
	expirations map[string]time.Duration
	err         error
}

func (c *fakeRedisSkillRateLimitClient) Incr(_ context.Context, key string) (int64, error) {
	if c.err != nil {
		return 0, c.err
	}
	if c.counts == nil {
		c.counts = map[string]int64{}
	}
	c.counts[key]++
	return c.counts[key], nil
}

func (c *fakeRedisSkillRateLimitClient) Expire(_ context.Context, key string, expiration time.Duration) error {
	if c.err != nil {
		return c.err
	}
	if c.expirations == nil {
		c.expirations = map[string]time.Duration{}
	}
	c.expirations[key] = expiration
	return nil
}
