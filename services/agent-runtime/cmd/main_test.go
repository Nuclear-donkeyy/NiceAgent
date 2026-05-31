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
