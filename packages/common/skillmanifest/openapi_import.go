package skillmanifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
	"niceagent/common/protocol"
)

const defaultOpenAPIImportSchema = `{"type":"object","additionalProperties":true}`

type openAPIDocument struct {
	OpenAPI    string                                 `json:"openapi"`
	Servers    []openAPIServer                        `json:"servers"`
	Paths      map[string]map[string]openAPIOperation `json:"paths"`
	Components openAPIComponents                      `json:"components"`
	Security   []map[string][]string                  `json:"security"`
}

type openAPIServer struct {
	URL string `json:"url"`
}

type openAPIComponents struct {
	SecuritySchemes map[string]openAPISecurityScheme `json:"securitySchemes"`
}

type openAPISecurityScheme struct {
	Type   string `json:"type"`
	Scheme string `json:"scheme"`
	In     string `json:"in"`
	Name   string `json:"name"`
}

type openAPIOperation struct {
	OperationID string                     `json:"operationId"`
	Summary     string                     `json:"summary"`
	Description string                     `json:"description"`
	Parameters  []openAPIParameter         `json:"parameters"`
	RequestBody openAPIRequestBody         `json:"requestBody"`
	Responses   map[string]openAPIResponse `json:"responses"`
	Security    []map[string][]string      `json:"security"`
}

type openAPIParameter struct {
	Name        string          `json:"name"`
	In          string          `json:"in"`
	Required    bool            `json:"required"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
}

type openAPIRequestBody struct {
	Content map[string]openAPIMediaType `json:"content"`
}

type openAPIResponse struct {
	Content map[string]openAPIMediaType `json:"content"`
}

type openAPIMediaType struct {
	Schema json.RawMessage `json:"schema"`
}

func PreviewOpenAPIHTTPSkills(document, baseURL string) ([]protocol.HTTPSkillImportCandidate, error) {
	if strings.TrimSpace(document) == "" {
		return nil, errors.New("document is required")
	}
	spec, err := decodeOpenAPIDocument(document)
	if err != nil {
		return nil, err
	}
	if len(spec.Paths) == 0 {
		return nil, errors.New("openapi paths are required")
	}
	targetBaseURL, err := openAPIBaseURL(spec, baseURL)
	if err != nil {
		return nil, err
	}

	paths := make([]string, 0, len(spec.Paths))
	for path := range spec.Paths {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	candidates := []protocol.HTTPSkillImportCandidate{}
	for _, path := range paths {
		operations := spec.Paths[path]
		methods := map[string]string{}
		methodNames := make([]string, 0, len(operations))
		for method := range operations {
			normalized := strings.ToUpper(method)
			methods[normalized] = method
			methodNames = append(methodNames, normalized)
		}
		sort.Strings(methodNames)
		for _, method := range methodNames {
			if method != http.MethodGet && method != http.MethodPost {
				continue
			}
			operation := operations[methods[method]]
			candidate, err := openAPICandidate(spec, targetBaseURL, path, method, operation)
			if err != nil {
				return nil, err
			}
			candidates = append(candidates, candidate)
		}
	}
	if len(candidates) == 0 {
		return nil, errors.New("no supported GET or POST operations found")
	}
	return candidates, nil
}

func decodeOpenAPIDocument(document string) (openAPIDocument, error) {
	var spec openAPIDocument
	if err := json.Unmarshal([]byte(document), &spec); err == nil {
		return spec, nil
	}
	var raw any
	if err := yaml.Unmarshal([]byte(document), &raw); err != nil {
		return openAPIDocument{}, fmt.Errorf("invalid openapi document: %w", err)
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return openAPIDocument{}, fmt.Errorf("invalid openapi yaml: %w", err)
	}
	if err := json.Unmarshal(data, &spec); err != nil {
		return openAPIDocument{}, fmt.Errorf("invalid openapi yaml: %w", err)
	}
	return spec, nil
}

func openAPIBaseURL(spec openAPIDocument, override string) (string, error) {
	base := strings.TrimSpace(override)
	if base == "" && len(spec.Servers) > 0 {
		base = strings.TrimSpace(spec.Servers[0].URL)
	}
	if base == "" {
		return "", errors.New("base_url or OpenAPI servers[0].url is required")
	}
	base = strings.TrimRight(base, "/")
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("valid https base_url is required")
	}
	if parsed.Scheme != "https" {
		return "", errors.New("base_url scheme must be https")
	}
	if parsed.User != nil {
		return "", errors.New("base_url must not include credentials")
	}
	return base, nil
}

func openAPICandidate(spec openAPIDocument, baseURL, path, method string, operation openAPIOperation) (protocol.HTTPSkillImportCandidate, error) {
	authType, requiresSecret, securityScheme, unsupportedAuth := openAPIAuthHint(spec, operation)
	inputSchema, err := openAPIInputSchema(operation)
	if err != nil {
		return protocol.HTTPSkillImportCandidate{}, err
	}
	outputSchema, err := compactRawJSON(openAPIOutputSchema(operation))
	if err != nil {
		return protocol.HTTPSkillImportCandidate{}, fmt.Errorf("invalid output schema for %s %s: %w", method, path, err)
	}
	if outputSchema == "" {
		outputSchema = defaultOpenAPIImportSchema
	}
	return protocol.HTTPSkillImportCandidate{
		Name:            openAPIName(method, path, operation),
		Description:     firstNonEmpty(operation.Summary, operation.Description),
		Method:          method,
		URL:             baseURL + path,
		Path:            path,
		OperationID:     strings.TrimSpace(operation.OperationID),
		InputSchema:     inputSchema,
		OutputSchema:    outputSchema,
		AuthType:        authType,
		RequiresSecret:  requiresSecret,
		SecurityScheme:  securityScheme,
		UnsupportedAuth: unsupportedAuth,
	}, nil
}

func openAPIName(method, path string, operation openAPIOperation) string {
	if value := strings.TrimSpace(operation.OperationID); value != "" {
		return value
	}
	if value := strings.TrimSpace(operation.Summary); value != "" {
		return value
	}
	return method + " " + path
}

func openAPIInputSchema(operation openAPIOperation) (string, error) {
	if raw := schemaFromContent(operation.RequestBody.Content); len(raw) > 0 {
		compact, err := compactRawJSON(raw)
		if err != nil {
			return "", fmt.Errorf("invalid request schema: %w", err)
		}
		return compact, nil
	}
	if len(operation.Parameters) == 0 {
		return defaultOpenAPIImportSchema, nil
	}
	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
	required := []string{}
	properties := schema["properties"].(map[string]any)
	for _, parameter := range operation.Parameters {
		name := strings.TrimSpace(parameter.Name)
		if name == "" {
			continue
		}
		property := map[string]any{}
		if len(parameter.Schema) > 0 {
			var parsed any
			if err := json.Unmarshal(parameter.Schema, &parsed); err != nil {
				return "", fmt.Errorf("invalid parameter schema for %s: %w", name, err)
			}
			if parsedMap, ok := parsed.(map[string]any); ok {
				property = parsedMap
			}
		}
		if parameter.Description != "" {
			property["description"] = parameter.Description
		}
		if parameter.In != "" {
			property["x-openapi-in"] = parameter.In
		}
		if len(property) == 0 {
			property["type"] = "string"
		}
		properties[name] = property
		if parameter.Required {
			required = append(required, name)
		}
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	data, _ := json.Marshal(schema)
	return string(data), nil
}

func openAPIOutputSchema(operation openAPIOperation) json.RawMessage {
	codes := make([]string, 0, len(operation.Responses))
	for code := range operation.Responses {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	for _, code := range codes {
		if !strings.HasPrefix(code, "2") {
			continue
		}
		if raw := schemaFromContent(operation.Responses[code].Content); len(raw) > 0 {
			return raw
		}
	}
	return nil
}

func schemaFromContent(content map[string]openAPIMediaType) json.RawMessage {
	if len(content) == 0 {
		return nil
	}
	for _, mediaType := range []string{"application/json", "application/*+json"} {
		if item, ok := content[mediaType]; ok && len(item.Schema) > 0 {
			return item.Schema
		}
	}
	for mediaType, item := range content {
		if strings.Contains(mediaType, "json") && len(item.Schema) > 0 {
			return item.Schema
		}
	}
	return nil
}

func openAPIAuthHint(spec openAPIDocument, operation openAPIOperation) (string, bool, string, bool) {
	security := operation.Security
	if security == nil {
		security = spec.Security
	}
	for _, requirement := range security {
		for name := range requirement {
			scheme := spec.Components.SecuritySchemes[name]
			if strings.EqualFold(scheme.Type, "http") && strings.EqualFold(scheme.Scheme, "bearer") {
				return "bearer", true, name, false
			}
			if name != "" {
				return "none", true, name, true
			}
		}
	}
	return "none", false, "", false
}

func compactRawJSON(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
