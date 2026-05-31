package modelprovider

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func TestNewOpenAICompatibleChatModelBuildsEinoModel(t *testing.T) {
	chatModel, err := NewOpenAICompatibleChatModel(context.Background(), OpenAICompatibleProviderConfig{
		BaseURL: "http://example.test",
		APIKey:  "secret",
		Model:   "test-model",
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("new chat model: %v", err)
	}
	if chatModel == nil {
		t.Fatal("expected chat model")
	}
	var _ model.ToolCallingChatModel = chatModel
}

func TestNewOpenAICompatibleChatModelValidatesConfig(t *testing.T) {
	cases := []OpenAICompatibleProviderConfig{
		{APIKey: "key", Model: "model"},
		{BaseURL: "http://example.test", Model: "model"},
		{BaseURL: "http://example.test", APIKey: "key"},
	}
	for _, tc := range cases {
		if _, err := NewOpenAICompatibleChatModel(context.Background(), tc); err == nil {
			t.Fatalf("config %#v returned nil error", tc)
		}
	}
}

func TestMockChatModelReturnsToolCallAndObservation(t *testing.T) {
	chatModel := MockChatModel{ToolCalls: []schema.ToolCall{{
		ID:   "call_cli_exec",
		Type: "function",
		Function: schema.FunctionCall{
			Name:      "cli_exec",
			Arguments: `{"command":["echo","hello"]}`,
		},
	}}}

	msg, err := chatModel.Generate(context.Background(), []*schema.Message{schema.UserMessage("hello")})
	if err != nil {
		t.Fatalf("generate tool call: %v", err)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Function.Name != "cli_exec" {
		t.Fatalf("tool calls = %#v", msg.ToolCalls)
	}

	msg, err = chatModel.Generate(context.Background(), []*schema.Message{
		schema.UserMessage("hello"),
		schema.AssistantMessage("", msg.ToolCalls),
		{Role: schema.Tool, ToolCallID: "call_cli_exec", ToolName: "cli_exec", Content: "hello"},
	})
	if err != nil {
		t.Fatalf("generate final: %v", err)
	}
	if !strings.Contains(msg.Content, "hello") {
		t.Fatalf("content = %q, want tool observation", msg.Content)
	}
}
