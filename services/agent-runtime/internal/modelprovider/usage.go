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

type UsagePricer interface {
	PriceUsage(usage protocol.RunUsage) protocol.RunUsage
}

type HealthReporter interface {
	ModelProviderHealth() protocol.ModelProviderHealth
}

type UsageTracker struct {
	mu       sync.Mutex
	provider string
	model    string
	pricing  PricingConfig
	usage    protocol.RunUsage
	hasReal  bool

	requestCount  int64
	successCount  int64
	errorCount    int64
	lastError     string
	lastLatencyMs int64
	lastSuccessAt time.Time
	lastFailureAt time.Time

	probeEnabled bool
	probeCount   int64
	probeSuccess int64
	probeError   int64
	probeStatus  string
	lastProbeAt  time.Time
}

func NewUsageTracker(provider, modelName string) *UsageTracker {
	return NewUsageTrackerWithPricing(provider, modelName, PricingConfig{})
}

func NewUsageTrackerWithPricing(provider, modelName string, pricing PricingConfig) *UsageTracker {
	return &UsageTracker{
		provider: provider,
		model:    modelName,
		pricing:  pricing,
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
	t.lastLatencyMs = duration.Milliseconds()
}

func (t *UsageTracker) ObserveCall(duration time.Duration, err error) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.requestCount++
	t.usage.LatencyMillis += duration.Milliseconds()
	t.lastLatencyMs = duration.Milliseconds()
	if err == nil {
		t.successCount++
		t.lastSuccessAt = time.Now().UTC()
		t.mu.Unlock()
		return
	}
	t.errorCount++
	t.lastFailureAt = time.Now().UTC()
	classified := ClassifyProviderError(err)
	if classified != nil {
		t.usage.ErrorClass = string(classified.Class)
		t.lastError = classified.Message
	} else {
		t.lastError = err.Error()
	}
	t.mu.Unlock()
}

func (t *UsageTracker) MarkProbeEnabled() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.probeEnabled = true
	t.mu.Unlock()
}

func (t *UsageTracker) ObserveProbe(duration time.Duration, err error) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.probeEnabled = true
	t.probeCount++
	t.lastProbeAt = time.Now().UTC()
	t.lastLatencyMs = duration.Milliseconds()
	if err == nil {
		t.probeSuccess++
		t.probeStatus = "healthy"
		t.lastSuccessAt = t.lastProbeAt
		t.mu.Unlock()
		return
	}
	t.probeError++
	t.probeStatus = "degraded"
	t.lastFailureAt = t.lastProbeAt
	classified := ClassifyProviderError(err)
	if classified != nil {
		t.usage.ErrorClass = string(classified.Class)
		t.lastError = classified.Message
	} else {
		t.lastError = err.Error()
	}
	t.mu.Unlock()
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
	t.lastError = classified.Message
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
	return t.PriceUsage(usage)
}

func (t *UsageTracker) PriceUsage(usage protocol.RunUsage) protocol.RunUsage {
	if t == nil {
		return protocol.NormalizeRunUsage(usage)
	}
	return t.pricing.Apply(usage)
}

func (t *UsageTracker) HealthSnapshot() protocol.ModelProviderHealth {
	if t == nil {
		return protocol.ModelProviderHealth{Status: "unknown"}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	status := "unknown"
	if t.requestCount > 0 {
		status = "healthy"
	}
	if t.probeEnabled && t.probeStatus != "" {
		status = t.probeStatus
	}
	if !t.lastFailureAt.IsZero() && (t.lastSuccessAt.IsZero() || t.lastFailureAt.After(t.lastSuccessAt)) {
		status = "degraded"
	}
	return protocol.ModelProviderHealth{
		Provider:       firstNonEmpty(t.usage.Provider, t.provider),
		Model:          firstNonEmpty(t.usage.Model, t.model),
		Status:         status,
		ProbeEnabled:   t.probeEnabled,
		ProbeStatus:    t.probeStatus,
		ProbeCount:     t.probeCount,
		ProbeSuccess:   t.probeSuccess,
		ProbeError:     t.probeError,
		LastProbeAt:    formatHealthTime(t.lastProbeAt),
		RequestCount:   t.requestCount,
		SuccessCount:   t.successCount,
		ErrorCount:     t.errorCount,
		RetryCount:     t.usage.RetryCount,
		LastErrorClass: t.usage.ErrorClass,
		LastError:      t.lastError,
		LastLatencyMs:  t.lastLatencyMs,
		LastSuccessAt:  formatHealthTime(t.lastSuccessAt),
		LastFailureAt:  formatHealthTime(t.lastFailureAt),
	}
}

func (t *UsageTracker) HealthTargetSnapshot(name string) protocol.ModelProviderTargetHealth {
	health := t.HealthSnapshot()
	return protocol.ModelProviderTargetHealth{
		Name:           firstNonEmpty(name, health.Provider, health.Model),
		Status:         health.Status,
		ProbeEnabled:   health.ProbeEnabled,
		ProbeStatus:    health.ProbeStatus,
		ProbeCount:     health.ProbeCount,
		ProbeSuccess:   health.ProbeSuccess,
		ProbeError:     health.ProbeError,
		LastProbeAt:    health.LastProbeAt,
		RequestCount:   health.RequestCount,
		SuccessCount:   health.SuccessCount,
		ErrorCount:     health.ErrorCount,
		RetryCount:     health.RetryCount,
		LastErrorClass: health.LastErrorClass,
		LastError:      health.LastError,
		LastLatencyMs:  health.LastLatencyMs,
		LastSuccessAt:  health.LastSuccessAt,
		LastFailureAt:  health.LastFailureAt,
	}
}

func formatHealthTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func (t *UsageTracker) HasRealUsage() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.hasReal
}
