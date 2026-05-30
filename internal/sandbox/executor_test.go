package sandbox

import (
	"context"
	"strings"
	"testing"

	"niceagent/internal/protocol"
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

func TestExecutorRequiresApprovalForDangerousCommand(t *testing.T) {
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
	if !result.ApprovalRequired {
		t.Fatalf("approval required = false, error = %q", result.Error)
	}
	if !strings.Contains(result.Error, "approval required") {
		t.Fatalf("error = %q, want approval policy error", result.Error)
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

func TestExecutorRejectsNetworkAccess(t *testing.T) {
	executor := NewExecutor()
	result := executor.Execute(context.Background(), protocol.SandboxCommand{
		RunID:         "run-5",
		WorkspaceID:   "ws-1",
		WorkspaceRoot: t.TempDir(),
		Command:       []string{"echo", "hello"},
		Network:       true,
	})

	if result.ExitCode != -1 {
		t.Fatalf("exit code = %d, want -1", result.ExitCode)
	}
	if !strings.Contains(result.Error, "network access is disabled") {
		t.Fatalf("error = %q, want network policy error", result.Error)
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
