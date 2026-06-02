package main

import (
	"context"
	"database/sql"
	"log"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"niceagent/common/platform"
	"niceagent/control-plane/internal/app"
	"niceagent/control-plane/internal/config"
	"niceagent/control-plane/internal/dispatch"
	"niceagent/control-plane/internal/events"
	"niceagent/control-plane/internal/httpapi"
	"niceagent/control-plane/internal/mailer"
	"niceagent/control-plane/internal/quota"
	"niceagent/control-plane/internal/repository"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	cfg := config.FromEnv()
	if err := cfg.Validate(); err != nil {
		log.Fatal(err)
	}
	logger := platform.NewLogger("control-plane")
	shutdownTelemetry, err := platform.InitOpenTelemetry(context.Background(), platform.OpenTelemetryConfigFromEnv("control_plane", cfg.Environment))
	if err != nil {
		log.Fatalf("init opentelemetry: %v", err)
	}
	defer shutdownTelemetryWithTimeout(shutdownTelemetry)
	store, closeStore := newStore(cfg, logger)
	defer closeStore()
	store = withEventFanout(cfg, store, logger)
	quotaLimiter := newQuotaLimiter(cfg, logger)
	if quotaLimiter != nil {
		store = quota.NewReleasingRepository(store, quotaLimiter, logger)
	}
	dispatcher := newDispatcher(cfg, store, logger)
	invitationMailer := newInvitationMailer(cfg, store, logger)
	server := httpapi.NewServerWithOptions(store, dispatcher, logger, httpapi.ServerOptions{
		AuthMode:              cfg.AuthMode,
		ControlPlanePublicURL: cfg.ControlPlanePublicURL,
		InternalAPIToken:      cfg.InternalAPIToken,
		InvitationWebhookVerification: httpapi.InvitationWebhookVerification{
			SharedSecret:      cfg.InvitationEmailWebhookSecret,
			SendGridPublicKey: cfg.InvitationEmailSendGridPublicKey,
			MailgunSigningKey: cfg.InvitationEmailMailgunSigningKey,
		},
		InvitationMailer: invitationMailer,
		RunQuota: httpapi.RunQuota{
			MaxConcurrentRuns:       cfg.MaxConcurrentRuns,
			MaxRunsPerHour:          cfg.MaxRunsPerHour,
			MaxTokensPerDay:         cfg.MaxModelTokensPerDay,
			MaxToolCallsPerDay:      cfg.MaxToolCallsPerDay,
			MaxSandboxSecondsPerDay: cfg.MaxSandboxSecondsPerDay,
		},
		QuotaLimiter: quotaLimiter,
		TokenReservation: httpapi.TokenReservationOptions{
			Mode:         cfg.QuotaModelTokenReservationMode,
			OutputBuffer: cfg.QuotaModelTokenOutputBuffer,
			Model:        cfg.QuotaModelTokenEstimatorModel,
		},
		ArtifactRetention:    time.Duration(cfg.ArtifactRetentionDays) * 24 * time.Hour,
		ArtifactCleanupFiles: cfg.ArtifactCleanupDeleteFiles,
		OIDC: httpapi.OIDCConfig{
			Issuer:           cfg.OIDCIssuerURL,
			Audience:         cfg.OIDCAudience,
			JWKSURL:          cfg.OIDCJWKSURL,
			DefaultProjectID: cfg.OIDCDefaultProjectID,
			DefaultOrgID:     cfg.OIDCDefaultOrgID,
			UserIDClaim:      cfg.OIDCUserIDClaim,
			ProjectIDClaim:   cfg.OIDCProjectIDClaim,
			OrgIDClaim:       cfg.OIDCOrgIDClaim,
			RolesClaim:       cfg.OIDCRolesClaim,
			EmailClaim:       cfg.OIDCEmailClaim,
			NameClaim:        cfg.OIDCNameClaim,
		},
		OIDCBrowser: httpapi.OIDCBrowserConfig{
			ClientID:          cfg.OIDCClientID,
			ClientSecret:      cfg.OIDCClientSecret,
			AuthURL:           cfg.OIDCAuthURL,
			TokenURL:          cfg.OIDCTokenURL,
			RedirectURL:       cfg.OIDCRedirectURL,
			SessionSecret:     cfg.OIDCSessionSecret,
			SessionTTLSeconds: cfg.OIDCSessionTTLSeconds,
		},
	})

	logger.Info("starting control plane", "addr", cfg.Addr, "auth_mode", cfg.AuthMode)
	if err := http.ListenAndServe(cfg.Addr, server.Handler()); err != nil {
		log.Fatal(err)
	}
}

func newInvitationMailer(cfg config.Config, store app.Repository, logger *slog.Logger) app.InvitationMailer {
	switch strings.ToLower(strings.TrimSpace(cfg.InvitationEmailMode)) {
	case "", "disabled":
		logger.Info("invitation email disabled")
		return nil
	case "smtp":
		logger.Info("using smtp invitation email", "host", cfg.SMTPHost, "port", cfg.SMTPPort, "from", cfg.SMTPFrom)
		smtpMailer := mailer.NewSMTPInvitationMailer(mailer.SMTPConfig{
			Host:            cfg.SMTPHost,
			Port:            cfg.SMTPPort,
			Username:        cfg.SMTPUsername,
			Password:        cfg.SMTPPassword,
			From:            cfg.SMTPFrom,
			PublicBaseURL:   cfg.InvitationPublicBaseURL,
			SubjectTemplate: cfg.InvitationEmailSubjectTemplate,
			BodyTemplate:    cfg.InvitationEmailBodyTemplate,
		})
		if strings.EqualFold(strings.TrimSpace(cfg.InvitationEmailQueueMode), "memory") {
			logger.Info(
				"using memory invitation email queue",
				"size", cfg.InvitationEmailQueueSize,
				"workers", cfg.InvitationEmailQueueWorkers,
				"retry_attempts", cfg.InvitationEmailRetryAttempts,
				"retry_initial_delay_ms", cfg.InvitationEmailRetryInitialDelay,
			)
			return mailer.NewQueuedInvitationMailer(smtpMailer, mailer.QueueConfig{
				Size:              cfg.InvitationEmailQueueSize,
				Workers:           cfg.InvitationEmailQueueWorkers,
				RetryAttempts:     cfg.InvitationEmailRetryAttempts,
				RetryInitialDelay: time.Duration(cfg.InvitationEmailRetryInitialDelay) * time.Millisecond,
			}, logger)
		}
		if strings.EqualFold(strings.TrimSpace(cfg.InvitationEmailQueueMode), "outbox") {
			logger.Info(
				"using durable invitation email outbox",
				"workers", cfg.InvitationEmailQueueWorkers,
				"retry_attempts", cfg.InvitationEmailRetryAttempts,
				"retry_initial_delay_ms", cfg.InvitationEmailRetryInitialDelay,
			)
			return mailer.NewOutboxInvitationMailer(smtpMailer, store, mailer.DurableQueueConfig{
				Workers:           cfg.InvitationEmailQueueWorkers,
				RetryAttempts:     cfg.InvitationEmailRetryAttempts,
				RetryInitialDelay: time.Duration(cfg.InvitationEmailRetryInitialDelay) * time.Millisecond,
				WorkerID:          "control-plane",
			}, logger)
		}
		return smtpMailer
	default:
		logger.Warn("unknown INVITATION_EMAIL_MODE; invitation email disabled", "mode", cfg.InvitationEmailMode)
		return nil
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

func newQuotaLimiter(cfg config.Config, logger *slog.Logger) quota.Limiter {
	switch cfg.QuotaCounterMode {
	case "", "repository", "postgres", "memory":
		return nil
	case "redis":
		if cfg.RedisAddr == "" {
			log.Fatal("REDIS_ADDR is required when QUOTA_COUNTER_MODE=redis")
		}
		logger.Info("using redis quota counter", "addr", cfg.RedisAddr, "prefix", cfg.QuotaCounterPrefix)
		return quota.NewRedisLimiterWithOptions(cfg.RedisAddr, cfg.QuotaCounterPrefix, quota.LimiterOptions{
			ModelTokenReservationPerRun: cfg.QuotaModelTokenReservationPerRun,
		})
	default:
		logger.Warn("unknown QUOTA_COUNTER_MODE; using repository quota counters", "quota_counter_mode", cfg.QuotaCounterMode)
		return nil
	}
}

func withEventFanout(cfg config.Config, store app.Repository, logger *slog.Logger) app.Repository {
	if cfg.EventFanoutMode == "" || cfg.EventFanoutMode == "local" {
		return store
	}
	if cfg.EventFanoutMode != "redis" {
		logger.Warn("unknown EVENT_FANOUT_MODE; using local repository fanout", "event_fanout_mode", cfg.EventFanoutMode)
		return store
	}
	if cfg.RedisAddr == "" {
		log.Fatal("REDIS_ADDR is required when EVENT_FANOUT_MODE=redis")
	}
	logger.Info("using redis event fanout", "addr", cfg.RedisAddr, "prefix", cfg.EventFanoutPrefix)
	return events.NewFanoutRepository(store, events.NewRedisNudgeBus(cfg.RedisAddr, cfg.EventFanoutPrefix), logger)
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
	if cfg.DispatchMode == "redis" {
		if cfg.RedisAddr == "" {
			log.Fatal("REDIS_ADDR is required when DISPATCH_MODE=redis")
		}
		logger.Info(
			"using redis streams dispatcher",
			"addr", cfg.RedisAddr,
			"stream", cfg.RunQueueStream,
			"group", cfg.RunQueueGroup,
			"consumer", cfg.RunQueueConsumer,
			"max_len", cfg.RunQueueMaxLen,
		)
		queue := dispatch.NewRedisStreamsRunQueue(cfg.RedisAddr, cfg.RunQueueStream, cfg.RunQueueGroup, cfg.RunQueueConsumer)
		queue.MaxLen = cfg.RunQueueMaxLen
		return dispatch.NewQueueDispatcher(store, queue)
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
