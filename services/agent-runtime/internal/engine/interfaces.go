package engine

import (
	"context"

	"github.com/cloudwego/eino/components/model"

	"niceagent/agent-runtime/internal/tools"
	"niceagent/common/protocol"
)

type AgentEngine interface {
	Execute(ctx context.Context, req protocol.RunRequest, userMessage string, sink tools.EventSink) protocol.RunResult
}

type ModelProvider interface {
	model.ToolCallingChatModel
}
