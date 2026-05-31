package config

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr               string
	ControlPlaneURL    string
	InternalAPIToken   string
	SandboxExecutorURL string
	ModelProvider      string
	ModelBaseURL       string
	ModelAPIKey        string
	ModelName          string
	ModelTimeout       time.Duration
}

func FromEnv() Config {
	return Config{
		Addr:               env("AGENT_RUNTIME_ADDR", ":8081"),
		ControlPlaneURL:    os.Getenv("CONTROL_PLANE_URL"),
		InternalAPIToken:   os.Getenv("INTERNAL_API_TOKEN"),
		SandboxExecutorURL: os.Getenv("SANDBOX_EXECUTOR_URL"),
		ModelProvider:      strings.TrimSpace(env("MODEL_PROVIDER", "mock")),
		ModelBaseURL:       os.Getenv("MODEL_BASE_URL"),
		ModelAPIKey:        os.Getenv("MODEL_API_KEY"),
		ModelName:          os.Getenv("MODEL_NAME"),
		ModelTimeout:       modelTimeout(),
	}
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
