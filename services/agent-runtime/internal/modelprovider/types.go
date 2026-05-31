package modelprovider

import (
	"context"

	"niceagent/common/protocol"
)

type Provider interface {
	Stream(ctx context.Context, request Request) (<-chan Chunk, error)
}

type Request struct {
	RunID       string
	ModelPolicy string
	Messages    []protocol.Message
	Tools       []ToolDefinition
}

type ToolDefinition struct {
	ID          string
	Name        string
	Description string
	InputSchema string
	Risk        protocol.SkillRisk
}

type Chunk struct {
	Text  string
	Error error
	Done  bool
}
