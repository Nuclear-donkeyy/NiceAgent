package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"niceagent/common/platform"
	"niceagent/common/protocol"
	"niceagent/common/skillmanifest"
)

type mcpSkillObservation struct {
	OK         bool   `json:"ok"`
	StatusCode int    `json:"status_code"`
	ErrorType  string `json:"error_type,omitempty"`
	Message    string `json:"message,omitempty"`
	Data       any    `json:"data,omitempty"`
}

type mcpCallRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      string        `json:"id"`
	Method  string        `json:"method"`
	Params  mcpCallParams `json:"params"`
}

type mcpCallParams struct {
	Name      string `json:"name"`
	Arguments any    `json:"arguments"`
}

type mcpCallResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      any              `json:"id,omitempty"`
	Result  *mcpCallResult   `json:"result,omitempty"`
	Error   *mcpJSONRPCError `json:"error,omitempty"`
}

type mcpCallResult struct {
	Content           any  `json:"content,omitempty"`
	StructuredContent any  `json:"structuredContent,omitempty"`
	IsError           bool `json:"isError,omitempty"`
}

type mcpJSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (t *runtimeTool) invokeMCP(ctx context.Context, argumentsInJSON string) (string, bool, error) {
	skill := t.runtimeSkill.Skill
	if err := skillmanifest.ValidateJSONDocument(skill.InputSchema, argumentsInJSON); err != nil {
		return marshalMCPSkillObservation(mcpSkillObservation{
			OK:        false,
			ErrorType: "invalid_arguments",
			Message:   "MCP skill arguments do not match input_schema: " + err.Error(),
		}), false, nil
	}
	cfg, err := skillmanifest.ParseMCPSkillRuntimeConfig(skill.RuntimeConfig)
	if err != nil {
		return marshalMCPSkillObservation(mcpSkillObservation{
			OK:        false,
			ErrorType: "invalid_runtime_config",
			Message:   err.Error(),
		}), false, nil
	}
	if err := rejectUnsafeHTTPURL(cfg.ServerURL, t.bridge.AllowLocalHTTP); err != nil {
		return marshalMCPSkillObservation(mcpSkillObservation{
			OK:        false,
			ErrorType: "ssrf_rejected",
			Message:   err.Error(),
		}), false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(cfg.TimeoutSeconds)*time.Second)
	defer cancel()
	if err := rejectUnsafeResolvedHTTPHost(ctx, t.bridge.Resolver, cfg.ServerURL, t.bridge.AllowLocalHTTP); err != nil {
		errorType := "upstream_dns"
		if errors.Is(err, ErrUnsafeResolvedHost) {
			errorType = "ssrf_rejected"
		}
		return marshalMCPSkillObservation(mcpSkillObservation{
			OK:        false,
			ErrorType: errorType,
			Message:   err.Error(),
		}), false, nil
	}
	if !t.allowMCPSkill(ctx, cfg) {
		t.recordRateLimitDenial(protocol.SkillKindMCP)
		return marshalMCPSkillObservation(mcpSkillObservation{
			OK:        false,
			ErrorType: "rate_limited",
			Message:   fmt.Sprintf("MCP skill rate limit exceeded: %d requests per minute", cfg.RateLimit.RequestsPerMinute),
		}), false, nil
	}

	var args any = map[string]any{}
	if strings.TrimSpace(argumentsInJSON) != "" {
		if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
			return marshalMCPSkillObservation(mcpSkillObservation{
				OK:        false,
				ErrorType: "invalid_arguments",
				Message:   "MCP skill arguments are not valid JSON: " + err.Error(),
			}), false, nil
		}
	}

	var secretValues []string
	var bearerToken string
	if cfg.AuthType == "bearer" {
		bearerToken, err = t.resolveSecret(ctx, "bearer_token")
		if err != nil {
			return marshalMCPSkillObservation(mcpSkillObservation{
				OK:        false,
				ErrorType: "secret_unresolved",
				Message:   "MCP skill bearer token is not available: " + err.Error(),
			}), false, nil
		}
		secretValues = append(secretValues, bearerToken)
	}

	requestBody, _ := json.Marshal(mcpCallRequest{
		JSONRPC: "2.0",
		ID:      "niceagent-" + t.runID,
		Method:  "tools/call",
		Params:  mcpCallParams{Name: cfg.ToolName, Arguments: args},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.ServerURL, bytes.NewReader(requestBody))
	if err != nil {
		return marshalMCPSkillObservation(mcpSkillObservation{
			OK:        false,
			ErrorType: "invalid_runtime_config",
			Message:   err.Error(),
		}), false, nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	platform.InjectTraceHeaders(ctx, req.Header)

	ctx, endSpan := platform.StartSpan(ctx, "niceagent/agent_runtime", "tool.mcp_skill.request", platform.Labels{
		"run_id":   t.runID,
		"skill_id": skill.ID,
		"host":     safeURLHost(cfg.ServerURL),
		"mcp_tool": cfg.ToolName,
	})
	resp, err := httpSkillClient(t.bridge.Client, t.bridge.Resolver, t.bridge.AllowLocalHTTP).Do(req)
	if err != nil {
		errorType := classifyHTTPClientError(ctx, err)
		endSpan(err, platform.Labels{"error_type": errorType})
		return marshalMCPSkillObservation(mcpSkillObservation{
			OK:        false,
			ErrorType: errorType,
			Message:   "MCP skill request failed: " + redactError(err, secretValues),
		}), false, nil
	}
	defer resp.Body.Close()

	body, tooLarge, readErr := readLimitedResponse(resp.Body, maxHTTPSkillResponseBytes)
	body = redactHTTPBody(body, secretValues)
	spanLabels := platform.Labels{"http_status": fmt.Sprint(resp.StatusCode)}
	if readErr != nil {
		endSpan(readErr, spanLabels)
		return marshalMCPSkillObservation(mcpSkillObservation{
			OK:         false,
			StatusCode: resp.StatusCode,
			ErrorType:  "upstream_read_error",
			Message:    "MCP response could not be read: " + redactError(readErr, secretValues),
		}), false, nil
	}
	if tooLarge {
		err := fmt.Errorf("MCP response exceeded %d bytes", maxHTTPSkillResponseBytes)
		endSpan(err, spanLabels)
		return marshalMCPSkillObservation(mcpSkillObservation{
			OK:         false,
			StatusCode: resp.StatusCode,
			ErrorType:  "response_too_large",
			Message:    err.Error(),
		}), false, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("MCP server returned status %d", resp.StatusCode)
		endSpan(err, spanLabels)
		return marshalMCPSkillObservation(mcpSkillObservation{
			OK:         false,
			StatusCode: resp.StatusCode,
			ErrorType:  "upstream_status",
			Message:    err.Error(),
			Data:       observationData(body),
		}), false, nil
	}

	var decoded mcpCallResponse
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		endSpan(err, spanLabels)
		return marshalMCPSkillObservation(mcpSkillObservation{
			OK:         false,
			StatusCode: resp.StatusCode,
			ErrorType:  "invalid_response",
			Message:    "MCP response is not valid JSON-RPC: " + err.Error(),
			Data:       observationData(body),
		}), false, nil
	}
	if decoded.Error != nil {
		err := fmt.Errorf("MCP JSON-RPC error %d: %s", decoded.Error.Code, decoded.Error.Message)
		endSpan(err, spanLabels)
		return marshalMCPSkillObservation(mcpSkillObservation{
			OK:         false,
			StatusCode: resp.StatusCode,
			ErrorType:  "mcp_error",
			Message:    err.Error(),
			Data:       platform.RedactValue(decoded.Error.Data),
		}), false, nil
	}
	if decoded.Result == nil {
		err := errors.New("MCP response missing result")
		endSpan(err, spanLabels)
		return marshalMCPSkillObservation(mcpSkillObservation{
			OK:         false,
			StatusCode: resp.StatusCode,
			ErrorType:  "invalid_response",
			Message:    err.Error(),
		}), false, nil
	}
	if strings.TrimSpace(skill.OutputSchema) != "" && decoded.Result.StructuredContent != nil {
		structuredBody, _ := json.Marshal(decoded.Result.StructuredContent)
		if err := skillmanifest.ValidateJSONDocument(skill.OutputSchema, string(structuredBody)); err != nil {
			endSpan(err, spanLabels)
			return marshalMCPSkillObservation(mcpSkillObservation{
				OK:         false,
				StatusCode: resp.StatusCode,
				ErrorType:  "invalid_output",
				Message:    "MCP structuredContent does not match output_schema: " + err.Error(),
				Data:       platform.RedactValue(decoded.Result),
			}), false, nil
		}
	}
	ok := !decoded.Result.IsError
	endSpan(nil, spanLabels)
	return marshalMCPSkillObservation(mcpSkillObservation{
		OK:         ok,
		StatusCode: resp.StatusCode,
		ErrorType:  mcpResultErrorType(decoded.Result),
		Message:    mcpResultMessage(decoded.Result),
		Data:       platform.RedactValue(decoded.Result),
	}), ok, nil
}

func (t *runtimeTool) allowMCPSkill(ctx context.Context, cfg skillmanifest.MCPSkillRuntimeConfig) bool {
	if cfg.RateLimit.RequestsPerMinute <= 0 {
		return true
	}
	limiter := t.bridge.RateLimiter
	if limiter == nil {
		return true
	}
	key := t.runtimeSkill.Skill.ID
	if key == "" {
		key = safeURLHost(cfg.ServerURL)
	}
	return limiter.Allow(ctx, key, cfg.RateLimit.RequestsPerMinute)
}

func mcpResultErrorType(result *mcpCallResult) string {
	if result != nil && result.IsError {
		return "mcp_tool_error"
	}
	return ""
}

func mcpResultMessage(result *mcpCallResult) string {
	if result != nil && result.IsError {
		return "MCP tool returned an error result."
	}
	return ""
}

func marshalMCPSkillObservation(observation mcpSkillObservation) string {
	data, _ := json.Marshal(observation)
	return string(data)
}
