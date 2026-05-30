package sandbox

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"time"

	"niceagent/internal/protocol"
)

type Executor struct {
	AllowedCommands []string
}

func NewExecutor() *Executor {
	return &Executor{AllowedCommands: []string{"echo", "pwd", "ls", "date"}}
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
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result.Stdout = stdout.String()
	result.Stderr = stderr.String()
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
	if !slices.Contains(e.AllowedCommands, name) {
		return errors.New("command is not allowed by the local executor policy")
	}
	if request.Network {
		return errors.New("network access is disabled by default")
	}
	return nil
}

