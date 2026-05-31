package controlplane

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"niceagent/common/protocol"
)

func TestHTTPDispatcherCallsRuntime(t *testing.T) {
	store := NewStore()
	chat := mustCreateChat(t, store, "demo-user", "dispatch")
	_, run, err := store.AddUserMessage(chat.ID, "demo-user", "hello")
	if err != nil {
		t.Fatalf("add user message: %v", err)
	}
	received := make(chan protocol.RunExecutionRequest, 1)
	runtimeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/runs/execute" {
			t.Fatalf("runtime path = %q", r.URL.Path)
		}
		var request protocol.RunExecutionRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode runtime request: %v", err)
		}
		received <- request
		w.WriteHeader(http.StatusOK)
	}))
	defer runtimeServer.Close()

	dispatcher := NewHTTPDispatcher(store, runtimeServer.URL, "http://control-plane.local", "", discardLogger())
	if err := dispatcher.Dispatch(context.Background(), run, "hello"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	select {
	case request := <-received:
		if request.Request.RunID != run.ID {
			t.Fatalf("run id = %q, want %q", request.Request.RunID, run.ID)
		}
		if request.UserMessage != "hello" {
			t.Fatalf("user message = %q, want hello", request.UserMessage)
		}
		if request.ControlPlaneURL != "http://control-plane.local" {
			t.Fatalf("control plane url = %q", request.ControlPlaneURL)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime did not receive dispatch request")
	}
}

func TestHTTPDispatcherMarksRunFailedWhenRuntimeFails(t *testing.T) {
	store := NewStore()
	chat := mustCreateChat(t, store, "demo-user", "dispatch")
	_, run, err := store.AddUserMessage(chat.ID, "demo-user", "hello")
	if err != nil {
		t.Fatalf("add user message: %v", err)
	}
	runtimeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "offline", http.StatusBadGateway)
	}))
	defer runtimeServer.Close()

	dispatcher := NewHTTPDispatcher(store, runtimeServer.URL, "http://control-plane.local", "", discardLogger())
	if err := dispatcher.Dispatch(context.Background(), run, "hello"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	if !eventually(time.Second, func() bool {
		got, err := store.GetRun(run.ID)
		return err == nil && got.Status == protocol.RunFailed
	}) {
		got, err := store.GetRun(run.ID)
		t.Fatalf("run = %#v, err = %v; want failed", got, err)
	}
	events := store.ListEvents(run.ID, 0)
	if len(events) == 0 || events[len(events)-1].Type != protocol.EventRunFailed {
		t.Fatalf("events = %#v, want trailing run.failed", events)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
