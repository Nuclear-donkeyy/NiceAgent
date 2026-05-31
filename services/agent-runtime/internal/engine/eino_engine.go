package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"niceagent/agent-runtime/internal/modelprovider"
	"niceagent/agent-runtime/internal/tools"
	"niceagent/common/platform"
	"niceagent/common/protocol"
)

type EinoAgentEngine struct {
	Sandbox tools.SandboxExecutor
	Models  model.ToolCallingChatModel
	Tools   *tools.DefaultToolBridge
	Limits  LoopLimits
}

func NewEinoAgentEngine(executor tools.SandboxExecutor) *EinoAgentEngine {
	return &EinoAgentEngine{
		Sandbox: executor,
		Models:  modelprovider.MockChatModel{},
		Tools:   tools.NewDefaultToolBridge(executor),
		Limits:  LoopLimits{MaxSteps: 8, Timeout: 2 * time.Minute},
	}
}

func (e *EinoAgentEngine) Execute(ctx context.Context, req protocol.RunRequest, userMessage string, sink tools.EventSink) protocol.RunResult {
	ctx, endSpan := platform.StartSpan(ctx, "niceagent/agent_runtime", "run.execute", platform.Labels{
		"run_id":     req.RunID,
		"chat_id":    req.ChatID,
		"attempt_id": req.AttemptID,
		"user_id":    req.UserID,
	})
	result := e.execute(ctx, req, userMessage, sink)
	var spanErr error
	if result.Status == protocol.RunFailed && strings.TrimSpace(result.Error) != "" {
		spanErr = fmt.Errorf("%s", result.Error)
	}
	endSpan(spanErr, platform.Labels{"status": string(result.Status)})
	return result
}

func (e *EinoAgentEngine) execute(ctx context.Context, req protocol.RunRequest, userMessage string, sink tools.EventSink) protocol.RunResult {
	if e.Limits.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.Limits.Timeout)
		defer cancel()
	}
	if err := sink.Emit(req.RunID, protocol.EventRunStarted, "Eino agent runtime accepted the run.", map[string]any{
		"workspace_id":  req.WorkspaceID,
		"model_policy":  req.ModelPolicy,
		"skill_ids":     req.SkillIDs,
		"model_runtime": "eino_chat_model",
	}); err != nil {
		return failed(req.RunID, err)
	}
	if sink.IsCanceled(req.RunID) {
		_ = sink.Fail(req.RunID, "run canceled before execution")
		return protocol.RunResult{RunID: req.RunID, Status: protocol.RunCanceled}
	}

	bridge := e.Tools
	if bridge == nil {
		bridge = tools.NewDefaultToolBridge(e.Sandbox)
	}
	runtimeTools := bridge.BuildTools(req, sink)
	chatModel := e.Models
	if chatModel == nil {
		chatModel = modelprovider.MockChatModel{}
	}
	usageReporter, _ := chatModel.(modelprovider.UsageReporter)
	usagePricer, _ := chatModel.(modelprovider.UsagePricer)
	eventUsage := modelprovider.NewUsageTracker("", "")
	toolUsage := &toolUsageCollector{}
	if command, ok := parseCLICommand(userMessage); ok {
		if !hasRuntimeTool(runtimeTools, "cli_exec") {
			message := "当前用户未启用系统 CLI 工具，无法执行 /cli 请求。"
			_ = sink.Fail(req.RunID, message)
			return protocol.RunResult{RunID: req.RunID, Status: protocol.RunFailed, Error: message}
		}
		chatModel = forcedToolCallModel{
			inner: chatModel,
			call: schema.ToolCall{
				ID:   "call_cli_exec",
				Type: "function",
				Function: schema.FunctionCall{
					Name:      "cli_exec",
					Arguments: mustJSON(map[string]any{"command": command}),
				},
			},
		}
	}
	chatModel = newGuardedToolModel(chatModel, runtimeTools)

	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:          "niceagent_runtime",
		Description:   "NiceAgent remote agent runtime.",
		Instruction:   "你是 NiceAgent 的远端 agent。根据用户目标自主选择可用工具，工具结果只能作为观察信息，最终用简洁中文回复用户。",
		Model:         chatModel,
		ToolsConfig:   adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: runtimeTools, ExecuteSequentially: true}},
		MaxIterations: e.Limits.MaxSteps,
	})
	if err != nil {
		_ = sink.Fail(req.RunID, err.Error())
		return protocol.RunResult{RunID: req.RunID, Status: protocol.RunFailed, Error: err.Error()}
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent, EnableStreaming: false})
	iterator := runner.Query(ctx, userMessage)

	var final strings.Builder
	var artifacts []protocol.Artifact
	for {
		event, ok := iterator.Next()
		if !ok {
			break
		}
		if event != nil && event.Err != nil {
			_ = sink.Fail(req.RunID, event.Err.Error())
			return protocol.RunResult{RunID: req.RunID, Status: protocol.RunFailed, Error: event.Err.Error()}
		}
		msg, _, err := adk.GetMessage(event)
		if err != nil || msg == nil {
			continue
		}
		if msg.Role == schema.Assistant && msg.Content != "" {
			final.Reset()
			final.WriteString(msg.Content)
		}
		if msg.Role == schema.Tool && msg.Content != "" {
			artifacts = appendArtifactsFromObservation(artifacts, msg.Content)
			toolUsage.ObserveToolObservation(msg.Content)
		}
		eventUsage.ObserveMessage(msg)
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
		if sink.IsCanceled(req.RunID) {
			return protocol.RunResult{RunID: req.RunID, Status: protocol.RunCanceled}
		}
	}
	usage := buildRunUsage(req, usageReporter, usagePricer, eventUsage, toolUsage, userMessage, content)
	if err := completeWithUsage(sink, req.RunID, content, usage, artifacts...); err != nil {
		return failed(req.RunID, err)
	}
	return protocol.RunResult{
		RunID:      req.RunID,
		Status:     protocol.RunSucceeded,
		Artifacts:  artifacts,
		TokenUsage: protocol.TokenUsageFromRunUsage(usage),
		Usage:      usage,
	}
}

type usageCompleter interface {
	CompleteWithUsage(runID string, content string, usage protocol.RunUsage, artifacts ...protocol.Artifact) error
}

func completeWithUsage(sink tools.EventSink, runID string, content string, usage protocol.RunUsage, artifacts ...protocol.Artifact) error {
	if completer, ok := sink.(usageCompleter); ok {
		return completer.CompleteWithUsage(runID, content, usage, artifacts...)
	}
	return sink.Complete(runID, content, artifacts...)
}

func buildRunUsage(req protocol.RunRequest, reporter modelprovider.UsageReporter, pricer modelprovider.UsagePricer, eventUsage *modelprovider.UsageTracker, toolUsage *toolUsageCollector, userMessage, content string) protocol.RunUsage {
	var usage protocol.RunUsage
	if reporter != nil {
		usage = reporter.UsageSnapshot()
	}
	if !hasUsageTokens(usage) && eventUsage != nil {
		observed := eventUsage.UsageSnapshot()
		if hasUsageTokens(observed) {
			observed.Provider = firstNonEmpty(observed.Provider, usage.Provider)
			observed.Model = firstNonEmpty(observed.Model, usage.Model)
			usage = observed
		}
	}
	if !hasUsageTokens(usage) {
		usage.InputTokens = estimateTokens(userMessage)
		usage.OutputTokens = estimateTokens(content)
		usage.Estimated = true
	}
	usage.Provider = firstNonEmpty(usage.Provider, req.ModelPolicy, "mock")
	usage.Model = firstNonEmpty(usage.Model, req.ModelPolicy, "mock")
	if toolUsage != nil {
		usage = mergeRunUsage(usage, toolUsage.Snapshot())
	}
	if pricer != nil {
		usage = pricer.PriceUsage(usage)
	}
	return protocol.NormalizeRunUsage(usage)
}

func hasUsageTokens(usage protocol.RunUsage) bool {
	return usage.InputTokens > 0 || usage.OutputTokens > 0 || usage.TotalTokens > 0 || usage.ReasoningTokens > 0 || usage.CachedTokens > 0
}

type toolUsageCollector struct {
	usage protocol.RunUsage
}

func (c *toolUsageCollector) ObserveToolObservation(content string) {
	if c == nil {
		return
	}
	if isQuotaReservationObservation(content) {
		return
	}
	c.usage.ToolCalls++
	var sandbox protocol.SandboxResult
	if err := json.Unmarshal([]byte(content), &sandbox); err != nil || !looksLikeSandboxResult(sandbox) {
		return
	}
	c.usage.SandboxCommands++
	if sandbox.ExitCode != 0 || strings.TrimSpace(sandbox.Error) != "" {
		c.usage.ToolErrors++
	}
	if duration, err := time.ParseDuration(sandbox.Duration); err == nil {
		c.usage.SandboxDurationMillis += duration.Milliseconds()
	}
	c.usage.SandboxOutputBytes += len(sandbox.Stdout) + len(sandbox.Stderr)
	c.usage.SandboxCPUMillis += sandbox.ResourceUsage.CPUMillis
	if sandbox.ResourceUsage.MemoryMaxBytes > c.usage.SandboxMemoryMaxBytes {
		c.usage.SandboxMemoryMaxBytes = sandbox.ResourceUsage.MemoryMaxBytes
	}
	c.usage.ArtifactCount += len(sandbox.Artifacts)
	for _, artifact := range sandbox.Artifacts {
		c.usage.ArtifactBytes += artifact.SizeBytes
	}
}

func isQuotaReservationObservation(content string) bool {
	var observation struct {
		ErrorType string `json:"error_type"`
	}
	if err := json.Unmarshal([]byte(content), &observation); err != nil {
		return false
	}
	return observation.ErrorType == "quota_denied" || observation.ErrorType == "quota_unavailable"
}

func (c *toolUsageCollector) Snapshot() protocol.RunUsage {
	if c == nil {
		return protocol.RunUsage{}
	}
	return c.usage
}

func looksLikeSandboxResult(result protocol.SandboxResult) bool {
	return result.RunID != "" || len(result.Command) > 0 || result.Duration != "" || result.Stdout != "" || result.Stderr != "" || result.Error != ""
}

func mergeRunUsage(base, extra protocol.RunUsage) protocol.RunUsage {
	base.ToolCalls += extra.ToolCalls
	base.ToolErrors += extra.ToolErrors
	base.SandboxCommands += extra.SandboxCommands
	base.SandboxDurationMillis += extra.SandboxDurationMillis
	base.SandboxOutputBytes += extra.SandboxOutputBytes
	base.SandboxCPUMillis += extra.SandboxCPUMillis
	if extra.SandboxMemoryMaxBytes > base.SandboxMemoryMaxBytes {
		base.SandboxMemoryMaxBytes = extra.SandboxMemoryMaxBytes
	}
	base.ArtifactCount += extra.ArtifactCount
	base.ArtifactBytes += extra.ArtifactBytes
	return base
}

type forcedToolCallModel struct {
	inner model.ToolCallingChatModel
	call  schema.ToolCall
}

func (m forcedToolCallModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	if content, ok := latestToolObservation(input); ok {
		return schema.AssistantMessage(formatToolObservation(content), nil), nil
	}
	if !hasAssistantToolCall(input) {
		return schema.AssistantMessage("", []schema.ToolCall{m.call}), nil
	}
	return m.inner.Generate(ctx, input, opts...)
}

func (m forcedToolCallModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return modelprovider.StreamSingle(ctx, msg)
}

func (m forcedToolCallModel) WithTools(toolInfos []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	nextInner, err := m.inner.WithTools(toolInfos)
	if err != nil {
		return nil, err
	}
	next := m
	next.inner = nextInner
	return next, nil
}

type guardedToolModel struct {
	inner   model.ToolCallingChatModel
	allowed map[string]struct{}
}

func newGuardedToolModel(inner model.ToolCallingChatModel, runtimeTools []tool.BaseTool) model.ToolCallingChatModel {
	return guardedToolModel{inner: inner, allowed: allowedToolNames(runtimeTools)}
}

func (m guardedToolModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	msg, err := m.inner.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	if err := m.validateToolCalls(msg); err != nil {
		return nil, err
	}
	return msg, nil
}

func (m guardedToolModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return modelprovider.StreamSingle(ctx, msg)
}

func (m guardedToolModel) WithTools(toolInfos []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	nextInner, err := m.inner.WithTools(toolInfos)
	if err != nil {
		return nil, err
	}
	return guardedToolModel{inner: nextInner, allowed: allowedToolInfoNames(toolInfos)}, nil
}

func (m guardedToolModel) validateToolCalls(msg *schema.Message) error {
	if msg == nil {
		return nil
	}
	for _, call := range msg.ToolCalls {
		if _, ok := m.allowed[call.Function.Name]; !ok {
			return fmt.Errorf("model requested unavailable tool %q", call.Function.Name)
		}
	}
	return nil
}

func allowedToolNames(runtimeTools []tool.BaseTool) map[string]struct{} {
	names := map[string]struct{}{}
	for _, runtimeTool := range runtimeTools {
		info, err := runtimeTool.Info(context.Background())
		if err == nil && info != nil && info.Name != "" {
			names[info.Name] = struct{}{}
		}
	}
	return names
}

func allowedToolInfoNames(toolInfos []*schema.ToolInfo) map[string]struct{} {
	names := map[string]struct{}{}
	for _, info := range toolInfos {
		if info != nil && info.Name != "" {
			names[info.Name] = struct{}{}
		}
	}
	return names
}

func hasRuntimeTool(runtimeTools []tool.BaseTool, name string) bool {
	_, ok := allowedToolNames(runtimeTools)[name]
	return ok
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
		return "系统 CLI 策略拒绝或执行错误：\n" + reason
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

func appendArtifactsFromObservation(existing []protocol.Artifact, content string) []protocol.Artifact {
	var sandbox protocol.SandboxResult
	if err := json.Unmarshal([]byte(content), &sandbox); err != nil || len(sandbox.Artifacts) == 0 {
		return existing
	}
	seen := map[string]struct{}{}
	for _, artifact := range existing {
		key := artifact.ID
		if key == "" {
			key = artifact.Path
		}
		if key != "" {
			seen[key] = struct{}{}
		}
	}
	for _, artifact := range sandbox.Artifacts {
		key := artifact.ID
		if key == "" {
			key = artifact.Path
		}
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		existing = append(existing, artifact)
	}
	return existing
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

var _ model.ToolCallingChatModel = forcedToolCallModel{}
var _ model.ToolCallingChatModel = guardedToolModel{}
