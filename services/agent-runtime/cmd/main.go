package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"

	"niceagent/agent-runtime/internal/config"
	"niceagent/agent-runtime/internal/engine"
	"niceagent/agent-runtime/internal/httpapi"
	"niceagent/agent-runtime/internal/modelprovider"
	"niceagent/agent-runtime/internal/runqueue"
	"niceagent/agent-runtime/internal/tools"
	"niceagent/common/platform"
	"niceagent/common/sandbox"
)

func main() {
	cfg := config.FromEnv()
	if err := cfg.Validate(); err != nil {
		log.Fatal(err)
	}
	logger := platform.NewLogger("agent-runtime")
	shutdownTelemetry, err := platform.InitOpenTelemetry(context.Background(), platform.OpenTelemetryConfigFromEnv("agent_runtime", cfg.Environment))
	if err != nil {
		log.Fatalf("init opentelemetry: %v", err)
	}
	defer shutdownTelemetryWithTimeout(shutdownTelemetry)
	agentEngine := engine.NewEinoAgentEngine(newSandboxExecutor(cfg, logger))
	metrics := platform.NewMetrics("agent_runtime")
	agentEngine.Tools.Metrics = metrics
	configureHTTPSkillTransport(cfg, agentEngine, logger)
	configureSkillRateLimiter(cfg, agentEngine, logger)
	configureSkillRiskPolicy(cfg, agentEngine, logger)
	modelProvider, err := modelProviderFromEnv(cfg, logger)
	if err != nil {
		log.Fatal(err)
	}
	agentEngine.Models = modelProvider
	startModelHealthProbe(cfg, modelProvider, logger, metrics)
	startQueueWorker(cfg, agentEngine, logger, metrics)
	var modelHealth httpapi.ModelHealthReporter
	if reporter, ok := modelProvider.(httpapi.ModelHealthReporter); ok {
		modelHealth = reporter
	}
	handler := httpapi.NewHandlerWithOptions(agentEngine, cfg.ControlPlaneURL, cfg.InternalAPIToken, httpapi.HandlerOptions{
		RuntimeID:   cfg.RuntimeID,
		ModelHealth: modelHealth,
		Metrics:     metrics,
		Logger:      logger,
	})

	logger.Info("starting agent runtime", "addr", cfg.Addr)
	if err := http.ListenAndServe(cfg.Addr, handler); err != nil {
		log.Fatal(err)
	}
}

func configureHTTPSkillTransport(cfg config.Config, agentEngine *engine.EinoAgentEngine, logger *slog.Logger) {
	if agentEngine == nil || agentEngine.Tools == nil {
		return
	}
	agentEngine.Tools.AllowLocalHTTP = cfg.HTTPSkillAllowLocalTargets
	if cfg.HTTPSkillAllowLocalTargets {
		logger.Warn("HTTP skill local targets are enabled; use only for local smoke tests or trusted internal environments")
	}
	if strings.TrimSpace(cfg.HTTPSkillCAFile) == "" {
		return
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	pemData, err := os.ReadFile(cfg.HTTPSkillCAFile)
	if err != nil {
		log.Fatalf("read HTTP_SKILL_CA_FILE: %v", err)
	}
	if ok := pool.AppendCertsFromPEM(pemData); !ok {
		log.Fatalf("HTTP_SKILL_CA_FILE did not contain a valid PEM certificate")
	}
	agentEngine.Tools.Client = &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		},
	}
	logger.Info("using HTTP skill custom CA bundle", "path", cfg.HTTPSkillCAFile)
}

func configureSkillRateLimiter(cfg config.Config, agentEngine *engine.EinoAgentEngine, logger *slog.Logger) {
	if agentEngine == nil || agentEngine.Tools == nil {
		return
	}
	switch cfg.SkillRateLimitMode {
	case "", "local":
		logger.Info("using local skill rate limiter")
	case "redis":
		logger.Info("using redis skill rate limiter", "addr", cfg.RedisAddr, "prefix", cfg.SkillRateLimitPrefix)
		agentEngine.Tools.RateLimiter = tools.NewRedisSkillRateLimiter(cfg.RedisAddr, cfg.SkillRateLimitPrefix)
	}
}

func configureSkillRiskPolicy(cfg config.Config, agentEngine *engine.EinoAgentEngine, logger *slog.Logger) {
	if agentEngine == nil || agentEngine.Tools == nil {
		return
	}
	policy := tools.SkillRiskPolicy(strings.TrimSpace(cfg.SkillRiskPolicy))
	if policy == "" {
		policy = tools.SkillRiskPolicyAllow
	}
	agentEngine.Tools.RiskPolicy = policy
	logger.Info("using skill risk policy", "policy", string(policy))
}

func shutdownTelemetryWithTimeout(shutdown func(context.Context) error) {
	if shutdown == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = shutdown(ctx)
}

func startModelHealthProbe(cfg config.Config, modelProvider model.ToolCallingChatModel, logger *slog.Logger, metrics *platform.Metrics) {
	if !cfg.ModelHealthProbeEnabled {
		return
	}
	logger.Info(
		"starting model health probe",
		"interval", cfg.ModelHealthProbeInterval.String(),
		"timeout", cfg.ModelHealthProbeTimeout.String(),
	)
	modelprovider.StartHealthProbeLoop(context.Background(), modelProvider, modelprovider.HealthProbeConfig{
		Interval:     cfg.ModelHealthProbeInterval,
		Timeout:      cfg.ModelHealthProbeTimeout,
		InitialDelay: cfg.ModelHealthProbeInitialDelay,
	}, logger, metrics)
}

func startQueueWorker(cfg config.Config, agentEngine engine.AgentEngine, logger *slog.Logger, metrics *platform.Metrics) {
	if cfg.QueueMode == "" || cfg.QueueMode == "disabled" || cfg.QueueMode == "http" {
		return
	}
	if cfg.QueueMode != "redis" {
		log.Fatalf("unsupported RUNTIME_QUEUE_MODE %q", cfg.QueueMode)
	}
	if cfg.RedisAddr == "" {
		log.Fatal("REDIS_ADDR is required when RUNTIME_QUEUE_MODE=redis")
	}
	if cfg.ControlPlaneURL == "" {
		log.Fatal("CONTROL_PLANE_URL is required when RUNTIME_QUEUE_MODE=redis")
	}
	worker := runqueue.NewRedisWorker(runqueue.Config{
		RedisAddr:         cfg.RedisAddr,
		Stream:            cfg.RunQueueStream,
		Group:             cfg.RunQueueGroup,
		Consumer:          cfg.RunQueueConsumer,
		ControlPlaneURL:   cfg.ControlPlaneURL,
		InternalToken:     cfg.InternalAPIToken,
		RuntimeID:         cfg.RuntimeID,
		ReclaimMinIdle:    cfg.RunQueueReclaimMinIdle,
		ReclaimCount:      cfg.RunQueueReclaimCount,
		MaxDeliveries:     cfg.RunQueueMaxDeliveries,
		DeadLetterStream:  cfg.RunQueueDLQStream,
		DeadLetterMaxLen:  cfg.RunQueueDLQMaxLen,
		LeaseSeconds:      cfg.RunAttemptLeaseSeconds,
		HeartbeatInterval: cfg.RunAttemptHeartbeat,
		Metrics:           metrics,
	}, agentEngine, logger)
	logger.Info(
		"starting redis run queue worker",
		"addr", cfg.RedisAddr,
		"stream", cfg.RunQueueStream,
		"group", cfg.RunQueueGroup,
		"consumer", cfg.RunQueueConsumer,
		"reclaim_min_idle", cfg.RunQueueReclaimMinIdle.String(),
		"max_deliveries", cfg.RunQueueMaxDeliveries,
		"dlq_max_len", cfg.RunQueueDLQMaxLen,
	)
	go worker.Run(context.Background())
}

func newSandboxExecutor(cfg config.Config, logger *slog.Logger) tools.SandboxExecutor {
	if cfg.SandboxExecutorURL != "" {
		logger.Info("using http sandbox executor", "url", cfg.SandboxExecutorURL)
		return sandbox.NewHTTPExecutor(cfg.SandboxExecutorURL, cfg.InternalAPIToken)
	}
	logger.Warn("SANDBOX_EXECUTOR_URL is empty; falling back to local sandbox executor")
	return sandbox.NewExecutor()
}

func modelProviderFromEnv(cfg config.Config, logger *slog.Logger) (model.ToolCallingChatModel, error) {
	switch cfg.ModelProvider {
	case "", "mock":
		logger.Info("using mock model provider")
		return modelprovider.MockChatModel{}, nil
	case "openai-compatible":
		providerID := "openai-compatible"
		if cfg.ModelProviderProfile != "" {
			providerID = cfg.ModelProviderProfile
		}
		modelProvider, err := newOpenAICompatibleModel(cfg, modelprovider.OpenAICompatibleProviderConfig{
			ID:      providerID,
			BaseURL: cfg.ModelBaseURL,
			APIKey:  cfg.ModelAPIKey,
			Model:   cfg.ModelName,
		})
		if err != nil {
			return nil, err
		}
		modelProvider, err = withFallbackModel(cfg, modelProvider, "openai-compatible:"+cfg.ModelName, logger)
		if err != nil {
			return nil, err
		}
		logger.Info("using openai-compatible model provider", "profile", cfg.ModelProviderProfile, "base_url", strings.TrimRight(cfg.ModelBaseURL, "/"), "model", cfg.ModelName)
		return modelProvider, nil
	default:
		return nil, fmt.Errorf("unsupported MODEL_PROVIDER %q", cfg.ModelProvider)
	}
}

func newOpenAICompatibleModel(cfg config.Config, providerConfig modelprovider.OpenAICompatibleProviderConfig) (model.ToolCallingChatModel, error) {
	providerConfig.Timeout = cfg.ModelTimeout
	providerConfig.Pricing = modelprovider.PricingConfig{
		InputPer1M:           cfg.ModelInputPricePer1M,
		CachedInputPer1M:     cfg.ModelCachedInputPricePer1M,
		OutputPer1M:          cfg.ModelOutputPricePer1M,
		ReasoningOutputPer1M: cfg.ModelReasoningPricePer1M,
		Currency:             cfg.ModelPriceCurrency,
	}
	providerConfig.RateLimit = modelprovider.RateLimitConfig{
		RequestsPerMinute: cfg.ModelRequestsPerMinute,
		MaxConcurrent:     cfg.ModelMaxConcurrentRequests,
	}
	return modelprovider.NewOpenAICompatibleChatModel(context.Background(), providerConfig)
}

func withFallbackModel(cfg config.Config, primary model.ToolCallingChatModel, primaryName string, logger *slog.Logger) (model.ToolCallingChatModel, error) {
	switch strings.TrimSpace(cfg.ModelFallbackProvider) {
	case "":
		return primary, nil
	case "mock":
		logger.Info("using mock model fallback", "primary", primaryName)
		return modelprovider.NewFallbackChatModel(
			modelprovider.FallbackTarget{Name: primaryName, Model: primary},
			modelprovider.FallbackTarget{Name: "mock", Model: modelprovider.MockChatModel{}},
		), nil
	case "openai-compatible":
		fallback, err := newOpenAICompatibleModel(cfg, modelprovider.OpenAICompatibleProviderConfig{
			ID:      "openai-compatible-fallback",
			BaseURL: cfg.ModelFallbackBaseURL,
			APIKey:  cfg.ModelFallbackAPIKey,
			Model:   cfg.ModelFallbackName,
		})
		if err != nil {
			return nil, err
		}
		fallbackName := "openai-compatible:" + cfg.ModelFallbackName
		logger.Info("using openai-compatible model fallback", "primary", primaryName, "fallback", fallbackName)
		return modelprovider.NewFallbackChatModel(
			modelprovider.FallbackTarget{Name: primaryName, Model: primary},
			modelprovider.FallbackTarget{Name: fallbackName, Model: fallback},
		), nil
	default:
		return nil, fmt.Errorf("unsupported MODEL_FALLBACK_PROVIDER %q", cfg.ModelFallbackProvider)
	}
}
