package tools

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"niceagent/common/platform"
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

func TestHTTPSkillResolvesFileSecretRef(t *testing.T) {
	secretDir := t.TempDir()
	secretPath := filepath.Join(secretDir, "bearer-token")
	if err := os.WriteFile(secretPath, []byte("file-secret-token\n"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	t.Setenv("NICEAGENT_SECRET_FILE_ROOTS", secretDir)
	runtimeTool := newTestHTTPSkillTool(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get("Authorization"); got != "Bearer file-secret-token" {
			t.Fatalf("authorization = %q, want file secret token", got)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			Header:     make(http.Header),
		}, nil
	}))
	runtimeTool.runtimeSkill.Skill.RuntimeConfig = `{"type":"http","method":"POST","url":"https://api.example.com/weather","timeout_seconds":15,"auth_type":"bearer"}`
	runtimeTool.runtimeSkill.SecretMaterials = map[string]protocol.RuntimeSecret{
		"bearer_token": {SecretRef: "file://" + secretPath},
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

func TestHTTPSkillRejectsFileSecretOutsideAllowedRoots(t *testing.T) {
	allowedDir := t.TempDir()
	blockedDir := t.TempDir()
	secretPath := filepath.Join(blockedDir, "bearer-token")
	if err := os.WriteFile(secretPath, []byte("file-secret-token"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	t.Setenv("NICEAGENT_SECRET_FILE_ROOTS", allowedDir)
	var calls int
	runtimeTool := newTestHTTPSkillTool(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		t.Fatalf("unexpected request to %s", req.URL.String())
		return nil, nil
	}))
	runtimeTool.runtimeSkill.Skill.RuntimeConfig = `{"type":"http","method":"POST","url":"https://api.example.com/weather","timeout_seconds":15,"auth_type":"bearer"}`
	runtimeTool.runtimeSkill.SecretMaterials = map[string]protocol.RuntimeSecret{
		"bearer_token": {SecretRef: "file://" + secretPath},
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

func TestHTTPSkillRejectsFileSecretSymlinkEscape(t *testing.T) {
	allowedDir := t.TempDir()
	blockedDir := t.TempDir()
	targetPath := filepath.Join(blockedDir, "bearer-token")
	if err := os.WriteFile(targetPath, []byte("file-secret-token"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	linkPath := filepath.Join(allowedDir, "linked-token")
	if err := os.Symlink(targetPath, linkPath); err != nil {
		t.Fatalf("symlink secret: %v", err)
	}
	t.Setenv("NICEAGENT_SECRET_FILE_ROOTS", allowedDir)
	var calls int
	runtimeTool := newTestHTTPSkillTool(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		t.Fatalf("unexpected request to %s", req.URL.String())
		return nil, nil
	}))
	runtimeTool.runtimeSkill.Skill.RuntimeConfig = `{"type":"http","method":"POST","url":"https://api.example.com/weather","timeout_seconds":15,"auth_type":"bearer"}`
	runtimeTool.runtimeSkill.SecretMaterials = map[string]protocol.RuntimeSecret{
		"bearer_token": {SecretRef: "file://" + linkPath},
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

func TestHTTPSkillRetriesRetryableStatus(t *testing.T) {
	var calls int
	runtimeTool := newTestHTTPSkillTool(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		status := http.StatusBadGateway
		body := `{"ok":false}`
		if calls == 2 {
			status = http.StatusOK
			body = `{"ok":true}`
		}
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	}))
	runtimeTool.runtimeSkill.Skill.RuntimeConfig = `{"type":"http","method":"POST","url":"https://api.example.com/weather","timeout_seconds":15,"auth_type":"none","retry":{"max_attempts":2,"base_delay_ms":1,"max_delay_ms":1}}`

	output, ok, err := runtimeTool.invokeHTTP(context.Background(), `{"query":"weather"}`)
	if err != nil {
		t.Fatalf("invoke http skill: %v", err)
	}
	if !ok {
		t.Fatalf("ok = false, output = %s", output)
	}
	observation := decodeObservation(t, output)
	if !observation.OK || observation.StatusCode != http.StatusOK || observation.RetryCount != 1 {
		t.Fatalf("observation = %#v, want ok after one retry", observation)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestHTTPSkillDoesNotRetryRequestErrorStatus(t *testing.T) {
	var calls int
	runtimeTool := newTestHTTPSkillTool(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       io.NopCloser(strings.NewReader(`{"ok":false}`)),
			Header:     make(http.Header),
		}, nil
	}))
	runtimeTool.runtimeSkill.Skill.RuntimeConfig = `{"type":"http","method":"POST","url":"https://api.example.com/weather","timeout_seconds":15,"auth_type":"none","retry":{"max_attempts":3,"base_delay_ms":1,"max_delay_ms":1}}`

	output, ok, err := runtimeTool.invokeHTTP(context.Background(), `{"query":"weather"}`)
	if err != nil {
		t.Fatalf("invoke http skill: %v", err)
	}
	if ok {
		t.Fatal("ok = true, want false")
	}
	observation := decodeObservation(t, output)
	if observation.OK || observation.ErrorType != "upstream_status" || observation.RetryCount != 0 {
		t.Fatalf("observation = %#v, want non-retried upstream_status", observation)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestHTTPSkillRateLimitReturnsObservationWithoutRequest(t *testing.T) {
	var calls int
	runtimeTool := newTestHTTPSkillTool(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			Header:     make(http.Header),
		}, nil
	}))
	runtimeTool.runtimeSkill.Skill.RuntimeConfig = `{"type":"http","method":"POST","url":"https://api.example.com/weather","timeout_seconds":15,"auth_type":"none","rate_limit":{"requests_per_minute":1}}`

	output, ok, err := runtimeTool.invokeHTTP(context.Background(), `{"query":"weather"}`)
	if err != nil || !ok {
		t.Fatalf("first invoke = ok:%v output:%s err:%v", ok, output, err)
	}
	output, ok, err = runtimeTool.invokeHTTP(context.Background(), `{"query":"weather"}`)
	if err != nil {
		t.Fatalf("second invoke http skill: %v", err)
	}
	if ok {
		t.Fatal("second ok = true, want false")
	}
	observation := decodeObservation(t, output)
	if observation.OK || observation.ErrorType != "rate_limited" {
		t.Fatalf("observation = %#v, want rate_limited", observation)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want only first request", calls)
	}
	renderedMetrics := runtimeTool.bridge.Metrics.Render()
	if !strings.Contains(renderedMetrics, `niceagent_skill_rate_limit_denials_total{service="agent_runtime_tool_test",kind="http",mode="local"} 1`) {
		t.Fatalf("metrics body = %s", renderedMetrics)
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
	bridge.Metrics = platform.NewMetrics("agent_runtime_tool_test")
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
