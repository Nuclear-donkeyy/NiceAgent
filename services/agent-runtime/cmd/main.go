package main

import (
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"niceagent/agent-runtime/internal/runtime"
	"niceagent/common/platform"
	"niceagent/common/protocol"
	"niceagent/common/sandbox"
)

func main() {
	addr := env("AGENT_RUNTIME_ADDR", ":8081")
	logger := platform.NewLogger("agent-runtime")
	engine := runtime.NewEinoAgentEngine(newSandboxExecutor(logger))
	modelProvider, err := modelProviderFromEnv(logger)
	if err != nil {
		log.Fatal(err)
	}
	engine.Models = modelProvider

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

func modelProviderFromEnv(logger *slog.Logger) (runtime.ModelProvider, error) {
	provider := strings.TrimSpace(env("MODEL_PROVIDER", "mock"))
	switch provider {
	case "", "mock":
		logger.Info("using mock model provider")
		return runtime.MockProvider{}, nil
	case "openai-compatible":
		modelProvider, err := runtime.NewOpenAICompatibleProvider(runtime.OpenAICompatibleProviderConfig{
			BaseURL: os.Getenv("MODEL_BASE_URL"),
			APIKey:  os.Getenv("MODEL_API_KEY"),
			Model:   os.Getenv("MODEL_NAME"),
			Timeout: modelTimeout(),
		})
		if err != nil {
			return nil, err
		}
		logger.Info("using openai-compatible model provider", "base_url", strings.TrimRight(os.Getenv("MODEL_BASE_URL"), "/"), "model", os.Getenv("MODEL_NAME"))
		return modelProvider, nil
	default:
		return nil, fmt.Errorf("unsupported MODEL_PROVIDER %q", provider)
	}
}

func modelTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("MODEL_TIMEOUT_SECONDS"))
	if raw == "" {
		return 2 * time.Minute
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		log.Fatalf("MODEL_TIMEOUT_SECONDS must be a positive integer, got %q", raw)
	}
	return time.Duration(seconds) * time.Second
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
