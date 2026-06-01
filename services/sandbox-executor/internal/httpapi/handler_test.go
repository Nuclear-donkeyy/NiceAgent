package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"niceagent/common/platform"
	"niceagent/common/protocol"
)

func TestHandlerPropagatesRequestAndTraceHeaders(t *testing.T) {
	handler := NewHandler(fakeExecutor{}, "")
	body, _ := json.Marshal(protocol.SandboxCommand{
		RunID:       "run-1",
		WorkspaceID: "ws-1",
		Command:     []string{"echo", "hello"},
	})
	request := httptest.NewRequest(http.MethodPost, "/internal/sandbox/exec", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Request-ID", "req-sandbox-1")
	request.Header.Set("X-Trace-ID", "trace-sandbox-1")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("sandbox status = %d, body = %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("X-Request-ID"); got != "req-sandbox-1" {
		t.Fatalf("request id response header = %q", got)
	}
	if got := response.Header().Get("X-Trace-ID"); got != "trace-sandbox-1" {
		t.Fatalf("trace id response header = %q", got)
	}
}

func TestHealthIncludesExecutorModeAndContainerPolicy(t *testing.T) {
	handler := NewHandlerWithOptions(fakeExecutor{}, "", HandlerOptions{
		Health: HealthInfo{
			ExecutorMode:           "container",
			ContainerImage:         "alpine:3.20",
			ContainerAllowedImages: []string{"alpine:3.20", "busybox:1.36"},
		},
	})
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("health status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload struct {
		Status                 string   `json:"status"`
		ExecutorMode           string   `json:"executor_mode"`
		ContainerImage         string   `json:"container_image"`
		ContainerAllowedImages []string `json:"container_allowed_images"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if payload.Status != "ok" || payload.ExecutorMode != "container" || payload.ContainerImage != "alpine:3.20" {
		t.Fatalf("health payload = %#v", payload)
	}
	if len(payload.ContainerAllowedImages) != 2 || payload.ContainerAllowedImages[1] != "busybox:1.36" {
		t.Fatalf("allowed images = %#v", payload.ContainerAllowedImages)
	}
}

type fakeExecutor struct{}

func (fakeExecutor) Execute(ctx context.Context, request protocol.SandboxCommand) protocol.SandboxResult {
	return protocol.SandboxResult{
		RunID:     request.RunID,
		ExitCode:  0,
		Stdout:    platform.RequestIDFromContext(ctx),
		Stderr:    platform.TraceIDFromContext(ctx),
		Artifacts: nil,
	}
}
