package modelprovider

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"niceagent/common/protocol"
)

type MockChatModel struct {
	Response  string
	ToolCalls []schema.ToolCall
	Usage     *schema.TokenUsage
	tools     []*schema.ToolInfo
}

type MockProvider = MockChatModel

func (m MockChatModel) Generate(ctx context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if content, ok := latestToolObservation(input); ok {
		return m.withUsage(schema.AssistantMessage(formatToolObservation(content), nil)), nil
	}
	if len(m.ToolCalls) > 0 && !hasAssistantToolCall(input) {
		return m.withUsage(schema.AssistantMessage("", append([]schema.ToolCall(nil), m.ToolCalls...))), nil
	}
	response := m.Response
	if response == "" {
		response = "我已经接收到任务，并会以远端 agent 的方式处理。\n\n当前实现会把会话状态保存在 Control Plane，由独立 Runtime 执行本次请求，并在回复中实时更新当前状态。"
	}
	return m.withUsage(schema.AssistantMessage(response, nil)), nil
}

func (m MockChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return StreamSingle(ctx, msg)
}

func (m MockChatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	next := m
	next.tools = append([]*schema.ToolInfo(nil), tools...)
	return next, nil
}

func (m MockChatModel) Probe(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}

func (m MockChatModel) withUsage(msg *schema.Message) *schema.Message {
	if msg != nil && m.Usage != nil {
		msg.ResponseMeta = &schema.ResponseMeta{Usage: m.Usage}
	}
	return msg
}

func (m MockChatModel) ModelProviderHealth() protocol.ModelProviderHealth {
	return protocol.ModelProviderHealth{
		Provider: "mock",
		Model:    "mock",
		Status:   "healthy",
	}
}

func latestToolObservation(messages []*schema.Message) (string, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i] != nil && messages[i].Role == schema.Tool {
			return messages[i].Content, true
		}
	}
	return "", false
}

func hasAssistantToolCall(messages []*schema.Message) bool {
	for _, msg := range messages {
		if msg != nil && msg.Role == schema.Assistant && len(msg.ToolCalls) > 0 {
			return true
		}
	}
	return false
}

func formatToolObservation(content string) string {
	content = strings.TrimSpace(content)
	if len(content) > 2000 {
		content = content[:2000] + "\n...[已截断]"
	}
	if content == "" {
		content = "能力调用完成，但没有返回可展示内容。"
	}
	return "我已经调用相关能力并获得结果：\n" + content
}

var _ model.ToolCallingChatModel = MockChatModel{}
var _ Probeable = MockChatModel{}
