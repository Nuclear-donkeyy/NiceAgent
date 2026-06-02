package platform

import "testing"

func TestEstimateTextTokensUsesModelTokenizer(t *testing.T) {
	tests := []struct {
		name      string
		model     string
		estimator string
	}{
		{name: "gpt 4o", model: "gpt-4o", estimator: TiktokenO200KTokenEstimator},
		{name: "openai o series", model: "o3-mini", estimator: TiktokenO200KTokenEstimator},
		{name: "gpt 5 family", model: "gpt-5-chat", estimator: TiktokenO200KTokenEstimator},
		{name: "deepseek openai compatible", model: "deepseek-chat", estimator: TiktokenCL100KTokenEstimator},
		{name: "qwen openai compatible", model: "qwen-max", estimator: TiktokenCL100KTokenEstimator},
		{name: "moonshot kimi compatible", model: "kimi-k2", estimator: TiktokenCL100KTokenEstimator},
		{name: "doubao compatible", model: "doubao-pro-32k", estimator: TiktokenCL100KTokenEstimator},
		{name: "glm compatible", model: "glm-4.5", estimator: TiktokenCL100KTokenEstimator},
		{name: "mistral compatible", model: "mistral-large", estimator: TiktokenCL100KTokenEstimator},
		{name: "llama compatible", model: "llama-3.3-70b", estimator: TiktokenCL100KTokenEstimator},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			estimate := EstimateTextTokensForModel("hello world", tt.model)
			if estimate.Tokens != 2 {
				t.Fatalf("%s tokens = %d, want 2", tt.model, estimate.Tokens)
			}
			if estimate.Estimator != tt.estimator {
				t.Fatalf("%s estimator = %q, want %q", tt.model, estimate.Estimator, tt.estimator)
			}
		})
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
