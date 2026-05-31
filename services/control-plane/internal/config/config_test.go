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

func TestFromEnvReadsQuotaCounterConfig(t *testing.T) {
	t.Setenv("QUOTA_COUNTER_MODE", "redis")
	t.Setenv("QUOTA_COUNTER_PREFIX", "custom:quota")
	t.Setenv("QUOTA_MODEL_TOKEN_RESERVATION_PER_RUN", "512")
	t.Setenv("QUOTA_MODEL_TOKEN_RESERVATION_MODE", "dynamic")
	t.Setenv("QUOTA_MODEL_TOKEN_DYNAMIC_OUTPUT_BUFFER", "128")
	t.Setenv("QUOTA_TOOL_CALLS_PER_DAY", "25")
	t.Setenv("QUOTA_SANDBOX_SECONDS_PER_DAY", "120")

	cfg := FromEnv()

	if cfg.QuotaCounterMode != "redis" || cfg.QuotaCounterPrefix != "custom:quota" || cfg.QuotaModelTokenReservationPerRun != 512 {
		t.Fatalf("quota counter config = mode:%q prefix:%q reservation:%d", cfg.QuotaCounterMode, cfg.QuotaCounterPrefix, cfg.QuotaModelTokenReservationPerRun)
	}
	if cfg.QuotaModelTokenReservationMode != "dynamic" || cfg.QuotaModelTokenOutputBuffer != 128 {
		t.Fatalf("token reservation config = mode:%q buffer:%d", cfg.QuotaModelTokenReservationMode, cfg.QuotaModelTokenOutputBuffer)
	}
	if cfg.MaxToolCallsPerDay != 25 || cfg.MaxSandboxSecondsPerDay != 120 {
		t.Fatalf("usage quota config = tool:%d sandbox:%d", cfg.MaxToolCallsPerDay, cfg.MaxSandboxSecondsPerDay)
	}
}
