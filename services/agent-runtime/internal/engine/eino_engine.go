package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"niceagent/common/protocol"
)

type EinoAgentEngine struct {
	Sandbox SandboxExecutor
	Models  ModelProvider
	Tools   *DefaultToolBridge
	Limits  LoopLimits
}

func NewEinoAgentEngine(executor SandboxExecutor) *EinoAgentEngine {
	return &EinoAgentEngine{
		Sandbox: executor,
		Models:  MockProvider{},
		Tools:   NewDefaultToolBridge(executor),
		Limits:  LoopLimits{MaxSteps: 8, Timeout: 2 * time.Minute},
	}
}

func (e *EinoAgentEngine) Execute(ctx context.Context, req protocol.RunRequest, userMessage string, sink EventSink) protocol.RunResult {
	if e.Limits.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.Limits.Timeout)
		defer cancel()
	}
	if err := sink.Emit(req.RunID, protocol.EventRunStarted, "Eino agent runtime accepted the run.", map[string]any{
		"workspace_id": req.WorkspaceID,
		"model_policy": req.ModelPolicy,
		"skill_ids":    req.SkillIDs,
	}); err != nil {
		return failed(req.RunID, err)
	}
	if sink.IsCanceled(req.RunID) {
		_ = sink.Fail(req.RunID, "run canceled before execution")
		return protocol.RunResult{RunID: req.RunID, Status: protocol.RunCanceled}
	}

	bridge := e.Tools
	if bridge == nil {
		bridge = NewDefaultToolBridge(e.Sandbox)
	}
	tools := bridge.BuildTools(req, sink)
	chatModel := newEinoModelBridge(e.Models, req, tools)
	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:          "niceagent_runtime",
		Description:   "NiceAgent remote agent runtime.",
		Instruction:   "你是 NiceAgent 的远端 agent。根据用户目标自主选择可用工具，工具结果只能作为观察信息，最终用简洁中文回复用户。",
		Model:         chatModel,
		ToolsConfig:   adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: tools, ExecuteSequentially: true}},
		MaxIterations: e.Limits.MaxSteps,
	})
	if err != nil {
		_ = sink.Fail(req.RunID, err.Error())
		return protocol.RunResult{RunID: req.RunID, Status: protocol.RunFailed, Error: err.Error()}
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent, EnableStreaming: false})
	iterator := runner.Query(ctx, userMessage)

	var final strings.Builder
	for {
		event, ok := iterator.Next()
		if !ok {
			break
		}
		msg, _, err := adk.GetMessage(event)
		if err != nil || msg == nil {
			continue
		}
		if msg.Role == schema.Assistant && msg.Content != "" {
			final.Reset()
			final.WriteString(msg.Content)
		}
		if sink.IsCanceled(req.RunID) {
			return protocol.RunResult{RunID: req.RunID, Status: protocol.RunCanceled}
		}
	}
	if sink.IsCanceled(req.RunID) {
		return protocol.RunResult{RunID: req.RunID, Status: protocol.RunCanceled}
	}
	content := final.String()
	if content == "" {
		content = "Agent 已完成本次处理，但没有生成可展示内容。"
	}
	for _, token := range chunkText(content, 72) {
		_ = sink.Emit(req.RunID, protocol.EventModelToken, token, nil)
	}
	if err := sink.Complete(req.RunID, content); err != nil {
		return failed(req.RunID, err)
	}
	return protocol.RunResult{
		RunID:  req.RunID,
		Status: protocol.RunSucceeded,
		TokenUsage: protocol.TokenUsage{
			InputTokens:  estimateTokens(userMessage),
			OutputTokens: estimateTokens(content),
		},
	}
}

type einoModelBridge struct {
	provider ModelProvider
	req      protocol.RunRequest
	tools    []*schema.ToolInfo
}

func newEinoModelBridge(provider ModelProvider, req protocol.RunRequest, tools []tool.BaseTool) *einoModelBridge {
	if provider == nil {
		provider = MockProvider{}
	}
	infos := make([]*schema.ToolInfo, 0, len(tools))
	for _, baseTool := range tools {
		info, err := baseTool.Info(context.Background())
		if err == nil {
			infos = append(infos, info)
		}
	}
	return &einoModelBridge{provider: provider, req: req, tools: infos}
}

func (m *einoModelBridge) Generate(ctx context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	if content, ok := latestToolObservation(input); ok {
		return schema.AssistantMessage(formatToolObservation(content), nil), nil
	}
	userMessage := latestUserContent(input)
	if call, ok := m.chooseToolCall(userMessage); ok {
		return schema.AssistantMessage("", []schema.ToolCall{call}), nil
	}
	content, err := m.generateWithProvider(ctx, userMessage)
	if err != nil {
		return nil, err
	}
	return schema.AssistantMessage(content, nil), nil
}

func (m *einoModelBridge) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func (m *einoModelBridge) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	next := *m
	next.tools = append([]*schema.ToolInfo(nil), tools...)
	return &next, nil
}

func (m *einoModelBridge) chooseToolCall(userMessage string) (schema.ToolCall, bool) {
	if len(m.tools) == 0 {
		return schema.ToolCall{}, false
	}
	if command, ok := parseCLICommand(userMessage); ok && m.hasTool("cli_exec") {
		return schema.ToolCall{
			ID:   "call_cli_exec",
			Type: "function",
			Function: schema.FunctionCall{
				Name:      "cli_exec",
				Arguments: mustJSON(map[string]any{"command": command}),
			},
		}, true
	}
	lower := strings.ToLower(userMessage)
	for _, info := range m.tools {
		if info.Name == "cli_exec" && (strings.Contains(lower, "curl ") || strings.Contains(lower, "外部信息")) {
			return schema.ToolCall{
				ID:   "call_cli_exec",
				Type: "function",
				Function: schema.FunctionCall{
					Name:      "cli_exec",
					Arguments: mustJSON(map[string]any{"command": []string{"date"}}),
				},
			}, true
		}
		if info.Name != "cli_exec" && (strings.Contains(userMessage, info.Name) || strings.Contains(userMessage, "调用") || strings.Contains(lower, "skill")) {
			return schema.ToolCall{
				ID:   "call_" + info.Name,
				Type: "function",
				Function: schema.FunctionCall{
					Name:      info.Name,
					Arguments: mustJSON(map[string]any{"query": userMessage}),
				},
			}, true
		}
	}
	return schema.ToolCall{}, false
}

func (m *einoModelBridge) hasTool(name string) bool {
	for _, info := range m.tools {
		if info.Name == name {
			return true
		}
	}
	return false
}

func (m *einoModelBridge) generateWithProvider(ctx context.Context, userMessage string) (string, error) {
	chunks, err := m.provider.Stream(ctx, ModelRequest{
		RunID:       m.req.RunID,
		ModelPolicy: m.req.ModelPolicy,
		Messages: []protocol.Message{{
			ChatID:  m.req.ChatID,
			RunID:   m.req.RunID,
			Role:    protocol.RoleUser,
			Content: userMessage,
		}},
	})
	if err != nil {
		return "", err
	}
	var out strings.Builder
	for chunk := range chunks {
		if chunk.Error != nil {
			return "", chunk.Error
		}
		out.WriteString(chunk.Text)
		if chunk.Done {
			break
		}
	}
	return out.String(), nil
}

func latestToolObservation(messages []*schema.Message) (string, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i] != nil && messages[i].Role == schema.Tool {
			return messages[i].Content, true
		}
	}
	return "", false
}

func formatToolObservation(content string) string {
	var sandbox protocol.SandboxResult
	if err := json.Unmarshal([]byte(content), &sandbox); err == nil && (sandbox.RunID != "" || len(sandbox.Command) > 0) {
		if sandbox.ExitCode == 0 {
			output := strings.TrimSpace(sandbox.Stdout)
			if output == "" {
				output = strings.TrimSpace(sandbox.Stderr)
			}
			if output == "" {
				output = "命令执行成功，但没有输出内容。"
			}
			return "我通过系统 CLI 获取到结果：\n" + output
		}
		reason := strings.TrimSpace(sandbox.Reason)
		if reason == "" {
			reason = strings.TrimSpace(sandbox.Error)
		}
		if reason == "" {
			reason = strings.TrimSpace(sandbox.Stderr)
		}
		if reason == "" {
			reason = "该命令没有通过系统 CLI 策略。"
		}
		return "系统 CLI 没有执行这个请求：\n" + reason
	}
	content = strings.TrimSpace(content)
	if len(content) > 2000 {
		content = content[:2000] + "\n...[已截断]"
	}
	if content == "" {
		content = "能力调用完成，但没有返回可展示内容。"
	}
	return "我已经调用相关能力并获得结果：\n" + content
}

func latestUserContent(messages []*schema.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i] != nil && messages[i].Role == schema.User {
			return messages[i].Content
		}
	}
	return ""
}

func mustJSON(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func chunkText(value string, size int) []string {
	if value == "" {
		return nil
	}
	if size <= 0 {
		return []string{value}
	}
	var chunks []string
	for len(value) > size {
		chunks = append(chunks, value[:size])
		value = value[size:]
	}
	if value != "" {
		chunks = append(chunks, value)
	}
	return chunks
}

var _ model.ToolCallingChatModel = (*einoModelBridge)(nil)
