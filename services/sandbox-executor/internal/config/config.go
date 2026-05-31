package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Addr                   string
	Environment            string
	InternalAPIToken       string
	InternalTokenRequired  bool
	ExecutorMode           string
	WorkspaceRoot          string
	MaxOutputBytes         int
	MaxArtifacts           int
	MaxArtifactBytes       int64
	MaxArtifactTotalBytes  int64
	ContainerImage         string
	ContainerDockerBinary  string
	ContainerCPUs          string
	ContainerMemory        string
	ContainerPidsLimit     int
	ContainerReadOnlyRoot  bool
	ContainerDropAllCaps   bool
	ContainerNoNewPrivs    bool
	ContainerTmpfs         string
	ContainerLocalFallback bool
}

func FromEnv() Config {
	environment := strings.TrimSpace(env("NICEAGENT_ENV", "local"))
	return Config{
		Addr:                   env("SANDBOX_EXECUTOR_ADDR", ":8082"),
		Environment:            environment,
		InternalAPIToken:       strings.TrimSpace(os.Getenv("INTERNAL_API_TOKEN")),
		InternalTokenRequired:  envBool("INTERNAL_API_TOKEN_REQUIRED", isNonLocalEnvironment(environment)),
		ExecutorMode:           strings.ToLower(env("EXECUTOR_MODE", "local")),
		WorkspaceRoot:          env("SANDBOX_WORKSPACE_ROOT", "workspaces"),
		MaxOutputBytes:         envInt("SANDBOX_MAX_OUTPUT_BYTES", 64*1024),
		MaxArtifacts:           envInt("SANDBOX_MAX_ARTIFACTS", 32),
		MaxArtifactBytes:       envInt64("SANDBOX_MAX_ARTIFACT_BYTES", 10*1024*1024),
		MaxArtifactTotalBytes:  envInt64("SANDBOX_MAX_ARTIFACT_TOTAL_BYTES", 50*1024*1024),
		ContainerImage:         env("SANDBOX_CONTAINER_IMAGE", "alpine:3.20"),
		ContainerDockerBinary:  env("SANDBOX_CONTAINER_DOCKER_BINARY", "docker"),
		ContainerCPUs:          env("SANDBOX_CONTAINER_CPUS", "1"),
		ContainerMemory:        env("SANDBOX_CONTAINER_MEMORY", "512m"),
		ContainerPidsLimit:     envInt("SANDBOX_CONTAINER_PIDS_LIMIT", 128),
		ContainerReadOnlyRoot:  envBool("SANDBOX_CONTAINER_READ_ONLY_ROOTFS", true),
		ContainerDropAllCaps:   envBool("SANDBOX_CONTAINER_DROP_CAPS", true),
		ContainerNoNewPrivs:    envBool("SANDBOX_CONTAINER_NO_NEW_PRIVILEGES", true),
		ContainerTmpfs:         env("SANDBOX_CONTAINER_TMPFS", "/tmp:rw,noexec,nosuid,size=64m"),
		ContainerLocalFallback: envBool("SANDBOX_CONTAINER_LOCAL_FALLBACK", true),
	}
}

func (c Config) Validate() error {
	if c.InternalTokenRequired && strings.TrimSpace(c.InternalAPIToken) == "" {
		return fmt.Errorf("INTERNAL_API_TOKEN is required when INTERNAL_API_TOKEN_REQUIRED=true or NICEAGENT_ENV is non-local")
	}
	return nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt64(key string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func envBool(key string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	switch value {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
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
