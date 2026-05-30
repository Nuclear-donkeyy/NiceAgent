package main

import (
	"log"
	"net/http"
	"os"

	"niceagent/common/platform"
	"niceagent/control-plane/internal/controlplane"
)

func main() {
	addr := env("CONTROL_PLANE_ADDR", ":8080")
	logger := platform.NewLogger("control-plane")
	store := controlplane.NewStore()
	dispatcher := controlplane.NewLocalDispatcher(store, logger)
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
