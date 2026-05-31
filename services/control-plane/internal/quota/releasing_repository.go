package quota

import (
	"context"
	"log/slog"

	"niceagent/common/protocol"
	"niceagent/control-plane/internal/app"
)

type ReleasingRepository struct {
	app.Repository
	Limiter Limiter
	Logger  *slog.Logger
}

func NewReleasingRepository(repo app.Repository, limiter Limiter, logger *slog.Logger) app.Repository {
	if limiter == nil {
		return repo
	}
	return &ReleasingRepository{Repository: repo, Limiter: limiter, Logger: logger}
}

func (r *ReleasingRepository) UpdateRunStatus(runID string, status protocol.RunStatus, errMessage string) (protocol.Run, error) {
	before, _ := r.Repository.GetRun(runID)
	run, err := r.Repository.UpdateRunStatus(runID, status, errMessage)
	if err != nil {
		return run, err
	}
	if !app.IsTerminalRunStatus(before.Status) && app.IsTerminalRunStatus(run.Status) {
		if projectID := r.projectIDForRun(run); projectID != "" {
			usage, _ := r.Repository.GetRunUsage(run.ID)
			if releaseErr := r.Limiter.ReleaseRun(context.Background(), run, projectID, usage); releaseErr != nil && r.Logger != nil {
				r.Logger.Warn("release redis quota reservation failed", "run_id", run.ID, "error", releaseErr)
			}
		}
	}
	return run, nil
}

func (r *ReleasingRepository) projectIDForRun(run protocol.Run) string {
	chat, _, err := r.Repository.GetChat(run.ChatID)
	if err != nil {
		if r.Logger != nil {
			r.Logger.Warn("load chat for quota release failed", "run_id", run.ID, "chat_id", run.ChatID, "error", err)
		}
		return ""
	}
	return chat.ProjectID
}
