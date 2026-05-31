package sandbox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"niceagent/common/platform"
	"niceagent/common/protocol"
)

func TestHTTPExecutorCallsSandboxService(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/sandbox/exec" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("X-Trace-ID") != "trace-sandbox-1" {
			t.Fatalf("trace id = %q", r.Header.Get("X-Trace-ID"))
		}
		var request protocol.SandboxCommand
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode command: %v", err)
		}
		if request.RunID != "run-1" || request.Command[0] != "echo" {
			t.Fatalf("request = %#v", request)
		}
		_ = json.NewEncoder(w).Encode(protocol.SandboxResult{
			RunID:    request.RunID,
			ExitCode: 0,
			Stdout:   "hello\n",
		})
	}))
	defer server.Close()

	executor := NewHTTPExecutor(server.URL, "secret")
	result := executor.Execute(platform.ContextWithTraceID(context.Background(), "trace-sandbox-1"), protocol.SandboxCommand{
		RunID:       "run-1",
		WorkspaceID: "ws-1",
		Command:     []string{"echo", "hello"},
	})
	if result.ExitCode != 0 || result.Stdout != "hello\n" || result.Error != "" {
		t.Fatalf("result = %#v", result)
	}
}

func TestHTTPExecutorReturnsStructuredErrorOnHTTPFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no capacity", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	executor := NewHTTPExecutor(server.URL, "")
	result := executor.Execute(context.Background(), protocol.SandboxCommand{
		RunID:       "run-2",
		WorkspaceID: "ws-1",
		Command:     []string{"echo", "hello"},
	})
	if result.RunID != "run-2" || result.ExitCode != -1 || result.Error == "" {
		t.Fatalf("result = %#v, want structured error", result)
	}
}
