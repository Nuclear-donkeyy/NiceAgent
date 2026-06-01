package tools

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"

	"niceagent/common/platform"
	"niceagent/common/protocol"
	"niceagent/common/skillmanifest"
)

const maxHTTPSkillResponseBytes = 64 * 1024

type SecretResolver interface {
	ResolveSecret(ctx context.Context, material protocol.RuntimeSecret) (string, error)
}

type LocalSecretResolver struct{}

var (
	ErrSecretMissing            = errors.New("secret material is missing")
	ErrSecretRefResolverUnwired = errors.New("secret_ref resolver is not configured")
	ErrUnsafeResolvedHost       = errors.New("http skill resolved host is not allowed")
)

func (LocalSecretResolver) ResolveSecret(_ context.Context, material protocol.RuntimeSecret) (string, error) {
	if strings.TrimSpace(material.EncryptedValue) != "" {
		return material.EncryptedValue, nil
	}
	secretRef := strings.TrimSpace(material.SecretRef)
	if strings.HasPrefix(secretRef, "env://") {
		envName := strings.TrimSpace(strings.TrimPrefix(secretRef, "env://"))
		if envName == "" {
			return "", ErrSecretRefResolverUnwired
		}
		if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
			return value, nil
		}
		return "", fmt.Errorf("secret_ref %s is not available in environment", secretRef)
	}
	if secretRef != "" {
		return "", ErrSecretRefResolverUnwired
	}
	return "", ErrSecretMissing
}

type httpSkillObservation struct {
	OK         bool   `json:"ok"`
	StatusCode int    `json:"status_code"`
	ErrorType  string `json:"error_type,omitempty"`
	Message    string `json:"message,omitempty"`
	Data       any    `json:"data"`
}

func (t *runtimeTool) invokeHTTP(ctx context.Context, argumentsInJSON string) (string, bool, error) {
	skill := t.runtimeSkill.Skill
	if err := skillmanifest.ValidateJSONDocument(skill.InputSchema, argumentsInJSON); err != nil {
		return marshalObservation(httpSkillObservation{
			OK:        false,
			ErrorType: "invalid_arguments",
			Message:   "HTTP skill arguments do not match input_schema: " + err.Error(),
		}), false, nil
	}

	cfg, err := skillmanifest.ParseHTTPSkillRuntimeConfig(skill.RuntimeConfig)
	if err != nil {
		return marshalObservation(httpSkillObservation{
			OK:        false,
			ErrorType: "invalid_runtime_config",
			Message:   err.Error(),
		}), false, nil
	}
	if err := rejectUnsafeHTTPURL(cfg.URL); err != nil {
		return marshalObservation(httpSkillObservation{
			OK:        false,
			ErrorType: "ssrf_rejected",
			Message:   err.Error(),
		}), false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(cfg.TimeoutSeconds)*time.Second)
	defer cancel()
	if err := rejectUnsafeResolvedHTTPHost(ctx, t.bridge.Resolver, cfg.URL); err != nil {
		errorType := "upstream_dns"
		if errors.Is(err, ErrUnsafeResolvedHost) {
			errorType = "ssrf_rejected"
		}
		return marshalObservation(httpSkillObservation{
			OK:        false,
			ErrorType: errorType,
			Message:   err.Error(),
		}), false, nil
	}

	var secretValues []string
	var bearerToken string
	if cfg.AuthType == "bearer" {
		bearerToken, err = t.resolveSecret(ctx, "bearer_token")
		if err != nil {
			return marshalObservation(httpSkillObservation{
				OK:        false,
				ErrorType: "secret_unresolved",
				Message:   "HTTP skill bearer token is not available: " + err.Error(),
			}), false, nil
		}
		secretValues = append(secretValues, bearerToken)
	}

	ctx, endSpan := platform.StartSpan(ctx, "niceagent/agent_runtime", "tool.http_skill.request", platform.Labels{
		"run_id":   t.runID,
		"skill_id": skill.ID,
		"method":   cfg.Method,
		"host":     safeURLHost(cfg.URL),
	})
	request, err := buildHTTPSkillRequest(ctx, cfg, argumentsInJSON, bearerToken)
	if err != nil {
		endSpan(err, nil)
		return marshalObservation(httpSkillObservation{
			OK:        false,
			ErrorType: "invalid_runtime_config",
			Message:   err.Error(),
		}), false, nil
	}
	platform.InjectTraceHeaders(ctx, request.Header)
	response, err := httpSkillClient(t.bridge.Client, t.bridge.Resolver).Do(request)
	if err != nil {
		errorType := classifyHTTPClientError(ctx, err)
		endSpan(err, platform.Labels{"error_type": errorType})
		return marshalObservation(httpSkillObservation{
			OK:        false,
			ErrorType: errorType,
			Message:   "HTTP skill request failed: " + redactError(err, secretValues),
		}), false, nil
	}
	defer response.Body.Close()

	body, tooLarge, readErr := readLimitedResponse(response.Body, maxHTTPSkillResponseBytes)
	body = redactHTTPBody(body, secretValues)
	spanLabels := platform.Labels{"http_status": fmt.Sprint(response.StatusCode)}
	if readErr != nil {
		endSpan(readErr, spanLabels)
		return marshalObservation(httpSkillObservation{
			OK:         false,
			StatusCode: response.StatusCode,
			ErrorType:  "upstream_read_error",
			Message:    "HTTP skill response could not be read: " + redactError(readErr, secretValues),
		}), false, nil
	}
	if tooLarge {
		endSpan(fmt.Errorf("HTTP skill response exceeded %d bytes", maxHTTPSkillResponseBytes), spanLabels)
		return marshalObservation(httpSkillObservation{
			OK:         false,
			StatusCode: response.StatusCode,
			ErrorType:  "response_too_large",
			Message:    fmt.Sprintf("HTTP skill response exceeded %d bytes", maxHTTPSkillResponseBytes),
		}), false, nil
	}
	data := observationData(body)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		endSpan(fmt.Errorf("HTTP skill returned status %d", response.StatusCode), spanLabels)
		return marshalObservation(httpSkillObservation{
			OK:         false,
			StatusCode: response.StatusCode,
			ErrorType:  "upstream_status",
			Message:    fmt.Sprintf("HTTP skill returned status %d", response.StatusCode),
			Data:       data,
		}), false, nil
	}
	if strings.TrimSpace(skill.OutputSchema) != "" {
		if err := skillmanifest.ValidateJSONDocument(skill.OutputSchema, body); err != nil {
			endSpan(err, spanLabels)
			return marshalObservation(httpSkillObservation{
				OK:         false,
				StatusCode: response.StatusCode,
				ErrorType:  "invalid_output",
				Message:    "HTTP skill response does not match output_schema: " + err.Error(),
				Data:       data,
			}), false, nil
		}
	}
	endSpan(nil, spanLabels)
	return marshalObservation(httpSkillObservation{
		OK:         true,
		StatusCode: response.StatusCode,
		Data:       data,
	}), true, nil
}

func (t *runtimeTool) resolveSecret(ctx context.Context, key string) (string, error) {
	resolver := t.bridge.SecretResolver
	if resolver == nil {
		resolver = LocalSecretResolver{}
	}
	return resolver.ResolveSecret(ctx, t.runtimeSkill.SecretMaterial(key))
}

func buildHTTPSkillRequest(ctx context.Context, cfg skillmanifest.HTTPSkillRuntimeConfig, argumentsInJSON, bearerToken string) (*http.Request, error) {
	var body io.Reader
	targetURL := cfg.URL
	if cfg.Method == http.MethodGet {
		if strings.Contains(targetURL, "?") {
			targetURL += "&"
		} else {
			targetURL += "?"
		}
		targetURL += "input=" + url.QueryEscape(argumentsInJSON)
	} else {
		body = bytes.NewBufferString(argumentsInJSON)
	}
	request, err := http.NewRequestWithContext(ctx, cfg.Method, targetURL, body)
	if err != nil {
		return nil, err
	}
	if cfg.Method != http.MethodGet {
		request.Header.Set("Content-Type", "application/json")
	}
	if bearerToken != "" {
		request.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	request.Header.Set("Accept", "application/json")
	return request, nil
}

func httpSkillClient(base *http.Client, resolver HostResolver) *http.Client {
	if base == nil {
		base = &http.Client{}
	}
	client := *base
	client.Timeout = 0
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	if transport := guardedTransport(base.Transport, resolver); transport != nil {
		client.Transport = transport
	}
	return &client
}

func guardedTransport(base http.RoundTripper, resolver HostResolver) http.RoundTripper {
	guard := &ssrfGuardedDialer{Resolver: resolver}
	if base == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.DialContext = guard.DialContext
		return transport
	}
	transport, ok := base.(*http.Transport)
	if !ok {
		return nil
	}
	clone := transport.Clone()
	clone.DialContext = guard.DialContext
	return clone
}

type ssrfGuardedDialer struct {
	Resolver HostResolver
	Dialer   net.Dialer
}

func (d *ssrfGuardedDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	host = strings.Trim(strings.ToLower(host), "[]")
	if addr, err := netip.ParseAddr(host); err == nil {
		if !isPublicAddr(addr) {
			return nil, fmt.Errorf("%w: dial target %q is not allowed", ErrUnsafeResolvedHost, addr.String())
		}
		return d.Dialer.DialContext(ctx, network, net.JoinHostPort(addr.String(), port))
	}
	resolver := d.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addrs, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	var blocked []string
	for _, resolved := range addrs {
		addr, ok := netip.AddrFromSlice(resolved.IP)
		if !ok || !isPublicAddr(addr) {
			blocked = append(blocked, resolved.IP.String())
			continue
		}
		return d.Dialer.DialContext(ctx, network, net.JoinHostPort(addr.String(), port))
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("http skill host %q resolved no addresses", host)
	}
	return nil, fmt.Errorf("%w: host %q resolved only blocked addresses %q", ErrUnsafeResolvedHost, host, strings.Join(blocked, ","))
}

func readLimitedResponse(reader io.Reader, limit int64) (string, bool, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return "", false, err
	}
	if int64(len(data)) > limit {
		return string(data[:limit]), true, nil
	}
	return string(data), false, nil
}

func observationData(body string) any {
	if strings.TrimSpace(body) == "" {
		return nil
	}
	var data any
	if err := json.Unmarshal([]byte(body), &data); err == nil {
		return platform.RedactValue(data)
	}
	return body
}

func redactHTTPBody(body string, secrets []string) string {
	body = platform.RedactTextSecrets(body, secrets)
	return platform.RedactJSON(body)
}

func redactError(err error, secrets []string) string {
	return platform.RedactTextSecrets(err.Error(), secrets)
}

func marshalObservation(observation httpSkillObservation) string {
	data, _ := json.Marshal(observation)
	return string(data)
}

func classifyHTTPClientError(ctx context.Context, err error) string {
	if errors.Is(err, ErrUnsafeResolvedHost) {
		return "ssrf_rejected"
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "upstream_timeout"
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return "upstream_canceled"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "upstream_timeout"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "upstream_dns"
	}
	var unknownAuthority x509.UnknownAuthorityError
	if errors.As(err, &unknownAuthority) {
		return "upstream_tls"
	}
	var certificateInvalid x509.CertificateInvalidError
	if errors.As(err, &certificateInvalid) {
		return "upstream_tls"
	}
	var recordHeaderError tls.RecordHeaderError
	if errors.As(err, &recordHeaderError) {
		return "upstream_tls"
	}
	return "upstream_network_error"
}

func rejectUnsafeHTTPURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	host := strings.Trim(strings.ToLower(parsed.Hostname()), "[]")
	if host == "" {
		return errors.New("http skill host is required")
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return fmt.Errorf("http skill host %q is not allowed", host)
	}
	if isMetadataHost(host) {
		return fmt.Errorf("http skill metadata host %q is not allowed", host)
	}
	if addr, err := netip.ParseAddr(host); err == nil && !isPublicAddr(addr) {
		return fmt.Errorf("http skill private address %q is not allowed", host)
	}
	return nil
}

func rejectUnsafeResolvedHTTPHost(ctx context.Context, resolver HostResolver, rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	host := strings.Trim(strings.ToLower(parsed.Hostname()), "[]")
	if host == "" {
		return errors.New("http skill host is required")
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return nil
	}
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addrs, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("http skill host %q could not be resolved: %w", host, err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("http skill host %q resolved no addresses", host)
	}
	for _, resolved := range addrs {
		addr, ok := netip.AddrFromSlice(resolved.IP)
		if !ok {
			return fmt.Errorf("%w: host %q resolved to invalid address %q", ErrUnsafeResolvedHost, host, resolved.IP.String())
		}
		if !isPublicAddr(addr) {
			return fmt.Errorf("%w: host %q resolved to private address %q", ErrUnsafeResolvedHost, host, addr.String())
		}
	}
	return nil
}

func safeURLHost(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "invalid"
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return "unknown"
	}
	return host
}

func isMetadataHost(host string) bool {
	switch host {
	case "169.254.169.254", "metadata.google.internal", "metadata.aliyun.com", "100.100.100.200":
		return true
	default:
		return false
	}
}

func isPublicAddr(addr netip.Addr) bool {
	if addr.Is4In6() {
		return isPublicAddr(addr.Unmap())
	}
	if isMetadataAddr(addr) {
		return false
	}
	if addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsPrivate() || addr.IsUnspecified() || addr.IsMulticast() {
		return false
	}
	return true
}

func isMetadataAddr(addr netip.Addr) bool {
	if addr.Is4In6() {
		return isMetadataAddr(addr.Unmap())
	}
	switch addr.String() {
	case "169.254.169.254", "100.100.100.200":
		return true
	default:
		return false
	}
}
