package sink

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"niceagent/common/protocol"

	"go.opentelemetry.io/otel/trace"
)

func TestControlPlaneSinkWritesEventsCompleteFailAndReadsCancel(t *testing.T) {
	var sawEvent bool
	var sawClaim bool
	var sawListArtifacts bool
	var sawReadArtifact bool
	var sawComplete bool
	var sawCompleteUsage bool
	var sawFail bool
	var sawQuotaReserve bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Header.Get("X-Trace-ID") != "trace-sink-1" {
			t.Fatalf("trace id = %q", r.Header.Get("X-Trace-ID"))
		}
		switch r.URL.Path {
		case "/internal/runs/run-1/events":
			var request protocol.RunEventWriteRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode event: %v", err)
			}
			if request.Type != protocol.EventRunStarted || request.Message != "started" || request.AttemptID != "attempt-1" {
				t.Fatalf("event request = %#v", request)
			}
			sawEvent = true
			w.WriteHeader(http.StatusCreated)
		case "/internal/runs/run-1/claim":
			var request protocol.RunClaimRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode claim: %v", err)
			}
			if request.AttemptID != "attempt-1" || request.ClaimedBy != "runtime-a" {
				t.Fatalf("claim request = %#v", request)
			}
			sawClaim = true
			_ = json.NewEncoder(w).Encode(protocol.RunClaimResponse{Run: protocol.Run{ID: "run-1", Status: protocol.RunRunning}})
		case "/internal/runs/run-1/complete":
			var request protocol.RunCompleteRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode complete: %v", err)
			}
			if request.AttemptID != "attempt-1" {
				t.Fatalf("complete attempt = %q", request.AttemptID)
			}
			switch request.Content {
			case "done":
				if len(request.Artifacts) != 1 || request.Artifacts[0].Path != "output/report.txt" {
					t.Fatalf("complete artifacts = %#v", request.Artifacts)
				}
			case "done with usage":
				if request.Usage.Provider != "openai-compatible" || request.Usage.InputTokens != 12 || request.TokenUsage.OutputTokens != 7 {
					t.Fatalf("complete usage request = %#v", request)
				}
				if len(request.Artifacts) != 1 || request.Artifacts[0].Path != "output/usage.txt" {
					t.Fatalf("complete usage artifacts = %#v", request.Artifacts)
				}
				sawCompleteUsage = true
			default:
				t.Fatalf("complete content = %q", request.Content)
			}
			sawComplete = true
			w.WriteHeader(http.StatusOK)
		case "/internal/runs/run-1/fail":
			var request protocol.RunFailRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode fail: %v", err)
			}
			if request.Error != "bad" || request.AttemptID != "attempt-1" {
				t.Fatalf("fail error = %q", request.Error)
			}
			sawFail = true
			w.WriteHeader(http.StatusOK)
		case "/internal/runs/run-1/status":
			_ = json.NewEncoder(w).Encode(protocol.RunStatusResponse{
				Run: protocol.Run{ID: "run-1", Status: protocol.RunCanceled},
			})
		case "/internal/runs/run-1/quota-reserve":
			var request protocol.ToolQuotaReserveRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode quota reserve: %v", err)
			}
			if request.AttemptID != "attempt-1" || request.SkillID != "cli.exec" || request.ToolCalls != 1 || request.SandboxSeconds != 10 {
				t.Fatalf("quota reserve request = %#v", request)
			}
			sawQuotaReserve = true
			_ = json.NewEncoder(w).Encode(protocol.ToolQuotaReserveResponse{Allowed: true})
		case "/internal/runs/run-1/artifacts":
			if r.URL.Query().Get("attempt_id") != "attempt-1" {
				t.Fatalf("artifact list query = %q", r.URL.RawQuery)
			}
			sawListArtifacts = true
			_ = json.NewEncoder(w).Encode(protocol.ArtifactListResponse{Artifacts: []protocol.Artifact{{ID: "art-1", RunID: "run-1", Path: "output/report.txt"}}})
		case "/internal/artifacts/art-1/content":
			if r.URL.Query().Get("run_id") != "run-1" || r.URL.Query().Get("attempt_id") != "attempt-1" || r.URL.Query().Get("max_bytes") != "32" {
				t.Fatalf("artifact content query = %q", r.URL.RawQuery)
			}
			sawReadArtifact = true
			_ = json.NewEncoder(w).Encode(protocol.ArtifactTextResponse{Artifact: protocol.Artifact{ID: "art-1"}, Content: "report"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	sink := NewControlPlaneSink(server.URL, "secret").WithAttempt("attempt-1").WithTraceID("trace-sink-1")
	if run, err := sink.Claim("run-1", "attempt-1", "runtime-a", 60); err != nil || run.ID != "run-1" {
		t.Fatalf("claim run = %#v err=%v", run, err)
	}
	if err := sink.Emit("run-1", protocol.EventRunStarted, "started", nil); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if err := sink.Complete("run-1", "done", protocol.Artifact{Path: "output/report.txt"}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if err := sink.CompleteWithUsage("run-1", "done with usage", protocol.RunUsage{
		Provider:     "openai-compatible",
		Model:        "deepseek-v4-flash",
		InputTokens:  12,
		OutputTokens: 7,
	}, protocol.Artifact{Path: "output/usage.txt"}); err != nil {
		t.Fatalf("complete with usage: %v", err)
	}
	if err := sink.Fail("run-1", "bad"); err != nil {
		t.Fatalf("fail: %v", err)
	}
	if !sink.IsCanceled("run-1") {
		t.Fatal("expected canceled status")
	}
	if response, err := sink.ReserveToolQuota(context.Background(), "run-1", protocol.ToolQuotaReserveRequest{SkillID: "cli.exec", ToolCalls: 1, SandboxSeconds: 10}); err != nil || !response.Allowed {
		t.Fatalf("reserve tool quota = %#v err=%v", response, err)
	}
	artifacts, err := sink.ListArtifacts(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("list artifacts: %v", err)
	}
	if len(artifacts) != 1 || artifacts[0].ID != "art-1" {
		t.Fatalf("artifacts = %#v", artifacts)
	}
	text, err := sink.ReadArtifactText(context.Background(), "run-1", "art-1", 32)
	if err != nil {
		t.Fatalf("read artifact text: %v", err)
	}
	if text.Content != "report" {
		t.Fatalf("artifact text = %#v", text)
	}
	if !sawClaim || !sawEvent || !sawListArtifacts || !sawReadArtifact || !sawComplete || !sawCompleteUsage || !sawFail || !sawQuotaReserve {
		t.Fatalf("saw claim=%v event=%v list=%v read=%v complete=%v complete_usage=%v fail=%v quota=%v", sawClaim, sawEvent, sawListArtifacts, sawReadArtifact, sawComplete, sawCompleteUsage, sawFail, sawQuotaReserve)
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

func TestControlPlaneSinkPropagatesTraceparentFromTraceContext(t *testing.T) {
	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatal(err)
	}
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatal(err)
	}
	traceContext := trace.ContextWithRemoteSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	}))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Trace-ID"); got != traceID.String() {
			t.Fatalf("X-Trace-ID = %q", got)
		}
		if got := r.Header.Get("Traceparent"); !strings.Contains(got, traceID.String()) {
			t.Fatalf("Traceparent = %q", got)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	sink := NewControlPlaneSink(server.URL, "").WithTraceContext(traceContext)
	if err := sink.Emit("run-1", protocol.EventRunStarted, "started", nil); err != nil {
		t.Fatalf("emit: %v", err)
	}
}
