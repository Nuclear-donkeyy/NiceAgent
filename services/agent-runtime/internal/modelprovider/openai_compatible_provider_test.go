package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"niceagent/common/protocol"
)

func TestOpenAICompatibleProviderStreamsTokens(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %q, want /v1/chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("authorization = %q, want bearer token", got)
		}
		var input openAIChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if input.Model != "test-model" || !input.Stream {
			t.Fatalf("request = %#v, want model and stream=true", input)
		}
		if len(input.Messages) != 2 || input.Messages[0].Role != "system" || input.Messages[1].Content != "hello" {
			t.Fatalf("messages = %#v", input.Messages)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(OpenAICompatibleProviderConfig{
		BaseURL: server.URL,
		APIKey:  "secret",
		Model:   "test-model",
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	chunks, err := provider.Stream(context.Background(), ModelRequest{
		Messages: []protocol.Message{
			{Role: protocol.RoleSystem, Content: "be brief"},
			{Role: protocol.RoleUser, Content: "hello"},
		},
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	var builder strings.Builder
	done := false
	for chunk := range chunks {
		if chunk.Error != nil {
			t.Fatalf("unexpected chunk error: %v", chunk.Error)
		}
		builder.WriteString(chunk.Text)
		if chunk.Done {
			done = true
		}
	}
	if builder.String() != "hello" || !done {
		t.Fatalf("stream output = %q done=%v, want hello and done", builder.String(), done)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
}

func TestOpenAICompatibleProviderSurfacesHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad key", http.StatusUnauthorized)
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(OpenAICompatibleProviderConfig{
		BaseURL: server.URL,
		APIKey:  "secret",
		Model:   "test-model",
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	_, err = provider.Stream(context.Background(), ModelRequest{Messages: []protocol.Message{{Role: protocol.RoleUser, Content: "hello"}}})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("error = %v, want http status", err)
	}
}

func TestOpenAICompatibleProviderSurfacesInvalidStreamJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {not-json}\n\n"))
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(OpenAICompatibleProviderConfig{
		BaseURL: server.URL,
		APIKey:  "secret",
		Model:   "test-model",
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	chunks, err := provider.Stream(context.Background(), ModelRequest{Messages: []protocol.Message{{Role: protocol.RoleUser, Content: "hello"}}})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	var gotErr error
	for chunk := range chunks {
		if chunk.Error != nil {
			gotErr = chunk.Error
		}
	}
	if gotErr == nil || !strings.Contains(gotErr.Error(), "decode openai-compatible stream chunk") {
		t.Fatalf("stream error = %v, want decode error", gotErr)
	}
}

func TestNewOpenAICompatibleProviderValidatesConfig(t *testing.T) {
	cases := []OpenAICompatibleProviderConfig{
		{APIKey: "key", Model: "model"},
		{BaseURL: "http://example.test", Model: "model"},
		{BaseURL: "http://example.test", APIKey: "key"},
	}
	for _, tc := range cases {
		if _, err := NewOpenAICompatibleProvider(tc); err == nil {
			t.Fatalf("config %#v returned nil error", tc)
		}
	}
}
