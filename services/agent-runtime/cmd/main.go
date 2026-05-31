package main

import (
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"strings"

	"niceagent/agent-runtime/internal/config"
	"niceagent/agent-runtime/internal/engine"
	"niceagent/agent-runtime/internal/httpapi"
	"niceagent/agent-runtime/internal/modelprovider"
	"niceagent/agent-runtime/internal/tools"
	"niceagent/common/platform"
	"niceagent/common/sandbox"
)

func main() {
	cfg := config.FromEnv()
	logger := platform.NewLogger("agent-runtime")
	agentEngine := engine.NewEinoAgentEngine(newSandboxExecutor(cfg, logger))
	modelProvider, err := modelProviderFromEnv(cfg, logger)
	if err != nil {
		log.Fatal(err)
	}
	agentEngine.Models = modelProvider
	handler := httpapi.NewHandler(agentEngine, cfg.ControlPlaneURL, cfg.InternalAPIToken)

	logger.Info("starting agent runtime", "addr", cfg.Addr)
	if err := http.ListenAndServe(cfg.Addr, handler); err != nil {
		log.Fatal(err)
	}
}

func newSandboxExecutor(cfg config.Config, logger *slog.Logger) tools.SandboxExecutor {
	if cfg.SandboxExecutorURL != "" {
		logger.Info("using http sandbox executor", "url", cfg.SandboxExecutorURL)
		return sandbox.NewHTTPExecutor(cfg.SandboxExecutorURL, cfg.InternalAPIToken)
	}
	logger.Warn("SANDBOX_EXECUTOR_URL is empty; falling back to local sandbox executor")
	return sandbox.NewExecutor()
}

func modelProviderFromEnv(cfg config.Config, logger *slog.Logger) (modelprovider.Provider, error) {
	switch cfg.ModelProvider {
	case "", "mock":
		logger.Info("using mock model provider")
		return modelprovider.MockProvider{}, nil
	case "openai-compatible":
		modelProvider, err := modelprovider.NewOpenAICompatibleProvider(modelprovider.OpenAICompatibleProviderConfig{
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
