package tools

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"niceagent/common/protocol"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type resolverFunc func(context.Context, string) ([]net.IPAddr, error)

func (f resolverFunc) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return f(ctx, host)
}

func TestHTTPSkillRejectsInvalidArgumentsWithoutRequest(t *testing.T) {
	var calls int
	runtimeTool := newTestHTTPSkillTool(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		t.Fatalf("unexpected request to %s", req.URL.String())
		return nil, nil
	}))
	runtimeTool.runtimeSkill.Skill.InputSchema = `{"type":"object","required":["query"],"properties":{"query":{"type":"string"}},"additionalProperties":false}`

	output, ok, err := runtimeTool.invokeHTTP(context.Background(), `{"city":"Beijing"}`)
	if err != nil {
		t.Fatalf("invoke http skill: %v", err)
	}
	if ok {
		t.Fatal("ok = true, want false")
	}
	observation := decodeObservation(t, output)
	if observation.OK || observation.ErrorType != "invalid_arguments" {
		t.Fatalf("observation = %#v, want invalid_arguments", observation)
	}
	if calls != 0 {
		t.Fatalf("calls = %d, want 0", calls)
	}
}

func TestHTTPSkillReturnsStructuredErrorAndRedactsSecret(t *testing.T) {
	runtimeTool := newTestHTTPSkillTool(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get("Authorization"); got != "Bearer secret-token" {
			t.Fatalf("authorization = %q, want bearer token", got)
		}
		return &http.Response{
			StatusCode: http.StatusBadGateway,
			Body:       io.NopCloser(strings.NewReader(`{"token":"secret-token","message":"secret-token failed"}`)),
			Header:     make(http.Header),
		}, nil
	}))
	runtimeTool.runtimeSkill.Skill.RuntimeConfig = `{"type":"http","method":"POST","url":"https://api.example.com/weather","timeout_seconds":15,"auth_type":"bearer"}`
	runtimeTool.runtimeSkill.SecretMaterials = map[string]protocol.RuntimeSecret{
		"bearer_token": {EncryptedValue: "secret-token"},
	}

	output, ok, err := runtimeTool.invokeHTTP(context.Background(), `{"query":"weather"}`)
	if err != nil {
		t.Fatalf("invoke http skill: %v", err)
	}
	if ok {
		t.Fatal("ok = true, want false")
	}
	if strings.Contains(output, "secret-token") {
		t.Fatalf("output leaked secret: %s", output)
	}
	observation := decodeObservation(t, output)
	if observation.OK || observation.ErrorType != "upstream_status" || observation.StatusCode != http.StatusBadGateway {
		t.Fatalf("observation = %#v, want upstream_status 502", observation)
	}
}

func TestHTTPSkillSecretRefPlaceholderDoesNotRequest(t *testing.T) {
	var calls int
	runtimeTool := newTestHTTPSkillTool(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		t.Fatalf("unexpected request to %s", req.URL.String())
		return nil, nil
	}))
	runtimeTool.runtimeSkill.Skill.RuntimeConfig = `{"type":"http","method":"POST","url":"https://api.example.com/weather","timeout_seconds":15,"auth_type":"bearer"}`
	runtimeTool.runtimeSkill.SecretMaterials = map[string]protocol.RuntimeSecret{
		"bearer_token": {SecretRef: "vault://niceagent/weather"},
	}

	output, ok, err := runtimeTool.invokeHTTP(context.Background(), `{"query":"weather"}`)
	if err != nil {
		t.Fatalf("invoke http skill: %v", err)
	}
	if ok {
		t.Fatal("ok = true, want false")
	}
	observation := decodeObservation(t, output)
	if observation.OK || observation.ErrorType != "secret_unresolved" {
		t.Fatalf("observation = %#v, want secret_unresolved", observation)
	}
	if calls != 0 {
		t.Fatalf("calls = %d, want 0", calls)
	}
}

func TestHTTPSkillResolvesEnvSecretRef(t *testing.T) {
	t.Setenv("NICEAGENT_TEST_BEARER_TOKEN", "env-secret-token")
	runtimeTool := newTestHTTPSkillTool(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get("Authorization"); got != "Bearer env-secret-token" {
			t.Fatalf("authorization = %q, want env secret token", got)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			Header:     make(http.Header),
		}, nil
	}))
	runtimeTool.runtimeSkill.Skill.RuntimeConfig = `{"type":"http","method":"POST","url":"https://api.example.com/weather","timeout_seconds":15,"auth_type":"bearer"}`
	runtimeTool.runtimeSkill.SecretMaterials = map[string]protocol.RuntimeSecret{
		"bearer_token": {SecretRef: "env://NICEAGENT_TEST_BEARER_TOKEN"},
	}

	output, ok, err := runtimeTool.invokeHTTP(context.Background(), `{"query":"weather"}`)
	if err != nil {
		t.Fatalf("invoke http skill: %v", err)
	}
	if !ok {
		t.Fatalf("ok = false, output = %s", output)
	}
	observation := decodeObservation(t, output)
	if !observation.OK || observation.StatusCode != http.StatusOK {
		t.Fatalf("observation = %#v, want ok", observation)
	}
}

func TestHTTPSkillValidatesOutputSchema(t *testing.T) {
	runtimeTool := newTestHTTPSkillTool(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"ok":"yes"}`)),
			Header:     make(http.Header),
		}, nil
	}))
	runtimeTool.runtimeSkill.Skill.OutputSchema = `{"type":"object","required":["ok"],"properties":{"ok":{"type":"boolean"}}}`

	output, ok, err := runtimeTool.invokeHTTP(context.Background(), `{"query":"weather"}`)
	if err != nil {
		t.Fatalf("invoke http skill: %v", err)
	}
	if ok {
		t.Fatal("ok = true, want false")
	}
	observation := decodeObservation(t, output)
	if observation.OK || observation.ErrorType != "invalid_output" {
		t.Fatalf("observation = %#v, want invalid_output", observation)
	}
}

func TestHTTPSkillRejectsPrivateAddressWithoutRequest(t *testing.T) {
	var calls int
	runtimeTool := newTestHTTPSkillTool(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		t.Fatalf("unexpected request to %s", req.URL.String())
		return nil, nil
	}))
	runtimeTool.runtimeSkill.Skill.RuntimeConfig = `{"type":"http","method":"POST","url":"https://127.0.0.1/hook","timeout_seconds":15,"auth_type":"none"}`

	output, ok, err := runtimeTool.invokeHTTP(context.Background(), `{"query":"weather"}`)
	if err != nil {
		t.Fatalf("invoke http skill: %v", err)
	}
	if ok {
		t.Fatal("ok = true, want false")
	}
	observation := decodeObservation(t, output)
	if observation.OK || observation.ErrorType != "ssrf_rejected" {
		t.Fatalf("observation = %#v, want ssrf_rejected", observation)
	}
	if calls != 0 {
		t.Fatalf("calls = %d, want 0", calls)
	}
}

func TestHTTPSkillRejectsPrivateResolvedAddressWithoutRequest(t *testing.T) {
	var calls int
	runtimeTool := newTestHTTPSkillTool(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		t.Fatalf("unexpected request to %s", req.URL.String())
		return nil, nil
	}))
	runtimeTool.bridge.Resolver = resolverFunc(func(_ context.Context, host string) ([]net.IPAddr, error) {
		if host != "api.example.com" {
			t.Fatalf("resolved host = %q, want api.example.com", host)
		}
		return []net.IPAddr{{IP: net.ParseIP("10.0.0.10")}}, nil
	})

	output, ok, err := runtimeTool.invokeHTTP(context.Background(), `{"query":"weather"}`)
	if err != nil {
		t.Fatalf("invoke http skill: %v", err)
	}
	if ok {
		t.Fatal("ok = true, want false")
	}
	observation := decodeObservation(t, output)
	if observation.OK || observation.ErrorType != "ssrf_rejected" || !strings.Contains(observation.Message, "resolved to private address") {
		t.Fatalf("observation = %#v, want ssrf_rejected resolved private address", observation)
	}
	if calls != 0 {
		t.Fatalf("calls = %d, want 0", calls)
	}
}

func TestHTTPSkillRejectsResolvedMetadataAddressWithoutRequest(t *testing.T) {
	var calls int
	runtimeTool := newTestHTTPSkillTool(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		t.Fatalf("unexpected request to %s", req.URL.String())
		return nil, nil
	}))
	runtimeTool.bridge.Resolver = resolverFunc(func(_ context.Context, _ string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("100.100.100.200")}}, nil
	})

	output, ok, err := runtimeTool.invokeHTTP(context.Background(), `{"query":"weather"}`)
	if err != nil {
		t.Fatalf("invoke http skill: %v", err)
	}
	if ok {
		t.Fatal("ok = true, want false")
	}
	observation := decodeObservation(t, output)
	if observation.OK || observation.ErrorType != "ssrf_rejected" {
		t.Fatalf("observation = %#v, want ssrf_rejected", observation)
	}
	if calls != 0 {
		t.Fatalf("calls = %d, want 0", calls)
	}
}

func TestHTTPSkillReturnsDNSErrorWhenHostCannotResolve(t *testing.T) {
	var calls int
	runtimeTool := newTestHTTPSkillTool(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		t.Fatalf("unexpected request to %s", req.URL.String())
		return nil, nil
	}))
	runtimeTool.bridge.Resolver = resolverFunc(func(_ context.Context, host string) ([]net.IPAddr, error) {
		return nil, &net.DNSError{Name: host, Err: "no such host"}
	})

	output, ok, err := runtimeTool.invokeHTTP(context.Background(), `{"query":"weather"}`)
	if err != nil {
		t.Fatalf("invoke http skill: %v", err)
	}
	if ok {
		t.Fatal("ok = true, want false")
	}
	observation := decodeObservation(t, output)
	if observation.OK || observation.ErrorType != "upstream_dns" {
		t.Fatalf("observation = %#v, want upstream_dns", observation)
	}
	if calls != 0 {
		t.Fatalf("calls = %d, want 0", calls)
	}
}

func TestSSRFGuardedDialerRejectsReboundPrivateAddress(t *testing.T) {
	dialer := &ssrfGuardedDialer{
		Resolver: resolverFunc(func(_ context.Context, host string) ([]net.IPAddr, error) {
			if host != "api.example.com" {
				t.Fatalf("resolved host = %q, want api.example.com", host)
			}
			return []net.IPAddr{{IP: net.ParseIP("192.168.1.10")}}, nil
		}),
	}

	_, err := dialer.DialContext(context.Background(), "tcp", "api.example.com:443")
	if err == nil {
		t.Fatal("expected private resolved address to be rejected")
	}
	if !strings.Contains(err.Error(), "resolved only blocked addresses") {
		t.Fatalf("error = %v, want blocked address message", err)
	}
	if got := classifyHTTPClientError(context.Background(), err); got != "ssrf_rejected" {
		t.Fatalf("error type = %q, want ssrf_rejected", got)
	}
}

func newTestHTTPSkillTool(transport http.RoundTripper) *runtimeTool {
	bridge := NewDefaultToolBridge(nil)
	bridge.Client = &http.Client{Transport: transport}
	bridge.Resolver = resolverFunc(func(_ context.Context, _ string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	})
	return &runtimeTool{
		bridge: bridge,
		runID:  "run-http-skill",
		runtimeSkill: protocol.RuntimeSkill{
			Skill: protocol.Skill{
				ID:            "skill_weather",
				Name:          "Weather API",
				Description:   "Fetch weather",
				Scope:         protocol.SkillScopeUser,
				Kind:          protocol.SkillKindHTTP,
				Enabled:       true,
				InputSchema:   `{"type":"object","additionalProperties":true}`,
				RuntimeConfig: `{"type":"http","method":"POST","url":"https://api.example.com/weather","timeout_seconds":15,"auth_type":"none"}`,
			},
		},
	}
}

func decodeObservation(t *testing.T, output string) httpSkillObservation {
	t.Helper()
	var observation httpSkillObservation
	if err := json.Unmarshal([]byte(output), &observation); err != nil {
		t.Fatalf("decode observation %s: %v", output, err)
	}
	return observation
}
