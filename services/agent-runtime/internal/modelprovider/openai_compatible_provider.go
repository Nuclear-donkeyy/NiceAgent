package modelprovider

import (
	"context"
	"errors"
	"strings"
	"time"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
)

func NewOpenAICompatibleChatModel(ctx context.Context, config OpenAICompatibleProviderConfig) (model.ToolCallingChatModel, error) {
	config.BaseURL = strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	config.APIKey = strings.TrimSpace(config.APIKey)
	config.Model = strings.TrimSpace(config.Model)
	if config.BaseURL == "" {
		return nil, errors.New("MODEL_BASE_URL is required when MODEL_PROVIDER=openai-compatible")
	}
	if config.APIKey == "" {
		return nil, errors.New("MODEL_API_KEY is required when MODEL_PROVIDER=openai-compatible")
	}
	if config.Model == "" {
		return nil, errors.New("MODEL_NAME is required when MODEL_PROVIDER=openai-compatible")
	}
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	return einoopenai.NewChatModel(ctx, &einoopenai.ChatModelConfig{
		BaseURL: config.BaseURL,
		APIKey:  config.APIKey,
		Model:   config.Model,
		Timeout: timeout,
	})
}
