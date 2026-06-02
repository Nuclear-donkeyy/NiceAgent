package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"niceagent/common/protocol"
)

func TestMCPSkillCallsJSONRPCTool(t *testing.T) {
	runtimeTool := newTestMCPSkillTool(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != "https://mcp.example.com/rpc" {
			t.Fatalf("url = %s", req.URL.String())
		}
		var body mcpCallRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body.Method != "tools/call" || body.Params.Name != "weather.lookup" {
			t.Fatalf("request = %#v", body)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`{
				"jsonrpc":"2.0",
				"id":"niceagent-run-mcp-skill",
				"result":{"structuredContent":{"summary":"sunny"},"content":[{"type":"text","text":"sunny"}]}
			}`)),
			Header: make(http.Header),
		}, nil
	}))

	output, ok, err := runtimeTool.invokeMCP(context.Background(), `{"city":"Beijing"}`)
	if err != nil {
		t.Fatalf("invoke mcp skill: %v", err)
	}
	if !ok {
		t.Fatalf("ok = false, output = %s", output)
	}
	observation := decodeMCPObservation(t, output)
	if !observation.OK || observation.StatusCode != http.StatusOK {
		t.Fatalf("observation = %#v", observation)
	}
	if !strings.Contains(output, "sunny") {
		t.Fatalf("output = %s", output)
	}
}

func TestMCPSkillReturnsJSONRPCErrorAndRedactsSecret(t *testing.T) {
	runtimeTool := newTestMCPSkillTool(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get("Authorization"); got != "Bearer secret-token" {
			t.Fatalf("authorization = %q, want bearer token", got)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`{
				"jsonrpc":"2.0",
				"id":"niceagent-run-mcp-skill",
				"error":{"code":-32000,"message":"secret-token failed","data":{"token":"secret-token"}}
			}`)),
			Header: make(http.Header),
		}, nil
	}))
	runtimeTool.runtimeSkill.Skill.RuntimeConfig = `{"type":"mcp","server_url":"https://mcp.example.com/rpc","tool_name":"weather.lookup","timeout_seconds":15,"auth_type":"bearer"}`
	runtimeTool.runtimeSkill.SecretMaterials = map[string]protocol.RuntimeSecret{
		"bearer_token": {EncryptedValue: "secret-token"},
	}

	output, ok, err := runtimeTool.invokeMCP(context.Background(), `{"city":"Beijing"}`)
	if err != nil {
		t.Fatalf("invoke mcp skill: %v", err)
	}
	if ok {
		t.Fatal("ok = true, want false")
	}
	if strings.Contains(output, "secret-token") {
		t.Fatalf("output leaked secret: %s", output)
	}
	observation := decodeMCPObservation(t, output)
	if observation.OK || observation.ErrorType != "mcp_error" {
		t.Fatalf("observation = %#v, want mcp_error", observation)
	}
}

func TestMCPSkillRejectsInvalidArgumentsWithoutRequest(t *testing.T) {
	var calls int
	runtimeTool := newTestMCPSkillTool(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		t.Fatalf("unexpected request to %s", req.URL.String())
		return nil, nil
	}))

	output, ok, err := runtimeTool.invokeMCP(context.Background(), `{"country":"CN"}`)
	if err != nil {
		t.Fatalf("invoke mcp skill: %v", err)
	}
	if ok {
		t.Fatal("ok = true, want false")
	}
	observation := decodeMCPObservation(t, output)
	if observation.OK || observation.ErrorType != "invalid_arguments" {
		t.Fatalf("observation = %#v, want invalid_arguments", observation)
	}
	if calls != 0 {
		t.Fatalf("calls = %d, want 0", calls)
	}
}

func TestMCPSkillRateLimitReturnsObservationWithoutRequest(t *testing.T) {
	var calls int
	runtimeTool := newTestMCPSkillTool(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`{
				"jsonrpc":"2.0",
				"id":"niceagent-run-mcp-skill",
				"result":{"structuredContent":{"summary":"sunny"}}
			}`)),
			Header: make(http.Header),
		}, nil
	}))
	runtimeTool.runtimeSkill.Skill.RuntimeConfig = `{
		"type":"mcp",
		"server_url":"https://mcp.example.com/rpc",
		"tool_name":"weather.lookup",
		"timeout_seconds":15,
		"auth_type":"none",
		"rate_limit":{"requests_per_minute":1}
	}`

	output, ok, err := runtimeTool.invokeMCP(context.Background(), `{"city":"Beijing"}`)
	if err != nil || !ok {
		t.Fatalf("first invoke = ok:%v output:%s err:%v", ok, output, err)
	}
	output, ok, err = runtimeTool.invokeMCP(context.Background(), `{"city":"Beijing"}`)
	if err != nil {
		t.Fatalf("second invoke mcp skill: %v", err)
	}
	if ok {
		t.Fatal("second ok = true, want false")
	}
	observation := decodeMCPObservation(t, output)
	if observation.OK || observation.ErrorType != "rate_limited" {
		t.Fatalf("observation = %#v, want rate_limited", observation)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want only first request", calls)
	}
	renderedMetrics := runtimeTool.bridge.Metrics.Render()
	if !strings.Contains(renderedMetrics, `niceagent_skill_rate_limit_denials_total{service="agent_runtime_tool_test",kind="mcp",mode="local"} 1`) {
		t.Fatalf("metrics body = %s", renderedMetrics)
	}
}

func newTestMCPSkillTool(transport http.RoundTripper) *runtimeTool {
	runtimeTool := newTestHTTPSkillTool(transport)
	runtimeTool.runID = "run-mcp-skill"
	runtimeTool.runtimeSkill.Skill = protocol.Skill{
		ID:           "skill_weather_mcp",
		Name:         "Weather MCP",
		Description:  "Fetch weather through MCP",
		Scope:        protocol.SkillScopeUser,
		Kind:         protocol.SkillKindMCP,
		Enabled:      true,
		InputSchema:  `{"type":"object","required":["city"],"properties":{"city":{"type":"string"}},"additionalProperties":false}`,
		OutputSchema: `{"type":"object","properties":{"summary":{"type":"string"}}}`,
		RuntimeConfig: `{
			"type":"mcp",
			"server_url":"https://mcp.example.com/rpc",
			"tool_name":"weather.lookup",
			"timeout_seconds":15,
			"auth_type":"none"
		}`,
	}
	return runtimeTool
}

func decodeMCPObservation(t *testing.T, output string) mcpSkillObservation {
	t.Helper()
	var observation mcpSkillObservation
	if err := json.Unmarshal([]byte(output), &observation); err != nil {
		t.Fatalf("decode mcp observation %s: %v", output, err)
	}
	return observation
}
