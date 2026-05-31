package sink

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"niceagent/common/platform"
	"niceagent/common/protocol"
)

type ControlPlaneSink struct {
	BaseURL      string
	Token        string
	AttemptID    string
	TraceID      string
	TraceContext context.Context
	Client       *http.Client
}

func NewControlPlaneSink(baseURL, token string) *ControlPlaneSink {
	return &ControlPlaneSink{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		Client:  &http.Client{Timeout: 15 * time.Second},
	}
}

func (s *ControlPlaneSink) WithAttempt(attemptID string) *ControlPlaneSink {
	next := *s
	next.AttemptID = attemptID
	return &next
}

func (s *ControlPlaneSink) WithTraceID(traceID string) *ControlPlaneSink {
	next := *s
	next.TraceID = strings.TrimSpace(traceID)
	return &next
}

func (s *ControlPlaneSink) WithTraceContext(ctx context.Context) *ControlPlaneSink {
	next := *s
	next.TraceContext = ctx
	return &next
}

func (s *ControlPlaneSink) Claim(runID, attemptID, claimedBy string, leaseSeconds int) (protocol.Run, error) {
	var response protocol.RunClaimResponse
	err := s.post(context.Background(), "/internal/runs/"+runID+"/claim", protocol.RunClaimRequest{
		AttemptID:    attemptID,
		ClaimedBy:    claimedBy,
		LeaseSeconds: leaseSeconds,
	}, &response)
	return response.Run, err
}

func (s *ControlPlaneSink) Emit(runID string, typ protocol.RunEventType, message string, payload any) error {
	return s.post(context.Background(), "/internal/runs/"+runID+"/events", protocol.RunEventWriteRequest{
		Type:      typ,
		Message:   message,
		Payload:   payload,
		AttemptID: s.AttemptID,
	}, nil)
}

func (s *ControlPlaneSink) Complete(runID string, content string, artifacts ...protocol.Artifact) error {
	return s.post(context.Background(), "/internal/runs/"+runID+"/complete", protocol.RunCompleteRequest{
		Content:   content,
		Artifacts: artifacts,
		AttemptID: s.AttemptID,
	}, nil)
}

func (s *ControlPlaneSink) CompleteWithUsage(runID string, content string, usage protocol.RunUsage, artifacts ...protocol.Artifact) error {
	usage = protocol.NormalizeRunUsage(usage)
	return s.post(context.Background(), "/internal/runs/"+runID+"/complete", protocol.RunCompleteRequest{
		Content:    content,
		TokenUsage: protocol.TokenUsageFromRunUsage(usage),
		Usage:      usage,
		AttemptID:  s.AttemptID,
		Artifacts:  artifacts,
	}, nil)
}

func (s *ControlPlaneSink) Fail(runID string, message string) error {
	return s.post(context.Background(), "/internal/runs/"+runID+"/fail", protocol.RunFailRequest{
		Error:     message,
		AttemptID: s.AttemptID,
	}, nil)
}

func (s *ControlPlaneSink) IsCanceled(runID string) bool {
	var response protocol.RunStatusResponse
	if err := s.get(context.Background(), "/internal/runs/"+runID+"/status", &response); err != nil {
		return false
	}
	return response.Run.Status == protocol.RunCanceled
}

func (s *ControlPlaneSink) ReserveToolQuota(ctx context.Context, runID string, input protocol.ToolQuotaReserveRequest) (protocol.ToolQuotaReserveResponse, error) {
	input.AttemptID = firstNonEmpty(input.AttemptID, s.AttemptID)
	var response protocol.ToolQuotaReserveResponse
	err := s.post(ctx, "/internal/runs/"+runID+"/quota-reserve", input, &response)
	return response, err
}

func (s *ControlPlaneSink) ListArtifacts(ctx context.Context, runID string) ([]protocol.Artifact, error) {
	values := url.Values{}
	if s.AttemptID != "" {
		values.Set("attempt_id", s.AttemptID)
	}
	path := "/internal/runs/" + runID + "/artifacts"
	if encoded := values.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var response protocol.ArtifactListResponse
	if err := s.get(ctx, path, &response); err != nil {
		return nil, err
	}
	return response.Artifacts, nil
}

func (s *ControlPlaneSink) ReadArtifactText(ctx context.Context, runID, artifactID string, maxBytes int) (protocol.ArtifactTextResponse, error) {
	values := url.Values{}
	values.Set("run_id", runID)
	if s.AttemptID != "" {
		values.Set("attempt_id", s.AttemptID)
	}
	if maxBytes > 0 {
		values.Set("max_bytes", strconv.Itoa(maxBytes))
	}
	var response protocol.ArtifactTextResponse
	err := s.get(ctx, "/internal/artifacts/"+artifactID+"/content?"+values.Encode(), &response)
	return response, err
}

func (s *ControlPlaneSink) post(ctx context.Context, path string, payload any, target any) (err error) {
	ctx = s.requestContext(ctx)
	ctx, endSpan := platform.StartSpan(ctx, "niceagent/agent_runtime", "control_plane.callback.post", platform.Labels{
		"attempt_id": s.AttemptID,
		"path":       path,
	})
	defer func() { endSpan(err, nil) }()
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
	s.injectTrace(req)
	resp, err := s.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	spanLabels := platform.Labels{"http_status": fmt.Sprint(resp.StatusCode)}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		err = fmt.Errorf("control plane returned %s: %s", resp.Status, strings.TrimSpace(string(message)))
		endSpan(err, spanLabels)
		endSpan = func(error, platform.Labels) {}
		return err
	}
	if target == nil {
		endSpan(nil, spanLabels)
		endSpan = func(error, platform.Labels) {}
		return nil
	}
	err = json.NewDecoder(resp.Body).Decode(target)
	endSpan(err, spanLabels)
	endSpan = func(error, platform.Labels) {}
	return err
}

func (s *ControlPlaneSink) get(ctx context.Context, path string, target any) (err error) {
	ctx = s.requestContext(ctx)
	ctx, endSpan := platform.StartSpan(ctx, "niceagent/agent_runtime", "control_plane.callback.get", platform.Labels{
		"attempt_id": s.AttemptID,
		"path":       path,
	})
	defer func() { endSpan(err, nil) }()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.BaseURL+path, nil)
	if err != nil {
		return err
	}
	s.authorize(req)
	s.injectTrace(req)
	resp, err := s.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	spanLabels := platform.Labels{"http_status": fmt.Sprint(resp.StatusCode)}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		err = fmt.Errorf("control plane returned %s: %s", resp.Status, strings.TrimSpace(string(message)))
		endSpan(err, spanLabels)
		endSpan = func(error, platform.Labels) {}
		return err
	}
	err = json.NewDecoder(resp.Body).Decode(target)
	endSpan(err, spanLabels)
	endSpan = func(error, platform.Labels) {}
	return err
}

func (s *ControlPlaneSink) authorize(req *http.Request) {
	if s.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.Token)
	}
}

func (s *ControlPlaneSink) injectTrace(req *http.Request) {
	platform.InjectTraceHeaders(req.Context(), req.Header)
	if s.TraceID != "" {
		req.Header.Set("X-Trace-ID", s.TraceID)
	}
}

func (s *ControlPlaneSink) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (s *ControlPlaneSink) requestContext(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if s.TraceContext != nil && platform.TraceIDFromContext(ctx) == "" && platform.OpenTelemetryTraceIDFromContext(ctx) == "" {
		return s.TraceContext
	}
	return ctx
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
