package config

import (
	"fmt"
	"log"
	"net/mail"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Addr                             string
	Environment                      string
	AuthMode                         string
	StoreDriver                      string
	DatabaseURL                      string
	DispatchMode                     string
	EventFanoutMode                  string
	EventFanoutPrefix                string
	RedisAddr                        string
	RunQueueStream                   string
	RunQueueGroup                    string
	RunQueueConsumer                 string
	RunQueueMaxLen                   int64
	AgentRuntimeURL                  string
	ControlPlanePublicURL            string
	InvitationEmailMode              string
	InvitationPublicBaseURL          string
	SMTPHost                         string
	SMTPPort                         int
	SMTPUsername                     string
	SMTPPassword                     string
	SMTPFrom                         string
	InternalAPIToken                 string
	InternalTokenRequired            bool
	MaxConcurrentRuns                int
	MaxRunsPerHour                   int
	MaxModelTokensPerDay             int
	MaxToolCallsPerDay               int
	MaxSandboxSecondsPerDay          int
	QuotaCounterMode                 string
	QuotaCounterPrefix               string
	QuotaModelTokenReservationPerRun int
	QuotaModelTokenReservationMode   string
	QuotaModelTokenOutputBuffer      int
}

func FromEnv() Config {
	environment := strings.TrimSpace(env("NICEAGENT_ENV", "local"))
	controlPlanePublicURL := env("CONTROL_PLANE_PUBLIC_URL", "http://127.0.0.1:8080")
	return Config{
		Addr:                             env("CONTROL_PLANE_ADDR", ":8080"),
		Environment:                      environment,
		AuthMode:                         env("AUTH_MODE", "demo"),
		StoreDriver:                      env("STORE_DRIVER", "memory"),
		DatabaseURL:                      os.Getenv("DATABASE_URL"),
		DispatchMode:                     env("DISPATCH_MODE", "http"),
		EventFanoutMode:                  env("EVENT_FANOUT_MODE", "local"),
		EventFanoutPrefix:                env("EVENT_FANOUT_PREFIX", "niceagent:run-events"),
		RedisAddr:                        os.Getenv("REDIS_ADDR"),
		RunQueueStream:                   env("RUN_QUEUE_STREAM", "niceagent:runs"),
		RunQueueGroup:                    env("RUN_QUEUE_GROUP", "agent-runtimes"),
		RunQueueConsumer:                 env("RUN_QUEUE_CONSUMER", "control-plane"),
		RunQueueMaxLen:                   int64Env("RUN_QUEUE_MAX_LEN", 0),
		AgentRuntimeURL:                  os.Getenv("AGENT_RUNTIME_URL"),
		ControlPlanePublicURL:            controlPlanePublicURL,
		InvitationEmailMode:              env("INVITATION_EMAIL_MODE", "disabled"),
		InvitationPublicBaseURL:          env("INVITATION_PUBLIC_BASE_URL", controlPlanePublicURL),
		SMTPHost:                         strings.TrimSpace(os.Getenv("SMTP_HOST")),
		SMTPPort:                         intEnv("SMTP_PORT", 587),
		SMTPUsername:                     strings.TrimSpace(os.Getenv("SMTP_USERNAME")),
		SMTPPassword:                     os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:                         strings.TrimSpace(os.Getenv("SMTP_FROM")),
		InternalAPIToken:                 strings.TrimSpace(os.Getenv("INTERNAL_API_TOKEN")),
		InternalTokenRequired:            boolEnv("INTERNAL_API_TOKEN_REQUIRED", isNonLocalEnvironment(environment)),
		MaxConcurrentRuns:                intEnv("QUOTA_MAX_CONCURRENT_RUNS", 0),
		MaxRunsPerHour:                   intEnv("QUOTA_RUNS_PER_HOUR", 0),
		MaxModelTokensPerDay:             intEnv("QUOTA_MODEL_TOKENS_PER_DAY", 0),
		MaxToolCallsPerDay:               intEnv("QUOTA_TOOL_CALLS_PER_DAY", 0),
		MaxSandboxSecondsPerDay:          intEnv("QUOTA_SANDBOX_SECONDS_PER_DAY", 0),
		QuotaCounterMode:                 env("QUOTA_COUNTER_MODE", "repository"),
		QuotaCounterPrefix:               env("QUOTA_COUNTER_PREFIX", "niceagent:quota"),
		QuotaModelTokenReservationPerRun: intEnv("QUOTA_MODEL_TOKEN_RESERVATION_PER_RUN", 0),
		QuotaModelTokenReservationMode:   env("QUOTA_MODEL_TOKEN_RESERVATION_MODE", "fixed"),
		QuotaModelTokenOutputBuffer:      intEnv("QUOTA_MODEL_TOKEN_DYNAMIC_OUTPUT_BUFFER", 0),
	}
}

func (c Config) Validate() error {
	if c.InternalTokenRequired && strings.TrimSpace(c.InternalAPIToken) == "" {
		return fmt.Errorf("INTERNAL_API_TOKEN is required when INTERNAL_API_TOKEN_REQUIRED=true or NICEAGENT_ENV is non-local")
	}
	switch strings.ToLower(strings.TrimSpace(c.InvitationEmailMode)) {
	case "", "disabled", "smtp":
	default:
		return fmt.Errorf("INVITATION_EMAIL_MODE must be disabled or smtp")
	}
	if strings.EqualFold(strings.TrimSpace(c.InvitationEmailMode), "smtp") {
		if strings.TrimSpace(c.SMTPHost) == "" {
			return fmt.Errorf("SMTP_HOST is required when INVITATION_EMAIL_MODE=smtp")
		}
		if strings.TrimSpace(c.SMTPFrom) == "" {
			return fmt.Errorf("SMTP_FROM is required when INVITATION_EMAIL_MODE=smtp")
		}
		if _, err := mail.ParseAddress(c.SMTPFrom); err != nil {
			return fmt.Errorf("SMTP_FROM must be a valid email address")
		}
		if c.SMTPPort <= 0 || c.SMTPPort > 65535 {
			return fmt.Errorf("SMTP_PORT must be between 1 and 65535")
		}
	}
	return nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func intEnv(key string, fallback int) int {
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

func int64Env(key string, fallback int64) int64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		log.Fatalf("%s must be a non-negative integer, got %q", key, raw)
	}
	return value
}

func boolEnv(key string, fallback bool) bool {
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
