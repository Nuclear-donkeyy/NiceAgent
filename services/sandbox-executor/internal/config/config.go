package config

import "os"

type Config struct {
	Addr             string
	InternalAPIToken string
}

func FromEnv() Config {
	return Config{
		Addr:             env("SANDBOX_EXECUTOR_ADDR", ":8082"),
		InternalAPIToken: os.Getenv("INTERNAL_API_TOKEN"),
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
