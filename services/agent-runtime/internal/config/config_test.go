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

func TestFromEnvReadsModelRateLimitConfig(t *testing.T) {
	t.Setenv("MODEL_REQUESTS_PER_MINUTE", "30")
	t.Setenv("MODEL_MAX_CONCURRENT_REQUESTS", "4")

	cfg := FromEnv()

	if cfg.ModelRequestsPerMinute != 30 || cfg.ModelMaxConcurrentRequests != 4 {
		t.Fatalf("model rate limit config = rpm:%d concurrent:%d", cfg.ModelRequestsPerMinute, cfg.ModelMaxConcurrentRequests)
	}
}

func TestValidateRejectsNegativeModelRateLimitConfig(t *testing.T) {
	cfg := Config{ModelRequestsPerMinute: -1}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected negative model requests per minute to fail validation")
	}
}

func TestFromEnvReadsSkillRateLimitConfig(t *testing.T) {
	t.Setenv("SKILL_RATE_LIMIT_MODE", "redis")
	t.Setenv("SKILL_RATE_LIMIT_PREFIX", "custom:skill-rate")
	t.Setenv("REDIS_ADDR", "redis:6379")

	cfg := FromEnv()

	if cfg.SkillRateLimitMode != "redis" || cfg.SkillRateLimitPrefix != "custom:skill-rate" {
		t.Fatalf("skill rate limit config = mode:%q prefix:%q", cfg.SkillRateLimitMode, cfg.SkillRateLimitPrefix)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate redis skill limiter config: %v", err)
	}
}

func TestValidateRequiresRedisAddrForRedisSkillRateLimiter(t *testing.T) {
	cfg := Config{SkillRateLimitMode: "redis"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected redis skill limiter without REDIS_ADDR to fail validation")
	}
}

func TestValidateRejectsUnsupportedSkillRateLimitMode(t *testing.T) {
	cfg := Config{SkillRateLimitMode: "memcached", RedisAddr: "redis:6379"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected unsupported skill rate limit mode to fail validation")
	}
}

func TestFromEnvReadsSkillRiskPolicy(t *testing.T) {
	t.Setenv("SKILL_RISK_POLICY", "read-only")

	cfg := FromEnv()

	if cfg.SkillRiskPolicy != "read-only" {
		t.Fatalf("skill risk policy = %q, want read-only", cfg.SkillRiskPolicy)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate skill risk policy: %v", err)
	}
}

func TestValidateRejectsUnsupportedSkillRiskPolicy(t *testing.T) {
	cfg := Config{SkillRiskPolicy: "surprise-me"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected unsupported skill risk policy to fail validation")
	}
}
