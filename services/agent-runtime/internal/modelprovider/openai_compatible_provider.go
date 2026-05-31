package modelprovider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
)

func NewOpenAICompatibleChatModel(ctx context.Context, config OpenAICompatibleProviderConfig) (model.ToolCallingChatModel, error) {
	if config.ID == "" {
		config.ID = "openai-compatible"
	}
	config.BaseURL = strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	config.APIKey = strings.TrimSpace(config.APIKey)
	config.Model = strings.TrimSpace(config.Model)
	if err := ValidateOpenAICompatibleConfig(config); err != nil {
		return nil, err
	}
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	tracker := NewUsageTracker(config.ID, config.Model)
	redactor := NewRedactor(config.APIKey)
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	}
	baseTransport := httpClient.Transport
	if baseTransport == nil {
		baseTransport = http.DefaultTransport
	}
	httpClient = &http.Client{
		Timeout: httpClient.Timeout,
		Transport: RetryTransport{
			Base:     baseTransport,
			Policy:   config.RetryPolicy,
			Tracker:  tracker,
			Redactor: redactor,
		},
		CheckRedirect: httpClient.CheckRedirect,
		Jar:           httpClient.Jar,
	}
	chatModel, err := einoopenai.NewChatModel(ctx, &einoopenai.ChatModelConfig{
		BaseURL:    config.BaseURL,
		APIKey:     config.APIKey,
		Model:      config.Model,
		HTTPClient: httpClient,
	})
	if err != nil {
		return nil, err
	}
	return NewOperationalChatModel(chatModel, ProviderMetadata{Provider: config.ID, Model: config.Model}, tracker, redactor), nil
}

func ValidateOpenAICompatibleConfig(config OpenAICompatibleProviderConfig) error {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if baseURL == "" {
		return errors.New("MODEL_BASE_URL is required when MODEL_PROVIDER=openai-compatible")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("MODEL_BASE_URL must be an absolute http(s) URL, got %q", baseURL)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("MODEL_BASE_URL must use http or https, got %q", parsed.Scheme)
	}
	path := strings.TrimRight(strings.ToLower(parsed.EscapedPath()), "/")
	if strings.HasSuffix(path, "/chat/completions") || strings.HasSuffix(path, "/completions") {
		return errors.New("MODEL_BASE_URL must be the provider root URL and must not include /chat/completions")
	}
	if config.APIKey == "" {
		return errors.New("MODEL_API_KEY is required when MODEL_PROVIDER=openai-compatible")
	}
	if config.Model == "" {
		return errors.New("MODEL_NAME is required when MODEL_PROVIDER=openai-compatible")
	}
	return nil
}
