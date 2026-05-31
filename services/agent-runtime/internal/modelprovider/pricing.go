package modelprovider

import "niceagent/common/protocol"

const tokensPerMillion = 1_000_000

type PricingConfig struct {
	InputPer1M           float64
	CachedInputPer1M     float64
	OutputPer1M          float64
	ReasoningOutputPer1M float64
	Currency             string
}

func (p PricingConfig) Enabled() bool {
	return p.InputPer1M > 0 || p.CachedInputPer1M > 0 || p.OutputPer1M > 0 || p.ReasoningOutputPer1M > 0
}

func (p PricingConfig) Apply(usage protocol.RunUsage) protocol.RunUsage {
	if !p.Enabled() || usage.Cost > 0 {
		return protocol.NormalizeRunUsage(usage)
	}
	billableInput := usage.InputTokens - usage.CachedTokens
	if billableInput < 0 {
		billableInput = 0
	}
	visibleOutput := usage.OutputTokens
	reasoningOutput := 0
	if p.ReasoningOutputPer1M > 0 && usage.ReasoningTokens > 0 {
		reasoningOutput = usage.ReasoningTokens
		visibleOutput -= usage.ReasoningTokens
		if visibleOutput < 0 {
			visibleOutput = 0
		}
	}
	usage.Cost =
		float64(billableInput)*p.InputPer1M/tokensPerMillion +
			float64(usage.CachedTokens)*p.CachedInputPer1M/tokensPerMillion +
			float64(visibleOutput)*p.OutputPer1M/tokensPerMillion +
			float64(reasoningOutput)*p.ReasoningOutputPer1M/tokensPerMillion
	if usage.Currency == "" {
		usage.Currency = p.currency()
	}
	return protocol.NormalizeRunUsage(usage)
}

func (p PricingConfig) currency() string {
	if p.Currency == "" {
		return "USD"
	}
	return p.Currency
}
