package dispatch

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

	"niceagent/common/platform"
	"niceagent/common/protocol"
	"niceagent/control-plane/internal/app"
)

type HTTPDispatcher struct {
	repo            app.Repository
	runtimeURL      string
	controlPlaneURL string
	token           string
	client          *http.Client
	log             *slog.Logger
}

func NewHTTPDispatcher(repo app.Repository, runtimeURL, controlPlaneURL, token string, log *slog.Logger) *HTTPDispatcher {
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
	runtimeSkills := app.RuntimeSkillsForRun(d.repo, run)
	runtimePolicy := app.RuntimePolicyForRun(d.repo, run)
	skills := app.SkillIDsFromRuntimeSkills(runtimeSkills)
	request := protocol.RunExecutionRequest{
		Request: protocol.RunRequest{
			RunID:           run.ID,
			ChatID:          run.ChatID,
			UserID:          run.UserID,
			WorkspaceID:     run.WorkspaceID,
			AttemptID:       firstNonEmpty(run.AttemptID, platform.NewID("attempt")),
			SkillIDs:        skills,
			Skills:          runtimeSkills,
			ModelPolicy:     "mock-default",
			SkillRiskPolicy: runtimePolicy.SkillRiskPolicy,
		},
		UserMessage:     userMessage,
		ControlPlaneURL: d.controlPlaneURL,
	}
	if err := d.postRun(ctx, request); err != nil {
		d.log.Warn("http dispatch failed", "run_id", run.ID, "error", err)
		var runtimeErr *RuntimeHTTPError
		if errors.As(err, &runtimeErr) && runtimeErr.StatusCode == http.StatusConflict {
			return
		}
		sink := app.RepositorySink{Repo: d.repo}
		_ = sink.Fail(run.ID, fmt.Sprintf("Run dispatch failed: %v", err))
	}
}

func (d *HTTPDispatcher) postRun(ctx context.Context, payload protocol.RunExecutionRequest) (err error) {
	ctx, endSpan := platform.StartSpan(ctx, "niceagent/control_plane", "dispatch.agent_runtime.http", platform.Labels{
		"run_id":     payload.Request.RunID,
		"attempt_id": payload.Request.AttemptID,
	})
	defer func() {
		endSpan(err, nil)
	}()
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.runtimeURL+"/internal/runs/execute", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	platform.InjectTraceHeaders(ctx, req.Header)
	if d.token != "" {
		req.Header.Set("Authorization", "Bearer "+d.token)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	spanLabels := platform.Labels{"http_status": fmt.Sprint(resp.StatusCode)}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		err = &RuntimeHTTPError{
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
			Message:    strings.TrimSpace(string(message)),
		}
		endSpan(err, spanLabels)
		return err
	}
	endSpan(nil, spanLabels)
	endSpan = func(error, platform.Labels) {}
	return nil
}

type RuntimeHTTPError struct {
	StatusCode int
	Status     string
	Message    string
}

func (e *RuntimeHTTPError) Error() string {
	return fmt.Sprintf("agent runtime returned %s: %s", e.Status, e.Message)
}
