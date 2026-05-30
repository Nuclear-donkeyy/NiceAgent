package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"niceagent/common/protocol"
)

type EventSink interface {
	Emit(runID string, typ protocol.RunEventType, message string, payload any) error
	Complete(runID string, content string) error
	Fail(runID string, message string) error
	IsCanceled(runID string) bool
}

type Engine struct {
	Sandbox SandboxExecutor
	Models  ModelProvider
	Tools   ToolBridge
	Limits  LoopLimits
}

type LoopLimits struct {
	MaxSteps int
	Timeout  time.Duration
}

func NewEngine(executor SandboxExecutor) *Engine {
	return &Engine{
		Sandbox: executor,
		Models:  MockProvider{},
		Limits:  LoopLimits{MaxSteps: 8, Timeout: 2 * time.Minute},
	}
}

func (e *Engine) Execute(ctx context.Context, req protocol.RunRequest, userMessage string, sink EventSink) protocol.RunResult {
	if e.Limits.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.Limits.Timeout)
		defer cancel()
	}
	if err := sink.Emit(req.RunID, protocol.EventRunStarted, "Agent runtime accepted the run.", map[string]any{
		"workspace_id": req.WorkspaceID,
		"model_policy": req.ModelPolicy,
	}); err != nil {
		return failed(req.RunID, err)
	}
	if sink.IsCanceled(req.RunID) {
		_ = sink.Fail(req.RunID, "run canceled before execution")
		return protocol.RunResult{RunID: req.RunID, Status: protocol.RunCanceled}
	}

	answer := strings.Builder{}
	writeToken := func(token string) {
		answer.WriteString(token)
		_ = sink.Emit(req.RunID, protocol.EventModelToken, token, nil)
		time.Sleep(35 * time.Millisecond)
	}

	if err := e.streamModel(ctx, req, userMessage, writeToken); err != nil {
		_ = sink.Fail(req.RunID, err.Error())
		return protocol.RunResult{RunID: req.RunID, Status: protocol.RunFailed, Error: err.Error()}
	}
	if sink.IsCanceled(req.RunID) {
		return protocol.RunResult{RunID: req.RunID, Status: protocol.RunCanceled}
	}

	if command, ok := parseCLICommand(userMessage); ok {
		if e.Sandbox == nil {
			_ = sink.Fail(req.RunID, "sandbox executor is not configured")
			return protocol.RunResult{RunID: req.RunID, Status: protocol.RunFailed, Error: "sandbox executor is not configured"}
		}
		_ = sink.Emit(req.RunID, protocol.EventToolStarted, "Starting cli.exec skill.", map[string]any{"command": command})
		result := e.Sandbox.Execute(ctx, protocol.SandboxCommand{
			RunID:          req.RunID,
			WorkspaceID:    req.WorkspaceID,
			Command:        command,
			TimeoutSeconds: 10,
			Network:        false,
		})
		_ = sink.Emit(req.RunID, protocol.EventToolOutput, "CLI command completed.", result)
		_ = sink.Emit(req.RunID, protocol.EventToolFinished, "Finished cli.exec skill.", map[string]any{"exit_code": result.ExitCode})
		writeToken("\n\nCLI 执行结果：\n")
		if result.Stdout != "" {
			writeToken(result.Stdout)
		}
		if result.Stderr != "" {
			writeToken(result.Stderr)
		}
		if result.Error != "" {
			writeToken("执行错误: " + result.Error)
		}
		if sink.IsCanceled(req.RunID) {
			return protocol.RunResult{RunID: req.RunID, Status: protocol.RunCanceled}
		}
	} else {
		_ = sink.Emit(req.RunID, protocol.EventToolStarted, "Selecting available skills.", map[string]any{"skills": req.SkillIDs})
		_ = sink.Emit(req.RunID, protocol.EventToolFinished, "No tool call required for this turn.", nil)
		writeToken("\n\n你可以发送 `/cli echo hello` 来测试远端 CLI skill 的事件链路。")
	}

	content := answer.String()
	if sink.IsCanceled(req.RunID) {
		return protocol.RunResult{RunID: req.RunID, Status: protocol.RunCanceled}
	}
	if err := sink.Complete(req.RunID, content); err != nil {
		return failed(req.RunID, err)
	}
	return protocol.RunResult{
		RunID:     req.RunID,
		Status:    protocol.RunSucceeded,
		MessageID: "",
		TokenUsage: protocol.TokenUsage{
			InputTokens:  estimateTokens(userMessage),
			OutputTokens: estimateTokens(content),
		},
	}
}

func (e *Engine) streamModel(ctx context.Context, req protocol.RunRequest, userMessage string, writeToken func(string)) error {
	provider := e.Models
	if provider == nil {
		provider = MockProvider{}
	}
	chunks, err := provider.Stream(ctx, ModelRequest{
		RunID:       req.RunID,
		ModelPolicy: req.ModelPolicy,
		Messages: []protocol.Message{{
			ChatID:  req.ChatID,
			RunID:   req.RunID,
			Role:    protocol.RoleUser,
			Content: userMessage,
		}},
	})
	if err != nil {
		return err
	}
	for chunk := range chunks {
		if chunk.Error != nil {
			return chunk.Error
		}
		if chunk.Text != "" {
			writeToken(chunk.Text)
		}
		if chunk.Done {
			return nil
		}
	}
	return nil
}

func parseCLICommand(content string) ([]string, bool) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, false
	}
	if !strings.HasPrefix(content, "/cli ") {
		return nil, false
	}
	parts := strings.Fields(strings.TrimPrefix(content, "/cli "))
	if len(parts) == 0 {
		return nil, false
	}
	return parts, true
}

func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return len([]rune(s))/4 + 1
}

func failed(runID string, err error) protocol.RunResult {
	return protocol.RunResult{RunID: runID, Status: protocol.RunFailed, Error: fmt.Sprint(err)}
}
