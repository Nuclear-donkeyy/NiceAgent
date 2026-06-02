package config

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"niceagent/common/protocol"
)

type Config struct {
	Addr                         string
	Environment                  string
	RuntimeID                    string
	QueueMode                    string
	ControlPlaneURL              string
	InternalAPIToken             string
	InternalTokenRequired        bool
	SandboxExecutorURL           string
	RedisAddr                    string
	SkillRateLimitMode           string
	SkillRateLimitPrefix         string
	SkillRiskPolicy              string
	RunQueueStream               string
	RunQueueGroup                string
	RunQueueConsumer             string
	RunQueueReclaimMinIdle       time.Duration
	RunQueueReclaimCount         int64
	RunQueueMaxDeliveries        int64
	RunQueueDLQStream            string
	RunQueueDLQMaxLen            int64
	RunAttemptLeaseSeconds       int
	RunAttemptHeartbeat          time.Duration
	ModelProvider                string
	ModelProviderProfile         string
	ModelBaseURL                 string
	ModelAPIKey                  string
	ModelName                    string
	ModelFallbackProvider        string
	ModelFallbackBaseURL         string
	ModelFallbackAPIKey          string
	ModelFallbackName            string
	ModelTimeout                 time.Duration
	ModelInputPricePer1M         float64
	ModelCachedInputPricePer1M   float64
	ModelOutputPricePer1M        float64
	ModelReasoningPricePer1M     float64
	ModelPriceCurrency           string
	ModelRequestsPerMinute       int
	ModelMaxConcurrentRequests   int
	ModelHealthProbeEnabled      bool
	ModelHealthProbeInterval     time.Duration
	ModelHealthProbeTimeout      time.Duration
	ModelHealthProbeInitialDelay time.Duration
}

func FromEnv() Config {
	environment := strings.TrimSpace(env("NICEAGENT_ENV", "local"))
	profile := strings.TrimSpace(os.Getenv("MODEL_PROVIDER_PROFILE"))
	modelProvider := strings.TrimSpace(os.Getenv("MODEL_PROVIDER"))
	if modelProvider == "" {
		if normalizeModelProviderProfile(profile) == "deepseek" {
			modelProvider = "openai-compatible"
		} else {
			modelProvider = "mock"
		}
	}
	cfg := Config{
		Addr:                         env("AGENT_RUNTIME_ADDR", ":8081"),
		Environment:                  environment,
		RuntimeID:                    runtimeID(),
		QueueMode:                    strings.TrimSpace(env("RUNTIME_QUEUE_MODE", "disabled")),
		ControlPlaneURL:              os.Getenv("CONTROL_PLANE_URL"),
		InternalAPIToken:             strings.TrimSpace(os.Getenv("INTERNAL_API_TOKEN")),
		InternalTokenRequired:        boolFromEnv("INTERNAL_API_TOKEN_REQUIRED", isNonLocalEnvironment(environment)),
		SandboxExecutorURL:           os.Getenv("SANDBOX_EXECUTOR_URL"),
		RedisAddr:                    os.Getenv("REDIS_ADDR"),
		SkillRateLimitMode:           strings.TrimSpace(env("SKILL_RATE_LIMIT_MODE", "local")),
		SkillRateLimitPrefix:         strings.TrimSpace(env("SKILL_RATE_LIMIT_PREFIX", "niceagent:skill-rate")),
		SkillRiskPolicy:              strings.TrimSpace(env("SKILL_RISK_POLICY", "allow")),
		RunQueueStream:               env("RUN_QUEUE_STREAM", "niceagent:runs"),
		RunQueueGroup:                env("RUN_QUEUE_GROUP", "agent-runtimes"),
		RunQueueConsumer:             runtimeConsumer(),
		RunQueueReclaimMinIdle:       secondsDuration("RUN_QUEUE_RECLAIM_MIN_IDLE_SECONDS", 60*time.Second),
		RunQueueReclaimCount:         int64FromEnv("RUN_QUEUE_RECLAIM_COUNT", 1),
		RunQueueMaxDeliveries:        int64FromEnv("RUN_QUEUE_MAX_DELIVERIES", 5),
		RunQueueDLQStream:            os.Getenv("RUN_QUEUE_DLQ_STREAM"),
		RunQueueDLQMaxLen:            int64FromEnv("RUN_QUEUE_DLQ_MAX_LEN", 0),
		RunAttemptLeaseSeconds:       intFromEnv("RUN_ATTEMPT_LEASE_SECONDS", 600),
		RunAttemptHeartbeat:          secondsDuration("RUN_ATTEMPT_HEARTBEAT_SECONDS", 60*time.Second),
		ModelProvider:                modelProvider,
		ModelProviderProfile:         profile,
		ModelBaseURL:                 os.Getenv("MODEL_BASE_URL"),
		ModelAPIKey:                  os.Getenv("MODEL_API_KEY"),
		ModelName:                    os.Getenv("MODEL_NAME"),
		ModelFallbackProvider:        strings.TrimSpace(os.Getenv("MODEL_FALLBACK_PROVIDER")),
		ModelFallbackBaseURL:         os.Getenv("MODEL_FALLBACK_BASE_URL"),
		ModelFallbackAPIKey:          os.Getenv("MODEL_FALLBACK_API_KEY"),
		ModelFallbackName:            os.Getenv("MODEL_FALLBACK_NAME"),
		ModelTimeout:                 modelTimeout(),
		ModelInputPricePer1M:         floatFromEnv("MODEL_INPUT_PRICE_PER_1M_TOKENS", 0),
		ModelCachedInputPricePer1M:   floatFromEnv("MODEL_CACHED_INPUT_PRICE_PER_1M_TOKENS", 0),
		ModelOutputPricePer1M:        floatFromEnv("MODEL_OUTPUT_PRICE_PER_1M_TOKENS", 0),
		ModelReasoningPricePer1M:     floatFromEnv("MODEL_REASONING_PRICE_PER_1M_TOKENS", 0),
		ModelPriceCurrency:           strings.TrimSpace(env("MODEL_PRICE_CURRENCY", "USD")),
		ModelRequestsPerMinute:       nonNegativeIntFromEnv("MODEL_REQUESTS_PER_MINUTE", 0),
		ModelMaxConcurrentRequests:   nonNegativeIntFromEnv("MODEL_MAX_CONCURRENT_REQUESTS", 0),
		ModelHealthProbeEnabled:      boolFromEnv("MODEL_HEALTH_PROBE_ENABLED", false),
		ModelHealthProbeInterval:     secondsDuration("MODEL_HEALTH_PROBE_INTERVAL_SECONDS", time.Minute),
		ModelHealthProbeTimeout:      secondsDuration("MODEL_HEALTH_PROBE_TIMEOUT_SECONDS", 10*time.Second),
		ModelHealthProbeInitialDelay: secondsDuration("MODEL_HEALTH_PROBE_INITIAL_DELAY_SECONDS", 0),
	}
	return applyModelProviderProfile(cfg)
}

func (c Config) Validate() error {
	if c.InternalTokenRequired && strings.TrimSpace(c.InternalAPIToken) == "" {
		return fmt.Errorf("INTERNAL_API_TOKEN is required when INTERNAL_API_TOKEN_REQUIRED=true or NICEAGENT_ENV is non-local")
	}
	switch normalizeModelProviderProfile(c.ModelProviderProfile) {
	case "":
	case "deepseek":
		if strings.TrimSpace(c.ModelProvider) != "openai-compatible" {
			return fmt.Errorf("MODEL_PROVIDER_PROFILE=deepseek requires MODEL_PROVIDER=openai-compatible")
		}
	default:
		return fmt.Errorf("unsupported MODEL_PROVIDER_PROFILE %q", c.ModelProviderProfile)
	}
	if c.ModelHealthProbeEnabled {
		if c.ModelHealthProbeInterval <= 0 {
			return fmt.Errorf("MODEL_HEALTH_PROBE_INTERVAL_SECONDS must be positive when MODEL_HEALTH_PROBE_ENABLED=true")
		}
		if c.ModelHealthProbeTimeout <= 0 {
			return fmt.Errorf("MODEL_HEALTH_PROBE_TIMEOUT_SECONDS must be positive when MODEL_HEALTH_PROBE_ENABLED=true")
		}
	}
	if c.ModelRequestsPerMinute < 0 {
		return fmt.Errorf("MODEL_REQUESTS_PER_MINUTE must be greater than or equal to 0")
	}
	if c.ModelMaxConcurrentRequests < 0 {
		return fmt.Errorf("MODEL_MAX_CONCURRENT_REQUESTS must be greater than or equal to 0")
	}
	switch c.SkillRateLimitMode {
	case "", "local", "redis":
	default:
		return fmt.Errorf("unsupported SKILL_RATE_LIMIT_MODE %q", c.SkillRateLimitMode)
	}
	if c.SkillRateLimitMode == "redis" && strings.TrimSpace(c.RedisAddr) == "" {
		return fmt.Errorf("REDIS_ADDR is required when SKILL_RATE_LIMIT_MODE=redis")
	}
	switch c.SkillRiskPolicy {
	case "", protocol.SkillRiskPolicyAllow, protocol.SkillRiskPolicyBlockHigh, protocol.SkillRiskPolicyBlockDestructive, protocol.SkillRiskPolicyReadOnly:
	default:
		return fmt.Errorf("unsupported SKILL_RISK_POLICY %q", c.SkillRiskPolicy)
	}
	return nil
}

func applyModelProviderProfile(cfg Config) Config {
	switch normalizeModelProviderProfile(cfg.ModelProviderProfile) {
	case "deepseek":
		cfg.ModelProviderProfile = "deepseek"
		if strings.TrimSpace(cfg.ModelBaseURL) == "" {
			cfg.ModelBaseURL = "https://api.deepseek.com"
		}
	default:
		cfg.ModelProviderProfile = strings.TrimSpace(cfg.ModelProviderProfile)
	}
	return cfg
}

func normalizeModelProviderProfile(profile string) string {
	return strings.ToLower(strings.TrimSpace(profile))
}

func runtimeID() string {
	if value := strings.TrimSpace(os.Getenv("AGENT_RUNTIME_ID")); value != "" {
		return value
	}
	hostname, err := os.Hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		return "agent-runtime"
	}
	return "agent-runtime-" + hostname
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
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

func secondsDuration(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 0 {
		log.Fatalf("%s must be a non-negative integer, got %q", key, raw)
	}
	return time.Duration(seconds) * time.Second
}

func intFromEnv(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		log.Fatalf("%s must be a positive integer, got %q", key, raw)
	}
	return value
}

func nonNegativeIntFromEnv(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		log.Fatalf("%s must be a non-negative integer, got %q", key, raw)
	}
	return value
}

func int64FromEnv(key string, fallback int64) int64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		log.Fatalf("%s must be a positive integer, got %q", key, raw)
	}
	return value
}

func floatFromEnv(key string, fallback float64) float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < 0 {
		log.Fatalf("%s must be a non-negative number, got %q", key, raw)
	}
	return value
}

func boolFromEnv(key string, fallback bool) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if raw == "" {
		return fallback
	}
	switch raw {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		log.Fatalf("%s must be a boolean, got %q", key, raw)
		return fallback
	}
}

func isNonLocalEnvironment(environment string) bool {
	switch strings.ToLower(strings.TrimSpace(environment)) {
	case "", "local", "dev", "development", "test", "ci":
		return false
	default:
		return true
	}
}

func runtimeConsumer() string {
	if value := os.Getenv("RUN_QUEUE_CONSUMER"); strings.TrimSpace(value) != "" {
		return value
	}
	return runtimeID()
}
