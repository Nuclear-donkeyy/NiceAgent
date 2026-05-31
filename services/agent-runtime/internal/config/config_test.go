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

func TestValidateRequiresPositiveModelHealthProbeInterval(t *testing.T) {
	t.Setenv("MODEL_HEALTH_PROBE_ENABLED", "true")
	t.Setenv("MODEL_HEALTH_PROBE_INTERVAL_SECONDS", "0")

	cfg := FromEnv()

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected zero health probe interval to fail validation")
	}
}

func TestFromEnvReadsModelHealthProbeConfig(t *testing.T) {
	t.Setenv("MODEL_HEALTH_PROBE_ENABLED", "true")
	t.Setenv("MODEL_HEALTH_PROBE_INTERVAL_SECONDS", "30")
	t.Setenv("MODEL_HEALTH_PROBE_TIMEOUT_SECONDS", "5")
	t.Setenv("MODEL_HEALTH_PROBE_INITIAL_DELAY_SECONDS", "2")

	cfg := FromEnv()

	if !cfg.ModelHealthProbeEnabled {
		t.Fatal("expected model health probe to be enabled")
	}
	if cfg.ModelHealthProbeInterval.Seconds() != 30 || cfg.ModelHealthProbeTimeout.Seconds() != 5 || cfg.ModelHealthProbeInitialDelay.Seconds() != 2 {
		t.Fatalf("unexpected probe config: interval=%s timeout=%s initial_delay=%s", cfg.ModelHealthProbeInterval, cfg.ModelHealthProbeTimeout, cfg.ModelHealthProbeInitialDelay)
	}
}
