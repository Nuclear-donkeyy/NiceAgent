package runtime

import (
	"context"
	"testing"

	"niceagent/internal/protocol"
	"niceagent/internal/sandbox"
)

func TestEngineEmitsCompletion(t *testing.T) {
	sink := &recordingSink{}
	engine := NewEngine(sandbox.NewExecutor())

	result := engine.Execute(context.Background(), protocol.RunRequest{
		RunID:       "run-1",
		ChatID:      "chat-1",
		UserID:      "user-1",
		WorkspaceID: "ws-1",
		SkillIDs:    []string{"cli.exec"},
		ModelPolicy: "mock",
	}, "hello", sink)

	if result.Status != protocol.RunSucceeded {
		t.Fatalf("status = %q, want succeeded", result.Status)
	}
	if sink.completed == "" {
		t.Fatal("expected completed content")
	}
	if len(sink.events) == 0 {
		t.Fatal("expected events")
	}
}

func TestEngineRunsAllowedCLICommand(t *testing.T) {
	sink := &recordingSink{}
	engine := NewEngine(sandbox.NewExecutor())

	result := engine.Execute(context.Background(), protocol.RunRequest{
		RunID:       "run-2",
		ChatID:      "chat-1",
		UserID:      "user-1",
		WorkspaceID: "ws-1",
		SkillIDs:    []string{"cli.exec"},
		ModelPolicy: "mock",
	}, "/cli echo hello", sink)

	if result.Status != protocol.RunSucceeded {
		t.Fatalf("status = %q, want succeeded", result.Status)
	}
	if !sink.saw(protocol.EventToolOutput) {
		t.Fatal("expected tool output event")
	}
}

type recordingSink struct {
	events    []protocol.RunEventType
	completed string
	failed    string
	canceled  bool
}

func (s *recordingSink) Emit(_ string, typ protocol.RunEventType, _ string, _ any) error {
	s.events = append(s.events, typ)
	return nil
}

func (s *recordingSink) Complete(_ string, content string) error {
	s.completed = content
	return nil
}

func (s *recordingSink) Fail(_ string, message string) error {
	s.failed = message
	return nil
}

func (s *recordingSink) IsCanceled(string) bool {
	return s.canceled
}

func (s *recordingSink) saw(typ protocol.RunEventType) bool {
	for _, event := range s.events {
		if event == typ {
			return true
		}
	}
	return false
}

