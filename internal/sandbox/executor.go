package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"niceagent/internal/protocol"
)

type Executor struct {
	AllowedCommands []string
	Dangerous       []string
	WorkspaceRoot   string
	MaxOutputBytes  int
}

func NewExecutor() *Executor {
	return &Executor{
		AllowedCommands: []string{"echo", "pwd", "ls", "date"},
		Dangerous:       []string{"rm", "sudo", "chmod", "chown", "curl", "wget", "ssh"},
		WorkspaceRoot:   "workspaces",
		MaxOutputBytes:  64 * 1024,
	}
}

func (e *Executor) Execute(ctx context.Context, request protocol.SandboxCommand) protocol.SandboxResult {
	start := time.Now()
	result := protocol.SandboxResult{RunID: request.RunID}
	if len(request.Command) == 0 {
		result.ExitCode = -1
		result.Error = "empty command"
		result.Duration = time.Since(start).String()
		return result
	}
	if err := e.validate(request); err != nil {
		result.ExitCode = -1
		result.Error = err.Error()
		result.ApprovalRequired = errors.Is(err, ErrApprovalRequired)
		result.Duration = time.Since(start).String()
		return result
	}
	timeout := time.Duration(request.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, request.Command[0], request.Command[1:]...)
	if dir, err := e.workspaceDir(request); err == nil {
		cmd.Dir = dir
	} else {
		result.ExitCode = -1
		result.Error = err.Error()
		result.Duration = time.Since(start).String()
		return result
	}
	cmd.Env = filteredEnv(request.Env)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result.Stdout, result.Truncated = truncate(stdout.String(), e.outputLimit(request))
	var stderrTruncated bool
	result.Stderr, stderrTruncated = truncate(stderr.String(), e.outputLimit(request))
	result.Truncated = result.Truncated || stderrTruncated
	result.Duration = time.Since(start).String()
	if cmdCtx.Err() != nil {
		result.ExitCode = -1
		result.Error = cmdCtx.Err().Error()
		return result
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = -1
		}
		result.Error = err.Error()
		return result
	}
	result.ExitCode = 0
	return result
}

func (e *Executor) validate(request protocol.SandboxCommand) error {
	name := request.Command[0]
	if strings.Contains(name, "/") {
		return errors.New("absolute or relative command paths are not allowed in the local executor")
	}
	if slices.Contains(e.Dangerous, name) {
		return fmt.Errorf("%w: command requires explicit approval", ErrApprovalRequired)
	}
	if !slices.Contains(e.AllowedCommands, name) {
		return errors.New("command is not allowed by the local executor policy")
	}
	if request.Network {
		return errors.New("network access is disabled by default")
	}
	return nil
}

var ErrApprovalRequired = errors.New("approval required")

func (e *Executor) workspaceDir(request protocol.SandboxCommand) (string, error) {
	root := request.WorkspaceRoot
	if root == "" {
		root = e.WorkspaceRoot
	}
	if root == "" {
		root = "workspaces"
	}
	if request.WorkspaceID == "" {
		return "", errors.New("workspace_id is required")
	}
	dir := filepath.Clean(filepath.Join(root, request.WorkspaceID))
	rootClean := filepath.Clean(root)
	if dir != rootClean && !strings.HasPrefix(dir, rootClean+string(os.PathSeparator)) {
		return "", errors.New("workspace path escapes workspace root")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func (e *Executor) outputLimit(request protocol.SandboxCommand) int {
	if request.MaxOutputBytes > 0 {
		return request.MaxOutputBytes
	}
	if e.MaxOutputBytes > 0 {
		return e.MaxOutputBytes
	}
	return 64 * 1024
}

func truncate(value string, limit int) (string, bool) {
	if limit <= 0 || len(value) <= limit {
		return value, false
	}
	return value[:limit] + "\n[output truncated]", true
}

func filteredEnv(input map[string]string) []string {
	env := []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "HOME=/tmp"}
	for key, value := range input {
		if key == "" || strings.ContainsAny(key, "=\x00") || strings.HasPrefix(key, "SECRET_") {
			continue
		}
		env = append(env, key+"="+value)
	}
	return env
}
