package modelprovider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func TestNewOpenAICompatibleChatModelBuildsEinoModel(t *testing.T) {
	chatModel, err := NewOpenAICompatibleChatModel(context.Background(), OpenAICompatibleProviderConfig{
		BaseURL: "http://example.test",
		APIKey:  "secret",
		Model:   "test-model",
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("new chat model: %v", err)
	}
	if chatModel == nil {
		t.Fatal("expected chat model")
	}
	var _ model.ToolCallingChatModel = chatModel
}

func TestNewOpenAICompatibleChatModelValidatesConfig(t *testing.T) {
	cases := []OpenAICompatibleProviderConfig{
		{APIKey: "key", Model: "model"},
		{BaseURL: "http://example.test", Model: "model"},
		{BaseURL: "http://example.test", APIKey: "key"},
		{BaseURL: "http://example.test/v1/chat/completions", APIKey: "key", Model: "model"},
		{BaseURL: "ftp://example.test", APIKey: "key", Model: "model"},
	}
	for _, tc := range cases {
		if _, err := NewOpenAICompatibleChatModel(context.Background(), tc); err == nil {
			t.Fatalf("config %#v returned nil error", tc)
		}
	}
}

func TestOpenAICompatibleProviderClassifiesDeepSeekErrorsAndRedactsAPIKey(t *testing.T) {
	apiKey := "sk-test-secret"
	cases := []struct {
		status int
		class  ErrorClass
	}{
		{status: http.StatusUnauthorized, class: ErrorClassAuthError},
		{status: http.StatusPaymentRequired, class: ErrorClassBillingError},
		{status: http.StatusTooManyRequests, class: ErrorClassRateLimited},
		{status: http.StatusServiceUnavailable, class: ErrorClassProviderUnavailable},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			var attempts int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts++
				if r.Header.Get("Authorization") != "Bearer "+apiKey {
					t.Fatalf("authorization header = %q", r.Header.Get("Authorization"))
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"error":{"message":"provider rejected ` + apiKey + `"}}`))
			}))
			defer server.Close()

			chatModel, err := NewOpenAICompatibleChatModel(context.Background(), OpenAICompatibleProviderConfig{
				BaseURL: server.URL,
				APIKey:  apiKey,
				Model:   "deepseek-v4-flash",
				RetryPolicy: RetryPolicy{
					MaxAttempts: 1,
					BaseDelay:   time.Nanosecond,
					MaxDelay:    time.Nanosecond,
				},
			})
			if err != nil {
				t.Fatalf("new chat model: %v", err)
			}
			_, err = chatModel.Generate(context.Background(), []*schema.Message{schema.UserMessage("hello")})
			if err == nil {
				t.Fatal("expected provider error")
			}
			classified := ClassifyProviderError(err)
			if classified == nil || classified.Class != tc.class || classified.StatusCode != tc.status {
				t.Fatalf("classified = %#v, want class %s status %d", classified, tc.class, tc.status)
			}
			if strings.Contains(err.Error(), apiKey) || strings.Contains(err.Error(), "Bearer "+apiKey) {
				t.Fatalf("error leaked api key: %s", err)
			}
			if attempts != 1 {
				t.Fatalf("attempts = %d, want 1", attempts)
			}
		})
	}
}

func TestOpenAICompatibleProviderRetriesTransientErrorsAndCollectsUsage(t *testing.T) {
	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts == 1 {
			http.Error(w, "temporary outage", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-test",
			"object":"chat.completion",
			"created":1710000000,
			"model":"deepseek-v4-flash",
			"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":9,"completion_tokens":4,"total_tokens":13}
		}`))
	}))
	defer server.Close()

	chatModel, err := NewOpenAICompatibleChatModel(context.Background(), OpenAICompatibleProviderConfig{
		BaseURL: server.URL,
		APIKey:  "sk-test-secret",
		Model:   "deepseek-v4-flash",
		RetryPolicy: RetryPolicy{
			MaxAttempts: 2,
			BaseDelay:   time.Nanosecond,
			MaxDelay:    time.Nanosecond,
		},
	})
	if err != nil {
		t.Fatalf("new chat model: %v", err)
	}
	msg, err := chatModel.Generate(context.Background(), []*schema.Message{schema.UserMessage("hello")})
	if err != nil {
		t.Fatalf("generate after retry: %v", err)
	}
	if msg.Content != "ok" {
		t.Fatalf("content = %q, want ok", msg.Content)
	}
	reporter, ok := chatModel.(UsageReporter)
	if !ok {
		t.Fatalf("chat model %T does not report usage", chatModel)
	}
	usage := reporter.UsageSnapshot()
	if usage.InputTokens != 9 || usage.OutputTokens != 4 || usage.TotalTokens != 13 || usage.RetryCount != 1 {
		t.Fatalf("usage = %#v, want real usage with one retry", usage)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestMockChatModelReturnsToolCallAndObservation(t *testing.T) {
	chatModel := MockChatModel{ToolCalls: []schema.ToolCall{{
		ID:   "call_cli_exec",
		Type: "function",
		Function: schema.FunctionCall{
			Name:      "cli_exec",
			Arguments: `{"command":["echo","hello"]}`,
		},
	}}}

	msg, err := chatModel.Generate(context.Background(), []*schema.Message{schema.UserMessage("hello")})
	if err != nil {
		t.Fatalf("generate tool call: %v", err)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Function.Name != "cli_exec" {
		t.Fatalf("tool calls = %#v", msg.ToolCalls)
	}

	msg, err = chatModel.Generate(context.Background(), []*schema.Message{
		schema.UserMessage("hello"),
		schema.AssistantMessage("", msg.ToolCalls),
		{Role: schema.Tool, ToolCallID: "call_cli_exec", ToolName: "cli_exec", Content: "hello"},
	})
	if err != nil {
		t.Fatalf("generate final: %v", err)
	}
	if !strings.Contains(msg.Content, "hello") {
		t.Fatalf("content = %q, want tool observation", msg.Content)
	}
}
