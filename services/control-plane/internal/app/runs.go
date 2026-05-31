package app

import "niceagent/common/protocol"

type RepositorySink struct {
	Repo Repository
}

func (s RepositorySink) Emit(runID string, typ protocol.RunEventType, message string, payload any) error {
	_, err := s.Repo.AddEvent(runID, typ, message, payload)
	return err
}

func (s RepositorySink) Complete(runID string, content string) error {
	run, err := s.Repo.GetRun(runID)
	if err != nil {
		return err
	}
	if IsTerminalRunStatus(run.Status) {
		return nil
	}
	if _, err := s.Repo.AddAssistantMessage(run.ChatID, runID, content); err != nil {
		return err
	}
	if _, err := s.Repo.UpdateRunStatus(runID, protocol.RunSucceeded, ""); err != nil {
		return err
	}
	_, err = s.Repo.AddEvent(runID, protocol.EventRunSucceeded, "Run completed.", nil)
	return err
}

func (s RepositorySink) Fail(runID string, message string) error {
	run, err := s.Repo.GetRun(runID)
	if err != nil {
		return err
	}
	if IsTerminalRunStatus(run.Status) {
		return nil
	}
	if _, err := s.Repo.UpdateRunStatus(runID, protocol.RunFailed, message); err != nil {
		return err
	}
	_, err = s.Repo.AddEvent(runID, protocol.EventRunFailed, message, nil)
	return err
}

func (s RepositorySink) IsCanceled(runID string) bool {
	run, err := s.Repo.GetRun(runID)
	return err == nil && run.Status == protocol.RunCanceled
}

func IsTerminalRunStatus(status protocol.RunStatus) bool {
	return status == protocol.RunSucceeded || status == protocol.RunFailed || status == protocol.RunCanceled
}

func RuntimeSkillsForRun(repo Repository, run protocol.Run) []protocol.RuntimeSkill {
	chat, _, err := repo.GetChat(run.ChatID)
	if err != nil {
		return nil
	}
	return repo.ListRuntimeSkillsForUser(run.UserID, chat.ProjectID)
}

func SkillIDsForRun(repo Repository, run protocol.Run) []string {
	return SkillIDsFromRuntimeSkills(RuntimeSkillsForRun(repo, run))
}

func SkillIDsFromRuntimeSkills(skills []protocol.RuntimeSkill) []string {
	ids := make([]string, 0, len(skills))
	for _, runtimeSkill := range skills {
		ids = append(ids, runtimeSkill.Skill.ID)
	}
	return ids
}

func ContainsSkillID(skillIDs []string, id string) bool {
	for _, skillID := range skillIDs {
		if skillID == id {
			return true
		}
	}
	return false
}
