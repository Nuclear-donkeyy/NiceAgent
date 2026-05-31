package engine

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"niceagent/agent-runtime/internal/modelprovider"
	"niceagent/agent-runtime/internal/tools"
	"niceagent/common/protocol"
	"niceagent/common/sandbox"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestEinoAgentEngineRunsCLIToolLoop(t *testing.T) {
	sink := &recordingSink{}
	engine := NewEinoAgentEngine(sandbox.NewExecutor())

	result := engine.Execute(context.Background(), protocol.RunRequest{
		RunID:       "run-eino-cli",
		ChatID:      "chat-1",
		UserID:      "user-1",
		WorkspaceID: "ws-1",
		SkillIDs:    []string{"cli.exec"},
		Skills: []protocol.RuntimeSkill{{
			Skill: protocol.Skill{
				ID:          "cli.exec",
				Name:        "System CLI",
				Description: "Fetch external information",
				Scope:       protocol.SkillScopeSystem,
				Kind:        protocol.SkillKindBuiltin,
				Enabled:     true,
			},
		}},
		ModelPolicy: "mock",
	}, "/cli echo hello", sink)

	if result.Status != protocol.RunSucceeded {
		t.Fatalf("status = %q, want succeeded, error = %s", result.Status, result.Error)
	}
	if !sink.saw(protocol.EventToolStarted) || !sink.saw(protocol.EventToolOutput) || !sink.saw(protocol.EventToolFinished) {
		t.Fatalf("events = %v, want tool events", sink.events)
	}
	if !strings.Contains(sink.completed, "hello") {
		t.Fatalf("completed content = %q, want tool observation", sink.completed)
	}
	if result.Usage.ToolCalls != 1 || result.Usage.SandboxCommands != 1 || result.Usage.SandboxOutputBytes == 0 {
		t.Fatalf("usage = %#v, want one sandbox tool call with output bytes", result.Usage)
	}
	if sink.usage.ToolCalls != 1 || sink.usage.SandboxCommands != 1 {
		t.Fatalf("sink usage = %#v, want persisted tool usage", sink.usage)
	}
}

func TestEinoAgentEngineUsesUserHTTPSkill(t *testing.T) {
	sink := &recordingSink{}
	engine := NewEinoAgentEngine(nil)
	engine.Models = modelprovider.MockChatModel{ToolCalls: []schema.ToolCall{{
		ID:   "call_skill_weather",
		Type: "function",
		Function: schema.FunctionCall{
			Name:      "skill_weather",
			Arguments: `{"query":"weather"}`,
		},
	}}}
	engine.Tools = tools.NewDefaultToolBridge(nil)
	engine.Tools.Client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("Authorization") != "Bearer secret-token" {
			t.Fatalf("authorization header = %q, want bearer token", req.Header.Get("Authorization"))
		}
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			Header:     make(http.Header),
		}, nil
	})}

	result := engine.Execute(context.Background(), protocol.RunRequest{
		RunID:       "run-eino-http",
		ChatID:      "chat-1",
		UserID:      "user-1",
		WorkspaceID: "ws-1",
		SkillIDs:    []string{"skill_weather"},
		Skills: []protocol.RuntimeSkill{{
			Skill: protocol.Skill{
				ID:            "skill_weather",
				Name:          "Weather API",
				Description:   "Fetch weather",
				Scope:         protocol.SkillScopeUser,
				Kind:          protocol.SkillKindHTTP,
				Enabled:       true,
				RuntimeConfig: `{"method":"POST","url":"https://example.com/weather","auth_type":"bearer"}`,
				InputSchema:   `{"type":"object","additionalProperties":true}`,
			},
			Secrets: map[string]string{"bearer_token": "secret-token"},
		}},
		ModelPolicy: "mock",
	}, "调用 skill_weather", sink)

	if result.Status != protocol.RunSucceeded {
		t.Fatalf("status = %q, want succeeded, error = %s", result.Status, result.Error)
	}
	if !sink.saw(protocol.EventToolOutput) {
		t.Fatalf("events = %v, want tool output", sink.events)
	}
	if !strings.Contains(sink.completed, `"ok":true`) {
		t.Fatalf("completed content = %q, want http response", sink.completed)
	}
	if result.Usage.ToolCalls != 1 || result.Usage.SandboxCommands != 0 {
		t.Fatalf("usage = %#v, want one non-sandbox tool call", result.Usage)
	}
}

func TestEinoAgentEngineRejectsUnavailableModelToolCall(t *testing.T) {
	sink := &recordingSink{}
	engine := NewEinoAgentEngine(nil)
	engine.Models = modelprovider.MockChatModel{ToolCalls: []schema.ToolCall{{
		ID:   "call_unknown",
		Type: "function",
		Function: schema.FunctionCall{
			Name:      "unknown_tool",
			Arguments: `{}`,
		},
	}}}

	result := engine.Execute(context.Background(), protocol.RunRequest{
		RunID:       "run-eino-unknown-tool",
		ChatID:      "chat-1",
		UserID:      "user-1",
		WorkspaceID: "ws-1",
		SkillIDs:    []string{"cli.exec"},
		Skills: []protocol.RuntimeSkill{{
			Skill: protocol.Skill{
				ID:          "cli.exec",
				Name:        "System CLI",
				Description: "Fetch external information",
				Scope:       protocol.SkillScopeSystem,
				Kind:        protocol.SkillKindBuiltin,
				Enabled:     true,
			},
		}},
		ModelPolicy: "mock",
	}, "please call a tool", sink)

	if result.Status != protocol.RunFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if !strings.Contains(result.Error, "unknown_tool") {
		t.Fatalf("error = %q, want unavailable tool name", result.Error)
	}
	if sink.completed != "" {
		t.Fatalf("completed content = %q, want no completion", sink.completed)
	}
}

func TestToolUsageCollectorSkipsQuotaReservationFailures(t *testing.T) {
	collector := &toolUsageCollector{}
	collector.ObserveToolObservation(`{"ok":false,"error_type":"quota_denied","message":"limit"}`)
	collector.ObserveToolObservation(`{"ok":false,"error_type":"quota_unavailable","message":"backend"}`)
	if usage := collector.Snapshot(); usage.ToolCalls != 0 || usage.SandboxCommands != 0 {
		t.Fatalf("usage = %#v, want quota reservation observations skipped", usage)
	}
	collector.ObserveToolObservation(`{"ok":false,"error_type":"invalid_arguments","message":"bad"}`)
	if usage := collector.Snapshot(); usage.ToolCalls != 1 {
		t.Fatalf("usage = %#v, want normal tool observation counted", usage)
	}
}
