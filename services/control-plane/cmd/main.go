package main

import (
	"database/sql"
	"log"
	"log/slog"
	"net/http"

	"niceagent/common/platform"
	"niceagent/control-plane/internal/app"
	"niceagent/control-plane/internal/config"
	"niceagent/control-plane/internal/dispatch"
	"niceagent/control-plane/internal/httpapi"
	"niceagent/control-plane/internal/repository"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	cfg := config.FromEnv()
	logger := platform.NewLogger("control-plane")
	store, closeStore := newStore(cfg, logger)
	defer closeStore()
	dispatcher := newDispatcher(cfg, store, logger)
	server := httpapi.NewServer(store, dispatcher, logger)

	logger.Info("starting control plane", "addr", cfg.Addr)
	if err := http.ListenAndServe(cfg.Addr, server.Handler()); err != nil {
		log.Fatal(err)
	}
}

func newStore(cfg config.Config, logger *slog.Logger) (app.Repository, func()) {
	if cfg.StoreDriver == "postgres" {
		if cfg.DatabaseURL == "" {
			log.Fatal("DATABASE_URL is required when STORE_DRIVER=postgres")
		}
		db, err := sql.Open("pgx", cfg.DatabaseURL)
		if err != nil {
			log.Fatalf("open postgres: %v", err)
		}
		if err := db.Ping(); err != nil {
			_ = db.Close()
			log.Fatalf("ping postgres: %v", err)
		}
		logger.Info("using postgres store")
		return repository.NewPostgresStore(db), func() { _ = db.Close() }
	}
	if cfg.StoreDriver != "memory" {
		logger.Warn("unknown STORE_DRIVER; falling back to memory", "store_driver", cfg.StoreDriver)
	}
	logger.Info("using memory store")
	return repository.NewStore(), func() {}
}

func newDispatcher(cfg config.Config, store app.Repository, logger *slog.Logger) app.RunDispatcher {
	if cfg.DispatchMode == "local" {
		logger.Info("using local dispatcher", "mode", cfg.DispatchMode)
		return dispatch.NewLocalDispatcher(store, logger)
	}
	if cfg.AgentRuntimeURL == "" {
		logger.Warn("AGENT_RUNTIME_URL is empty; falling back to local dispatcher", "mode", cfg.DispatchMode)
		return dispatch.NewLocalDispatcher(store, logger)
	}
	logger.Info("using http dispatcher", "runtime_url", cfg.AgentRuntimeURL)
	return dispatch.NewHTTPDispatcher(
		store,
		cfg.AgentRuntimeURL,
		cfg.ControlPlanePublicURL,
		cfg.InternalAPIToken,
		logger,
	)
}
