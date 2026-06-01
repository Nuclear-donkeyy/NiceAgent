package platform

import (
	"strings"

	tiktoken "github.com/pkoukk/tiktoken-go"
)

const (
	HeuristicRuneDiv4TokenEstimator = "heuristic_rune_div4"
	TiktokenModelTokenEstimator     = "tiktoken_model"
	TiktokenCL100KTokenEstimator    = "tiktoken_cl100k_base"
	TiktokenO200KTokenEstimator     = "tiktoken_o200k_base"
)

type TokenEstimate struct {
	Tokens    int
	Estimator string
}

func EstimateTextTokens(text string) TokenEstimate {
	return EstimateTextTokensForModel(text, "")
}

func EstimateTextTokensForModel(text, model string) TokenEstimate {
	if text == "" {
		return TokenEstimate{Estimator: HeuristicRuneDiv4TokenEstimator}
	}
	if encoding, estimator, ok := tokenizerForModel(model); ok {
		return TokenEstimate{
			Tokens:    len(encoding.EncodeOrdinary(text)),
			Estimator: estimator,
		}
	}
	return TokenEstimate{
		Tokens:    len([]rune(text))/4 + 1,
		Estimator: HeuristicRuneDiv4TokenEstimator,
	}
}

func tokenizerForModel(model string) (*tiktoken.Tiktoken, string, bool) {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" || strings.HasPrefix(model, "mock") {
		return nil, "", false
	}
	switch {
	case strings.HasPrefix(model, "gpt-4o"), strings.HasPrefix(model, "gpt-4.1"), strings.HasPrefix(model, "gpt-4.5"):
		encoding, err := tiktoken.GetEncoding(tiktoken.MODEL_O200K_BASE)
		return encoding, TiktokenO200KTokenEstimator, err == nil
	case strings.HasPrefix(model, "gpt-"), strings.HasPrefix(model, "deepseek"):
		encoding, err := tiktoken.GetEncoding(tiktoken.MODEL_CL100K_BASE)
		return encoding, TiktokenCL100KTokenEstimator, err == nil
	default:
		if encoding, err := tiktoken.EncodingForModel(model); err == nil {
			return encoding, TiktokenModelTokenEstimator, true
		}
		return nil, "", false
	}
}
