package controlplane

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"niceagent/common/protocol"
	"niceagent/common/sandbox"
)

type RunDispatcher interface {
	Dispatch(ctx context.Context, run protocol.Run, userMessage string) error
}

type LocalDispatcher struct {
	repo    Repository
	sandbox *sandbox.Executor
	log     *slog.Logger
}

func NewLocalDispatcher(repo Repository, log *slog.Logger) *LocalDispatcher {
	return &LocalDispatcher{repo: repo, sandbox: sandbox.NewExecutor(), log: log}
}

func (d *LocalDispatcher) Dispatch(ctx context.Context, run protocol.Run, userMessage string) error {
	go func() {
		_, _ = d.repo.UpdateRunStatus(run.ID, protocol.RunRunning, "")
		sink := controlSink{repo: d.repo}
		_ = sink.Emit(run.ID, protocol.EventRunStarted, "Local demo dispatcher accepted the run.", map[string]any{
			"mode": "local-demo",
		})
		content := "我已经接收到任务。当前是 Control Plane 的本地演示 dispatcher；生产部署时应改由 Redis/HTTP 调度到独立 Agent Runtime 服务。"
		_ = sink.Emit(run.ID, protocol.EventModelToken, content, nil)
		time.Sleep(30 * time.Millisecond)
		if command, ok := parseLocalCLI(userMessage); ok {
			if !containsSkillID(skillIDsForRun(d.repo, run), "cli.exec") {
				content += "\n\n当前用户未启用系统 CLI 工具，无法执行 /cli 请求。"
				if err := sink.Complete(run.ID, content); err != nil {
					d.log.Warn("local run failed", "run_id", run.ID, "error", err)
				}
				return
			}
			_ = sink.Emit(run.ID, protocol.EventToolStarted, "Starting cli.exec skill.", map[string]any{"command": command})
			result := d.sandbox.Execute(ctx, protocol.SandboxCommand{
				RunID:          run.ID,
				WorkspaceID:    run.WorkspaceID,
				Command:        command,
				TimeoutSeconds: 10,
				Network:        true,
			})
			_ = sink.Emit(run.ID, protocol.EventToolOutput, "CLI command completed.", result)
			_ = sink.Emit(run.ID, protocol.EventToolFinished, "Finished cli.exec skill.", map[string]any{"exit_code": result.ExitCode})
			content += "\n\nCLI 获取结果：\n" + result.Stdout
			if result.Stderr != "" {
				content += result.Stderr
			}
			if result.Error != "" {
				content += "系统 CLI 策略拒绝或执行错误: " + result.Error
			}
		}
		if err := sink.Complete(run.ID, content); err != nil {
			d.log.Warn("local run failed", "run_id", run.ID, "error", err)
		}
	}()
	return nil
}

func parseLocalCLI(content string) ([]string, bool) {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "/cli ") {
		return nil, false
	}
	parts := strings.Fields(strings.TrimPrefix(content, "/cli "))
	return parts, len(parts) > 0
}

type QueueDispatcher struct {
	repo  Repository
	queue RunQueue
}

func NewQueueDispatcher(repo Repository, queue RunQueue) *QueueDispatcher {
	return &QueueDispatcher{repo: repo, queue: queue}
}

func (d *QueueDispatcher) Dispatch(ctx context.Context, run protocol.Run, userMessage string) error {
	runtimeSkills := runtimeSkillsForRun(d.repo, run)
	return d.queue.Enqueue(ctx, QueuedRun{
		RunID:       run.ID,
		ChatID:      run.ChatID,
		UserID:      run.UserID,
		WorkspaceID: run.WorkspaceID,
		UserMessage: userMessage,
		SkillIDs:    skillIDsFromRuntimeSkills(runtimeSkills),
		ModelPolicy: "mock-default",
	})
}

func skillIDsForRun(repo Repository, run protocol.Run) []string {
	return skillIDsFromRuntimeSkills(runtimeSkillsForRun(repo, run))
}

func runtimeSkillsForRun(repo Repository, run protocol.Run) []protocol.RuntimeSkill {
	chat, _, err := repo.GetChat(run.ChatID)
	if err != nil {
		return nil
	}
	return repo.ListRuntimeSkillsForUser(run.UserID, chat.ProjectID)
}

func skillIDsFromRuntimeSkills(skills []protocol.RuntimeSkill) []string {
	ids := make([]string, 0, len(skills))
	for _, runtimeSkill := range skills {
		ids = append(ids, runtimeSkill.Skill.ID)
	}
	return ids
}

func containsSkillID(skillIDs []string, id string) bool {
	for _, skillID := range skillIDs {
		if skillID == id {
			return true
		}
	}
	return false
}
