package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"niceagent/agent-runtime/internal/engine"
	"niceagent/agent-runtime/internal/sink"
	"niceagent/common/platform"
	"niceagent/common/protocol"
)

type Handler struct {
	Engine          engine.AgentEngine
	ControlPlaneURL string
	InternalToken   string
	RuntimeID       string
	Metrics         *platform.Metrics
	ModelHealth     ModelHealthReporter
	Logger          *slog.Logger
}

type ModelHealthReporter interface {
	ModelProviderHealth() protocol.ModelProviderHealth
}

type HandlerOptions struct {
	RuntimeID   string
	ModelHealth ModelHealthReporter
	Metrics     *platform.Metrics
	Logger      *slog.Logger
}

func NewHandler(agent engine.AgentEngine, controlPlaneURL, internalToken string) http.Handler {
	return NewHandlerWithRuntimeID(agent, controlPlaneURL, internalToken, "agent-runtime-http")
}

func NewHandlerWithRuntimeID(agent engine.AgentEngine, controlPlaneURL, internalToken, runtimeID string) http.Handler {
	return NewHandlerWithOptions(agent, controlPlaneURL, internalToken, HandlerOptions{RuntimeID: runtimeID})
}

func NewHandlerWithOptions(agent engine.AgentEngine, controlPlaneURL, internalToken string, opts HandlerOptions) http.Handler {
	metrics := opts.Metrics
	if metrics == nil {
		metrics = platform.NewMetrics("agent_runtime")
	}
	runtimeID := opts.RuntimeID
	if runtimeID == "" {
		runtimeID = "agent-runtime-http"
	}
	h := Handler{
		Engine:          agent,
		ControlPlaneURL: controlPlaneURL,
		InternalToken:   internalToken,
		RuntimeID:       runtimeID,
		Metrics:         metrics,
		ModelHealth:     opts.ModelHealth,
		Logger:          opts.Logger,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", platform.Method(http.MethodGet, h.health))
	mux.Handle("/metrics", h.Metrics.Handler())
	mux.HandleFunc("/internal/runs/execute", platform.Method(http.MethodPost, h.executeRun))
	handler := platform.MetricsMiddleware(h.Metrics, mux)
	handler = platform.RequestLogger(h.Logger, "agent_runtime", handler)
	handler = platform.WithTraceID(handler)
	return platform.WithRequestID(platform.OpenTelemetryMiddleware("agent_runtime", handler))
}

func (h Handler) health(w http.ResponseWriter, _ *http.Request) {
	response := map[string]any{"status": "ok"}
	if h.ModelHealth != nil {
		response["model_provider"] = h.ModelHealth.ModelProviderHealth()
	}
	platform.WriteJSON(w, http.StatusOK, response)
}

func (h Handler) executeRun(w http.ResponseWriter, r *http.Request) {
	if !authorizeInternal(w, r, h.InternalToken) {
		return
	}
	var input protocol.RunExecutionRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	controlPlaneURL := input.ControlPlaneURL
	if controlPlaneURL == "" {
		controlPlaneURL = h.ControlPlaneURL
	}
	if controlPlaneURL == "" {
		platform.WriteError(w, http.StatusBadRequest, "control_plane_url is required")
		return
	}
	controlSink := sink.NewControlPlaneSink(controlPlaneURL, h.InternalToken).
		WithAttempt(input.Request.AttemptID).
		WithTraceID(platform.TraceIDFromContext(r.Context())).
		WithTraceContext(r.Context())
	if input.Request.AttemptID != "" {
		claimed, err := controlSink.Claim(input.Request.RunID, input.Request.AttemptID, h.RuntimeID, 600)
		if err != nil {
			platform.WriteError(w, http.StatusConflict, err.Error())
			return
		}
		if isTerminalRunStatus(claimed.Status) {
			platform.WriteJSON(w, http.StatusOK, protocol.RunResult{RunID: claimed.ID, Status: claimed.Status})
			return
		}
	}
	result := h.Engine.Execute(r.Context(), input.Request, input.UserMessage, controlSink)
	h.Metrics.IncCounter("niceagent_runtime_runs_total", platform.Labels{"status": string(result.Status)})
	h.recordModelMetrics(result)
	platform.WriteJSON(w, http.StatusOK, result)
}

func (h Handler) recordModelMetrics(result protocol.RunResult) {
	usage := protocol.NormalizeRunUsage(result.Usage)
	provider := usage.Provider
	if provider == "" {
		provider = result.Usage.Provider
	}
	if provider == "" {
		provider = "unknown"
	}
	model := usage.Model
	if model == "" {
		model = "unknown"
	}
	errorClass := usage.ErrorClass
	if errorClass == "" {
		errorClass = "none"
	}
	fallback := "false"
	if usage.FallbackTo != "" {
		fallback = "true"
	}
	h.Metrics.IncCounter("niceagent_model_runs_total", platform.Labels{
		"provider":    provider,
		"model":       model,
		"status":      string(result.Status),
		"error_class": errorClass,
		"fallback":    fallback,
	})
	if usage.LatencyMillis > 0 {
		h.Metrics.ObserveDuration("niceagent_model_latency_seconds", platform.Labels{"provider": provider, "model": model}, time.Duration(usage.LatencyMillis)*time.Millisecond)
	}
}

func isTerminalRunStatus(status protocol.RunStatus) bool {
	return status == protocol.RunSucceeded || status == protocol.RunFailed || status == protocol.RunCanceled
}

func authorizeInternal(w http.ResponseWriter, r *http.Request, token string) bool {
	if token == "" {
		return true
	}
	if r.Header.Get("Authorization") == "Bearer "+token {
		return true
	}
	platform.WriteError(w, http.StatusUnauthorized, "unauthorized")
	return false
}
