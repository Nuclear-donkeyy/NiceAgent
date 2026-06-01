package config

import "testing"

func TestValidateRequiresInternalTokenWhenFlagEnabled(t *testing.T) {
	t.Setenv("INTERNAL_API_TOKEN_REQUIRED", "true")
	t.Setenv("INTERNAL_API_TOKEN", "")

	cfg := FromEnv()

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected missing internal token to fail validation")
	}
}

func TestValidateAllowsLocalWithoutInternalToken(t *testing.T) {
	t.Setenv("NICEAGENT_ENV", "local")
	t.Setenv("INTERNAL_API_TOKEN", "")

	cfg := FromEnv()

	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected local config without internal token to be valid: %v", err)
	}
}

func TestNonLocalEnvironmentRequiresInternalTokenByDefault(t *testing.T) {
	t.Setenv("NICEAGENT_ENV", "production")
	t.Setenv("INTERNAL_API_TOKEN", "")

	cfg := FromEnv()

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected production config without internal token to fail validation")
	}
}

func TestValidateRequiresOIDCIssuerAndAudience(t *testing.T) {
	t.Setenv("AUTH_MODE", "oidc")

	cfg := FromEnv()
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected oidc mode without issuer and audience to fail validation")
	}

	t.Setenv("OIDC_ISSUER_URL", "https://issuer.example.test")
	t.Setenv("OIDC_AUDIENCE", "niceagent")
	cfg = FromEnv()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected oidc config to validate: %v", err)
	}
	if cfg.OIDCProjectIDClaim != "niceagent_project_id" || cfg.OIDCRolesClaim != "niceagent_roles" {
		t.Fatalf("oidc claim config = %#v", cfg)
	}
}

func TestFromEnvReadsQuotaCounterConfig(t *testing.T) {
	t.Setenv("QUOTA_COUNTER_MODE", "redis")
	t.Setenv("QUOTA_COUNTER_PREFIX", "custom:quota")
	t.Setenv("QUOTA_MODEL_TOKEN_RESERVATION_PER_RUN", "512")
	t.Setenv("QUOTA_MODEL_TOKEN_RESERVATION_MODE", "dynamic")
	t.Setenv("QUOTA_MODEL_TOKEN_DYNAMIC_OUTPUT_BUFFER", "128")
	t.Setenv("QUOTA_MODEL_TOKEN_ESTIMATOR_MODEL", "gpt-4o")
	t.Setenv("QUOTA_TOOL_CALLS_PER_DAY", "25")
	t.Setenv("QUOTA_SANDBOX_SECONDS_PER_DAY", "120")

	cfg := FromEnv()

	if cfg.QuotaCounterMode != "redis" || cfg.QuotaCounterPrefix != "custom:quota" || cfg.QuotaModelTokenReservationPerRun != 512 {
		t.Fatalf("quota counter config = mode:%q prefix:%q reservation:%d", cfg.QuotaCounterMode, cfg.QuotaCounterPrefix, cfg.QuotaModelTokenReservationPerRun)
	}
	if cfg.QuotaModelTokenReservationMode != "dynamic" || cfg.QuotaModelTokenOutputBuffer != 128 {
		t.Fatalf("token reservation config = mode:%q buffer:%d", cfg.QuotaModelTokenReservationMode, cfg.QuotaModelTokenOutputBuffer)
	}
	if cfg.QuotaModelTokenEstimatorModel != "gpt-4o" {
		t.Fatalf("token estimator model = %q", cfg.QuotaModelTokenEstimatorModel)
	}
	if cfg.MaxToolCallsPerDay != 25 || cfg.MaxSandboxSecondsPerDay != 120 {
		t.Fatalf("usage quota config = tool:%d sandbox:%d", cfg.MaxToolCallsPerDay, cfg.MaxSandboxSecondsPerDay)
	}
}

func TestFromEnvReadsArtifactRetention(t *testing.T) {
	t.Setenv("ARTIFACT_RETENTION_DAYS", "14")

	cfg := FromEnv()

	if cfg.ArtifactRetentionDays != 14 {
		t.Fatalf("artifact retention days = %d, want 14", cfg.ArtifactRetentionDays)
	}
}

func TestFromEnvReadsInvitationEmailConfig(t *testing.T) {
	t.Setenv("CONTROL_PLANE_PUBLIC_URL", "https://control.example.test")
	t.Setenv("INVITATION_EMAIL_MODE", "smtp")
	t.Setenv("INVITATION_PUBLIC_BASE_URL", "https://app.example.test")
	t.Setenv("SMTP_HOST", "smtp.example.test")
	t.Setenv("SMTP_PORT", "2525")
	t.Setenv("SMTP_USERNAME", "mailer")
	t.Setenv("SMTP_PASSWORD", "secret")
	t.Setenv("SMTP_FROM", "NiceAgent <noreply@example.test>")
	t.Setenv("INVITATION_EMAIL_SUBJECT_TEMPLATE", "Join {{.OrganizationID}}")
	t.Setenv("INVITATION_EMAIL_BODY_TEMPLATE", "Accept {{.AcceptURL}} as {{.Role}}")
	t.Setenv("INVITATION_EMAIL_QUEUE_MODE", "memory")
	t.Setenv("INVITATION_EMAIL_QUEUE_SIZE", "25")
	t.Setenv("INVITATION_EMAIL_QUEUE_WORKERS", "2")
	t.Setenv("INVITATION_EMAIL_RETRY_ATTEMPTS", "3")
	t.Setenv("INVITATION_EMAIL_RETRY_INITIAL_DELAY_MS", "10")
	t.Setenv("INVITATION_EMAIL_WEBHOOK_SECRET", "webhook-secret")

	cfg := FromEnv()

	if cfg.InvitationEmailMode != "smtp" || cfg.InvitationPublicBaseURL != "https://app.example.test" {
		t.Fatalf("invitation email config = mode:%q base:%q", cfg.InvitationEmailMode, cfg.InvitationPublicBaseURL)
	}
	if cfg.SMTPHost != "smtp.example.test" || cfg.SMTPPort != 2525 || cfg.SMTPUsername != "mailer" || cfg.SMTPPassword != "secret" || cfg.SMTPFrom != "NiceAgent <noreply@example.test>" {
		t.Fatalf("smtp config = %#v", cfg)
	}
	if cfg.InvitationEmailSubjectTemplate != "Join {{.OrganizationID}}" || cfg.InvitationEmailBodyTemplate != "Accept {{.AcceptURL}} as {{.Role}}" {
		t.Fatalf("invitation templates = subject:%q body:%q", cfg.InvitationEmailSubjectTemplate, cfg.InvitationEmailBodyTemplate)
	}
	if cfg.InvitationEmailQueueMode != "memory" || cfg.InvitationEmailQueueSize != 25 || cfg.InvitationEmailQueueWorkers != 2 || cfg.InvitationEmailRetryAttempts != 3 || cfg.InvitationEmailRetryInitialDelay != 10 {
		t.Fatalf("invitation queue config = mode:%q size:%d workers:%d attempts:%d delay:%d", cfg.InvitationEmailQueueMode, cfg.InvitationEmailQueueSize, cfg.InvitationEmailQueueWorkers, cfg.InvitationEmailRetryAttempts, cfg.InvitationEmailRetryInitialDelay)
	}
	if cfg.InvitationEmailWebhookSecret != "webhook-secret" {
		t.Fatalf("invitation webhook secret = %q", cfg.InvitationEmailWebhookSecret)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected smtp config to validate: %v", err)
	}
}

func TestValidateRequiresSMTPFieldsWhenInvitationEmailEnabled(t *testing.T) {
	t.Setenv("INVITATION_EMAIL_MODE", "smtp")
	t.Setenv("SMTP_HOST", "")
	t.Setenv("SMTP_FROM", "")

	cfg := FromEnv()

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected missing smtp fields to fail validation")
	}
}

func TestValidateRejectsInvalidInvitationEmailTemplates(t *testing.T) {
	t.Setenv("INVITATION_EMAIL_MODE", "smtp")
	t.Setenv("SMTP_HOST", "smtp.example.test")
	t.Setenv("SMTP_FROM", "noreply@example.test")
	t.Setenv("INVITATION_EMAIL_BODY_TEMPLATE", "Unknown {{.Missing}}")

	cfg := FromEnv()

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid invitation email template to fail validation")
	}
}

func TestValidateRejectsInvalidInvitationEmailQueueMode(t *testing.T) {
	t.Setenv("INVITATION_EMAIL_MODE", "smtp")
	t.Setenv("SMTP_HOST", "smtp.example.test")
	t.Setenv("SMTP_FROM", "noreply@example.test")
	t.Setenv("INVITATION_EMAIL_QUEUE_MODE", "durable")

	cfg := FromEnv()

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid invitation email queue mode to fail validation")
	}
}

func TestValidateAllowsInvitationEmailOutboxQueueMode(t *testing.T) {
	t.Setenv("INVITATION_EMAIL_MODE", "smtp")
	t.Setenv("SMTP_HOST", "smtp.example.test")
	t.Setenv("SMTP_FROM", "noreply@example.test")
	t.Setenv("INVITATION_EMAIL_QUEUE_MODE", "outbox")
	t.Setenv("INVITATION_EMAIL_QUEUE_WORKERS", "1")
	t.Setenv("INVITATION_EMAIL_RETRY_ATTEMPTS", "2")

	cfg := FromEnv()

	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected outbox queue mode to validate: %v", err)
	}
}
