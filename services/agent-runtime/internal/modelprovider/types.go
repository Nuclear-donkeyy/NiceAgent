package modelprovider

import (
	"context"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type Provider = model.ToolCallingChatModel

type OpenAICompatibleProviderConfig struct {
	ID      string
	BaseURL string
	APIKey  string
	Model   string
	Timeout time.Duration
}

func StreamSingle(ctx context.Context, message *schema.Message) (*schema.StreamReader[*schema.Message], error) {
	_ = ctx
	return schema.StreamReaderFromArray([]*schema.Message{message}), nil
}
