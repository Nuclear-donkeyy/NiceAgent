package runtime

import (
	"context"

	"niceagent/common/protocol"
)

type AgentEngine interface {
	Execute(ctx context.Context, req protocol.RunRequest, userMessage string, sink EventSink) protocol.RunResult
}

type ModelProvider interface {
	Stream(ctx context.Context, request ModelRequest) (<-chan ModelChunk, error)
}

type ModelRequest struct {
	RunID       string
	ModelPolicy string
	Messages    []protocol.Message
	Tools       []ToolDefinition
}

type ModelChunk struct {
	Text  string
	Error error
	Done  bool
}

type ToolBridge interface {
	Definitions(ctx context.Context, skills []protocol.RuntimeSkill) ([]ToolDefinition, error)
	Invoke(ctx context.Context, invocation protocol.SkillInvocation) (any, error)
}

type ToolDefinition struct {
	ID          string
	Name        string
	Description string
	InputSchema string
	Risk        protocol.SkillRisk
}

type SandboxExecutor interface {
	Execute(ctx context.Context, request protocol.SandboxCommand) protocol.SandboxResult
}
