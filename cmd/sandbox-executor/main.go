package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"

	"niceagent/internal/platform"
	"niceagent/internal/protocol"
	"niceagent/internal/sandbox"
)

func main() {
	addr := env("SANDBOX_EXECUTOR_ADDR", ":8082")
	logger := platform.NewLogger("sandbox-executor")
	executor := sandbox.NewExecutor()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", platform.Method(http.MethodGet, func(w http.ResponseWriter, _ *http.Request) {
		platform.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))
	mux.HandleFunc("/internal/sandbox/exec", platform.Method(http.MethodPost, func(w http.ResponseWriter, r *http.Request) {
		var request protocol.SandboxCommand
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			platform.WriteError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		platform.WriteJSON(w, http.StatusOK, executor.Execute(r.Context(), request))
	}))

	logger.Info("starting sandbox executor", "addr", addr)
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
