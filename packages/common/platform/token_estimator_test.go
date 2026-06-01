package platform

import "testing"

func TestEstimateTextTokensUsesModelTokenizer(t *testing.T) {
	estimate := EstimateTextTokensForModel("hello world", "gpt-4o")
	if estimate.Tokens != 2 {
		t.Fatalf("gpt-4o tokens = %d, want 2", estimate.Tokens)
	}
	if estimate.Estimator != TiktokenO200KTokenEstimator {
		t.Fatalf("gpt-4o estimator = %q, want %q", estimate.Estimator, TiktokenO200KTokenEstimator)
	}

	deepseekEstimate := EstimateTextTokensForModel("hello world", "deepseek-chat")
	if deepseekEstimate.Tokens != 2 {
		t.Fatalf("deepseek-compatible tokens = %d, want 2", deepseekEstimate.Tokens)
	}
	if deepseekEstimate.Estimator != TiktokenCL100KTokenEstimator {
		t.Fatalf("deepseek-compatible estimator = %q, want %q", deepseekEstimate.Estimator, TiktokenCL100KTokenEstimator)
	}
}

func TestEstimateTextTokensFallsBackToHeuristic(t *testing.T) {
	estimate := EstimateTextTokensForModel("hello world", "unknown-model")
	if estimate.Tokens != 3 {
		t.Fatalf("fallback tokens = %d, want 3", estimate.Tokens)
	}
	if estimate.Estimator != HeuristicRuneDiv4TokenEstimator {
		t.Fatalf("fallback estimator = %q, want %q", estimate.Estimator, HeuristicRuneDiv4TokenEstimator)
	}
}
