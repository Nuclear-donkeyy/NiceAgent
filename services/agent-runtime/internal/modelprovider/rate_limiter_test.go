package modelprovider

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
)

func TestLocalRateLimiterRejectsRequestsOverFixedWindow(t *testing.T) {
	limiter := NewLocalRateLimiter(RateLimitConfig{RequestsPerMinute: 1})
	limiter.now = func() time.Time { return time.Unix(100, 0) }

	release, err := limiter.Acquire(context.Background())
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	release()

	_, err = limiter.Acquire(context.Background())
	if err == nil {
		t.Fatal("expected local rate limit error")
	}
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) || providerErr.Class != ErrorClassRateLimited || !providerErr.Retryable {
		t.Fatalf("rate limit error = %#v, want retryable rate_limited ProviderError", err)
	}
}

func TestLocalRateLimiterResetsWindow(t *testing.T) {
	limiter := NewLocalRateLimiter(RateLimitConfig{RequestsPerMinute: 1})
	now := time.Unix(100, 0)
	limiter.now = func() time.Time { return now }

	release, err := limiter.Acquire(context.Background())
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	release()

	now = now.Add(time.Minute)
	release, err = limiter.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire after reset: %v", err)
	}
	release()
}

func TestOperationalChatModelAppliesLocalRateLimitAndTracksUsage(t *testing.T) {
	inner := &testChatModel{message: schema.AssistantMessage("ok", nil)}
	tracker := NewUsageTracker("provider", "model")
	limiter := NewLocalRateLimiter(RateLimitConfig{RequestsPerMinute: 1})
	limiter.now = func() time.Time { return time.Unix(100, 0) }
	chatModel := NewOperationalChatModelWithRateLimiter(inner, ProviderMetadata{Provider: "provider", Model: "model"}, tracker, NewRedactor(""), limiter)

	if _, err := chatModel.Generate(context.Background(), []*schema.Message{schema.UserMessage("hello")}); err != nil {
		t.Fatalf("first generate: %v", err)
	}
	if _, err := chatModel.Generate(context.Background(), []*schema.Message{schema.UserMessage("again")}); err == nil {
		t.Fatal("expected second generate to hit local rate limit")
	}
	if inner.calls != 1 {
		t.Fatalf("inner calls = %d, want only the first request to reach provider", inner.calls)
	}
	usage := tracker.UsageSnapshot()
	if usage.ErrorClass != string(ErrorClassRateLimited) {
		t.Fatalf("usage error class = %q, want rate_limited", usage.ErrorClass)
	}
}

func TestOperationalChatModelWithToolsSharesLocalRateLimit(t *testing.T) {
	inner := &testChatModel{message: schema.AssistantMessage("ok", nil)}
	limiter := NewLocalRateLimiter(RateLimitConfig{RequestsPerMinute: 1})
	limiter.now = func() time.Time { return time.Unix(100, 0) }
	chatModel := NewOperationalChatModelWithRateLimiter(inner, ProviderMetadata{Provider: "provider", Model: "model"}, nil, NewRedactor(""), limiter)

	withTools, err := chatModel.WithTools(nil)
	if err != nil {
		t.Fatalf("with tools: %v", err)
	}
	if _, err := withTools.Generate(context.Background(), []*schema.Message{schema.UserMessage("hello")}); err != nil {
		t.Fatalf("first generate: %v", err)
	}
	if _, err := chatModel.Generate(context.Background(), []*schema.Message{schema.UserMessage("again")}); err == nil {
		t.Fatal("expected shared limiter to reject request across WithTools wrapper")
	}
}
