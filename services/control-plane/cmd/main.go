package main

import (
	"log"
	"log/slog"
	"net/http"
	"os"

	"niceagent/common/platform"
	"niceagent/control-plane/internal/controlplane"
)

func main() {
	addr := env("CONTROL_PLANE_ADDR", ":8080")
	logger := platform.NewLogger("control-plane")
	store := controlplane.NewStore()
	dispatcher := newDispatcher(store, logger)
	server := controlplane.NewServer(store, dispatcher, logger)

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

func newDispatcher(store controlplane.Repository, logger *slog.Logger) controlplane.RunDispatcher {
	mode := env("DISPATCH_MODE", "http")
	if mode == "local" {
		logger.Info("using local dispatcher", "mode", mode)
		return controlplane.NewLocalDispatcher(store, logger)
	}
	runtimeURL := os.Getenv("AGENT_RUNTIME_URL")
	if runtimeURL == "" {
		logger.Warn("AGENT_RUNTIME_URL is empty; falling back to local dispatcher", "mode", mode)
		return controlplane.NewLocalDispatcher(store, logger)
	}
	logger.Info("using http dispatcher", "runtime_url", runtimeURL)
	return controlplane.NewHTTPDispatcher(
		store,
		runtimeURL,
		env("CONTROL_PLANE_PUBLIC_URL", "http://127.0.0.1:8080"),
		os.Getenv("INTERNAL_API_TOKEN"),
		logger,
	)
}
