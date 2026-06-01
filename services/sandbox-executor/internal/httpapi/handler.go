package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"niceagent/common/platform"
	"niceagent/common/protocol"
)

type Handler struct {
	executor   Executor
	token      string
	metrics    *platform.Metrics
	logger     *slog.Logger
	healthInfo HealthInfo
}

type HealthInfo struct {
	ExecutorMode           string   `json:"executor_mode,omitempty"`
	ContainerImage         string   `json:"container_image,omitempty"`
	ContainerAllowedImages []string `json:"container_allowed_images,omitempty"`
}

type HandlerOptions struct {
	Logger *slog.Logger
	Health HealthInfo
}

type Executor interface {
	Execute(ctx context.Context, request protocol.SandboxCommand) protocol.SandboxResult
}

func NewHandler(executor Executor, token string) http.Handler {
	return NewHandlerWithLogger(executor, token, nil)
}

func NewHandlerWithLogger(executor Executor, token string, logger *slog.Logger) http.Handler {
	return NewHandlerWithOptions(executor, token, HandlerOptions{Logger: logger})
}

func NewHandlerWithOptions(executor Executor, token string, opts HandlerOptions) http.Handler {
	h := Handler{executor: executor, token: token, metrics: platform.NewMetrics("sandbox_executor"), logger: opts.Logger, healthInfo: opts.Health}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", platform.Method(http.MethodGet, h.health))
	mux.Handle("/metrics", h.metrics.Handler())
	mux.HandleFunc("/internal/sandbox/exec", platform.Method(http.MethodPost, h.exec))
	handler := platform.MetricsMiddleware(h.metrics, mux)
	handler = platform.RequestLogger(h.logger, "sandbox_executor", handler)
	handler = platform.WithTraceID(handler)
	return platform.WithRequestID(platform.OpenTelemetryMiddleware("sandbox_executor", handler))
}

func (h Handler) health(w http.ResponseWriter, _ *http.Request) {
	response := map[string]any{"status": "ok"}
	if h.healthInfo.ExecutorMode != "" {
		response["executor_mode"] = h.healthInfo.ExecutorMode
	}
	if h.healthInfo.ContainerImage != "" {
		response["container_image"] = h.healthInfo.ContainerImage
	}
	if len(h.healthInfo.ContainerAllowedImages) > 0 {
		response["container_allowed_images"] = h.healthInfo.ContainerAllowedImages
	}
	platform.WriteJSON(w, http.StatusOK, response)
}

func (h Handler) exec(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r) {
		return
	}
	var request protocol.SandboxCommand
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	ctx, endSpan := platform.StartSpan(r.Context(), "niceagent/sandbox_executor", "sandbox.exec", platform.Labels{
		"run_id":       request.RunID,
		"workspace_id": request.WorkspaceID,
	})
	result := h.executor.Execute(ctx, request)
	spanLabels := platform.Labels{"exit_code": fmt.Sprint(result.ExitCode)}
	if result.Error != "" {
		endSpan(fmt.Errorf("%s", result.Error), spanLabels)
	} else {
		endSpan(nil, spanLabels)
	}
	h.metrics.IncCounter("niceagent_sandbox_exec_total", platform.Labels{"exit_code": fmt.Sprint(result.ExitCode)})
	platform.WriteJSON(w, http.StatusOK, result)
}

func (h Handler) authorize(w http.ResponseWriter, r *http.Request) bool {
	if h.token == "" {
		return true
	}
	if r.Header.Get("Authorization") == "Bearer "+h.token {
		return true
	}
	platform.WriteError(w, http.StatusUnauthorized, "unauthorized")
	return false
}
