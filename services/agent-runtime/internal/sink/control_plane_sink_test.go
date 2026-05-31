package sink

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"niceagent/common/protocol"
)

func TestControlPlaneSinkWritesEventsCompleteFailAndReadsCancel(t *testing.T) {
	var sawEvent bool
	var sawComplete bool
	var sawFail bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/internal/runs/run-1/events":
			var request protocol.RunEventWriteRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode event: %v", err)
			}
			if request.Type != protocol.EventRunStarted || request.Message != "started" {
				t.Fatalf("event request = %#v", request)
			}
			sawEvent = true
			w.WriteHeader(http.StatusCreated)
		case "/internal/runs/run-1/complete":
			var request protocol.RunCompleteRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode complete: %v", err)
			}
			if request.Content != "done" {
				t.Fatalf("complete content = %q", request.Content)
			}
			sawComplete = true
			w.WriteHeader(http.StatusOK)
		case "/internal/runs/run-1/fail":
			var request protocol.RunFailRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode fail: %v", err)
			}
			if request.Error != "bad" {
				t.Fatalf("fail error = %q", request.Error)
			}
			sawFail = true
			w.WriteHeader(http.StatusOK)
		case "/internal/runs/run-1/status":
			_ = json.NewEncoder(w).Encode(protocol.RunStatusResponse{
				Run: protocol.Run{ID: "run-1", Status: protocol.RunCanceled},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	sink := NewControlPlaneSink(server.URL, "secret")
	if err := sink.Emit("run-1", protocol.EventRunStarted, "started", nil); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if err := sink.Complete("run-1", "done"); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if err := sink.Fail("run-1", "bad"); err != nil {
		t.Fatalf("fail: %v", err)
	}
	if !sink.IsCanceled("run-1") {
		t.Fatal("expected canceled status")
	}
	if !sawEvent || !sawComplete || !sawFail {
		t.Fatalf("saw event=%v complete=%v fail=%v", sawEvent, sawComplete, sawFail)
	}
}

func TestControlPlaneSinkSurfacesWriteErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer server.Close()

	sink := NewControlPlaneSink(server.URL, "")
	if err := sink.Emit("run-1", protocol.EventRunStarted, "started", nil); err == nil {
		t.Fatal("expected emit error")
	}
	if sink.IsCanceled("run-1") {
		t.Fatal("failed status check should not report canceled")
	}
}
