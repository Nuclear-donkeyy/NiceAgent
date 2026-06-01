package platform

const HeuristicRuneDiv4TokenEstimator = "heuristic_rune_div4"

type TokenEstimate struct {
	Tokens    int
	Estimator string
}

func EstimateTextTokens(text string) TokenEstimate {
	if text == "" {
		return TokenEstimate{Estimator: HeuristicRuneDiv4TokenEstimator}
	}
	return TokenEstimate{
		Tokens:    len([]rune(text))/4 + 1,
		Estimator: HeuristicRuneDiv4TokenEstimator,
	}
}
