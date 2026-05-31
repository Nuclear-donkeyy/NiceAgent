package modelprovider

import (
	"context"
	"errors"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

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
}

func NewOperationalChatModel(inner model.ToolCallingChatModel, meta ProviderMetadata, tracker *UsageTracker, redactor Redactor) *OperationalChatModel {
	if tracker == nil {
		tracker = NewUsageTracker(meta.Provider, meta.Model)
	}
	return &OperationalChatModel{
		inner:    inner,
		meta:     meta,
		tracker:  tracker,
		redactor: redactor,
	}
}

func (m *OperationalChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	start := time.Now()
	msg, err := m.inner.Generate(ctx, input, opts...)
	m.tracker.ObserveLatency(time.Since(start))
	if err != nil {
		m.tracker.ObserveError(err)
		return nil, m.redactError(err)
	}
	m.tracker.ObserveMessage(msg)
	return msg, nil
}

func (m *OperationalChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	start := time.Now()
	reader, err := m.inner.Stream(ctx, input, opts...)
	m.tracker.ObserveLatency(time.Since(start))
	if err != nil {
		m.tracker.ObserveError(err)
		return nil, m.redactError(err)
	}
	return reader, nil
}

func (m *OperationalChatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	nextInner, err := m.inner.WithTools(tools)
	if err != nil {
		return nil, m.redactError(err)
	}
	return NewOperationalChatModel(nextInner, m.meta, m.tracker, m.redactor), nil
}

func (m *OperationalChatModel) UsageSnapshot() protocol.RunUsage {
	return m.tracker.UsageSnapshot()
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

var _ model.ToolCallingChatModel = (*OperationalChatModel)(nil)
