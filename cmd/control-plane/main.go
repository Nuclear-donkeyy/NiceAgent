package main

import (
	"log"
	"net/http"
	"os"

	"niceagent/internal/controlplane"
	"niceagent/internal/platform"
	"niceagent/internal/runtime"
	"niceagent/internal/sandbox"
)

func main() {
	addr := env("CONTROL_PLANE_ADDR", ":8080")
	logger := platform.NewLogger("control-plane")
	store := controlplane.NewStore()
	engine := runtime.NewEngine(sandbox.NewExecutor())
	server := controlplane.NewServer(store, engine, logger)

	logger.Info("starting control plane", "addr", addr)
	if err := http.ListenAndServe(addr, server.Handler()); err != nil {
		log.Fatal(err)
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

