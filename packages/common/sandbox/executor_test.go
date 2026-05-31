package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"niceagent/common/protocol"
)

func TestExecutorRunsAllowedCommand(t *testing.T) {
	executor := NewExecutor()
	result := executor.Execute(context.Background(), protocol.SandboxCommand{
		RunID:          "run-1",
		WorkspaceID:    "ws-1",
		WorkspaceRoot:  t.TempDir(),
		Command:        []string{"echo", "hello"},
		TimeoutSeconds: 10,
	})

	if result.ExitCode != 0 {
		t.Fatalf("exit code = %d, error = %q, want 0", result.ExitCode, result.Error)
	}
	if strings.TrimSpace(result.Stdout) != "hello" {
		t.Fatalf("stdout = %q, want hello", result.Stdout)
	}
	if result.RunID != "run-1" {
		t.Fatalf("run id = %q, want run-1", result.RunID)
	}
}

func TestExecutorRejectsDangerousCommandWithoutApproval(t *testing.T) {
	executor := NewExecutor()
	result := executor.Execute(context.Background(), protocol.SandboxCommand{
		RunID:         "run-2",
		WorkspaceID:   "ws-1",
		WorkspaceRoot: t.TempDir(),
		Command:       []string{"rm", "-rf", "/"},
	})

	if result.ExitCode != -1 {
		t.Fatalf("exit code = %d, want -1", result.ExitCode)
	}
	if result.ApprovalRequired {
		t.Fatalf("approval required = true, error = %q", result.Error)
	}
	if result.Policy != PolicyDangerousCommand {
		t.Fatalf("policy = %q, want %q", result.Policy, PolicyDangerousCommand)
	}
	if !strings.Contains(result.Reason, "read-only policy") {
		t.Fatalf("reason = %q, want read-only policy reason", result.Reason)
	}
	if strings.Join(result.Command, " ") != "rm -rf /" {
		t.Fatalf("command = %v, want rm -rf /", result.Command)
	}
}

func TestExecutorRejectsDisallowedCommand(t *testing.T) {
	executor := NewExecutor()
	result := executor.Execute(context.Background(), protocol.SandboxCommand{
		RunID:         "run-3",
		WorkspaceID:   "ws-1",
		WorkspaceRoot: t.TempDir(),
		Command:       []string{"python", "--version"},
	})

	if result.ExitCode != -1 {
		t.Fatalf("exit code = %d, want -1", result.ExitCode)
	}
	if !strings.Contains(result.Error, "not allowed") {
		t.Fatalf("error = %q, want allowlist policy error", result.Error)
	}
	if result.ApprovalRequired {
		t.Fatal("disallowed command should not require approval")
	}
}

func TestExecutorRejectsCommandPath(t *testing.T) {
	executor := NewExecutor()
	result := executor.Execute(context.Background(), protocol.SandboxCommand{
		RunID:         "run-4",
		WorkspaceID:   "ws-1",
		WorkspaceRoot: t.TempDir(),
		Command:       []string{"/bin/echo", "hello"},
	})

	if result.ExitCode != -1 {
		t.Fatalf("exit code = %d, want -1", result.ExitCode)
	}
	if !strings.Contains(result.Error, "paths are not allowed") {
		t.Fatalf("error = %q, want path policy error", result.Error)
	}
}

func TestExecutorAllowsNetworkFlagForReadOnlyCommands(t *testing.T) {
	executor := NewExecutor()
	result := executor.Execute(context.Background(), protocol.SandboxCommand{
		RunID:         "run-5",
		WorkspaceID:   "ws-1",
		WorkspaceRoot: t.TempDir(),
		Command:       []string{"echo", "hello"},
		Network:       true,
	})

	if result.ExitCode != 0 {
		t.Fatalf("exit code = %d, error = %q, want 0", result.ExitCode, result.Error)
	}
}

func TestExecutorHandlesEmptyCommand(t *testing.T) {
	executor := NewExecutor()
	result := executor.Execute(context.Background(), protocol.SandboxCommand{
		RunID:         "run-6",
		WorkspaceID:   "ws-1",
		WorkspaceRoot: t.TempDir(),
	})

	if result.ExitCode != -1 {
		t.Fatalf("exit code = %d, want -1", result.ExitCode)
	}
	if result.Error != "empty command" {
		t.Fatalf("error = %q, want empty command", result.Error)
	}
}

func TestExecutorTruncatesLargeOutput(t *testing.T) {
	executor := NewExecutor()
	result := executor.Execute(context.Background(), protocol.SandboxCommand{
		RunID:          "run-7",
		WorkspaceID:    "ws-1",
		WorkspaceRoot:  t.TempDir(),
		Command:        []string{"echo", "abcdef"},
		TimeoutSeconds: 10,
		MaxOutputBytes: 3,
	})

	if result.ExitCode != 0 {
		t.Fatalf("exit code = %d, error = %q, want 0", result.ExitCode, result.Error)
	}
	if !result.Truncated {
		t.Fatalf("truncated = false, stdout = %q", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "[output truncated]") {
		t.Fatalf("stdout = %q, want truncation marker", result.Stdout)
	}
}

func TestExecutorReturnsArtifactMetadataForOutputFiles(t *testing.T) {
	executor := NewExecutor()
	executor.AllowedCommands = append(executor.AllowedCommands, "sh")
	executor.Dangerous = []string{"rm", "sudo"}
	root := t.TempDir()

	result := executor.Execute(context.Background(), protocol.SandboxCommand{
		RunID:          "run-artifact",
		WorkspaceID:    "ws-artifact",
		WorkspaceRoot:  root,
		Command:        []string{"sh", "-c", "printf 'report' > output/report.txt"},
		TimeoutSeconds: 10,
	})

	if result.ExitCode != 0 {
		t.Fatalf("exit code = %d, error = %q, stderr = %q", result.ExitCode, result.Error, result.Stderr)
	}
	if len(result.Artifacts) != 1 {
		t.Fatalf("artifacts len = %d, want 1, result = %#v", len(result.Artifacts), result)
	}
	artifact := result.Artifacts[0]
	if artifact.RunID != "run-artifact" || artifact.WorkspaceID != "ws-artifact" {
		t.Fatalf("artifact run/workspace = %q/%q", artifact.RunID, artifact.WorkspaceID)
	}
	if artifact.Path != "output/report.txt" || artifact.Name != "report.txt" {
		t.Fatalf("artifact path/name = %q/%q", artifact.Path, artifact.Name)
	}
	if artifact.SizeBytes != 6 || artifact.SHA256 == "" || artifact.StorageBackend != "local" {
		t.Fatalf("artifact metadata = %#v", artifact)
	}
	if len(result.WorkspaceDiff.Created) != 1 || result.WorkspaceDiff.Created[0] != "output/report.txt" {
		t.Fatalf("workspace diff = %#v", result.WorkspaceDiff)
	}
	if _, err := os.Stat(filepath.Join(root, "ws-artifact", "output", "report.txt")); err != nil {
		t.Fatalf("artifact file missing: %v", err)
	}
}

func TestExecutorRejectsEscapingWorkspaceID(t *testing.T) {
	executor := NewExecutor()
	result := executor.Execute(context.Background(), protocol.SandboxCommand{
		RunID:         "run-escape",
		WorkspaceID:   "../escape",
		WorkspaceRoot: t.TempDir(),
		Command:       []string{"echo", "hello"},
	})

	if result.ExitCode != -1 {
		t.Fatalf("exit code = %d, want -1", result.ExitCode)
	}
	if !strings.Contains(result.Error, "workspace_id") {
		t.Fatalf("error = %q, want workspace_id policy error", result.Error)
	}
}

func TestExecutorRejectsSymlinkArtifactOutput(t *testing.T) {
	executor := NewExecutor()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "outside"), 0o755); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "ws-symlink"), 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "outside"), filepath.Join(root, "ws-symlink", "output")); err != nil {
		t.Fatalf("symlink output: %v", err)
	}

	result := executor.Execute(context.Background(), protocol.SandboxCommand{
		RunID:         "run-symlink",
		WorkspaceID:   "ws-symlink",
		WorkspaceRoot: root,
		Command:       []string{"echo", "hello"},
	})

	if result.ExitCode != -1 {
		t.Fatalf("exit code = %d, want -1", result.ExitCode)
	}
	if !strings.Contains(result.Error, "symlink") {
		t.Fatalf("error = %q, want symlink policy error", result.Error)
	}
}
