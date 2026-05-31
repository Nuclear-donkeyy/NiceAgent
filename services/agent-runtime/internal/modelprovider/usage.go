package modelprovider

import (
	"sync"
	"time"

	"github.com/cloudwego/eino/schema"

	"niceagent/common/protocol"
)

type UsageReporter interface {
	UsageSnapshot() protocol.RunUsage
}

type UsageTracker struct {
	mu       sync.Mutex
	provider string
	model    string
	usage    protocol.RunUsage
	hasReal  bool
}

func NewUsageTracker(provider, modelName string) *UsageTracker {
	return &UsageTracker{
		provider: provider,
		model:    modelName,
		usage: protocol.RunUsage{
			Provider: provider,
			Model:    modelName,
		},
	}
}

func (t *UsageTracker) ObserveMessage(message *schema.Message) {
	if t == nil || message == nil || message.ResponseMeta == nil || message.ResponseMeta.Usage == nil {
		return
	}
	t.ObserveTokenUsage(message.ResponseMeta.Usage)
}

func (t *UsageTracker) ObserveTokenUsage(usage *schema.TokenUsage) {
	if t == nil || usage == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.hasReal = true
	t.usage.Estimated = false
	t.usage.InputTokens += usage.PromptTokens
	t.usage.OutputTokens += usage.CompletionTokens
	t.usage.CachedTokens += usage.PromptTokenDetails.CachedTokens
	t.usage.ReasoningTokens += usage.CompletionTokensDetails.ReasoningTokens
	if usage.TotalTokens > 0 {
		t.usage.TotalTokens += usage.TotalTokens
	} else {
		t.usage.TotalTokens += usage.PromptTokens + usage.CompletionTokens
	}
}

func (t *UsageTracker) ObserveLatency(duration time.Duration) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.usage.LatencyMillis += duration.Milliseconds()
}

func (t *UsageTracker) ObserveRetry() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.usage.RetryCount++
}

func (t *UsageTracker) ObserveError(err error) {
	if t == nil || err == nil {
		return
	}
	classified := ClassifyProviderError(err)
	if classified == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.usage.ErrorClass = string(classified.Class)
}

func (t *UsageTracker) UsageSnapshot() protocol.RunUsage {
	if t == nil {
		return protocol.RunUsage{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	usage := t.usage
	if usage.Provider == "" {
		usage.Provider = t.provider
	}
	if usage.Model == "" {
		usage.Model = t.model
	}
	return protocol.NormalizeRunUsage(usage)
}

func (t *UsageTracker) HasRealUsage() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.hasReal
}
