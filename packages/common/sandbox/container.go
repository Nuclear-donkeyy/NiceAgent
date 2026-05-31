package sandbox

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"time"

	"niceagent/common/protocol"
)

type ContainerExecutor struct {
	Image string
	Local *Executor
}

func NewContainerExecutor(image string) *ContainerExecutor {
	if image == "" {
		image = "alpine:3.20"
	}
	return &ContainerExecutor{Image: image, Local: NewExecutor()}
}

func (e *ContainerExecutor) Execute(ctx context.Context, request protocol.SandboxCommand) protocol.SandboxResult {
	start := time.Now()
	result := protocol.SandboxResult{RunID: request.RunID, Command: append([]string(nil), request.Command...)}
	if e.Local == nil {
		e.Local = NewExecutor()
	}
	if len(request.Command) == 0 {
		result.ExitCode = -1
		result.Error = "empty command"
		result.Duration = time.Since(start).String()
		return result
	}
	if err := e.Local.validate(request); err != nil {
		result.ExitCode = -1
		result.Error = err.Error()
		result.ApprovalRequired = errors.Is(err, ErrApprovalRequired)
		if result.ApprovalRequired {
			result.Reason = "command requires explicit approval"
			result.Policy = PolicyDangerousCommand
		}
		result.Duration = time.Since(start).String()
		return result
	}
	workspace, err := e.Local.workspaceDir(request)
	if err != nil {
		result.ExitCode = -1
		result.Error = err.Error()
		result.Duration = time.Since(start).String()
		return result
	}
	timeout := time.Duration(request.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := []string{"run", "--rm", "--cpus", "1", "--memory", "512m", "-v", workspace + ":/workspace", "-w", "/workspace"}
	if !request.Network {
		args = append(args, "--network", "none")
	}
	args = append(args, e.Image)
	args = append(args, request.Command...)
	cmd := exec.CommandContext(cmdCtx, "docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	result.Stdout, result.Truncated = truncate(stdout.String(), e.Local.outputLimit(request))
	var stderrTruncated bool
	result.Stderr, stderrTruncated = truncate(stderr.String(), e.Local.outputLimit(request))
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
