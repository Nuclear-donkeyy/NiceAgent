package main

import (
	"io"
	"log/slog"
	"testing"

	"niceagent/agent-runtime/internal/config"
	"niceagent/agent-runtime/internal/modelprovider"
)

func TestModelProviderFromEnvDefaultsToMock(t *testing.T) {
	t.Setenv("MODEL_PROVIDER", "")
	provider, err := modelProviderFromEnv(config.FromEnv(), discardLogger())
	if err != nil {
		t.Fatalf("model provider: %v", err)
	}
	if _, ok := provider.(modelprovider.MockChatModel); !ok {
		t.Fatalf("provider = %T, want MockChatModel", provider)
	}
}

func TestModelProviderFromEnvBuildsOpenAICompatibleChatModel(t *testing.T) {
	t.Setenv("MODEL_PROVIDER", "openai-compatible")
	t.Setenv("MODEL_BASE_URL", "http://example.test/")
	t.Setenv("MODEL_API_KEY", "secret")
	t.Setenv("MODEL_NAME", "model")
	t.Setenv("MODEL_TIMEOUT_SECONDS", "9")

	provider, err := modelProviderFromEnv(config.FromEnv(), discardLogger())
	if err != nil {
		t.Fatalf("model provider: %v", err)
	}
	if _, ok := provider.(modelprovider.MockChatModel); ok {
		t.Fatalf("provider = %T, want non-mock OpenAI-compatible Eino model", provider)
	}
}

func TestModelProviderFromEnvBuildsDeepSeekProfile(t *testing.T) {
	t.Setenv("MODEL_PROVIDER_PROFILE", "deepseek")
	t.Setenv("MODEL_API_KEY", "secret")
	t.Setenv("MODEL_NAME", "official-model")

	cfg := config.FromEnv()
	if cfg.ModelProvider != "openai-compatible" {
		t.Fatalf("model provider = %q, want openai-compatible", cfg.ModelProvider)
	}
	if cfg.ModelProviderProfile != "deepseek" {
		t.Fatalf("model profile = %q, want deepseek", cfg.ModelProviderProfile)
	}
	if cfg.ModelBaseURL != "https://api.deepseek.com" {
		t.Fatalf("model base url = %q, want DeepSeek default", cfg.ModelBaseURL)
	}
	provider, err := modelProviderFromEnv(cfg, discardLogger())
	if err != nil {
		t.Fatalf("model provider: %v", err)
	}
	reporter, ok := provider.(modelprovider.UsageReporter)
	if !ok {
		t.Fatalf("provider = %T, want usage reporter", provider)
	}
	if usage := reporter.UsageSnapshot(); usage.Provider != "deepseek" || usage.Model != "official-model" {
		t.Fatalf("usage metadata = %#v, want deepseek profile provider", usage)
	}
}

func TestDeepSeekProfileRejectsConflictingProvider(t *testing.T) {
	t.Setenv("MODEL_PROVIDER_PROFILE", "deepseek")
	t.Setenv("MODEL_PROVIDER", "mock")

	if err := config.FromEnv().Validate(); err == nil {
		t.Fatal("expected deepseek profile to reject mock provider")
	}
}

func TestModelProviderFromEnvBuildsMockFallback(t *testing.T) {
	t.Setenv("MODEL_PROVIDER", "openai-compatible")
	t.Setenv("MODEL_BASE_URL", "http://example.test/")
	t.Setenv("MODEL_API_KEY", "secret")
	t.Setenv("MODEL_NAME", "model")
	t.Setenv("MODEL_FALLBACK_PROVIDER", "mock")

	provider, err := modelProviderFromEnv(config.FromEnv(), discardLogger())
	if err != nil {
		t.Fatalf("model provider: %v", err)
	}
	if _, ok := provider.(*modelprovider.FallbackChatModel); !ok {
		t.Fatalf("provider = %T, want fallback chat model", provider)
	}
}

func TestModelProviderFromEnvBuildsOpenAICompatibleWithRateLimit(t *testing.T) {
	t.Setenv("MODEL_PROVIDER", "openai-compatible")
	t.Setenv("MODEL_BASE_URL", "http://example.test/")
	t.Setenv("MODEL_API_KEY", "secret")
	t.Setenv("MODEL_NAME", "model")
	t.Setenv("MODEL_REQUESTS_PER_MINUTE", "1")

	provider, err := modelProviderFromEnv(config.FromEnv(), discardLogger())
	if err != nil {
		t.Fatalf("model provider: %v", err)
	}
	if _, ok := provider.(modelprovider.UsageReporter); !ok {
		t.Fatalf("provider = %T, want operational provider with usage tracking", provider)
	}
}

func TestModelProviderFromEnvRejectsInvalidOpenAICompatibleConfig(t *testing.T) {
	t.Setenv("MODEL_PROVIDER", "openai-compatible")
	t.Setenv("MODEL_BASE_URL", "")
	t.Setenv("MODEL_API_KEY", "secret")
	t.Setenv("MODEL_NAME", "model")

	if _, err := modelProviderFromEnv(config.FromEnv(), discardLogger()); err == nil {
		t.Fatal("expected missing base url error")
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
