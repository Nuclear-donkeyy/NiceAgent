package main

import (
	"encoding/json"
	"log"
	"log/slog"
	"net/http"
	"os"

	"niceagent/agent-runtime/internal/runtime"
	"niceagent/common/platform"
	"niceagent/common/protocol"
	"niceagent/common/sandbox"
)

func main() {
	addr := env("AGENT_RUNTIME_ADDR", ":8081")
	logger := platform.NewLogger("agent-runtime")
	engine := runtime.NewEngine(newSandboxExecutor(logger))

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", platform.Method(http.MethodGet, func(w http.ResponseWriter, _ *http.Request) {
		platform.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))
	mux.HandleFunc("/internal/runs/execute", platform.Method(http.MethodPost, func(w http.ResponseWriter, r *http.Request) {
		if !authorizeInternal(w, r) {
			return
		}
		var input protocol.RunExecutionRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			platform.WriteError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		controlPlaneURL := input.ControlPlaneURL
		if controlPlaneURL == "" {
			controlPlaneURL = os.Getenv("CONTROL_PLANE_URL")
		}
		if controlPlaneURL == "" {
			platform.WriteError(w, http.StatusBadRequest, "control_plane_url is required")
			return
		}
		sink := runtime.NewControlPlaneSink(controlPlaneURL, os.Getenv("INTERNAL_API_TOKEN"))
		result := engine.Execute(r.Context(), input.Request, input.UserMessage, sink)
		platform.WriteJSON(w, http.StatusOK, result)
	}))

	logger.Info("starting agent runtime", "addr", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func newSandboxExecutor(logger *slog.Logger) runtime.SandboxExecutor {
	if executorURL := os.Getenv("SANDBOX_EXECUTOR_URL"); executorURL != "" {
		logger.Info("using http sandbox executor", "url", executorURL)
		return sandbox.NewHTTPExecutor(executorURL, os.Getenv("INTERNAL_API_TOKEN"))
	}
	logger.Warn("SANDBOX_EXECUTOR_URL is empty; falling back to local sandbox executor")
	return sandbox.NewExecutor()
}

func authorizeInternal(w http.ResponseWriter, r *http.Request) bool {
	token := os.Getenv("INTERNAL_API_TOKEN")
	if token == "" {
		return true
	}
	if r.Header.Get("Authorization") == "Bearer "+token {
		return true
	}
	platform.WriteError(w, http.StatusUnauthorized, "unauthorized")
	return false
}
