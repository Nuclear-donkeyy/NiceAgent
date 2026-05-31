package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"niceagent/common/platform"
	"niceagent/sandbox-executor/internal/config"
	"niceagent/sandbox-executor/internal/executor"
	"niceagent/sandbox-executor/internal/httpapi"
	"niceagent/sandbox-executor/internal/policy"
)

func main() {
	cfg := config.FromEnv()
	if err := cfg.Validate(); err != nil {
		log.Fatal(err)
	}
	logger := platform.NewLogger("sandbox-executor")
	shutdownTelemetry, err := platform.InitOpenTelemetry(context.Background(), platform.OpenTelemetryConfigFromEnv("sandbox_executor", cfg.Environment))
	if err != nil {
		log.Fatalf("init opentelemetry: %v", err)
	}
	defer shutdownTelemetryWithTimeout(shutdownTelemetry)
	sandboxExecutor := executor.NewConfiguredExecutor(cfg)
	handler := httpapi.NewHandler(sandboxExecutor, cfg.InternalAPIToken)

	logger.Info("starting sandbox executor", "addr", cfg.Addr, "policy", policy.SystemCLIMode, "executor_mode", cfg.ExecutorMode)
	if err := http.ListenAndServe(cfg.Addr, handler); err != nil {
		log.Fatal(err)
	}
}

func shutdownTelemetryWithTimeout(shutdown func(context.Context) error) {
	if shutdown == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = shutdown(ctx)
}
