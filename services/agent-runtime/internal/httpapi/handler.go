package httpapi

import (
	"encoding/json"
	"net/http"

	"niceagent/agent-runtime/internal/engine"
	"niceagent/agent-runtime/internal/sink"
	"niceagent/common/platform"
	"niceagent/common/protocol"
)

type Handler struct {
	Engine          engine.AgentEngine
	ControlPlaneURL string
	InternalToken   string
}

func NewHandler(agent engine.AgentEngine, controlPlaneURL, internalToken string) http.Handler {
	h := Handler{
		Engine:          agent,
		ControlPlaneURL: controlPlaneURL,
		InternalToken:   internalToken,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", platform.Method(http.MethodGet, h.health))
	mux.HandleFunc("/internal/runs/execute", platform.Method(http.MethodPost, h.executeRun))
	return mux
}

func (h Handler) health(w http.ResponseWriter, _ *http.Request) {
	platform.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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
	result := h.Engine.Execute(r.Context(), input.Request, input.UserMessage, sink.NewControlPlaneSink(controlPlaneURL, h.InternalToken))
	platform.WriteJSON(w, http.StatusOK, result)
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
