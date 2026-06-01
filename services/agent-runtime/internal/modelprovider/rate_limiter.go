package modelprovider

import (
	"context"
	"sync"
	"time"
)

type RateLimitConfig struct {
	RequestsPerMinute int
	MaxConcurrent     int
}

func (c RateLimitConfig) Enabled() bool {
	return c.RequestsPerMinute > 0 || c.MaxConcurrent > 0
}

type LocalRateLimiter struct {
	requestsPerMinute int
	slots             chan struct{}

	mu          sync.Mutex
	windowStart time.Time
	windowUsed  int
	now         func() time.Time
}

func NewLocalRateLimiter(config RateLimitConfig) *LocalRateLimiter {
	if !config.Enabled() {
		return nil
	}
	limiter := &LocalRateLimiter{
		requestsPerMinute: config.RequestsPerMinute,
		now:               time.Now,
	}
	if config.MaxConcurrent > 0 {
		limiter.slots = make(chan struct{}, config.MaxConcurrent)
	}
	return limiter
}

func (l *LocalRateLimiter) Acquire(ctx context.Context) (func(), error) {
	if l == nil {
		return func() {}, nil
	}
	if l.slots != nil {
		select {
		case l.slots <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	release := func() {
		if l.slots != nil {
			select {
			case <-l.slots:
			default:
			}
		}
	}
	if l.requestsPerMinute <= 0 {
		return release, nil
	}
	if err := l.reserveRequest(); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

func (l *LocalRateLimiter) reserveRequest() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if l.windowStart.IsZero() || now.Sub(l.windowStart) >= time.Minute {
		l.windowStart = now
		l.windowUsed = 0
	}
	if l.windowUsed >= l.requestsPerMinute {
		return &ProviderError{
			Class:     ErrorClassRateLimited,
			Retryable: true,
			Message:   "local model provider request rate limit exceeded",
		}
	}
	l.windowUsed++
	return nil
}
