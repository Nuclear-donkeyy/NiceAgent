package main

import (
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"strings"

	"niceagent/agent-runtime/internal/config"
	"niceagent/agent-runtime/internal/runtime"
	"niceagent/common/platform"
	"niceagent/common/protocol"
	"niceagent/common/sandbox"
)

func main() {
	cfg := config.FromEnv()
	logger := platform.NewLogger("agent-runtime")
	engine := runtime.NewEinoAgentEngine(newSandboxExecutor(cfg, logger))
	modelProvider, err := modelProviderFromEnv(cfg, logger)
	if err != nil {
		log.Fatal(err)
	}
	engine.Models = modelProvider

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", platform.Method(http.MethodGet, func(w http.ResponseWriter, _ *http.Request) {
		platform.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))
	mux.HandleFunc("/internal/runs/execute", platform.Method(http.MethodPost, func(w http.ResponseWriter, r *http.Request) {
		if !authorizeInternal(w, r, cfg.InternalAPIToken) {
			return
		}
		var input protocol.RunExecutionRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			platform.WriteError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		controlPlaneURL := input.ControlPlaneURL
		if controlPlaneURL == "" {
			controlPlaneURL = cfg.ControlPlaneURL
		}
		if controlPlaneURL == "" {
			platform.WriteError(w, http.StatusBadRequest, "control_plane_url is required")
			return
		}
		sink := runtime.NewControlPlaneSink(controlPlaneURL, cfg.InternalAPIToken)
		result := engine.Execute(r.Context(), input.Request, input.UserMessage, sink)
		platform.WriteJSON(w, http.StatusOK, result)
	}))

	logger.Info("starting agent runtime", "addr", cfg.Addr)
	if err := http.ListenAndServe(cfg.Addr, mux); err != nil {
		log.Fatal(err)
	}
}

func newSandboxExecutor(cfg config.Config, logger *slog.Logger) runtime.SandboxExecutor {
	if cfg.SandboxExecutorURL != "" {
		logger.Info("using http sandbox executor", "url", cfg.SandboxExecutorURL)
		return sandbox.NewHTTPExecutor(cfg.SandboxExecutorURL, cfg.InternalAPIToken)
	}
	logger.Warn("SANDBOX_EXECUTOR_URL is empty; falling back to local sandbox executor")
	return sandbox.NewExecutor()
}

func modelProviderFromEnv(cfg config.Config, logger *slog.Logger) (runtime.ModelProvider, error) {
	switch cfg.ModelProvider {
	case "", "mock":
		logger.Info("using mock model provider")
		return runtime.MockProvider{}, nil
	case "openai-compatible":
		modelProvider, err := runtime.NewOpenAICompatibleProvider(runtime.OpenAICompatibleProviderConfig{
			BaseURL: cfg.ModelBaseURL,
			APIKey:  cfg.ModelAPIKey,
			Model:   cfg.ModelName,
			Timeout: cfg.ModelTimeout,
		})
		if err != nil {
			return nil, err
		}
		logger.Info("using openai-compatible model provider", "base_url", strings.TrimRight(cfg.ModelBaseURL, "/"), "model", cfg.ModelName)
		return modelProvider, nil
	default:
		return nil, fmt.Errorf("unsupported MODEL_PROVIDER %q", cfg.ModelProvider)
	}
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
