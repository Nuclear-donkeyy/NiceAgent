package skillmanifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"niceagent/common/protocol"
)

const (
	DefaultHTTPSkillTimeoutSeconds = 15
	MaxHTTPSkillTimeoutSeconds     = 60
	DefaultHTTPRetryBaseDelayMS    = 200
	DefaultHTTPRetryMaxDelayMS     = 2000
	MaxHTTPRetryAttempts           = 5
	MaxHTTPRetryDelayMS            = 10000
	MaxHTTPRateLimitPerMinute      = 600
)

type HTTPSkillRuntimeConfig struct {
	Type           string                   `json:"type,omitempty"`
	Method         string                   `json:"method"`
	URL            string                   `json:"url"`
	TimeoutSeconds int                      `json:"timeout_seconds"`
	AuthType       string                   `json:"auth_type"`
	Retry          HTTPSkillRetryConfig     `json:"retry,omitempty"`
	RateLimit      HTTPSkillRateLimitConfig `json:"rate_limit,omitempty"`
}

type HTTPSkillRetryConfig struct {
	MaxAttempts int `json:"max_attempts,omitempty"`
	BaseDelayMS int `json:"base_delay_ms,omitempty"`
	MaxDelayMS  int `json:"max_delay_ms,omitempty"`
}

type HTTPSkillRateLimitConfig struct {
	RequestsPerMinute int `json:"requests_per_minute,omitempty"`
}

func NewHTTPSkillRuntimeConfig(input protocol.HTTPSkillInput) (HTTPSkillRuntimeConfig, error) {
	cfg := HTTPSkillRuntimeConfig{
		Type:           "http",
		Method:         strings.ToUpper(strings.TrimSpace(input.Method)),
		URL:            strings.TrimSpace(input.URL),
		TimeoutSeconds: input.TimeoutSeconds,
		AuthType:       strings.TrimSpace(input.AuthType),
		Retry: HTTPSkillRetryConfig{
			MaxAttempts: input.RetryMaxAttempts,
		},
		RateLimit: HTTPSkillRateLimitConfig{
			RequestsPerMinute: input.RateLimitPerMinute,
		},
	}
	if cfg.Method == "" {
		cfg.Method = http.MethodPost
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = DefaultHTTPSkillTimeoutSeconds
	}
	if cfg.AuthType == "" {
		cfg.AuthType = "none"
	}
	cfg.Retry = normalizeHTTPRetryConfig(cfg.Retry)
	if err := cfg.Validate(); err != nil {
		return HTTPSkillRuntimeConfig{}, err
	}
	return cfg, nil
}

func ParseHTTPSkillRuntimeConfig(raw string) (HTTPSkillRuntimeConfig, error) {
	if strings.TrimSpace(raw) == "" {
		return HTTPSkillRuntimeConfig{}, errors.New("runtime_config is required")
	}
	var cfg HTTPSkillRuntimeConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return HTTPSkillRuntimeConfig{}, fmt.Errorf("invalid runtime_config json: %w", err)
	}
	cfg.Method = strings.ToUpper(strings.TrimSpace(cfg.Method))
	cfg.URL = strings.TrimSpace(cfg.URL)
	cfg.AuthType = strings.TrimSpace(cfg.AuthType)
	if cfg.Method == "" {
		cfg.Method = http.MethodPost
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = DefaultHTTPSkillTimeoutSeconds
	}
	if cfg.AuthType == "" {
		cfg.AuthType = "none"
	}
	cfg.Retry = normalizeHTTPRetryConfig(cfg.Retry)
	if err := cfg.Validate(); err != nil {
		return HTTPSkillRuntimeConfig{}, err
	}
	return cfg, nil
}

func (c HTTPSkillRuntimeConfig) Validate() error {
	if c.Type != "" && c.Type != "http" {
		return errors.New("runtime_config.type must be http")
	}
	switch c.Method {
	case http.MethodGet, http.MethodPost:
	default:
		return errors.New("method must be GET or POST")
	}
	parsed, err := url.Parse(c.URL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return errors.New("valid url is required")
	}
	if parsed.Scheme != "https" {
		return errors.New("url scheme must be https")
	}
	if parsed.User != nil {
		return errors.New("url must not include credentials")
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
	if c.Retry.BaseDelayMS < 0 || c.Retry.BaseDelayMS > MaxHTTPRetryDelayMS {
		return fmt.Errorf("retry.base_delay_ms must be between 0 and %d", MaxHTTPRetryDelayMS)
	}
	if c.Retry.MaxDelayMS < 0 || c.Retry.MaxDelayMS > MaxHTTPRetryDelayMS {
		return fmt.Errorf("retry.max_delay_ms must be between 0 and %d", MaxHTTPRetryDelayMS)
	}
	if c.Retry.MaxDelayMS > 0 && c.Retry.BaseDelayMS > c.Retry.MaxDelayMS {
		return errors.New("retry.base_delay_ms must be less than or equal to retry.max_delay_ms")
	}
	if c.RateLimit.RequestsPerMinute < 0 || c.RateLimit.RequestsPerMinute > MaxHTTPRateLimitPerMinute {
		return fmt.Errorf("rate_limit.requests_per_minute must be between 0 and %d", MaxHTTPRateLimitPerMinute)
	}
	return nil
}

func normalizeHTTPRetryConfig(cfg HTTPSkillRetryConfig) HTTPSkillRetryConfig {
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 1
	}
	if cfg.MaxAttempts > 1 {
		if cfg.BaseDelayMS <= 0 {
			cfg.BaseDelayMS = DefaultHTTPRetryBaseDelayMS
		}
		if cfg.MaxDelayMS <= 0 {
			cfg.MaxDelayMS = DefaultHTTPRetryMaxDelayMS
		}
	}
	return cfg
}

func (c HTTPSkillRuntimeConfig) JSONString() string {
	data, _ := json.Marshal(c)
	return string(data)
}

func ValidateHTTPSkillInput(input protocol.HTTPSkillInput) error {
	if strings.TrimSpace(input.Name) == "" {
		return errors.New("name is required")
	}
	if _, err := NewHTTPSkillRuntimeConfig(input); err != nil {
		return err
	}
	if err := ValidateJSONSchema(firstNonEmpty(input.InputSchema, `{"type":"object","additionalProperties":true}`)); err != nil {
		return fmt.Errorf("input_schema %w", err)
	}
	if strings.TrimSpace(input.OutputSchema) != "" {
		if err := ValidateJSONSchema(input.OutputSchema); err != nil {
			return fmt.Errorf("output_schema %w", err)
		}
	}
	if strings.TrimSpace(input.BearerToken) != "" && strings.TrimSpace(input.BearerTokenSecretRef) != "" {
		return errors.New("bearer_token and bearer_token_secret_ref cannot both be set")
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
