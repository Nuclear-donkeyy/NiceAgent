package config

import "testing"

func TestFromEnvReadsExecutorModeAndContainerConfig(t *testing.T) {
	t.Setenv("EXECUTOR_MODE", "container")
	t.Setenv("SANDBOX_WORKSPACE_ROOT", "/tmp/niceagent-workspaces")
	t.Setenv("SANDBOX_CONTAINER_IMAGE", "busybox:1.36")
	t.Setenv("SANDBOX_CONTAINER_CPUS", "0.5")
	t.Setenv("SANDBOX_CONTAINER_MEMORY", "256m")
	t.Setenv("SANDBOX_CONTAINER_PIDS_LIMIT", "64")
	t.Setenv("SANDBOX_CONTAINER_READ_ONLY_ROOTFS", "false")
	t.Setenv("SANDBOX_CONTAINER_LOCAL_FALLBACK", "false")

	cfg := FromEnv()

	if cfg.ExecutorMode != "container" {
		t.Fatalf("executor mode = %q, want container", cfg.ExecutorMode)
	}
	if cfg.WorkspaceRoot != "/tmp/niceagent-workspaces" {
		t.Fatalf("workspace root = %q", cfg.WorkspaceRoot)
	}
	if cfg.ContainerImage != "busybox:1.36" || cfg.ContainerCPUs != "0.5" || cfg.ContainerMemory != "256m" {
		t.Fatalf("container config = %#v", cfg)
	}
	if cfg.ContainerPidsLimit != 64 || cfg.ContainerReadOnlyRoot || cfg.ContainerLocalFallback {
		t.Fatalf("container booleans/limits = %#v", cfg)
	}
}

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
