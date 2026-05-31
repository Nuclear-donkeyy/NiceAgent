package modelprovider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"niceagent/common/protocol"
)

type OpenAICompatibleProvider struct {
	BaseURL string
	APIKey  string
	Model   string
	Client  *http.Client
}

func NewOpenAICompatibleProvider(config OpenAICompatibleProviderConfig) (*OpenAICompatibleProvider, error) {
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
	return &OpenAICompatibleProvider{
		BaseURL: config.BaseURL,
		APIKey:  config.APIKey,
		Model:   config.Model,
		Client:  &http.Client{Timeout: timeout},
	}, nil
}

func (p *OpenAICompatibleProvider) Stream(ctx context.Context, request Request) (<-chan Chunk, error) {
	if p == nil {
		return nil, errors.New("openai-compatible provider is nil")
	}
	body, err := json.Marshal(openAIChatCompletionRequest{
		Model:    p.Model,
		Messages: openAIMessages(request.Messages),
		Stream:   true,
	})
	if err != nil {
		return nil, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpRequest.Header.Set("Authorization", "Bearer "+p.APIKey)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "text/event-stream")

	client := p.Client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("openai-compatible provider returned %s: %s", response.Status, strings.TrimSpace(string(raw)))
	}

	chunks := make(chan Chunk, 16)
	go func() {
		defer close(chunks)
		defer response.Body.Close()
		if err := scanOpenAIStream(response.Body, chunks); err != nil {
			select {
			case chunks <- Chunk{Error: err, Done: true}:
			case <-ctx.Done():
			}
		}
	}()
	return chunks, nil
}

type OpenAICompatibleProviderConfig struct {
	ID      string
	BaseURL string
	APIKey  string
	Model   string
	Timeout time.Duration
}

type openAIChatCompletionRequest struct {
	Model    string          `json:"model"`
	Messages []openAIMessage `json:"messages"`
	Stream   bool            `json:"stream"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
}

func openAIMessages(messages []protocol.Message) []openAIMessage {
	out := make([]openAIMessage, 0, len(messages))
	for _, message := range messages {
		if strings.TrimSpace(message.Content) == "" {
			continue
		}
		out = append(out, openAIMessage{
			Role:    openAIRole(message.Role),
			Content: message.Content,
		})
	}
	if len(out) == 0 {
		out = append(out, openAIMessage{Role: "user", Content: ""})
	}
	return out
}

func openAIRole(role protocol.MessageRole) string {
	switch role {
	case protocol.RoleSystem:
		return "system"
	case protocol.RoleAssistant:
		return "assistant"
	case protocol.RoleTool:
		return "tool"
	default:
		return "user"
	}
}

func scanOpenAIStream(body io.Reader, chunks chan<- Chunk) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			chunks <- Chunk{Done: true}
			return nil
		}
		var event openAIStreamChunk
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return fmt.Errorf("decode openai-compatible stream chunk: %w", err)
		}
		for _, choice := range event.Choices {
			if choice.Delta.Content != "" {
				chunks <- Chunk{Text: choice.Delta.Content}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return io.ErrUnexpectedEOF
}
