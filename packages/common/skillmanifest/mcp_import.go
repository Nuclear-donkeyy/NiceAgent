package skillmanifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"niceagent/common/protocol"
)

const defaultMCPImportSchema = `{"type":"object","additionalProperties":true}`

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
