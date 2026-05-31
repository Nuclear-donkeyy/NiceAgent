package sink

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"niceagent/common/protocol"
)

type ControlPlaneSink struct {
	BaseURL string
	Token   string
	Client  *http.Client
}

func NewControlPlaneSink(baseURL, token string) *ControlPlaneSink {
	return &ControlPlaneSink{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		Client:  &http.Client{Timeout: 15 * time.Second},
	}
}

func (s *ControlPlaneSink) Emit(runID string, typ protocol.RunEventType, message string, payload any) error {
	return s.post(context.Background(), "/internal/runs/"+runID+"/events", protocol.RunEventWriteRequest{
		Type:    typ,
		Message: message,
		Payload: payload,
	}, nil)
}

func (s *ControlPlaneSink) Complete(runID string, content string) error {
	return s.post(context.Background(), "/internal/runs/"+runID+"/complete", protocol.RunCompleteRequest{
		Content: content,
	}, nil)
}

func (s *ControlPlaneSink) Fail(runID string, message string) error {
	return s.post(context.Background(), "/internal/runs/"+runID+"/fail", protocol.RunFailRequest{
		Error: message,
	}, nil)
}

func (s *ControlPlaneSink) IsCanceled(runID string) bool {
	var response protocol.RunStatusResponse
	if err := s.get(context.Background(), "/internal/runs/"+runID+"/status", &response); err != nil {
		return false
	}
	return response.Run.Status == protocol.RunCanceled
}

func (s *ControlPlaneSink) post(ctx context.Context, path string, payload any, target any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	s.authorize(req)
	resp, err := s.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("control plane returned %s: %s", resp.Status, strings.TrimSpace(string(message)))
	}
	if target == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(target)
}

func (s *ControlPlaneSink) get(ctx context.Context, path string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.BaseURL+path, nil)
	if err != nil {
		return err
	}
	s.authorize(req)
	resp, err := s.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("control plane returned %s: %s", resp.Status, strings.TrimSpace(string(message)))
	}
	return json.NewDecoder(resp.Body).Decode(target)
}

func (s *ControlPlaneSink) authorize(req *http.Request) {
	if s.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.Token)
	}
}

func (s *ControlPlaneSink) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return &http.Client{Timeout: 15 * time.Second}
}
