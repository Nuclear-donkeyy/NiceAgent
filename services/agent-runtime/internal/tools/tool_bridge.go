package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"

	"niceagent/common/platform"
	"niceagent/common/protocol"
)

type EventSink interface {
	Emit(runID string, typ protocol.RunEventType, message string, payload any) error
	Complete(runID string, content string, artifacts ...protocol.Artifact) error
	Fail(runID string, message string) error
	IsCanceled(runID string) bool
}

type SandboxExecutor interface {
	Execute(ctx context.Context, request protocol.SandboxCommand) protocol.SandboxResult
}

type WorkspaceReader interface {
	ListArtifacts(ctx context.Context, runID string) ([]protocol.Artifact, error)
	ReadArtifactText(ctx context.Context, runID, artifactID string, maxBytes int) (protocol.ArtifactTextResponse, error)
}

type ArtifactRegistrar interface {
	RegisterArtifacts(ctx context.Context, runID string, artifacts []protocol.Artifact) ([]protocol.Artifact, error)
}

type ToolQuotaReserver interface {
	ReserveToolQuota(ctx context.Context, runID string, input protocol.ToolQuotaReserveRequest) (protocol.ToolQuotaReserveResponse, error)
}

type HostResolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

type Definition struct {
	ID          string
	Name        string
	Description string
	InputSchema string
	Risk        protocol.SkillRisk
}

type DefaultToolBridge struct {
	Sandbox        SandboxExecutor
	Client         *http.Client
	Resolver       HostResolver
	RateLimiter    SkillRateLimiter
	SecretResolver SecretResolver
	RiskPolicy     SkillRiskPolicy
	AllowLocalHTTP bool
	Metrics        *platform.Metrics
}

func NewDefaultToolBridge(executor SandboxExecutor) *DefaultToolBridge {
	return &DefaultToolBridge{
		Sandbox:        executor,
		Client:         &http.Client{Timeout: 15 * time.Second},
		Resolver:       net.DefaultResolver,
		RateLimiter:    NewSkillRateLimiter(),
		SecretResolver: LocalSecretResolver{},
		RiskPolicy:     SkillRiskPolicyAllow,
	}
}

type SkillRiskPolicy string

const (
	SkillRiskPolicyAllow            SkillRiskPolicy = "allow"
	SkillRiskPolicyBlockHigh        SkillRiskPolicy = "block-high"
	SkillRiskPolicyBlockDestructive SkillRiskPolicy = "block-destructive"
	SkillRiskPolicyReadOnly         SkillRiskPolicy = "read-only"
)

type SkillRateLimiter interface {
	Allow(ctx context.Context, key string, limitPerMinute int) bool
}

type LocalSkillRateLimiter struct {
	mu      sync.Mutex
	windows map[string]skillRateWindow
	now     func() time.Time
}

type skillRateWindow struct {
	start time.Time
	count int
}

func NewSkillRateLimiter() *LocalSkillRateLimiter {
	return &LocalSkillRateLimiter{
		windows: map[string]skillRateWindow{},
		now:     time.Now,
	}
}

func (l *LocalSkillRateLimiter) Allow(_ context.Context, key string, limitPerMinute int) bool {
	if l == nil || limitPerMinute <= 0 {
		return true
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	window := l.windows[key]
	if window.start.IsZero() || now.Sub(window.start) >= time.Minute {
		l.windows[key] = skillRateWindow{start: now, count: 1}
		return true
	}
	if window.count >= limitPerMinute {
		return false
	}
	window.count++
	l.windows[key] = window
	return true
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
	riskPolicy := b.RiskPolicy
	if strings.TrimSpace(req.SkillRiskPolicy) != "" {
		if normalized := protocol.NormalizeSkillRiskPolicy(req.SkillRiskPolicy); normalized != "" {
			riskPolicy = SkillRiskPolicy(normalized)
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
			riskPolicy:   riskPolicy,
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
	riskPolicy   SkillRiskPolicy
	sink         EventSink
}

func (t *runtimeTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	_ = ctx
	return toolInfoForSkill(t.runtimeSkill.Skill)
}

func (t *runtimeTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	skill := t.runtimeSkill.Skill
	toolName := toolNameForSkillID(skill.ID)
	if output, allowed := t.enforceRiskPolicy(skill, toolName); !allowed {
		return output, nil
	}
	if output, reserved, err := t.reserveToolQuota(ctx, skill, argumentsInJSON); !reserved {
		if err != nil {
			return output, nil
		}
		return output, nil
	}
	ctx, endSpan := platform.StartSpan(ctx, "niceagent/agent_runtime", "tool.invoke", platform.Labels{
		"run_id":   t.runID,
		"skill_id": skill.ID,
		"kind":     string(skill.Kind),
		"tool":     toolName,
	})
	_ = t.sink.Emit(t.runID, protocol.EventToolStarted, "Starting skill invocation.", map[string]any{
		"skill_id": skill.ID,
		"tool":     toolName,
		"input":    summarizeToolInput(argumentsInJSON),
	})
	var output string
	ok := true
	var err error
	switch skill.Kind {
	case protocol.SkillKindHTTP:
		output, ok, err = t.invokeHTTP(ctx, argumentsInJSON)
	case protocol.SkillKindMCP:
		output, ok, err = t.invokeMCP(ctx, argumentsInJSON)
	default:
		output, err = t.invokeBuiltin(ctx, argumentsInJSON)
		ok = err == nil
	}
	output = t.registerArtifactsFromOutput(ctx, output)
	payload := map[string]any{
		"skill_id": skill.ID,
		"tool":     toolName,
		"output":   output,
		"ok":       ok && err == nil,
	}
	if err != nil {
		payload["error"] = err.Error()
	}
	spanLabels := platform.Labels{"ok": fmt.Sprint(ok && err == nil)}
	if err != nil {
		endSpan(err, spanLabels)
	} else {
		endSpan(nil, spanLabels)
	}
	_ = t.sink.Emit(t.runID, protocol.EventToolOutput, "Skill invocation returned output.", payload)
	_ = t.sink.Emit(t.runID, protocol.EventToolFinished, "Finished skill invocation.", map[string]any{
		"skill_id": skill.ID,
		"tool":     toolName,
		"ok":       ok && err == nil,
	})
	return output, err
}

func (t *runtimeTool) enforceRiskPolicy(skill protocol.Skill, toolName string) (string, bool) {
	decision := evaluateSkillRiskPolicy(t.riskPolicy, skill)
	if decision.Allowed {
		return "", true
	}
	t.bridge.Metrics.IncCounter("niceagent_skill_policy_denials_total", platform.Labels{
		"policy": string(decision.Policy),
		"reason": safeMetricReason(decision.Reason),
		"risk":   string(skill.Risk),
		"kind":   string(skill.Kind),
	})
	output := marshalSkillRiskPolicyObservation(decision)
	payload := map[string]any{
		"skill_id": skill.ID,
		"tool":     toolName,
		"output":   output,
		"ok":       false,
		"policy":   string(decision.Policy),
		"reason":   decision.Reason,
	}
	_ = t.sink.Emit(t.runID, protocol.EventToolOutput, "Skill invocation blocked by risk policy.", payload)
	_ = t.sink.Emit(t.runID, protocol.EventToolFinished, "Finished skill invocation.", map[string]any{
		"skill_id": skill.ID,
		"tool":     toolName,
		"ok":       false,
		"policy":   string(decision.Policy),
	})
	return output, false
}

func (t *runtimeTool) recordRateLimitDenial(kind protocol.SkillKind) {
	t.bridge.Metrics.IncCounter("niceagent_skill_rate_limit_denials_total", platform.Labels{
		"kind": string(kind),
		"mode": skillRateLimitMode(t.bridge.RateLimiter),
	})
}

func skillRateLimitMode(limiter SkillRateLimiter) string {
	switch limiter.(type) {
	case nil:
		return "none"
	case *RedisSkillRateLimiter:
		return "redis"
	case *LocalSkillRateLimiter:
		return "local"
	default:
		return "custom"
	}
}

func (t *runtimeTool) registerArtifactsFromOutput(ctx context.Context, output string) string {
	registrar, ok := t.sink.(ArtifactRegistrar)
	if !ok {
		return output
	}
	var result protocol.SandboxResult
	if err := json.Unmarshal([]byte(output), &result); err != nil || len(result.Artifacts) == 0 {
		return output
	}
	artifacts, err := registrar.RegisterArtifacts(ctx, t.runID, result.Artifacts)
	if err != nil || len(artifacts) == 0 {
		return output
	}
	result.Artifacts = artifacts
	body, err := json.Marshal(result)
	if err != nil {
		return output
	}
	return string(body)
}

func (t *runtimeTool) reserveToolQuota(ctx context.Context, skill protocol.Skill, argumentsInJSON string) (string, bool, error) {
	reserver, ok := t.sink.(ToolQuotaReserver)
	if !ok {
		return "", true, nil
	}
	input := protocol.ToolQuotaReserveRequest{
		SkillID:   skill.ID,
		ToolCalls: 1,
	}
	if skill.ID == "cli.exec" {
		input.SandboxSeconds = sandboxTimeoutSeconds(argumentsInJSON)
	}
	response, err := reserver.ReserveToolQuota(ctx, t.runID, input)
	if err != nil {
		return marshalToolQuotaObservation(false, "quota_unavailable", "工具配额服务暂时不可用: "+err.Error(), ""), false, err
	}
	if !response.Allowed {
		return marshalToolQuotaObservation(false, "quota_denied", response.Message, response.Quota), false, nil
	}
	return "", true, nil
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
		return t.invokeWorkspaceRead(ctx, argumentsInJSON)
	default:
		return "", fmt.Errorf("unknown builtin skill %s", t.runtimeSkill.Skill.ID)
	}
}

func (t *runtimeTool) invokeWorkspaceRead(ctx context.Context, argumentsInJSON string) (string, error) {
	reader, ok := t.sink.(WorkspaceReader)
	if !ok {
		return "", fmt.Errorf("workspace reader is not configured")
	}
	var input struct {
		Action     string `json:"action"`
		ArtifactID string `json:"artifact_id"`
		MaxBytes   int    `json:"max_bytes"`
	}
	_ = json.Unmarshal([]byte(argumentsInJSON), &input)
	action := strings.TrimSpace(input.Action)
	if action == "" {
		action = "list"
	}
	switch action {
	case "summary":
		artifacts, err := reader.ListArtifacts(ctx, t.runID)
		if err != nil {
			return "", err
		}
		body, _ := json.Marshal(workspaceSummary{
			RunID:        t.runID,
			WorkspaceID:  t.workspaceID,
			ArtifactMeta: summarizeArtifacts(artifacts),
		})
		return string(body), nil
	case "list":
		artifacts, err := reader.ListArtifacts(ctx, t.runID)
		if err != nil {
			return "", err
		}
		body, _ := json.Marshal(map[string]any{
			"artifacts": artifacts,
			"count":     len(artifacts),
		})
		return string(body), nil
	case "read":
		if strings.TrimSpace(input.ArtifactID) == "" {
			return "", fmt.Errorf("artifact_id is required")
		}
		if input.MaxBytes <= 0 {
			input.MaxBytes = 64 * 1024
		}
		response, err := reader.ReadArtifactText(ctx, t.runID, input.ArtifactID, input.MaxBytes)
		if err != nil {
			return "", err
		}
		body, _ := json.Marshal(response)
		return string(body), nil
	default:
		return "", fmt.Errorf("unsupported workspace.read action %q", action)
	}
}

type workspaceSummary struct {
	RunID        string          `json:"run_id"`
	WorkspaceID  string          `json:"workspace_id"`
	ArtifactMeta artifactSummary `json:"artifact_summary"`
}

type artifactSummary struct {
	Count             int             `json:"count"`
	TotalSizeBytes    int64           `json:"total_size_bytes"`
	MimeTypeCounts    map[string]int  `json:"mime_type_counts"`
	TextArtifactCount int             `json:"text_artifact_count"`
	LatestArtifact    *artifactBrief  `json:"latest_artifact,omitempty"`
	Artifacts         []artifactBrief `json:"artifacts"`
}

type artifactBrief struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Name      string `json:"name,omitempty"`
	MimeType  string `json:"mime_type"`
	SizeBytes int64  `json:"size_bytes"`
	CreatedAt string `json:"created_at,omitempty"`
}

func summarizeArtifacts(artifacts []protocol.Artifact) artifactSummary {
	summary := artifactSummary{
		Count:          len(artifacts),
		MimeTypeCounts: map[string]int{},
		Artifacts:      make([]artifactBrief, 0, len(artifacts)),
	}
	var latest *artifactBrief
	var latestTime time.Time
	for _, artifact := range artifacts {
		mimeType := strings.TrimSpace(artifact.MimeType)
		if mimeType == "" {
			mimeType = "unknown"
		}
		summary.TotalSizeBytes += artifact.SizeBytes
		summary.MimeTypeCounts[mimeType]++
		if isTextMimeType(mimeType) {
			summary.TextArtifactCount++
		}
		brief := artifactBrief{
			ID:        artifact.ID,
			Path:      artifact.Path,
			Name:      artifact.Name,
			MimeType:  mimeType,
			SizeBytes: artifact.SizeBytes,
		}
		if !artifact.CreatedAt.IsZero() {
			brief.CreatedAt = artifact.CreatedAt.UTC().Format(time.RFC3339)
		}
		summary.Artifacts = append(summary.Artifacts, brief)
		if latest == nil || artifact.CreatedAt.After(latestTime) {
			copy := brief
			latest = &copy
			latestTime = artifact.CreatedAt
		}
	}
	summary.LatestArtifact = latest
	return summary
}

func isTextMimeType(mimeType string) bool {
	mimeType = strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0]))
	if strings.HasPrefix(mimeType, "text/") {
		return true
	}
	switch mimeType {
	case "application/json", "application/xml", "application/yaml", "application/x-yaml", "application/csv", "application/javascript":
		return true
	default:
		return false
	}
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

func sandboxTimeoutSeconds(argumentsInJSON string) int {
	var parsed struct {
		TimeoutSeconds int `json:"timeout_seconds"`
	}
	_ = json.Unmarshal([]byte(argumentsInJSON), &parsed)
	if parsed.TimeoutSeconds <= 0 {
		return 10
	}
	if parsed.TimeoutSeconds > 300 {
		return 300
	}
	return parsed.TimeoutSeconds
}

func marshalToolQuotaObservation(ok bool, errorType, message, quota string) string {
	payload := map[string]any{
		"ok":         ok,
		"error_type": errorType,
		"message":    message,
	}
	if quota != "" {
		payload["quota"] = quota
	}
	body, _ := json.Marshal(payload)
	return string(body)
}

type skillRiskPolicyDecision struct {
	Allowed bool
	Policy  SkillRiskPolicy
	Reason  string
}

func evaluateSkillRiskPolicy(policy SkillRiskPolicy, skill protocol.Skill) skillRiskPolicyDecision {
	if policy == "" {
		policy = SkillRiskPolicyAllow
	}
	switch policy {
	case SkillRiskPolicyAllow:
		return skillRiskPolicyDecision{Allowed: true, Policy: policy}
	case SkillRiskPolicyBlockHigh:
		if skill.Risk == protocol.SkillRiskHigh {
			return skillRiskPolicyDecision{Policy: policy, Reason: "skill risk is high"}
		}
	case SkillRiskPolicyBlockDestructive:
		if skillAnnotations(skill).DestructiveHint {
			return skillRiskPolicyDecision{Policy: policy, Reason: "skill is marked destructive"}
		}
	case SkillRiskPolicyReadOnly:
		annotations := skillAnnotations(skill)
		if skill.Risk == protocol.SkillRiskHigh {
			return skillRiskPolicyDecision{Policy: policy, Reason: "skill risk is high"}
		}
		if annotations.DestructiveHint {
			return skillRiskPolicyDecision{Policy: policy, Reason: "skill is marked destructive"}
		}
		if !annotations.ReadOnlyHint {
			return skillRiskPolicyDecision{Policy: policy, Reason: "skill is not marked read-only"}
		}
	default:
		return skillRiskPolicyDecision{Allowed: true, Policy: SkillRiskPolicyAllow}
	}
	return skillRiskPolicyDecision{Allowed: true, Policy: policy}
}

type skillAnnotationHints struct {
	ReadOnlyHint    bool
	DestructiveHint bool
}

func skillAnnotations(skill protocol.Skill) skillAnnotationHints {
	var raw map[string]any
	_ = json.Unmarshal([]byte(skill.Annotations), &raw)
	return skillAnnotationHints{
		ReadOnlyHint:    boolAnnotation(raw, "readOnlyHint"),
		DestructiveHint: boolAnnotation(raw, "destructiveHint"),
	}
}

func boolAnnotation(values map[string]any, key string) bool {
	if values == nil {
		return false
	}
	switch value := values[key].(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(strings.TrimSpace(value), "true")
	default:
		return false
	}
}

func marshalSkillRiskPolicyObservation(decision skillRiskPolicyDecision) string {
	body, _ := json.Marshal(map[string]any{
		"ok":         false,
		"error_type": "skill_policy_denied",
		"policy":     string(decision.Policy),
		"message":    "skill invocation blocked by runtime risk policy: " + decision.Reason,
	})
	return string(body)
}

func safeMetricReason(reason string) string {
	reason = strings.TrimSpace(reason)
	switch reason {
	case "skill risk is high":
		return "high_risk"
	case "skill is marked destructive":
		return "destructive"
	case "skill is not marked read-only":
		return "not_read_only"
	default:
		return "unknown"
	}
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
	value = platform.RedactJSON(value)
	if len(value) <= 512 {
		return value
	}
	return value[:512] + "...[truncated]"
}

var _ tool.InvokableTool = (*runtimeTool)(nil)
