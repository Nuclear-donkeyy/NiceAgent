package modelprovider

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"niceagent/common/platform"
	"niceagent/common/protocol"
)

func TestFallbackChatModelUsesFallbackForRetryableProviderError(t *testing.T) {
	primary := &testChatModel{
		err:   &ProviderError{Class: ErrorClassProviderUnavailable, Retryable: true, Message: "outage"},
		usage: protocol.RunUsage{Provider: "primary", Model: "primary-model", RetryCount: 2},
	}
	fallback := &testChatModel{
		message: schema.AssistantMessage("fallback ok", nil),
		usage:   protocol.RunUsage{Provider: "fallback", Model: "fallback-model", InputTokens: 3, OutputTokens: 4},
	}
	chatModel := NewFallbackChatModel(
		FallbackTarget{Name: "primary-model", Model: primary},
		FallbackTarget{Name: "fallback-model", Model: fallback},
	)

	msg, err := chatModel.Generate(context.Background(), []*schema.Message{schema.UserMessage("hello")})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if msg.Content != "fallback ok" {
		t.Fatalf("content = %q, want fallback ok", msg.Content)
	}
	if primary.calls != 1 || fallback.calls != 1 {
		t.Fatalf("calls primary/fallback = %d/%d, want 1/1", primary.calls, fallback.calls)
	}
	usage := chatModel.UsageSnapshot()
	if usage.FallbackFrom != "primary-model" || usage.FallbackTo != "fallback-model" || usage.ErrorClass != string(ErrorClassProviderUnavailable) {
		t.Fatalf("usage fallback fields = %#v", usage)
	}
	if usage.InputTokens != 3 || usage.OutputTokens != 4 || usage.RetryCount != 2 {
		t.Fatalf("usage = %#v, want fallback usage plus primary retries", usage)
	}
}

func TestFallbackChatModelDoesNotFallbackForRequestError(t *testing.T) {
	primaryErr := &ProviderError{Class: ErrorClassRequestError, Retryable: false, Message: "bad request"}
	primary := &testChatModel{err: primaryErr}
	fallback := &testChatModel{message: schema.AssistantMessage("should not happen", nil)}
	chatModel := NewFallbackChatModel(
		FallbackTarget{Name: "primary", Model: primary},
		FallbackTarget{Name: "fallback", Model: fallback},
	)

	_, err := chatModel.Generate(context.Background(), []*schema.Message{schema.UserMessage("hello")})
	if !errors.Is(err, primaryErr) {
		t.Fatalf("err = %v, want primary request error", err)
	}
	if fallback.calls != 0 {
		t.Fatalf("fallback calls = %d, want 0", fallback.calls)
	}
}

func TestFallbackChatModelWithToolsWrapsBothModels(t *testing.T) {
	primary := &testChatModel{message: schema.AssistantMessage("primary", nil)}
	fallback := &testChatModel{message: schema.AssistantMessage("fallback", nil)}
	chatModel := NewFallbackChatModel(
		FallbackTarget{Name: "primary", Model: primary},
		FallbackTarget{Name: "fallback", Model: fallback},
	)

	wrapped, err := chatModel.WithTools([]*schema.ToolInfo{{Name: "cli_exec", Desc: "CLI"}})
	if err != nil {
		t.Fatalf("with tools: %v", err)
	}
	if wrapped == nil || !primary.withTools || !fallback.withTools {
		t.Fatalf("tools not applied primary=%v fallback=%v wrapped=%T", primary.withTools, fallback.withTools, wrapped)
	}
}

func TestFallbackChatModelSharesFallbackStateAcrossWithTools(t *testing.T) {
	primary := &testChatModel{err: &ProviderError{Class: ErrorClassRateLimited, Retryable: true, Message: "slow down"}}
	fallback := &testChatModel{
		message: schema.AssistantMessage("fallback ok", nil),
		usage:   protocol.RunUsage{Provider: "fallback", Model: "fallback-model", InputTokens: 1, OutputTokens: 2},
	}
	chatModel := NewFallbackChatModel(
		FallbackTarget{Name: "primary-model", Model: primary},
		FallbackTarget{Name: "fallback-model", Model: fallback},
	)
	wrapped, err := chatModel.WithTools([]*schema.ToolInfo{{Name: "cli_exec", Desc: "CLI"}})
	if err != nil {
		t.Fatalf("with tools: %v", err)
	}
	if _, err := wrapped.Generate(context.Background(), []*schema.Message{schema.UserMessage("hello")}); err != nil {
		t.Fatalf("generate with wrapped fallback: %v", err)
	}
	usage := chatModel.UsageSnapshot()
	if usage.FallbackFrom != "primary-model" || usage.FallbackTo != "fallback-model" || usage.ErrorClass != string(ErrorClassRateLimited) {
		t.Fatalf("shared usage = %#v, want fallback metadata on original model", usage)
	}
}

func TestOperationalChatModelReportsProviderHealth(t *testing.T) {
	inner := &testChatModel{message: schema.AssistantMessage("ok", nil)}
	tracker := NewUsageTracker("test-provider", "test-model")
	chatModel := NewOperationalChatModel(inner, ProviderMetadata{Provider: "test-provider", Model: "test-model"}, tracker, Redactor{})

	if _, err := chatModel.Generate(context.Background(), []*schema.Message{schema.UserMessage("hello")}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	health := chatModel.ModelProviderHealth()
	if health.Status != "healthy" || health.Provider != "test-provider" || health.Model != "test-model" || health.RequestCount != 1 || health.SuccessCount != 1 {
		t.Fatalf("health = %#v, want healthy provider with one success", health)
	}
}

func TestOperationalChatModelProbeUpdatesHealthWithoutRunUsage(t *testing.T) {
	inner := &testChatModel{message: schema.AssistantMessage("ok", nil)}
	tracker := NewUsageTracker("test-provider", "test-model")
	chatModel := NewOperationalChatModel(inner, ProviderMetadata{Provider: "test-provider", Model: "test-model"}, tracker, Redactor{})

	chatModel.MarkProbeEnabled()
	if health := chatModel.ModelProviderHealth(); !health.ProbeEnabled || health.ProbeCount != 0 {
		t.Fatalf("marked probe health = %#v, want enabled without calls", health)
	}
	if err := chatModel.Probe(context.Background()); err != nil {
		t.Fatalf("probe: %v", err)
	}
	health := chatModel.ModelProviderHealth()
	if !health.ProbeEnabled || health.ProbeStatus != "healthy" || health.ProbeCount != 1 || health.ProbeSuccess != 1 {
		t.Fatalf("probe health = %#v, want one healthy probe", health)
	}
	if health.RequestCount != 0 || tracker.UsageSnapshot().TotalTokens != 0 {
		t.Fatalf("probe should not be counted as run usage, health=%#v usage=%#v", health, tracker.UsageSnapshot())
	}
}

func TestHealthProbeRecordsMetrics(t *testing.T) {
	inner := &testChatModel{message: schema.AssistantMessage("ok", nil)}
	tracker := NewUsageTracker("test-provider", "test-model")
	chatModel := NewOperationalChatModel(inner, ProviderMetadata{Provider: "test-provider", Model: "test-model"}, tracker, Redactor{})
	metrics := platform.NewMetrics("agent_runtime_test")

	runHealthProbe(context.Background(), chatModel, time.Second, nil, metrics)

	rendered := metrics.Render()
	if !strings.Contains(rendered, `niceagent_model_health_probe_total{service="agent_runtime_test",error_class="none",model="test-model",provider="test-provider",status="success"} 1`) {
		t.Fatalf("metrics missing probe success counter:\n%s", rendered)
	}
}

func TestFallbackChatModelReportsFallbackHealth(t *testing.T) {
	primaryTracker := NewUsageTracker("primary", "primary-model")
	primary := NewOperationalChatModel(
		&testChatModel{err: &ProviderError{Class: ErrorClassProviderUnavailable, Retryable: true, Message: "outage"}},
		ProviderMetadata{Provider: "primary", Model: "primary-model"},
		primaryTracker,
		Redactor{},
	)
	fallbackTracker := NewUsageTracker("fallback", "fallback-model")
	fallback := NewOperationalChatModel(
		&testChatModel{message: schema.AssistantMessage("fallback ok", nil)},
		ProviderMetadata{Provider: "fallback", Model: "fallback-model"},
		fallbackTracker,
		Redactor{},
	)
	chatModel := NewFallbackChatModel(
		FallbackTarget{Name: "primary", Model: primary},
		FallbackTarget{Name: "fallback", Model: fallback},
	)
	if _, err := chatModel.Generate(context.Background(), []*schema.Message{schema.UserMessage("hello")}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	health := chatModel.ModelProviderHealth()
	if !health.FallbackEnabled || !health.LastFallback || health.FallbackFrom != "primary" || health.FallbackTo != "fallback" {
		t.Fatalf("fallback health = %#v", health)
	}
	if len(health.Targets) != 2 || health.Targets[0].Status != "degraded" || health.Targets[1].Status != "healthy" {
		t.Fatalf("target health = %#v", health.Targets)
	}
}

func TestFallbackChatModelProbeReportsTargetHealth(t *testing.T) {
	primary := NewOperationalChatModel(
		&testChatModel{err: &ProviderError{Class: ErrorClassProviderUnavailable, Retryable: true, Message: "outage"}},
		ProviderMetadata{Provider: "primary", Model: "primary-model"},
		NewUsageTracker("primary", "primary-model"),
		Redactor{},
	)
	fallback := NewOperationalChatModel(
		&testChatModel{message: schema.AssistantMessage("fallback ok", nil)},
		ProviderMetadata{Provider: "fallback", Model: "fallback-model"},
		NewUsageTracker("fallback", "fallback-model"),
		Redactor{},
	)
	chatModel := NewFallbackChatModel(
		FallbackTarget{Name: "primary", Model: primary},
		FallbackTarget{Name: "fallback", Model: fallback},
	)

	if err := chatModel.Probe(context.Background()); err == nil {
		t.Fatal("expected fallback probe to return the failing primary probe error")
	}
	health := chatModel.ModelProviderHealth()
	if health.Status != "degraded" || len(health.Targets) != 2 {
		t.Fatalf("fallback probe health = %#v, want degraded two-target health", health)
	}
	if health.Targets[0].ProbeStatus != "degraded" || health.Targets[1].ProbeStatus != "healthy" {
		t.Fatalf("target probe health = %#v", health.Targets)
	}
}

type testChatModel struct {
	message   *schema.Message
	err       error
	usage     protocol.RunUsage
	calls     int
	withTools bool
}

func (m *testChatModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	if m.message != nil {
		return m.message, nil
	}
	return schema.AssistantMessage("ok", nil), nil
}

func (m *testChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return StreamSingle(ctx, msg)
}

func (m *testChatModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	next := *m
	next.withTools = true
	m.withTools = true
	return &next, nil
}

func (m *testChatModel) UsageSnapshot() protocol.RunUsage {
	usage := m.usage
	if usage.Provider == "" {
		usage.Provider = strings.TrimSpace(usage.Model)
	}
	return protocol.NormalizeRunUsage(usage)
}

var _ model.ToolCallingChatModel = (*testChatModel)(nil)
