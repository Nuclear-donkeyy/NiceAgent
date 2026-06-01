package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	einotool "github.com/cloudwego/eino/components/tool"

	"niceagent/common/protocol"
)

func TestWorkspaceReadListsAndReadsArtifacts(t *testing.T) {
	reader := &fakeWorkspaceReader{
		artifacts: []protocol.Artifact{{
			ID:          "art_1",
			RunID:       "run_1",
			WorkspaceID: "ws_1",
			Path:        "output/report.txt",
			Name:        "report.txt",
			MimeType:    "text/plain",
			SizeBytes:   12,
			CreatedAt:   time.Now().UTC(),
		}},
		text: protocol.ArtifactTextResponse{
			Artifact:  protocol.Artifact{ID: "art_1", Path: "output/report.txt", MimeType: "text/plain"},
			Content:   "report body",
			BytesRead: 11,
		},
	}
	bridge := NewDefaultToolBridge(nil)
	runtimeTools := bridge.BuildTools(protocol.RunRequest{
		RunID:       "run_1",
		WorkspaceID: "ws_1",
		Skills: []protocol.RuntimeSkill{{
			Skill: protocol.Skill{
				ID:      "workspace.read",
				Name:    "Workspace Reader",
				Kind:    protocol.SkillKindBuiltin,
				Scope:   protocol.SkillScopeSystem,
				Enabled: true,
			},
		}},
	}, reader)
	if len(runtimeTools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(runtimeTools))
	}
	workspaceTool := runtimeTools[0].(einotool.InvokableTool)
	listOutput, err := workspaceTool.InvokableRun(context.Background(), `{"action":"list"}`)
	if err != nil {
		t.Fatalf("list artifacts: %v", err)
	}
	var listed struct {
		Artifacts []protocol.Artifact `json:"artifacts"`
		Count     int                 `json:"count"`
	}
	if err := json.Unmarshal([]byte(listOutput), &listed); err != nil {
		t.Fatalf("decode list output: %v", err)
	}
	if listed.Count != 1 || listed.Artifacts[0].ID != "art_1" {
		t.Fatalf("list output = %s", listOutput)
	}

	summaryOutput, err := workspaceTool.InvokableRun(context.Background(), `{"action":"summary"}`)
	if err != nil {
		t.Fatalf("summary artifacts: %v", err)
	}
	var summary struct {
		RunID           string `json:"run_id"`
		WorkspaceID     string `json:"workspace_id"`
		ArtifactSummary struct {
			Count             int            `json:"count"`
			TotalSizeBytes    int64          `json:"total_size_bytes"`
			MimeTypeCounts    map[string]int `json:"mime_type_counts"`
			TextArtifactCount int            `json:"text_artifact_count"`
			Artifacts         []struct {
				ID   string `json:"id"`
				Path string `json:"path"`
			} `json:"artifacts"`
		} `json:"artifact_summary"`
	}
	if err := json.Unmarshal([]byte(summaryOutput), &summary); err != nil {
		t.Fatalf("decode summary output: %v", err)
	}
	if summary.RunID != "run_1" || summary.WorkspaceID != "ws_1" {
		t.Fatalf("summary identity = %#v", summary)
	}
	if summary.ArtifactSummary.Count != 1 || summary.ArtifactSummary.TotalSizeBytes != 12 || summary.ArtifactSummary.MimeTypeCounts["text/plain"] != 1 || summary.ArtifactSummary.TextArtifactCount != 1 {
		t.Fatalf("summary output = %s", summaryOutput)
	}
	if len(summary.ArtifactSummary.Artifacts) != 1 || summary.ArtifactSummary.Artifacts[0].Path != "output/report.txt" {
		t.Fatalf("summary artifacts = %#v", summary.ArtifactSummary.Artifacts)
	}

	readOutput, err := workspaceTool.InvokableRun(context.Background(), `{"action":"read","artifact_id":"art_1","max_bytes":20}`)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	if !strings.Contains(readOutput, "report body") {
		t.Fatalf("read output = %s", readOutput)
	}
	if reader.readMaxBytes != 20 {
		t.Fatalf("read max bytes = %d, want 20", reader.readMaxBytes)
	}
}

func TestWorkspaceReadRequiresReaderAndArtifactID(t *testing.T) {
	bridge := NewDefaultToolBridge(nil)
	runtimeTools := bridge.BuildTools(protocol.RunRequest{
		RunID:       "run_1",
		WorkspaceID: "ws_1",
		Skills: []protocol.RuntimeSkill{{
			Skill: protocol.Skill{ID: "workspace.read", Kind: protocol.SkillKindBuiltin, Scope: protocol.SkillScopeSystem, Enabled: true},
		}},
	}, recordingSink{})
	if _, err := runtimeTools[0].(einotool.InvokableTool).InvokableRun(context.Background(), `{"action":"list"}`); err == nil {
		t.Fatal("expected missing workspace reader error")
	}

	reader := &fakeWorkspaceReader{}
	runtimeTools = bridge.BuildTools(protocol.RunRequest{
		RunID:       "run_1",
		WorkspaceID: "ws_1",
		Skills: []protocol.RuntimeSkill{{
			Skill: protocol.Skill{ID: "workspace.read", Kind: protocol.SkillKindBuiltin, Scope: protocol.SkillScopeSystem, Enabled: true},
		}},
	}, reader)
	if _, err := runtimeTools[0].(einotool.InvokableTool).InvokableRun(context.Background(), `{"action":"read"}`); err == nil {
		t.Fatal("expected missing artifact_id error")
	}
}

func TestToolBridgeStopsExecutionWhenQuotaDenied(t *testing.T) {
	executor := &recordingSandboxExecutor{}
	bridge := NewDefaultToolBridge(executor)
	sink := &quotaRecordingSink{response: protocol.ToolQuotaReserveResponse{
		Allowed: false,
		Message: "tool quota exceeded",
		Quota:   "tool_calls_per_day",
	}}
	runtimeTools := bridge.BuildTools(protocol.RunRequest{
		RunID:       "run_1",
		WorkspaceID: "ws_1",
		Skills: []protocol.RuntimeSkill{{
			Skill: protocol.Skill{ID: "cli.exec", Kind: protocol.SkillKindBuiltin, Scope: protocol.SkillScopeSystem, Enabled: true},
		}},
	}, sink)

	output, err := runtimeTools[0].(einotool.InvokableTool).InvokableRun(context.Background(), `{"command":["echo","hello"]}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if executor.called {
		t.Fatal("sandbox executor should not run when quota is denied")
	}
	if !strings.Contains(output, "quota_denied") || !strings.Contains(output, "tool quota exceeded") {
		t.Fatalf("quota output = %s", output)
	}
	if sink.request.SkillID != "cli.exec" || sink.request.ToolCalls != 1 || sink.request.SandboxSeconds != 10 {
		t.Fatalf("quota request = %#v", sink.request)
	}
}

func TestToolBridgeRegistersSandboxArtifactsIncrementally(t *testing.T) {
	executor := &recordingSandboxExecutor{
		result: protocol.SandboxResult{
			RunID:    "run_1",
			ExitCode: 0,
			Stdout:   "created",
			Artifacts: []protocol.Artifact{{
				Path:      "output/report.txt",
				Name:      "report.txt",
				MimeType:  "text/plain",
				SizeBytes: 7,
			}},
		},
	}
	bridge := NewDefaultToolBridge(executor)
	sink := &artifactRecordingSink{}
	runtimeTools := bridge.BuildTools(protocol.RunRequest{
		RunID:       "run_1",
		WorkspaceID: "ws_1",
		Skills: []protocol.RuntimeSkill{{
			Skill: protocol.Skill{ID: "cli.exec", Kind: protocol.SkillKindBuiltin, Scope: protocol.SkillScopeSystem, Enabled: true},
		}},
	}, sink)

	output, err := runtimeTools[0].(einotool.InvokableTool).InvokableRun(context.Background(), `{"command":["sh","-c","echo created > output/report.txt"]}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if len(sink.registered) != 1 || sink.registered[0].Path != "output/report.txt" {
		t.Fatalf("registered artifacts = %#v", sink.registered)
	}
	var result protocol.SandboxResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if len(result.Artifacts) != 1 || result.Artifacts[0].ID != "art_live" {
		t.Fatalf("output artifacts = %#v", result.Artifacts)
	}
}

type fakeWorkspaceReader struct {
	artifacts    []protocol.Artifact
	text         protocol.ArtifactTextResponse
	readMaxBytes int
}

func (r *fakeWorkspaceReader) Emit(string, protocol.RunEventType, string, any) error { return nil }
func (r *fakeWorkspaceReader) Complete(string, string, ...protocol.Artifact) error   { return nil }
func (r *fakeWorkspaceReader) Fail(string, string) error                             { return nil }
func (r *fakeWorkspaceReader) IsCanceled(string) bool                                { return false }

func (r *fakeWorkspaceReader) ListArtifacts(context.Context, string) ([]protocol.Artifact, error) {
	return r.artifacts, nil
}

func (r *fakeWorkspaceReader) ReadArtifactText(_ context.Context, _, _ string, maxBytes int) (protocol.ArtifactTextResponse, error) {
	r.readMaxBytes = maxBytes
	return r.text, nil
}

type recordingSink struct{}

func (recordingSink) Emit(string, protocol.RunEventType, string, any) error { return nil }
func (recordingSink) Complete(string, string, ...protocol.Artifact) error   { return nil }
func (recordingSink) Fail(string, string) error                             { return nil }
func (recordingSink) IsCanceled(string) bool                                { return false }

type quotaRecordingSink struct {
	recordingSink
	request  protocol.ToolQuotaReserveRequest
	response protocol.ToolQuotaReserveResponse
	err      error
}

func (s *quotaRecordingSink) ReserveToolQuota(_ context.Context, _ string, input protocol.ToolQuotaReserveRequest) (protocol.ToolQuotaReserveResponse, error) {
	s.request = input
	return s.response, s.err
}

type artifactRecordingSink struct {
	recordingSink
	registered []protocol.Artifact
}

func (s *artifactRecordingSink) RegisterArtifacts(_ context.Context, _ string, artifacts []protocol.Artifact) ([]protocol.Artifact, error) {
	s.registered = append([]protocol.Artifact(nil), artifacts...)
	saved := make([]protocol.Artifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		artifact.ID = "art_live"
		artifact.RunID = "run_1"
		saved = append(saved, artifact)
	}
	return saved, nil
}

type recordingSandboxExecutor struct {
	called bool
	result protocol.SandboxResult
}

func (e *recordingSandboxExecutor) Execute(context.Context, protocol.SandboxCommand) protocol.SandboxResult {
	e.called = true
	if e.result.RunID != "" || len(e.result.Command) > 0 || len(e.result.Artifacts) > 0 {
		return e.result
	}
	return protocol.SandboxResult{RunID: "run_1", ExitCode: 0, Stdout: "hello"}
}
