package sandbox

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

type HTTPExecutor struct {
	BaseURL string
	Token   string
	Client  *http.Client
}

func NewHTTPExecutor(baseURL, token string) *HTTPExecutor {
	return &HTTPExecutor{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		Client:  &http.Client{Timeout: 30 * time.Second},
	}
}

func (e *HTTPExecutor) Execute(ctx context.Context, request protocol.SandboxCommand) protocol.SandboxResult {
	body, err := json.Marshal(request)
	if err != nil {
		return sandboxHTTPError(request.RunID, err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, e.BaseURL+"/internal/sandbox/exec", bytes.NewReader(body))
	if err != nil {
		return sandboxHTTPError(request.RunID, err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if e.Token != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+e.Token)
	}
	resp, err := e.client().Do(httpRequest)
	if err != nil {
		return sandboxHTTPError(request.RunID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return sandboxHTTPError(request.RunID, fmt.Errorf("sandbox executor returned %s: %s", resp.Status, strings.TrimSpace(string(message))))
	}
	var result protocol.SandboxResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return sandboxHTTPError(request.RunID, err)
	}
	return result
}

func (e *HTTPExecutor) client() *http.Client {
	if e.Client != nil {
		return e.Client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func sandboxHTTPError(runID string, err error) protocol.SandboxResult {
	return protocol.SandboxResult{
		RunID:    runID,
		ExitCode: -1,
		Error:    err.Error(),
	}
}
