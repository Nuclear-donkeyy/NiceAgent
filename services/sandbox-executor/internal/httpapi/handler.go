package httpapi

import (
	"context"
	"encoding/json"
	"net/http"

	"niceagent/common/platform"
	"niceagent/common/protocol"
)

type Handler struct {
	executor Executor
	token    string
}

type Executor interface {
	Execute(ctx context.Context, request protocol.SandboxCommand) protocol.SandboxResult
}

func NewHandler(executor Executor, token string) http.Handler {
	h := Handler{executor: executor, token: token}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", platform.Method(http.MethodGet, h.health))
	mux.HandleFunc("/internal/sandbox/exec", platform.Method(http.MethodPost, h.exec))
	return mux
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
	platform.WriteJSON(w, http.StatusOK, h.executor.Execute(r.Context(), request))
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
