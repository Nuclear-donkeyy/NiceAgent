package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"

	"niceagent/agent-runtime/internal/modelprovider"
	"niceagent/agent-runtime/internal/tools"
	"niceagent/common/protocol"
)

type Engine struct {
	Sandbox tools.SandboxExecutor
	Models  model.ToolCallingChatModel
	Tools   *tools.DefaultToolBridge
	Limits  LoopLimits
}

type LoopLimits struct {
	MaxSteps int
	Timeout  time.Duration
}

func NewEngine(executor tools.SandboxExecutor) *Engine {
	return &Engine{
		Sandbox: executor,
		Models:  modelprovider.MockChatModel{},
		Tools:   tools.NewDefaultToolBridge(executor),
		Limits:  LoopLimits{MaxSteps: 8, Timeout: 2 * time.Minute},
	}
}

func (e *Engine) Execute(ctx context.Context, req protocol.RunRequest, userMessage string, sink tools.EventSink) protocol.RunResult {
	runtime := EinoAgentEngine{
		Sandbox: e.Sandbox,
		Models:  e.Models,
		Tools:   e.Tools,
		Limits:  e.Limits,
	}
	return runtime.Execute(ctx, req, userMessage, sink)
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
