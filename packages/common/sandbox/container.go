package sandbox

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"niceagent/common/protocol"
)

type ContainerExecutor struct {
	Image           string
	DockerBinary    string
	CPUs            string
	Memory          string
	PidsLimit       int
	ReadOnlyRootFS  bool
	DropAllCaps     bool
	NoNewPrivileges bool
	Tmpfs           string
	FallbackToLocal bool
	Local           *Executor
}

func NewContainerExecutor(image string) *ContainerExecutor {
	if image == "" {
		image = "alpine:3.20"
	}
	return &ContainerExecutor{
		Image:           image,
		DockerBinary:    "docker",
		CPUs:            "1",
		Memory:          "512m",
		PidsLimit:       128,
		ReadOnlyRootFS:  true,
		DropAllCaps:     true,
		NoNewPrivileges: true,
		Tmpfs:           "/tmp:rw,noexec,nosuid,size=64m",
		FallbackToLocal: true,
		Local:           NewExecutor(),
	}
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
		if errors.Is(err, ErrDangerousCommand) {
			result.Reason = "command is blocked by the system CLI read-only policy"
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
	before, err := e.Local.snapshotOutput(workspace, request)
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

	args := e.dockerArgs(workspace, request)
	cmd := exec.CommandContext(cmdCtx, e.dockerBinary(), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if e.FallbackToLocal && shouldFallbackToLocal(err, stderr.String()) {
		return e.Local.Execute(ctx, request)
	}
	result.Stdout, result.Truncated = truncate(stdout.String(), e.Local.outputLimit(request))
	var stderrTruncated bool
	result.Stderr, stderrTruncated = truncate(stderr.String(), e.Local.outputLimit(request))
	result.Truncated = result.Truncated || stderrTruncated
	artifacts, diff, scanErr := e.Local.collectArtifacts(workspace, before, request)
	result.Artifacts = artifacts
	result.WorkspaceDiff = diff
	result.Duration = time.Since(start).String()
	if cmdCtx.Err() != nil {
		result.ExitCode = -1
		result.Error = cmdCtx.Err().Error()
		return result
	}
	if scanErr != nil {
		result.ExitCode = -1
		result.Error = scanErr.Error()
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

func (e *ContainerExecutor) dockerArgs(workspace string, request protocol.SandboxCommand) []string {
	args := []string{"run", "--rm"}
	if e.CPUs != "" {
		args = append(args, "--cpus", e.CPUs)
	}
	if e.Memory != "" {
		args = append(args, "--memory", e.Memory)
	}
	if e.PidsLimit > 0 {
		args = append(args, "--pids-limit", strconv.Itoa(e.PidsLimit))
	}
	if e.ReadOnlyRootFS {
		args = append(args, "--read-only")
	}
	if e.DropAllCaps {
		args = append(args, "--cap-drop", "ALL")
	}
	if e.NoNewPrivileges {
		args = append(args, "--security-opt", "no-new-privileges")
	}
	if e.Tmpfs != "" {
		args = append(args, "--tmpfs", e.Tmpfs)
	}
	args = append(args, "-v", workspace+":/workspace", "-w", "/workspace")
	if !request.Network {
		args = append(args, "--network", "none")
	}
	args = append(args, e.Image)
	args = append(args, request.Command...)
	return args
}

func (e *ContainerExecutor) dockerBinary() string {
	if strings.TrimSpace(e.DockerBinary) == "" {
		return "docker"
	}
	return e.DockerBinary
}

func shouldFallbackToLocal(err error, stderr string) bool {
	if err == nil {
		return false
	}
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return true
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 125 {
		return true
	}
	stderr = strings.ToLower(stderr)
	return strings.Contains(stderr, "cannot connect to the docker daemon") ||
		strings.Contains(stderr, "docker daemon") ||
		strings.Contains(stderr, "executable file not found")
}
