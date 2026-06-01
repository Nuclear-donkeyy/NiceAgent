package skillmanifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"niceagent/common/protocol"
)

const defaultMCPImportSchema = `{"type":"object","additionalProperties":true}`
const DefaultMCPSkillTimeoutSeconds = 15

type mcpToolsDocument struct {
	Tools  []mcpTool       `json:"tools"`
	Result *mcpToolsResult `json:"result,omitempty"`
}

type mcpToolsResult struct {
	Tools []mcpTool `json:"tools"`
}

type mcpTool struct {
	Name         string         `json:"name"`
	Description  string         `json:"description,omitempty"`
	InputSchema  map[string]any `json:"inputSchema,omitempty"`
	OutputSchema map[string]any `json:"outputSchema,omitempty"`
	Annotations  map[string]any `json:"annotations,omitempty"`
}

type MCPSkillRuntimeConfig struct {
	Type             string                   `json:"type,omitempty"`
	ServerURL        string                   `json:"server_url"`
	ToolName         string                   `json:"tool_name"`
	TimeoutSeconds   int                      `json:"timeout_seconds"`
	AuthType         string                   `json:"auth_type"`
	Retry            HTTPSkillRetryConfig     `json:"retry,omitempty"`
	RateLimit        HTTPSkillRateLimitConfig `json:"rate_limit,omitempty"`
	ProtocolHint     string                   `json:"protocol_hint,omitempty"`
	StructuredOutput bool                     `json:"structured_output,omitempty"`
}

func PreviewMCPTools(document string) ([]protocol.MCPSkillImportCandidate, error) {
	document = strings.TrimSpace(document)
	if document == "" {
		return nil, errors.New("mcp document is required")
	}
	var parsed mcpToolsDocument
	if err := json.Unmarshal([]byte(document), &parsed); err != nil {
		return nil, fmt.Errorf("invalid MCP JSON document: %w", err)
	}
	tools := parsed.Tools
	if len(tools) == 0 && parsed.Result != nil {
		tools = parsed.Result.Tools
	}
	if len(tools) == 0 {
		return nil, errors.New("mcp document must contain tools or result.tools")
	}
	candidates := make([]protocol.MCPSkillImportCandidate, 0, len(tools))
	seen := map[string]struct{}{}
	for _, mcpTool := range tools {
		name := strings.TrimSpace(mcpTool.Name)
		if name == "" {
			return nil, errors.New("mcp tool name is required")
		}
		if _, ok := seen[name]; ok {
			return nil, fmt.Errorf("duplicate mcp tool name %q", name)
		}
		seen[name] = struct{}{}
		inputSchema, err := marshalMCPMapOrDefault(mcpTool.InputSchema, defaultMCPImportSchema)
		if err != nil {
			return nil, fmt.Errorf("invalid inputSchema for mcp tool %q: %w", name, err)
		}
		outputSchema, err := marshalMCPMap(mcpTool.OutputSchema)
		if err != nil {
			return nil, fmt.Errorf("invalid outputSchema for mcp tool %q: %w", name, err)
		}
		annotations, err := marshalMCPMap(mcpTool.Annotations)
		if err != nil {
			return nil, fmt.Errorf("invalid annotations for mcp tool %q: %w", name, err)
		}
		candidates = append(candidates, protocol.MCPSkillImportCandidate{
			Name:         name,
			Description:  strings.TrimSpace(mcpTool.Description),
			InputSchema:  inputSchema,
			OutputSchema: outputSchema,
			Annotations:  annotations,
			ReadOnlyHint: annotationBool(mcpTool.Annotations, "readOnlyHint"),
			Destructive:  annotationBool(mcpTool.Annotations, "destructiveHint"),
			Idempotent:   annotationBool(mcpTool.Annotations, "idempotentHint"),
			OpenWorld:    annotationBool(mcpTool.Annotations, "openWorldHint"),
		})
	}
	return candidates, nil
}

func BuildMCPSkillInput(input protocol.MCPImportCreateInput) (protocol.Skill, protocol.MCPSkillImportCandidate, protocol.RuntimeSecret, bool, error) {
	candidates, err := PreviewMCPTools(input.Document)
	if err != nil {
		return protocol.Skill{}, protocol.MCPSkillImportCandidate{}, protocol.RuntimeSecret{}, false, err
	}
	candidate, err := selectMCPImportCandidate(candidates, input.ToolName)
	if err != nil {
		return protocol.Skill{}, protocol.MCPSkillImportCandidate{}, protocol.RuntimeSecret{}, false, err
	}
	cfg, err := NewMCPSkillRuntimeConfig(input, candidate)
	if err != nil {
		return protocol.Skill{}, protocol.MCPSkillImportCandidate{}, protocol.RuntimeSecret{}, false, err
	}
	if strings.TrimSpace(input.BearerToken) != "" && strings.TrimSpace(input.BearerTokenSecretRef) != "" {
		return protocol.Skill{}, protocol.MCPSkillImportCandidate{}, protocol.RuntimeSecret{}, false, errors.New("bearer_token and bearer_token_secret_ref cannot both be set")
	}
	if cfg.AuthType == "bearer" && strings.TrimSpace(input.BearerToken) == "" && strings.TrimSpace(input.BearerTokenSecretRef) == "" {
		return protocol.Skill{}, protocol.MCPSkillImportCandidate{}, protocol.RuntimeSecret{}, false, errors.New("bearer_token or bearer_token_secret_ref is required when auth_type is bearer")
	}
	if err := ValidateJSONSchema(candidate.InputSchema); err != nil {
		return protocol.Skill{}, protocol.MCPSkillImportCandidate{}, protocol.RuntimeSecret{}, false, fmt.Errorf("input_schema %w", err)
	}
	if strings.TrimSpace(candidate.OutputSchema) != "" {
		if err := ValidateJSONSchema(candidate.OutputSchema); err != nil {
			return protocol.Skill{}, protocol.MCPSkillImportCandidate{}, protocol.RuntimeSecret{}, false, fmt.Errorf("output_schema %w", err)
		}
	}
	name := firstNonEmpty(input.Name, candidate.Name)
	description := firstNonEmpty(input.Description, candidate.Description, "Imported MCP tool.")
	skill := protocol.Skill{
		Name:          name,
		Version:       "1.0.0",
		Description:   description,
		Risk:          mcpRisk(candidate),
		RequiresAuth:  cfg.AuthType == "bearer",
		InputSchema:   candidate.InputSchema,
		OutputSchema:  candidate.OutputSchema,
		Annotations:   candidate.Annotations,
		RuntimeConfig: cfg.JSONString(),
		Enabled:       true,
	}
	secret := protocol.RuntimeSecret{EncryptedValue: strings.TrimSpace(input.BearerToken)}
	if secret.EncryptedValue == "" {
		secret.SecretRef = strings.TrimSpace(input.BearerTokenSecretRef)
	}
	return skill, candidate, secret, secret.EncryptedValue != "" || secret.SecretRef != "", nil
}

func NewMCPSkillRuntimeConfig(input protocol.MCPImportCreateInput, candidate protocol.MCPSkillImportCandidate) (MCPSkillRuntimeConfig, error) {
	cfg := MCPSkillRuntimeConfig{
		Type:           "mcp",
		ServerURL:      strings.TrimSpace(input.ServerURL),
		ToolName:       strings.TrimSpace(firstNonEmpty(input.ToolName, candidate.Name)),
		TimeoutSeconds: input.TimeoutSeconds,
		AuthType:       strings.TrimSpace(input.AuthType),
		Retry: HTTPSkillRetryConfig{
			MaxAttempts: input.RetryMaxAttempts,
		},
		RateLimit: HTTPSkillRateLimitConfig{
			RequestsPerMinute: input.RateLimitPerMinute,
		},
		ProtocolHint:     "http-json-rpc-tools-call",
		StructuredOutput: strings.TrimSpace(candidate.OutputSchema) != "",
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = DefaultMCPSkillTimeoutSeconds
	}
	if cfg.AuthType == "" {
		cfg.AuthType = "none"
	}
	cfg.Retry = normalizeHTTPRetryConfig(cfg.Retry)
	if err := cfg.Validate(); err != nil {
		return MCPSkillRuntimeConfig{}, err
	}
	return cfg, nil
}

func ParseMCPSkillRuntimeConfig(raw string) (MCPSkillRuntimeConfig, error) {
	if strings.TrimSpace(raw) == "" {
		return MCPSkillRuntimeConfig{}, errors.New("runtime_config is required")
	}
	var cfg MCPSkillRuntimeConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return MCPSkillRuntimeConfig{}, fmt.Errorf("invalid runtime_config json: %w", err)
	}
	cfg.ServerURL = strings.TrimSpace(cfg.ServerURL)
	cfg.ToolName = strings.TrimSpace(cfg.ToolName)
	cfg.AuthType = strings.TrimSpace(cfg.AuthType)
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = DefaultMCPSkillTimeoutSeconds
	}
	if cfg.AuthType == "" {
		cfg.AuthType = "none"
	}
	cfg.Retry = normalizeHTTPRetryConfig(cfg.Retry)
	if err := cfg.Validate(); err != nil {
		return MCPSkillRuntimeConfig{}, err
	}
	return cfg, nil
}

func (c MCPSkillRuntimeConfig) Validate() error {
	if c.Type != "" && c.Type != "mcp" {
		return errors.New("runtime_config.type must be mcp")
	}
	if c.ToolName == "" {
		return errors.New("tool_name is required")
	}
	parsed, err := url.Parse(c.ServerURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return errors.New("valid server_url is required")
	}
	if parsed.Scheme != "https" {
		return errors.New("server_url scheme must be https")
	}
	if parsed.User != nil {
		return errors.New("server_url must not include credentials")
	}
	if c.TimeoutSeconds <= 0 || c.TimeoutSeconds > MaxHTTPSkillTimeoutSeconds {
		return fmt.Errorf("timeout_seconds must be between 1 and %d", MaxHTTPSkillTimeoutSeconds)
	}
	switch c.AuthType {
	case "none", "bearer":
	default:
		return errors.New("auth_type must be none or bearer")
	}
	if c.Retry.MaxAttempts <= 0 || c.Retry.MaxAttempts > MaxHTTPRetryAttempts {
		return fmt.Errorf("retry.max_attempts must be between 1 and %d", MaxHTTPRetryAttempts)
	}
	if c.RateLimit.RequestsPerMinute < 0 || c.RateLimit.RequestsPerMinute > MaxHTTPRateLimitPerMinute {
		return fmt.Errorf("rate_limit.requests_per_minute must be between 0 and %d", MaxHTTPRateLimitPerMinute)
	}
	return nil
}

func (c MCPSkillRuntimeConfig) JSONString() string {
	data, _ := json.Marshal(c)
	return string(data)
}

func selectMCPImportCandidate(candidates []protocol.MCPSkillImportCandidate, toolName string) (protocol.MCPSkillImportCandidate, error) {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return protocol.MCPSkillImportCandidate{}, errors.New("tool_name is required")
	}
	for _, candidate := range candidates {
		if candidate.Name == toolName {
			return candidate, nil
		}
	}
	return protocol.MCPSkillImportCandidate{}, errors.New("selected MCP tool was not found")
}

func mcpRisk(candidate protocol.MCPSkillImportCandidate) protocol.SkillRisk {
	if candidate.Destructive {
		return protocol.SkillRiskHigh
	}
	if candidate.OpenWorld {
		return protocol.SkillRiskMedium
	}
	return protocol.SkillRiskLow
}

func marshalMCPMap(value map[string]any) (string, error) {
	if len(value) == 0 {
		return "", nil
	}
	body, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func marshalMCPMapOrDefault(value map[string]any, fallback string) (string, error) {
	if len(value) == 0 {
		return fallback, nil
	}
	return marshalMCPMap(value)
}

func annotationBool(annotations map[string]any, key string) bool {
	value, ok := annotations[key]
	if !ok {
		return false
	}
	typed, ok := value.(bool)
	return ok && typed
}
