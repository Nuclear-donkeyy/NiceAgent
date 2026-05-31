package main

import (
	"log"
	"net/http"

	"niceagent/common/platform"
	"niceagent/common/sandbox"
	"niceagent/sandbox-executor/internal/config"
	"niceagent/sandbox-executor/internal/httpapi"
)

func main() {
	cfg := config.FromEnv()
	logger := platform.NewLogger("sandbox-executor")
	executor := sandbox.NewExecutor()
	handler := httpapi.NewHandler(executor, cfg.InternalAPIToken)

	logger.Info("starting sandbox executor", "addr", cfg.Addr)
	if err := http.ListenAndServe(cfg.Addr, handler); err != nil {
		log.Fatal(err)
	}
}
