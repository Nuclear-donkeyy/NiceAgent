package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"niceagent/agent-runtime/internal/tools"
	"niceagent/common/protocol"
)

func TestHandlerHealthIncludesModelProviderHealth(t *testing.T) {
	handler := NewHandlerWithOptions(fakeEngine{}, "http://control-plane.local", "", HandlerOptions{
		RuntimeID:   "runtime-test",
		ModelHealth: fakeHealthReporter{},
	})

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("health status = %d, body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"model_provider"`) || !strings.Contains(response.Body.String(), `"provider":"fake"`) {
		t.Fatalf("health body = %s", response.Body.String())
	}
}

func TestHandlerRecordsModelMetricsAfterRun(t *testing.T) {
	handler := NewHandlerWithOptions(fakeEngine{}, "http://control-plane.local", "", HandlerOptions{
		RuntimeID:   "runtime-test",
		ModelHealth: fakeHealthReporter{},
	})
	body, _ := json.Marshal(protocol.RunExecutionRequest{
		Request:     protocol.RunRequest{RunID: "run-1", ChatID: "chat-1", UserID: "user-1"},
		UserMessage: "hello",
	})
	execute := httptest.NewRequest(http.MethodPost, "/internal/runs/execute", bytes.NewReader(body))
	execute.Header.Set("Content-Type", "application/json")
	executeResponse := httptest.NewRecorder()
	handler.ServeHTTP(executeResponse, execute)
	if executeResponse.Code != http.StatusOK {
		t.Fatalf("execute status = %d, body = %s", executeResponse.Code, executeResponse.Body.String())
	}

	metricsResponse := httptest.NewRecorder()
	handler.ServeHTTP(metricsResponse, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	metrics := metricsResponse.Body.String()
	if !strings.Contains(metrics, "niceagent_model_runs_total") || !strings.Contains(metrics, `provider="fake-provider"`) {
		t.Fatalf("metrics body = %s", metrics)
	}
}

func TestHandlerPropagatesRequestIDHeader(t *testing.T) {
	handler := NewHandlerWithOptions(fakeEngine{}, "http://control-plane.local", "", HandlerOptions{
		RuntimeID: "runtime-test",
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	body, _ := json.Marshal(protocol.RunExecutionRequest{
		Request:     protocol.RunRequest{RunID: "run-1", ChatID: "chat-1", UserID: "user-1"},
		UserMessage: "hello",
	})
	request := httptest.NewRequest(http.MethodPost, "/internal/runs/execute", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Request-ID", "req-runtime-1")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("execute status = %d, body = %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("X-Request-ID"); got != "req-runtime-1" {
		t.Fatalf("request id response header = %q", got)
	}
	if got := response.Header().Get("X-Trace-ID"); got == "" {
		t.Fatal("expected trace id response header")
	}
}

type fakeEngine struct{}

func (fakeEngine) Execute(context.Context, protocol.RunRequest, string, tools.EventSink) protocol.RunResult {
	return protocol.RunResult{
		RunID:  "run-1",
		Status: protocol.RunSucceeded,
		Usage: protocol.RunUsage{
			Provider:      "fake-provider",
			Model:         "fake-model",
			InputTokens:   3,
			OutputTokens:  4,
			LatencyMillis: 25,
		},
	}
}

type fakeHealthReporter struct{}

func (fakeHealthReporter) ModelProviderHealth() protocol.ModelProviderHealth {
	return protocol.ModelProviderHealth{Provider: "fake", Model: "fake-model", Status: "healthy"}
}
