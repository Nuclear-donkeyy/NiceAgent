package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"niceagent/agent-runtime/internal/modelprovider"
	"niceagent/common/platform"
	"niceagent/common/protocol"
	"niceagent/common/sandbox"
)

func TestEngineEmitsCompletionForPlainMessage(t *testing.T) {
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
	if !sink.saw(protocol.EventRunStarted) || !sink.saw(protocol.EventModelToken) {
		t.Fatalf("events = %v, want run started and model token events", sink.events)
	}
	if !strings.Contains(sink.completed, "远端 agent") {
		t.Fatalf("completed content = %q, want ordinary runtime reply", sink.completed)
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
	if !sink.saw(protocol.EventToolStarted) || !sink.saw(protocol.EventToolFinished) {
		t.Fatalf("events = %v, want tool started/output/finished", sink.events)
	}
	if !strings.Contains(sink.completed, "hello") {
		t.Fatalf("completed content = %q, want CLI stdout", sink.completed)
	}
}

func TestEngineCompletesWithSandboxArtifacts(t *testing.T) {
	sink := &recordingSink{}
	artifact := protocol.Artifact{
		ID:          "art-1",
		RunID:       "run-art",
		WorkspaceID: "ws-1",
		Path:        "output/report.txt",
		Name:        "report.txt",
		MimeType:    "text/plain",
		SizeBytes:   6,
	}
	engine := NewEngine(fakeSandbox{result: protocol.SandboxResult{
		RunID:     "run-art",
		ExitCode:  0,
		Stdout:    "created report",
		Artifacts: []protocol.Artifact{artifact},
	}})

	result := engine.Execute(context.Background(), protocol.RunRequest{
		RunID:       "run-art",
		ChatID:      "chat-1",
		UserID:      "user-1",
		WorkspaceID: "ws-1",
		SkillIDs:    []string{"cli.exec"},
		ModelPolicy: "mock",
	}, "/cli echo hello", sink)

	if result.Status != protocol.RunSucceeded {
		t.Fatalf("status = %q, want succeeded", result.Status)
	}
	if len(result.Artifacts) != 1 || result.Artifacts[0].Path != "output/report.txt" {
		t.Fatalf("result artifacts = %#v", result.Artifacts)
	}
	if len(sink.artifacts) != 1 || sink.artifacts[0].Path != "output/report.txt" {
		t.Fatalf("sink artifacts = %#v", sink.artifacts)
	}
}

func TestEngineReturnsPolicyErrorForDangerousCLICommand(t *testing.T) {
	sink := &recordingSink{}
	engine := NewEngine(sandbox.NewExecutor())

	result := engine.Execute(context.Background(), protocol.RunRequest{
		RunID:       "run-3",
		ChatID:      "chat-1",
		UserID:      "user-1",
		WorkspaceID: "ws-1",
		SkillIDs:    []string{"cli.exec"},
		ModelPolicy: "mock",
	}, "/cli rm -rf /", sink)

	if result.Status != protocol.RunSucceeded {
		t.Fatalf("status = %q, want succeeded with policy response", result.Status)
	}
	if sink.saw(protocol.EventApprovalNeeded) {
		t.Fatalf("events = %v, want no approval-needed event for system CLI", sink.events)
	}
	if !sink.saw(protocol.EventToolOutput) {
		t.Fatalf("events = %v, want ordinary tool output for policy result", sink.events)
	}
	if !strings.Contains(sink.completed, "系统 CLI 策略拒绝") {
		t.Fatalf("completed content = %q, want policy refusal", sink.completed)
	}
}

func TestEngineFailsWhenSandboxMissingForCLI(t *testing.T) {
	sink := &recordingSink{}
	engine := NewEngine(nil)

	result := engine.Execute(context.Background(), protocol.RunRequest{
		RunID:       "run-4",
		ChatID:      "chat-1",
		UserID:      "user-1",
		WorkspaceID: "ws-1",
		SkillIDs:    []string{"cli.exec"},
		ModelPolicy: "mock",
	}, "/cli echo hello", sink)

	if result.Status != protocol.RunFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if sink.failed == "" {
		t.Fatal("expected failure to be reported to sink")
	}
	if sink.completed != "" {
		t.Fatalf("completed content = %q, want no completion after failure", sink.completed)
	}
}

func TestEngineRejectsCLIWhenSkillNotAvailable(t *testing.T) {
	sink := &recordingSink{}
	engine := NewEngine(sandbox.NewExecutor())

	result := engine.Execute(context.Background(), protocol.RunRequest{
		RunID:       "run-no-cli",
		ChatID:      "chat-1",
		UserID:      "user-1",
		WorkspaceID: "ws-1",
		SkillIDs:    []string{"workspace.read"},
		ModelPolicy: "mock",
	}, "/cli echo hello", sink)

	if result.Status != protocol.RunFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if !strings.Contains(result.Error, "未启用系统 CLI 工具") {
		t.Fatalf("error = %q, want missing skill message", result.Error)
	}
	if sink.completed != "" {
		t.Fatalf("completed content = %q, want no completion", sink.completed)
	}
}

func TestEngineHonorsCancellation(t *testing.T) {
	sink := &recordingSink{cancelAfterTokens: 1}
	engine := NewEngine(sandbox.NewExecutor())

	result := engine.Execute(context.Background(), protocol.RunRequest{
		RunID:       "run-5",
		ChatID:      "chat-1",
		UserID:      "user-1",
		WorkspaceID: "ws-1",
		SkillIDs:    []string{"cli.exec"},
		ModelPolicy: "mock",
	}, "please stop", sink)

	if result.Status != protocol.RunCanceled {
		t.Fatalf("status = %q, want canceled", result.Status)
	}
	if sink.completed != "" {
		t.Fatalf("completed content = %q, want no completion after cancellation", sink.completed)
	}
}

func TestEngineHonorsCancellationBeforeExecution(t *testing.T) {
	sink := &recordingSink{canceled: true}
	engine := NewEngine(sandbox.NewExecutor())

	result := engine.Execute(context.Background(), protocol.RunRequest{
		RunID:       "run-6",
		ChatID:      "chat-1",
		UserID:      "user-1",
		WorkspaceID: "ws-1",
		SkillIDs:    []string{"cli.exec"},
		ModelPolicy: "mock",
	}, "hello", sink)

	if result.Status != protocol.RunCanceled {
		t.Fatalf("status = %q, want canceled", result.Status)
	}
	if sink.completed != "" {
		t.Fatalf("completed content = %q, want no completion", sink.completed)
	}
}

func TestEngineUsesConfiguredModelProvider(t *testing.T) {
	sink := &recordingSink{}
	engine := NewEngine(sandbox.NewExecutor())
	engine.Models = modelprovider.MockProvider{Response: "来自配置模型的回复"}

	result := engine.Execute(context.Background(), protocol.RunRequest{
		RunID:       "run-7",
		ChatID:      "chat-1",
		UserID:      "user-1",
		WorkspaceID: "ws-1",
		SkillIDs:    []string{"cli.exec"},
		ModelPolicy: "configured",
	}, "hello", sink)

	if result.Status != protocol.RunSucceeded {
		t.Fatalf("status = %q, want succeeded", result.Status)
	}
	if !strings.Contains(sink.completed, "来自配置模型的回复") {
		t.Fatalf("completed content = %q, want configured provider response", sink.completed)
	}
}

func TestEnginePrefersRealModelUsageAndFallsBackToEstimate(t *testing.T) {
	realUsageSink := &recordingSink{}
	realUsageEngine := NewEngine(sandbox.NewExecutor())
	realUsageEngine.Models = modelprovider.MockProvider{
		Response: "usage aware response",
		Usage: &schema.TokenUsage{
			PromptTokens:     11,
			CompletionTokens: 13,
			TotalTokens:      24,
		},
	}

	result := realUsageEngine.Execute(context.Background(), protocol.RunRequest{
		RunID:       "run-real-usage",
		ChatID:      "chat-1",
		UserID:      "user-1",
		WorkspaceID: "ws-1",
		ModelPolicy: "mock-real",
	}, "hello", realUsageSink)
	if result.Status != protocol.RunSucceeded {
		t.Fatalf("status = %q, want succeeded", result.Status)
	}
	if result.Usage.Estimated || result.Usage.InputTokens != 11 || result.Usage.OutputTokens != 13 {
		t.Fatalf("usage = %#v, want real provider usage", result.Usage)
	}
	if result.Usage.TokenEstimator != "" {
		t.Fatalf("usage token estimator = %q, want empty for real provider usage", result.Usage.TokenEstimator)
	}
	if realUsageSink.usage.InputTokens != 11 || realUsageSink.usage.OutputTokens != 13 {
		t.Fatalf("sink usage = %#v, want real usage", realUsageSink.usage)
	}

	estimateSink := &recordingSink{}
	estimateEngine := NewEngine(sandbox.NewExecutor())
	result = estimateEngine.Execute(context.Background(), protocol.RunRequest{
		RunID:       "run-estimate-usage",
		ChatID:      "chat-1",
		UserID:      "user-1",
		WorkspaceID: "ws-1",
		ModelPolicy: "mock-estimate",
	}, "hello", estimateSink)
	if result.Status != protocol.RunSucceeded {
		t.Fatalf("status = %q, want succeeded", result.Status)
	}
	if !result.Usage.Estimated || result.Usage.InputTokens == 0 || result.Usage.OutputTokens == 0 {
		t.Fatalf("usage = %#v, want estimated token fallback", result.Usage)
	}
	if result.Usage.TokenEstimator != platform.HeuristicRuneDiv4TokenEstimator {
		t.Fatalf("token estimator = %q, want %q", result.Usage.TokenEstimator, platform.HeuristicRuneDiv4TokenEstimator)
	}
}

type recordingSink struct {
	events            []protocol.RunEventType
	payloads          []any
	completed         string
	artifacts         []protocol.Artifact
	usage             protocol.RunUsage
	failed            string
	canceled          bool
	tokenEvents       int
	cancelAfterTokens int
}

func (s *recordingSink) Emit(_ string, typ protocol.RunEventType, _ string, payload any) error {
	s.events = append(s.events, typ)
	s.payloads = append(s.payloads, payload)
	if typ == protocol.EventModelToken {
		s.tokenEvents++
		if s.cancelAfterTokens > 0 && s.tokenEvents >= s.cancelAfterTokens {
			s.canceled = true
		}
	}
	return nil
}

func (s *recordingSink) Complete(_ string, content string, artifacts ...protocol.Artifact) error {
	s.completed = content
	s.artifacts = artifacts
	return nil
}

func (s *recordingSink) CompleteWithUsage(_ string, content string, usage protocol.RunUsage, artifacts ...protocol.Artifact) error {
	s.completed = content
	s.usage = usage
	s.artifacts = artifacts
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

func (s *recordingSink) lastPayload(typ protocol.RunEventType) any {
	for i := len(s.events) - 1; i >= 0; i-- {
		if s.events[i] == typ {
			return s.payloads[i]
		}
	}
	return nil
}

type fakeSandbox struct {
	result protocol.SandboxResult
}

func (f fakeSandbox) Execute(context.Context, protocol.SandboxCommand) protocol.SandboxResult {
	return f.result
}
