package main

import (
	"encoding/json"
	"log"
	"log/slog"
	"net/http"
	"os"

	"niceagent/internal/platform"
	"niceagent/internal/protocol"
	"niceagent/internal/runtime"
	"niceagent/internal/sandbox"
)

func main() {
	addr := env("AGENT_RUNTIME_ADDR", ":8081")
	logger := platform.NewLogger("agent-runtime")
	engine := runtime.NewEngine(sandbox.NewExecutor())

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", platform.Method(http.MethodGet, func(w http.ResponseWriter, _ *http.Request) {
		platform.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))
	mux.HandleFunc("/internal/runs/execute", platform.Method(http.MethodPost, func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Request     protocol.RunRequest `json:"request"`
			UserMessage string              `json:"user_message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			platform.WriteError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		result := engine.Execute(r.Context(), input.Request, input.UserMessage, loggingSink{log: logger})
		platform.WriteJSON(w, http.StatusOK, result)
	}))

	logger.Info("starting agent runtime", "addr", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

type loggingSink struct {
	log *slog.Logger
}

func (s loggingSink) Emit(runID string, typ protocol.RunEventType, message string, payload any) error {
	s.log.Info("run_event", "run_id", runID, "type", typ, "message", message, "payload", payload)
	return nil
}

func (s loggingSink) Complete(runID string, content string) error {
	s.log.Info("run_complete", "run_id", runID, "content_bytes", len(content))
	return nil
}

func (s loggingSink) Fail(runID string, message string) error {
	s.log.Error("run_failed", "run_id", runID, "message", message)
	return nil
}

func (s loggingSink) IsCanceled(string) bool {
	return false
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
