package engine

import (
	"context"

	"niceagent/agent-runtime/internal/modelprovider"
	"niceagent/agent-runtime/internal/tools"
	"niceagent/common/protocol"
)

type AgentEngine interface {
	Execute(ctx context.Context, req protocol.RunRequest, userMessage string, sink tools.EventSink) protocol.RunResult
}

type ModelProvider interface {
	Stream(ctx context.Context, request modelprovider.Request) (<-chan modelprovider.Chunk, error)
}
