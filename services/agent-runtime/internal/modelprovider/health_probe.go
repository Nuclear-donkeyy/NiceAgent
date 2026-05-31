package modelprovider

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"niceagent/common/platform"
)

type Probeable interface {
	Probe(ctx context.Context) error
}

type ProbeStateRecorder interface {
	MarkProbeEnabled()
}

type HealthProbeConfig struct {
	Interval     time.Duration
	Timeout      time.Duration
	InitialDelay time.Duration
}

func ProbeModel(ctx context.Context, chatModel model.ToolCallingChatModel) error {
	if chatModel == nil {
		return errors.New("model health probe requires a model")
	}
	if probeable, ok := chatModel.(Probeable); ok {
		return probeable.Probe(ctx)
	}
	_, err := chatModel.Generate(ctx, []*schema.Message{schema.UserMessage("health check: reply with ok")})
	return err
}

func StartHealthProbeLoop(ctx context.Context, chatModel model.ToolCallingChatModel, cfg HealthProbeConfig, logger *slog.Logger, metrics *platform.Metrics) {
	if cfg.Interval <= 0 {
		cfg.Interval = time.Minute
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	if recorder, ok := chatModel.(ProbeStateRecorder); ok {
		recorder.MarkProbeEnabled()
	}
	go func() {
		if cfg.InitialDelay > 0 {
			timer := time.NewTimer(cfg.InitialDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		ticker := time.NewTicker(cfg.Interval)
		defer ticker.Stop()
		for {
			runHealthProbe(ctx, chatModel, cfg.Timeout, logger, metrics)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func runHealthProbe(ctx context.Context, chatModel model.ToolCallingChatModel, timeout time.Duration, logger *slog.Logger, metrics *platform.Metrics) {
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	err := ProbeModel(probeCtx, chatModel)
	duration := time.Since(start)
	status := "success"
	errorClass := "none"
	if err != nil {
		status = "error"
		if classified := ClassifyProviderError(err); classified != nil && classified.Class != "" {
			errorClass = string(classified.Class)
		}
		if logger != nil {
			logger.Warn("model health probe failed", "error", err)
		}
	} else if logger != nil {
		logger.Debug("model health probe succeeded", "duration", duration.String())
	}
	if metrics != nil {
		labels := platform.Labels{"status": status, "error_class": errorClass}
		if reporter, ok := chatModel.(HealthReporter); ok {
			health := reporter.ModelProviderHealth()
			labels["provider"] = firstNonEmpty(health.Provider, "unknown")
			labels["model"] = firstNonEmpty(health.Model, "unknown")
		}
		metrics.IncCounter("niceagent_model_health_probe_total", labels)
		metrics.ObserveDuration("niceagent_model_health_probe_duration_seconds", labels, duration)
	}
}
