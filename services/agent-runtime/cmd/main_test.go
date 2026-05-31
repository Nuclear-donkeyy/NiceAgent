package main

import (
	"io"
	"log/slog"
	"testing"

	"niceagent/agent-runtime/internal/config"
	"niceagent/agent-runtime/internal/runtime"
)

func TestModelProviderFromEnvDefaultsToMock(t *testing.T) {
	t.Setenv("MODEL_PROVIDER", "")
	provider, err := modelProviderFromEnv(config.FromEnv(), discardLogger())
	if err != nil {
		t.Fatalf("model provider: %v", err)
	}
	if _, ok := provider.(runtime.MockProvider); !ok {
		t.Fatalf("provider = %T, want MockProvider", provider)
	}
}

func TestModelProviderFromEnvBuildsOpenAICompatibleProvider(t *testing.T) {
	t.Setenv("MODEL_PROVIDER", "openai-compatible")
	t.Setenv("MODEL_BASE_URL", "http://example.test/")
	t.Setenv("MODEL_API_KEY", "secret")
	t.Setenv("MODEL_NAME", "model")
	t.Setenv("MODEL_TIMEOUT_SECONDS", "9")

	provider, err := modelProviderFromEnv(config.FromEnv(), discardLogger())
	if err != nil {
		t.Fatalf("model provider: %v", err)
	}
	openAIProvider, ok := provider.(*runtime.OpenAICompatibleProvider)
	if !ok {
		t.Fatalf("provider = %T, want OpenAICompatibleProvider", provider)
	}
	if openAIProvider.BaseURL != "http://example.test" || openAIProvider.Model != "model" {
		t.Fatalf("provider config = %#v", openAIProvider)
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
