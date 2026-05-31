package main

import (
	"log"
	"net/http"

	"niceagent/common/platform"
	"niceagent/sandbox-executor/internal/config"
	"niceagent/sandbox-executor/internal/executor"
	"niceagent/sandbox-executor/internal/httpapi"
	"niceagent/sandbox-executor/internal/policy"
)

func main() {
	cfg := config.FromEnv()
	logger := platform.NewLogger("sandbox-executor")
	sandboxExecutor := executor.NewConfiguredExecutor(cfg)
	handler := httpapi.NewHandler(sandboxExecutor, cfg.InternalAPIToken)

	logger.Info("starting sandbox executor", "addr", cfg.Addr, "policy", policy.SystemCLIMode, "executor_mode", cfg.ExecutorMode)
	if err := http.ListenAndServe(cfg.Addr, handler); err != nil {
		log.Fatal(err)
	}
}
