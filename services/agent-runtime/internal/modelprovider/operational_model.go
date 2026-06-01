package modelprovider

import (
	"context"
	"errors"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"niceagent/common/platform"
	"niceagent/common/protocol"
)

type ProviderMetadata struct {
	Provider string
	Model    string
}

type OperationalChatModel struct {
	inner    model.ToolCallingChatModel
	meta     ProviderMetadata
	tracker  *UsageTracker
	redactor Redactor
	limiter  *LocalRateLimiter
}

func NewOperationalChatModel(inner model.ToolCallingChatModel, meta ProviderMetadata, tracker *UsageTracker, redactor Redactor) *OperationalChatModel {
	return NewOperationalChatModelWithRateLimiter(inner, meta, tracker, redactor, nil)
}

func NewOperationalChatModelWithRateLimiter(inner model.ToolCallingChatModel, meta ProviderMetadata, tracker *UsageTracker, redactor Redactor, limiter *LocalRateLimiter) *OperationalChatModel {
	if tracker == nil {
		tracker = NewUsageTracker(meta.Provider, meta.Model)
	}
	return &OperationalChatModel{
		inner:    inner,
		meta:     meta,
		tracker:  tracker,
		redactor: redactor,
		limiter:  limiter,
	}
}

func (m *OperationalChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	ctx, endSpan := platform.StartSpan(ctx, "niceagent/agent_runtime", "model.generate", platform.Labels{
		"provider": m.meta.Provider,
		"model":    m.meta.Model,
	})
	start := time.Now()
	release, err := m.acquire(ctx)
	if err != nil {
		m.tracker.ObserveCall(time.Since(start), err)
		err = m.redactError(err)
		endSpan(err, nil)
		return nil, err
	}
	defer release()
	msg, err := m.inner.Generate(ctx, input, opts...)
	m.tracker.ObserveCall(time.Since(start), err)
	if err != nil {
		err = m.redactError(err)
		endSpan(err, nil)
		return nil, err
	}
	m.tracker.ObserveMessage(msg)
	endSpan(nil, nil)
	return msg, nil
}

func (m *OperationalChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	ctx, endSpan := platform.StartSpan(ctx, "niceagent/agent_runtime", "model.stream", platform.Labels{
		"provider": m.meta.Provider,
		"model":    m.meta.Model,
	})
	start := time.Now()
	release, err := m.acquire(ctx)
	if err != nil {
		m.tracker.ObserveCall(time.Since(start), err)
		err = m.redactError(err)
		endSpan(err, nil)
		return nil, err
	}
	defer release()
	reader, err := m.inner.Stream(ctx, input, opts...)
	m.tracker.ObserveCall(time.Since(start), err)
	if err != nil {
		err = m.redactError(err)
		endSpan(err, nil)
		return nil, err
	}
	endSpan(nil, nil)
	return reader, nil
}

func (m *OperationalChatModel) Probe(ctx context.Context) error {
	ctx, endSpan := platform.StartSpan(ctx, "niceagent/agent_runtime", "model.probe", platform.Labels{
		"provider": m.meta.Provider,
		"model":    m.meta.Model,
	})
	start := time.Now()
	release, err := m.acquire(ctx)
	if err != nil {
		err = m.redactError(err)
		m.tracker.ObserveProbe(time.Since(start), err)
		endSpan(err, nil)
		return err
	}
	defer release()
	_, err = m.inner.Generate(ctx, []*schema.Message{schema.UserMessage("health check: reply with ok")})
	if err != nil {
		err = m.redactError(err)
	}
	m.tracker.ObserveProbe(time.Since(start), err)
	endSpan(err, nil)
	return err
}

func (m *OperationalChatModel) MarkProbeEnabled() {
	m.tracker.MarkProbeEnabled()
}

func (m *OperationalChatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	nextInner, err := m.inner.WithTools(tools)
	if err != nil {
		return nil, m.redactError(err)
	}
	return NewOperationalChatModelWithRateLimiter(nextInner, m.meta, m.tracker, m.redactor, m.limiter), nil
}

func (m *OperationalChatModel) UsageSnapshot() protocol.RunUsage {
	return m.tracker.UsageSnapshot()
}

func (m *OperationalChatModel) PriceUsage(usage protocol.RunUsage) protocol.RunUsage {
	return m.tracker.PriceUsage(usage)
}

func (m *OperationalChatModel) ModelProviderHealth() protocol.ModelProviderHealth {
	return m.tracker.HealthSnapshot()
}

func (m *OperationalChatModel) redactError(err error) error {
	if err == nil {
		return nil
	}
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		providerErr.Message = m.redactor.RedactString(providerErr.Message)
		return providerErr
	}
	return errors.New(m.redactor.RedactString(err.Error()))
}

func (m *OperationalChatModel) acquire(ctx context.Context) (func(), error) {
	if m == nil || m.limiter == nil {
		return func() {}, nil
	}
	return m.limiter.Acquire(ctx)
}

var _ model.ToolCallingChatModel = (*OperationalChatModel)(nil)
var _ Probeable = (*OperationalChatModel)(nil)
var _ ProbeStateRecorder = (*OperationalChatModel)(nil)
