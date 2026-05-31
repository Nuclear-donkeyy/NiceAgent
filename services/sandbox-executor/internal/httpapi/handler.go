package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"niceagent/common/platform"
	"niceagent/common/protocol"
)

type Handler struct {
	executor Executor
	token    string
	metrics  *platform.Metrics
}

type Executor interface {
	Execute(ctx context.Context, request protocol.SandboxCommand) protocol.SandboxResult
}

func NewHandler(executor Executor, token string) http.Handler {
	h := Handler{executor: executor, token: token, metrics: platform.NewMetrics("sandbox_executor")}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", platform.Method(http.MethodGet, h.health))
	mux.Handle("/metrics", h.metrics.Handler())
	mux.HandleFunc("/internal/sandbox/exec", platform.Method(http.MethodPost, h.exec))
	handler := platform.WithTraceID(platform.MetricsMiddleware(h.metrics, mux))
	return platform.OpenTelemetryMiddleware("sandbox_executor", handler)
}

func (h Handler) health(w http.ResponseWriter, _ *http.Request) {
	platform.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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
