package skillmanifest

import (
	"testing"

	"niceagent/common/protocol"
)

func TestValidateJSONDocument(t *testing.T) {
	schema := `{"type":"object","required":["query"],"properties":{"query":{"type":"string"}},"additionalProperties":false}`
	if err := ValidateJSONDocument(schema, `{"query":"weather"}`); err != nil {
		t.Fatalf("valid document rejected: %v", err)
	}
	if err := ValidateJSONDocument(schema, `{"query":42}`); err == nil {
		t.Fatal("invalid document accepted")
	}
}

func TestValidateHTTPSkillInput(t *testing.T) {
	err := ValidateHTTPSkillInput(testHTTPSkillInput("http://127.0.0.1:8080/hook"))
	if err == nil {
		t.Fatal("private http url accepted")
	}

	err = ValidateHTTPSkillInput(testHTTPSkillInput("https://api.example.com/hook"))
	if err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
}

func TestHTTPSkillRuntimeConfigIncludesRetryAndRateLimit(t *testing.T) {
	input := testHTTPSkillInput("https://api.example.com/hook")
	input.RetryMaxAttempts = 3
	input.RateLimitPerMinute = 30

	cfg, err := NewHTTPSkillRuntimeConfig(input)
	if err != nil {
		t.Fatalf("runtime config rejected: %v", err)
	}
	if cfg.Retry.MaxAttempts != 3 || cfg.Retry.BaseDelayMS != DefaultHTTPRetryBaseDelayMS || cfg.Retry.MaxDelayMS != DefaultHTTPRetryMaxDelayMS {
		t.Fatalf("retry config = %#v", cfg.Retry)
	}
	if cfg.RateLimit.RequestsPerMinute != 30 {
		t.Fatalf("rate limit = %#v", cfg.RateLimit)
	}
	parsed, err := ParseHTTPSkillRuntimeConfig(cfg.JSONString())
	if err != nil {
		t.Fatalf("parse runtime config: %v", err)
	}
	if parsed.Retry.MaxAttempts != 3 || parsed.RateLimit.RequestsPerMinute != 30 {
		t.Fatalf("parsed config = %#v", parsed)
	}
}

func TestHTTPSkillRuntimeConfigRejectsInvalidRetryAndRateLimit(t *testing.T) {
	input := testHTTPSkillInput("https://api.example.com/hook")
	input.RetryMaxAttempts = MaxHTTPRetryAttempts + 1
	if err := ValidateHTTPSkillInput(input); err == nil {
		t.Fatal("invalid retry max attempts accepted")
	}

	input = testHTTPSkillInput("https://api.example.com/hook")
	input.RateLimitPerMinute = MaxHTTPRateLimitPerMinute + 1
	if err := ValidateHTTPSkillInput(input); err == nil {
		t.Fatal("invalid rate limit accepted")
	}
}

func testHTTPSkillInput(targetURL string) protocol.HTTPSkillInput {
	return protocol.HTTPSkillInput{
		Name:   "Weather",
		Method: "POST",
		URL:    targetURL,
	}
}
