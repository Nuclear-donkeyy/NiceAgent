package config

import "os"

type Config struct {
	Addr                  string
	AuthMode              string
	StoreDriver           string
	DatabaseURL           string
	DispatchMode          string
	RedisAddr             string
	RunQueueStream        string
	RunQueueGroup         string
	RunQueueConsumer      string
	AgentRuntimeURL       string
	ControlPlanePublicURL string
	InternalAPIToken      string
}

func FromEnv() Config {
	return Config{
		Addr:                  env("CONTROL_PLANE_ADDR", ":8080"),
		AuthMode:              env("AUTH_MODE", "demo"),
		StoreDriver:           env("STORE_DRIVER", "memory"),
		DatabaseURL:           os.Getenv("DATABASE_URL"),
		DispatchMode:          env("DISPATCH_MODE", "http"),
		RedisAddr:             os.Getenv("REDIS_ADDR"),
		RunQueueStream:        env("RUN_QUEUE_STREAM", "niceagent:runs"),
		RunQueueGroup:         env("RUN_QUEUE_GROUP", "agent-runtimes"),
		RunQueueConsumer:      env("RUN_QUEUE_CONSUMER", "control-plane"),
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
