package config

import (
	"fmt"
	"log"
	"net/mail"
	"os"
	"strconv"
	"strings"

	"niceagent/control-plane/internal/mailer"
)

type Config struct {
	Addr                                    string
	Environment                             string
	AuthMode                                string
	OIDCIssuerURL                           string
	OIDCAudience                            string
	OIDCJWKSURL                             string
	OIDCDefaultProjectID                    string
	OIDCDefaultOrgID                        string
	OIDCUserIDClaim                         string
	OIDCProjectIDClaim                      string
	OIDCOrgIDClaim                          string
	OIDCRolesClaim                          string
	OIDCEmailClaim                          string
	OIDCNameClaim                           string
	OIDCClientID                            string
	OIDCClientSecret                        string
	OIDCAuthURL                             string
	OIDCTokenURL                            string
	OIDCRedirectURL                         string
	OIDCSessionSecret                       string
	OIDCSessionTTLSeconds                   int
	StoreDriver                             string
	DatabaseURL                             string
	DispatchMode                            string
	EventFanoutMode                         string
	EventFanoutPrefix                       string
	RedisAddr                               string
	RunQueueStream                          string
	RunQueueGroup                           string
	RunQueueConsumer                        string
	RunQueueMaxLen                          int64
	AgentRuntimeURL                         string
	ControlPlanePublicURL                   string
	InvitationEmailMode                     string
	InvitationPublicBaseURL                 string
	SMTPHost                                string
	SMTPPort                                int
	SMTPUsername                            string
	SMTPPassword                            string
	SMTPFrom                                string
	InvitationEmailSubjectTemplate          string
	InvitationEmailBodyTemplate             string
	InvitationEmailQueueMode                string
	InvitationEmailQueueSize                int
	InvitationEmailQueueWorkers             int
	InvitationEmailRetryAttempts            int
	InvitationEmailRetryInitialDelay        int
	InvitationEmailWebhookSecret            string
	InvitationEmailSendGridPublicKey        string
	InvitationEmailMailgunSigningKey        string
	InvitationEmailSNSSignatureVerification bool
	InvitationEmailSNSTopicARN              string
	InternalAPIToken                        string
	InternalTokenRequired                   bool
	MaxConcurrentRuns                       int
	MaxRunsPerHour                          int
	MaxModelTokensPerDay                    int
	MaxToolCallsPerDay                      int
	MaxSandboxSecondsPerDay                 int
	QuotaCounterMode                        string
	QuotaCounterPrefix                      string
	QuotaModelTokenReservationPerRun        int
	QuotaModelTokenReservationMode          string
	QuotaModelTokenOutputBuffer             int
	QuotaModelTokenEstimatorModel           string
	ArtifactRetentionDays                   int
	ArtifactCleanupDeleteFiles              bool
}

func FromEnv() Config {
	environment := strings.TrimSpace(env("NICEAGENT_ENV", "local"))
	controlPlanePublicURL := env("CONTROL_PLANE_PUBLIC_URL", "http://127.0.0.1:8080")
	return Config{
		Addr:                                    env("CONTROL_PLANE_ADDR", ":8080"),
		Environment:                             environment,
		AuthMode:                                env("AUTH_MODE", "demo"),
		OIDCIssuerURL:                           strings.TrimSpace(os.Getenv("OIDC_ISSUER_URL")),
		OIDCAudience:                            strings.TrimSpace(os.Getenv("OIDC_AUDIENCE")),
		OIDCJWKSURL:                             strings.TrimSpace(os.Getenv("OIDC_JWKS_URL")),
		OIDCDefaultProjectID:                    strings.TrimSpace(os.Getenv("OIDC_DEFAULT_PROJECT_ID")),
		OIDCDefaultOrgID:                        strings.TrimSpace(os.Getenv("OIDC_DEFAULT_ORG_ID")),
		OIDCUserIDClaim:                         strings.TrimSpace(env("OIDC_USER_ID_CLAIM", "sub")),
		OIDCProjectIDClaim:                      strings.TrimSpace(env("OIDC_PROJECT_ID_CLAIM", "niceagent_project_id")),
		OIDCOrgIDClaim:                          strings.TrimSpace(env("OIDC_ORG_ID_CLAIM", "niceagent_org_id")),
		OIDCRolesClaim:                          strings.TrimSpace(env("OIDC_ROLES_CLAIM", "niceagent_roles")),
		OIDCEmailClaim:                          strings.TrimSpace(env("OIDC_EMAIL_CLAIM", "email")),
		OIDCNameClaim:                           strings.TrimSpace(env("OIDC_NAME_CLAIM", "name")),
		OIDCClientID:                            strings.TrimSpace(os.Getenv("OIDC_CLIENT_ID")),
		OIDCClientSecret:                        os.Getenv("OIDC_CLIENT_SECRET"),
		OIDCAuthURL:                             strings.TrimSpace(os.Getenv("OIDC_AUTH_URL")),
		OIDCTokenURL:                            strings.TrimSpace(os.Getenv("OIDC_TOKEN_URL")),
		OIDCRedirectURL:                         strings.TrimSpace(os.Getenv("OIDC_REDIRECT_URL")),
		OIDCSessionSecret:                       os.Getenv("OIDC_SESSION_SECRET"),
		OIDCSessionTTLSeconds:                   intEnv("OIDC_SESSION_TTL_SECONDS", 12*60*60),
		StoreDriver:                             env("STORE_DRIVER", "memory"),
		DatabaseURL:                             os.Getenv("DATABASE_URL"),
		DispatchMode:                            env("DISPATCH_MODE", "http"),
		EventFanoutMode:                         env("EVENT_FANOUT_MODE", "local"),
		EventFanoutPrefix:                       env("EVENT_FANOUT_PREFIX", "niceagent:run-events"),
		RedisAddr:                               os.Getenv("REDIS_ADDR"),
		RunQueueStream:                          env("RUN_QUEUE_STREAM", "niceagent:runs"),
		RunQueueGroup:                           env("RUN_QUEUE_GROUP", "agent-runtimes"),
		RunQueueConsumer:                        env("RUN_QUEUE_CONSUMER", "control-plane"),
		RunQueueMaxLen:                          int64Env("RUN_QUEUE_MAX_LEN", 0),
		AgentRuntimeURL:                         os.Getenv("AGENT_RUNTIME_URL"),
		ControlPlanePublicURL:                   controlPlanePublicURL,
		InvitationEmailMode:                     env("INVITATION_EMAIL_MODE", "disabled"),
		InvitationPublicBaseURL:                 env("INVITATION_PUBLIC_BASE_URL", controlPlanePublicURL),
		SMTPHost:                                strings.TrimSpace(os.Getenv("SMTP_HOST")),
		SMTPPort:                                intEnv("SMTP_PORT", 587),
		SMTPUsername:                            strings.TrimSpace(os.Getenv("SMTP_USERNAME")),
		SMTPPassword:                            os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:                                strings.TrimSpace(os.Getenv("SMTP_FROM")),
		InvitationEmailSubjectTemplate:          strings.TrimSpace(os.Getenv("INVITATION_EMAIL_SUBJECT_TEMPLATE")),
		InvitationEmailBodyTemplate:             os.Getenv("INVITATION_EMAIL_BODY_TEMPLATE"),
		InvitationEmailQueueMode:                env("INVITATION_EMAIL_QUEUE_MODE", "inline"),
		InvitationEmailQueueSize:                intEnv("INVITATION_EMAIL_QUEUE_SIZE", 100),
		InvitationEmailQueueWorkers:             intEnv("INVITATION_EMAIL_QUEUE_WORKERS", 1),
		InvitationEmailRetryAttempts:            intEnv("INVITATION_EMAIL_RETRY_ATTEMPTS", 1),
		InvitationEmailRetryInitialDelay:        intEnv("INVITATION_EMAIL_RETRY_INITIAL_DELAY_MS", 250),
		InvitationEmailWebhookSecret:            os.Getenv("INVITATION_EMAIL_WEBHOOK_SECRET"),
		InvitationEmailSendGridPublicKey:        strings.TrimSpace(os.Getenv("INVITATION_EMAIL_SENDGRID_PUBLIC_KEY")),
		InvitationEmailMailgunSigningKey:        os.Getenv("INVITATION_EMAIL_MAILGUN_SIGNING_KEY"),
		InvitationEmailSNSSignatureVerification: boolEnv("INVITATION_EMAIL_SNS_SIGNATURE_VERIFICATION", false),
		InvitationEmailSNSTopicARN:              strings.TrimSpace(os.Getenv("INVITATION_EMAIL_SNS_TOPIC_ARN")),
		InternalAPIToken:                        strings.TrimSpace(os.Getenv("INTERNAL_API_TOKEN")),
		InternalTokenRequired:                   boolEnv("INTERNAL_API_TOKEN_REQUIRED", isNonLocalEnvironment(environment)),
		MaxConcurrentRuns:                       intEnv("QUOTA_MAX_CONCURRENT_RUNS", 0),
		MaxRunsPerHour:                          intEnv("QUOTA_RUNS_PER_HOUR", 0),
		MaxModelTokensPerDay:                    intEnv("QUOTA_MODEL_TOKENS_PER_DAY", 0),
		MaxToolCallsPerDay:                      intEnv("QUOTA_TOOL_CALLS_PER_DAY", 0),
		MaxSandboxSecondsPerDay:                 intEnv("QUOTA_SANDBOX_SECONDS_PER_DAY", 0),
		QuotaCounterMode:                        env("QUOTA_COUNTER_MODE", "repository"),
		QuotaCounterPrefix:                      env("QUOTA_COUNTER_PREFIX", "niceagent:quota"),
		QuotaModelTokenReservationPerRun:        intEnv("QUOTA_MODEL_TOKEN_RESERVATION_PER_RUN", 0),
		QuotaModelTokenReservationMode:          env("QUOTA_MODEL_TOKEN_RESERVATION_MODE", "fixed"),
		QuotaModelTokenOutputBuffer:             intEnv("QUOTA_MODEL_TOKEN_DYNAMIC_OUTPUT_BUFFER", 0),
		QuotaModelTokenEstimatorModel:           strings.TrimSpace(os.Getenv("QUOTA_MODEL_TOKEN_ESTIMATOR_MODEL")),
		ArtifactRetentionDays:                   intEnv("ARTIFACT_RETENTION_DAYS", 0),
		ArtifactCleanupDeleteFiles:              boolEnv("ARTIFACT_CLEANUP_DELETE_FILES", false),
	}
}

func (c Config) Validate() error {
	if c.InternalTokenRequired && strings.TrimSpace(c.InternalAPIToken) == "" {
		return fmt.Errorf("INTERNAL_API_TOKEN is required when INTERNAL_API_TOKEN_REQUIRED=true or NICEAGENT_ENV is non-local")
	}
	if strings.EqualFold(strings.TrimSpace(c.AuthMode), "oidc") {
		if strings.TrimSpace(c.OIDCIssuerURL) == "" {
			return fmt.Errorf("OIDC_ISSUER_URL is required when AUTH_MODE=oidc")
		}
		if strings.TrimSpace(c.OIDCAudience) == "" {
			return fmt.Errorf("OIDC_AUDIENCE is required when AUTH_MODE=oidc")
		}
		if strings.TrimSpace(c.OIDCAuthURL) != "" || strings.TrimSpace(c.OIDCTokenURL) != "" || strings.TrimSpace(c.OIDCClientID) != "" {
			if strings.TrimSpace(c.OIDCAuthURL) == "" {
				return fmt.Errorf("OIDC_AUTH_URL is required when OIDC browser login is enabled")
			}
			if strings.TrimSpace(c.OIDCTokenURL) == "" {
				return fmt.Errorf("OIDC_TOKEN_URL is required when OIDC browser login is enabled")
			}
			if strings.TrimSpace(c.OIDCClientID) == "" {
				return fmt.Errorf("OIDC_CLIENT_ID is required when OIDC browser login is enabled")
			}
			if strings.TrimSpace(c.OIDCSessionSecret) == "" {
				return fmt.Errorf("OIDC_SESSION_SECRET is required when OIDC browser login is enabled")
			}
			if c.OIDCSessionTTLSeconds <= 0 {
				return fmt.Errorf("OIDC_SESSION_TTL_SECONDS must be greater than 0")
			}
		}
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
		if err := mailer.ValidateTemplates(c.InvitationEmailSubjectTemplate, c.InvitationEmailBodyTemplate); err != nil {
			return err
		}
		switch strings.ToLower(strings.TrimSpace(c.InvitationEmailQueueMode)) {
		case "", "inline", "memory", "outbox":
		default:
			return fmt.Errorf("INVITATION_EMAIL_QUEUE_MODE must be inline, memory, or outbox")
		}
		queueMode := strings.ToLower(strings.TrimSpace(c.InvitationEmailQueueMode))
		if queueMode == "memory" {
			if c.InvitationEmailQueueSize <= 0 {
				return fmt.Errorf("INVITATION_EMAIL_QUEUE_SIZE must be greater than 0 when INVITATION_EMAIL_QUEUE_MODE=memory")
			}
		}
		if queueMode == "memory" || queueMode == "outbox" {
			if c.InvitationEmailQueueWorkers <= 0 {
				return fmt.Errorf("INVITATION_EMAIL_QUEUE_WORKERS must be greater than 0 when INVITATION_EMAIL_QUEUE_MODE=memory or outbox")
			}
			if c.InvitationEmailRetryAttempts <= 0 {
				return fmt.Errorf("INVITATION_EMAIL_RETRY_ATTEMPTS must be greater than 0 when INVITATION_EMAIL_QUEUE_MODE=memory or outbox")
			}
		}
	}
	if c.ArtifactRetentionDays < 0 {
		return fmt.Errorf("ARTIFACT_RETENTION_DAYS must be >= 0")
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
