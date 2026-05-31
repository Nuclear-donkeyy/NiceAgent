package main

import (
	"database/sql"
	"log"
	"log/slog"
	"net/http"
	"os"

	"niceagent/common/platform"
	"niceagent/control-plane/internal/controlplane"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	addr := env("CONTROL_PLANE_ADDR", ":8080")
	logger := platform.NewLogger("control-plane")
	store, closeStore := newStore(logger)
	defer closeStore()
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

func newStore(logger *slog.Logger) (controlplane.Repository, func()) {
	driver := env("STORE_DRIVER", "memory")
	if driver == "postgres" {
		databaseURL := os.Getenv("DATABASE_URL")
		if databaseURL == "" {
			log.Fatal("DATABASE_URL is required when STORE_DRIVER=postgres")
		}
		db, err := sql.Open("pgx", databaseURL)
		if err != nil {
			log.Fatalf("open postgres: %v", err)
		}
		if err := db.Ping(); err != nil {
			_ = db.Close()
			log.Fatalf("ping postgres: %v", err)
		}
		logger.Info("using postgres store")
		return controlplane.NewPostgresStore(db), func() { _ = db.Close() }
	}
	if driver != "memory" {
		logger.Warn("unknown STORE_DRIVER; falling back to memory", "store_driver", driver)
	}
	logger.Info("using memory store")
	return controlplane.NewStore(), func() {}
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
