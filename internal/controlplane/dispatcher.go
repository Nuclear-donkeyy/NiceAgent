package controlplane

import (
	"context"
	"log/slog"

	"niceagent/internal/protocol"
	"niceagent/internal/runtime"
)

type RunDispatcher interface {
	Dispatch(ctx context.Context, run protocol.Run, userMessage string) error
}

type LocalDispatcher struct {
	repo   Repository
	engine runtime.AgentEngine
	log    *slog.Logger
}

func NewLocalDispatcher(repo Repository, engine runtime.AgentEngine, log *slog.Logger) *LocalDispatcher {
	return &LocalDispatcher{repo: repo, engine: engine, log: log}
}

func (d *LocalDispatcher) Dispatch(ctx context.Context, run protocol.Run, userMessage string) error {
	go func() {
		_, _ = d.repo.UpdateRunStatus(run.ID, protocol.RunRunning, "")
		req := protocol.RunRequest{
			RunID:       run.ID,
			ChatID:      run.ChatID,
			UserID:      run.UserID,
			WorkspaceID: run.WorkspaceID,
			SkillIDs:    []string{"workspace.read", "cli.exec"},
			ModelPolicy: "mock-default",
		}
		result := d.engine.Execute(ctx, req, userMessage, controlSink{repo: d.repo})
		if result.Status == protocol.RunFailed {
			d.log.Warn("local run failed", "run_id", run.ID, "error", result.Error)
		}
	}()
	return nil
}

type QueueDispatcher struct {
	queue RunQueue
}

func NewQueueDispatcher(queue RunQueue) *QueueDispatcher {
	return &QueueDispatcher{queue: queue}
}

func (d *QueueDispatcher) Dispatch(ctx context.Context, run protocol.Run, userMessage string) error {
	return d.queue.Enqueue(ctx, QueuedRun{
		RunID:       run.ID,
		ChatID:      run.ChatID,
		UserID:      run.UserID,
		WorkspaceID: run.WorkspaceID,
		UserMessage: userMessage,
		SkillIDs:    []string{"workspace.read", "cli.exec"},
		ModelPolicy: "mock-default",
	})
}
