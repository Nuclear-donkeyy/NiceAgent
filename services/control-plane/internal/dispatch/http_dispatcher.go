package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"niceagent/common/protocol"
)

type HTTPDispatcher struct {
	repo            Repository
	runtimeURL      string
	controlPlaneURL string
	token           string
	client          *http.Client
	log             *slog.Logger
}

func NewHTTPDispatcher(repo Repository, runtimeURL, controlPlaneURL, token string, log *slog.Logger) *HTTPDispatcher {
	return &HTTPDispatcher{
		repo:            repo,
		runtimeURL:      strings.TrimRight(runtimeURL, "/"),
		controlPlaneURL: strings.TrimRight(controlPlaneURL, "/"),
		token:           token,
		client:          &http.Client{Timeout: 30 * time.Second},
		log:             log,
	}
}

func (d *HTTPDispatcher) Dispatch(ctx context.Context, run protocol.Run, userMessage string) error {
	if d.runtimeURL == "" {
		return errors.New("agent runtime url is not configured")
	}
	go d.dispatch(context.WithoutCancel(ctx), run, userMessage)
	return nil
}

func (d *HTTPDispatcher) dispatch(ctx context.Context, run protocol.Run, userMessage string) {
	runtimeSkills := runtimeSkillsForRun(d.repo, run)
	skills := skillIDsFromRuntimeSkills(runtimeSkills)
	request := protocol.RunExecutionRequest{
		Request: protocol.RunRequest{
			RunID:       run.ID,
			ChatID:      run.ChatID,
			UserID:      run.UserID,
			WorkspaceID: run.WorkspaceID,
			SkillIDs:    skills,
			Skills:      runtimeSkills,
			ModelPolicy: "mock-default",
		},
		UserMessage:     userMessage,
		ControlPlaneURL: d.controlPlaneURL,
	}
	if err := d.postRun(ctx, request); err != nil {
		d.log.Warn("http dispatch failed", "run_id", run.ID, "error", err)
		sink := controlSink{repo: d.repo}
		_ = sink.Fail(run.ID, fmt.Sprintf("Run dispatch failed: %v", err))
	}
}

func (d *HTTPDispatcher) postRun(ctx context.Context, payload protocol.RunExecutionRequest) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.runtimeURL+"/internal/runs/execute", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if d.token != "" {
		req.Header.Set("Authorization", "Bearer "+d.token)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("agent runtime returned %s: %s", resp.Status, strings.TrimSpace(string(message)))
	}
	return nil
}
