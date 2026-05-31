package modelprovider

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"niceagent/common/protocol"
)

type FallbackTarget struct {
	Name  string
	Model model.ToolCallingChatModel
}

type FallbackChatModel struct {
	Primary    FallbackTarget
	Fallback   FallbackTarget
	FallbackOn map[ErrorClass]bool
	state      *fallbackState
}

type fallbackState struct {
	mu           sync.Mutex
	lastFallback bool
	lastError    ErrorClass
}

func NewFallbackChatModel(primary FallbackTarget, fallback FallbackTarget) *FallbackChatModel {
	return &FallbackChatModel{
		Primary:    primary,
		Fallback:   fallback,
		FallbackOn: defaultFallbackClasses(),
		state:      &fallbackState{},
	}
}

func (m *FallbackChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	msg, err := m.Primary.Model.Generate(ctx, input, opts...)
	if !m.shouldFallback(err) {
		m.record(false, nil)
		return msg, err
	}
	fallbackMsg, fallbackErr := m.Fallback.Model.Generate(ctx, input, opts...)
	if fallbackErr != nil {
		return nil, errors.Join(err, fallbackErr)
	}
	m.record(true, err)
	return fallbackMsg, nil
}

func (m *FallbackChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	reader, err := m.Primary.Model.Stream(ctx, input, opts...)
	if !m.shouldFallback(err) {
		m.record(false, nil)
		return reader, err
	}
	fallbackReader, fallbackErr := m.Fallback.Model.Stream(ctx, input, opts...)
	if fallbackErr != nil {
		return nil, errors.Join(err, fallbackErr)
	}
	m.record(true, err)
	return fallbackReader, nil
}

func (m *FallbackChatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	primary, err := m.Primary.Model.WithTools(tools)
	if err != nil {
		return nil, err
	}
	fallback, err := m.Fallback.Model.WithTools(tools)
	if err != nil {
		return nil, err
	}
	next := NewFallbackChatModel(
		FallbackTarget{Name: m.Primary.Name, Model: primary},
		FallbackTarget{Name: m.Fallback.Name, Model: fallback},
	)
	next.FallbackOn = m.FallbackOn
	next.state = m.state
	return next, nil
}

func (m *FallbackChatModel) Probe(ctx context.Context) error {
	if m == nil {
		return errors.New("fallback model health probe requires a model")
	}
	var errs []error
	if err := ProbeModel(ctx, m.Primary.Model); err != nil {
		errs = append(errs, err)
	}
	if m.Fallback.Model != nil {
		if err := ProbeModel(ctx, m.Fallback.Model); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *FallbackChatModel) MarkProbeEnabled() {
	if m == nil {
		return
	}
	if recorder, ok := m.Primary.Model.(ProbeStateRecorder); ok {
		recorder.MarkProbeEnabled()
	}
	if recorder, ok := m.Fallback.Model.(ProbeStateRecorder); ok {
		recorder.MarkProbeEnabled()
	}
}

func (m *FallbackChatModel) UsageSnapshot() protocol.RunUsage {
	state := m.stateSnapshot()
	lastFallback := state.lastFallback
	lastError := state.lastError
	if lastFallback {
		usage := usageSnapshot(m.Fallback.Model)
		usage.Provider = firstNonEmpty(usage.Provider, m.Fallback.Name)
		usage.Model = firstNonEmpty(usage.Model, m.Fallback.Name)
		usage.FallbackFrom = firstNonEmpty(usage.FallbackFrom, m.Primary.Name)
		usage.FallbackTo = firstNonEmpty(usage.FallbackTo, m.Fallback.Name)
		if usage.ErrorClass == "" && lastError != "" {
			usage.ErrorClass = string(lastError)
		}
		primaryUsage := usageSnapshot(m.Primary.Model)
		usage.RetryCount += primaryUsage.RetryCount
		return protocol.NormalizeRunUsage(usage)
	}
	usage := usageSnapshot(m.Primary.Model)
	usage.Provider = firstNonEmpty(usage.Provider, m.Primary.Name)
	usage.Model = firstNonEmpty(usage.Model, m.Primary.Name)
	return protocol.NormalizeRunUsage(usage)
}

func (m *FallbackChatModel) PriceUsage(usage protocol.RunUsage) protocol.RunUsage {
	if m == nil {
		return protocol.NormalizeRunUsage(usage)
	}
	if m.stateSnapshot().lastFallback {
		if pricer, ok := m.Fallback.Model.(UsagePricer); ok {
			return pricer.PriceUsage(usage)
		}
	} else if pricer, ok := m.Primary.Model.(UsagePricer); ok {
		return pricer.PriceUsage(usage)
	}
	return protocol.NormalizeRunUsage(usage)
}

func (m *FallbackChatModel) ModelProviderHealth() protocol.ModelProviderHealth {
	if m == nil {
		return protocol.ModelProviderHealth{Status: "unknown"}
	}
	state := m.stateSnapshot()
	primary := targetHealth(m.Primary)
	fallback := targetHealth(m.Fallback)
	status := primary.Status
	if status == "" {
		status = "unknown"
	}
	if status == "healthy" && fallback.Status == "degraded" {
		status = "degraded"
	}
	if state.lastFallback && fallback.Status == "healthy" {
		status = "degraded"
	}
	health := protocol.ModelProviderHealth{
		Provider:        m.Primary.Name,
		Model:           m.Primary.Name,
		Status:          status,
		FallbackEnabled: true,
		LastFallback:    state.lastFallback,
		Targets:         []protocol.ModelProviderTargetHealth{primary, fallback},
	}
	if state.lastFallback {
		health.FallbackFrom = m.Primary.Name
		health.FallbackTo = m.Fallback.Name
	}
	if state.lastError != "" {
		health.LastErrorClass = string(state.lastError)
	}
	return health
}

func (m *FallbackChatModel) shouldFallback(err error) bool {
	if err == nil || m == nil || m.Fallback.Model == nil {
		return false
	}
	classified := ClassifyProviderError(err)
	if classified == nil || !classified.Retryable {
		return false
	}
	fallbackOn := m.FallbackOn
	if len(fallbackOn) == 0 {
		fallbackOn = defaultFallbackClasses()
	}
	return fallbackOn[classified.Class]
}

func (m *FallbackChatModel) record(usedFallback bool, err error) {
	if m == nil {
		return
	}
	state := m.ensureState()
	state.mu.Lock()
	defer state.mu.Unlock()
	state.lastFallback = usedFallback
	state.lastError = ""
	if err != nil {
		classified := ClassifyProviderError(err)
		if classified != nil {
			state.lastError = classified.Class
		}
	}
}

func (m *FallbackChatModel) ensureState() *fallbackState {
	if m.state == nil {
		m.state = &fallbackState{}
	}
	return m.state
}

func (m *FallbackChatModel) stateSnapshot() fallbackState {
	if m == nil {
		return fallbackState{}
	}
	state := m.ensureState()
	state.mu.Lock()
	defer state.mu.Unlock()
	return fallbackState{
		lastFallback: state.lastFallback,
		lastError:    state.lastError,
	}
}

func usageSnapshot(model model.ToolCallingChatModel) protocol.RunUsage {
	if reporter, ok := model.(UsageReporter); ok {
		return reporter.UsageSnapshot()
	}
	return protocol.RunUsage{}
}

func defaultFallbackClasses() map[ErrorClass]bool {
	return map[ErrorClass]bool{
		ErrorClassRateLimited:         true,
		ErrorClassProviderUnavailable: true,
		ErrorClassNetworkError:        true,
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func targetHealth(target FallbackTarget) protocol.ModelProviderTargetHealth {
	if reporter, ok := target.Model.(HealthReporter); ok {
		health := reporter.ModelProviderHealth()
		return protocol.ModelProviderTargetHealth{
			Name:           firstNonEmpty(target.Name, health.Provider, health.Model),
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
	return protocol.ModelProviderTargetHealth{Name: target.Name, Status: "unknown"}
}

var _ model.ToolCallingChatModel = (*FallbackChatModel)(nil)
var _ Probeable = (*FallbackChatModel)(nil)
var _ ProbeStateRecorder = (*FallbackChatModel)(nil)
