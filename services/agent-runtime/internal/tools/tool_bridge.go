package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"

	"niceagent/common/protocol"
)

type EventSink interface {
	Emit(runID string, typ protocol.RunEventType, message string, payload any) error
	Complete(runID string, content string) error
	Fail(runID string, message string) error
	IsCanceled(runID string) bool
}

type SandboxExecutor interface {
	Execute(ctx context.Context, request protocol.SandboxCommand) protocol.SandboxResult
}

type Definition struct {
	ID          string
	Name        string
	Description string
	InputSchema string
	Risk        protocol.SkillRisk
}

type DefaultToolBridge struct {
	Sandbox SandboxExecutor
	Client  *http.Client
}

func NewDefaultToolBridge(executor SandboxExecutor) *DefaultToolBridge {
	return &DefaultToolBridge{
		Sandbox: executor,
		Client:  &http.Client{Timeout: 15 * time.Second},
	}
}

func (b *DefaultToolBridge) Definitions(ctx context.Context, skills []protocol.RuntimeSkill) ([]Definition, error) {
	defs := make([]Definition, 0, len(skills))
	for _, runtimeSkill := range skills {
		info, err := toolInfoForSkill(runtimeSkill.Skill)
		if err != nil {
			return nil, err
		}
		defs = append(defs, Definition{
			ID:          runtimeSkill.Skill.ID,
			Name:        info.Name,
			Description: info.Desc,
			InputSchema: runtimeSkill.Skill.InputSchema,
			Risk:        runtimeSkill.Skill.Risk,
		})
	}
	_ = ctx
	return defs, nil
}

func (b *DefaultToolBridge) Invoke(ctx context.Context, invocation protocol.SkillInvocation) (any, error) {
	_ = ctx
	return nil, fmt.Errorf("direct invocation is not wired; use BuildTools for Eino execution")
}

func (b *DefaultToolBridge) BuildTools(req protocol.RunRequest, sink EventSink) []tool.BaseTool {
	runtimeSkills := req.Skills
	if len(runtimeSkills) == 0 {
		for _, id := range req.SkillIDs {
			runtimeSkills = append(runtimeSkills, protocol.RuntimeSkill{Skill: protocol.Skill{ID: id, Name: id, Kind: protocol.SkillKindBuiltin, Scope: protocol.SkillScopeSystem, Enabled: true}})
		}
	}
	tools := make([]tool.BaseTool, 0, len(runtimeSkills))
	for _, runtimeSkill := range runtimeSkills {
		if runtimeSkill.Skill.ID == "" || !runtimeSkill.Skill.Enabled {
			continue
		}
		tools = append(tools, &runtimeTool{
			bridge:       b,
			runID:        req.RunID,
			workspaceID:  req.WorkspaceID,
			runtimeSkill: runtimeSkill,
			sink:         sink,
		})
	}
	return tools
}

type runtimeTool struct {
	bridge       *DefaultToolBridge
	runID        string
	workspaceID  string
	runtimeSkill protocol.RuntimeSkill
	sink         EventSink
}

func (t *runtimeTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	_ = ctx
	return toolInfoForSkill(t.runtimeSkill.Skill)
}

func (t *runtimeTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	skill := t.runtimeSkill.Skill
	toolName := toolNameForSkillID(skill.ID)
	_ = t.sink.Emit(t.runID, protocol.EventToolStarted, "Starting skill invocation.", map[string]any{
		"skill_id": skill.ID,
		"tool":     toolName,
		"input":    summarizeToolInput(argumentsInJSON),
	})
	var output string
	var err error
	switch skill.Kind {
	case protocol.SkillKindHTTP:
		output, err = t.invokeHTTP(ctx, argumentsInJSON)
	default:
		output, err = t.invokeBuiltin(ctx, argumentsInJSON)
	}
	payload := map[string]any{
		"skill_id": skill.ID,
		"tool":     toolName,
		"output":   output,
	}
	if err != nil {
		payload["error"] = err.Error()
	}
	_ = t.sink.Emit(t.runID, protocol.EventToolOutput, "Skill invocation returned output.", payload)
	_ = t.sink.Emit(t.runID, protocol.EventToolFinished, "Finished skill invocation.", map[string]any{
		"skill_id": skill.ID,
		"tool":     toolName,
		"ok":       err == nil,
	})
	return output, err
}

func (t *runtimeTool) invokeBuiltin(ctx context.Context, argumentsInJSON string) (string, error) {
	switch t.runtimeSkill.Skill.ID {
	case "cli.exec":
		if t.bridge.Sandbox == nil {
			return "", fmt.Errorf("sandbox executor is not configured")
		}
		command := commandFromToolArgs(argumentsInJSON)
		result := t.bridge.Sandbox.Execute(ctx, protocol.SandboxCommand{
			RunID:          t.runID,
			WorkspaceID:    t.workspaceID,
			Command:        command,
			TimeoutSeconds: 10,
			Network:        true,
		})
		body, _ := json.Marshal(result)
		return string(body), nil
	case "workspace.read":
		return `{"message":"workspace.read is registered but file artifact browsing is not implemented in this phase"}`, nil
	default:
		return "", fmt.Errorf("unknown builtin skill %s", t.runtimeSkill.Skill.ID)
	}
}

func (t *runtimeTool) invokeHTTP(ctx context.Context, argumentsInJSON string) (string, error) {
	var cfg struct {
		Method         string `json:"method"`
		URL            string `json:"url"`
		TimeoutSeconds int    `json:"timeout_seconds"`
		AuthType       string `json:"auth_type"`
	}
	_ = json.Unmarshal([]byte(t.runtimeSkill.Skill.RuntimeConfig), &cfg)
	if strings.TrimSpace(cfg.URL) == "" {
		return "", fmt.Errorf("http skill url is not configured")
	}
	method := strings.ToUpper(strings.TrimSpace(cfg.Method))
	if method == "" {
		method = http.MethodPost
	}
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	client := t.bridge.Client
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	var body io.Reader
	targetURL := cfg.URL
	if method == http.MethodGet {
		if strings.Contains(targetURL, "?") {
			targetURL += "&"
		} else {
			targetURL += "?"
		}
		targetURL += "input=" + url.QueryEscape(argumentsInJSON)
	} else {
		body = bytes.NewBufferString(argumentsInJSON)
	}
	req, err := http.NewRequestWithContext(ctx, method, targetURL, body)
	if err != nil {
		return "", err
	}
	if method != http.MethodGet {
		req.Header.Set("Content-Type", "application/json")
	}
	if cfg.AuthType == "bearer" {
		if token := t.runtimeSkill.Secrets["bearer_token"]; token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return string(data), fmt.Errorf("http skill returned %s", resp.Status)
	}
	return string(data), nil
}

func toolInfoForSkill(skill protocol.Skill) (*schema.ToolInfo, error) {
	info := &schema.ToolInfo{
		Name: toolNameForSkillID(skill.ID),
		Desc: skill.Description,
		Extra: map[string]any{
			"skill_id": skill.ID,
			"scope":    skill.Scope,
			"kind":     skill.Kind,
		},
	}
	if skill.InputSchema != "" {
		js := &jsonschema.Schema{}
		if err := json.Unmarshal([]byte(skill.InputSchema), js); err != nil {
			return nil, fmt.Errorf("invalid input schema for %s: %w", skill.ID, err)
		}
		info.ParamsOneOf = schema.NewParamsOneOfByJSONSchema(js)
	}
	return info, nil
}

func commandFromToolArgs(argumentsInJSON string) []string {
	var parsed struct {
		Command []string `json:"command"`
		Query   string   `json:"query"`
	}
	_ = json.Unmarshal([]byte(argumentsInJSON), &parsed)
	if len(parsed.Command) > 0 {
		return parsed.Command
	}
	if parsed.Query != "" {
		return strings.Fields(parsed.Query)
	}
	return nil
}

func toolNameForSkillID(id string) string {
	name := strings.NewReplacer(".", "_", "-", "_", ":", "_").Replace(id)
	if name == "" {
		return "unknown_skill"
	}
	return name
}

func skillIDForToolName(name string, skills []protocol.RuntimeSkill) string {
	for _, skill := range skills {
		if toolNameForSkillID(skill.Skill.ID) == name {
			return skill.Skill.ID
		}
	}
	return name
}

func summarizeToolInput(value string) string {
	if len(value) <= 512 {
		return value
	}
	return value[:512] + "...[truncated]"
}

var _ tool.InvokableTool = (*runtimeTool)(nil)
