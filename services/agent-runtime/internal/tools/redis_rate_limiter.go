package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	redis "github.com/redis/go-redis/v9"

	"niceagent/common/platform"
)

const defaultSkillRateLimitPrefix = "niceagent:skill-rate"

type redisSkillRateLimitClient interface {
	Incr(ctx context.Context, key string) (int64, error)
	Expire(ctx context.Context, key string, expiration time.Duration) error
}

type RedisSkillRateLimiter struct {
	client redisSkillRateLimitClient
	prefix string
	now    func() time.Time
}

func NewRedisSkillRateLimiter(addr, prefix string) *RedisSkillRateLimiter {
	return NewRedisSkillRateLimiterWithClient(
		&goRedisSkillRateLimitClient{client: redis.NewClient(&redis.Options{Addr: addr})},
		prefix,
	)
}

func NewRedisSkillRateLimiterWithClient(client redisSkillRateLimitClient, prefix string) *RedisSkillRateLimiter {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = defaultSkillRateLimitPrefix
	}
	return &RedisSkillRateLimiter{
		client: client,
		prefix: strings.TrimRight(prefix, ":"),
		now:    time.Now,
	}
}

func (l *RedisSkillRateLimiter) Allow(ctx context.Context, key string, limitPerMinute int) bool {
	if l == nil || l.client == nil || limitPerMinute <= 0 {
		return true
	}
	redisKey := l.redisKey(key)
	ctx, endSpan := platform.StartSpan(ctx, "niceagent/agent_runtime", "redis.command", platform.Labels{
		"redis_command": "INCR",
		"component":     "skill_rate_limiter",
	})
	count, err := l.client.Incr(ctx, redisKey)
	if count == 1 && err == nil {
		err = l.client.Expire(ctx, redisKey, l.windowTTL())
	}
	endSpan(err, nil)
	if err != nil {
		return false
	}
	return count <= int64(limitPerMinute)
}

func (l *RedisSkillRateLimiter) redisKey(key string) string {
	hash := sha256.Sum256([]byte(strings.TrimSpace(key)))
	return l.prefix + ":" + hex.EncodeToString(hash[:8]) + ":" + l.windowStart().Format("200601021504")
}

func (l *RedisSkillRateLimiter) windowStart() time.Time {
	now := l.now()
	return now.Truncate(time.Minute).UTC()
}

func (l *RedisSkillRateLimiter) windowTTL() time.Duration {
	nextWindow := l.windowStart().Add(time.Minute)
	ttl := nextWindow.Sub(l.now()) + time.Minute
	if ttl < time.Minute {
		return time.Minute
	}
	return ttl
}

type goRedisSkillRateLimitClient struct {
	client *redis.Client
}

func (c *goRedisSkillRateLimitClient) Incr(ctx context.Context, key string) (int64, error) {
	return c.client.Incr(ctx, key).Result()
}

func (c *goRedisSkillRateLimitClient) Expire(ctx context.Context, key string, expiration time.Duration) error {
	return c.client.Expire(ctx, key, expiration).Err()
}
