package config

import "os"

type Config struct {
	Addr                  string
	StoreDriver           string
	DatabaseURL           string
	DispatchMode          string
	AgentRuntimeURL       string
	ControlPlanePublicURL string
	InternalAPIToken      string
}

func FromEnv() Config {
	return Config{
		Addr:                  env("CONTROL_PLANE_ADDR", ":8080"),
		StoreDriver:           env("STORE_DRIVER", "memory"),
		DatabaseURL:           os.Getenv("DATABASE_URL"),
		DispatchMode:          env("DISPATCH_MODE", "http"),
		AgentRuntimeURL:       os.Getenv("AGENT_RUNTIME_URL"),
		ControlPlanePublicURL: env("CONTROL_PLANE_PUBLIC_URL", "http://127.0.0.1:8080"),
		InternalAPIToken:      os.Getenv("INTERNAL_API_TOKEN"),
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
