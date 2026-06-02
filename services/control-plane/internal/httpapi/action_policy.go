package httpapi

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"niceagent/control-plane/internal/app"
)

type actionRequirement string

const (
	actionRequirementWrite             actionRequirement = "write"
	actionRequirementProjectAdmin      actionRequirement = "project_admin"
	actionRequirementOrganizationAdmin actionRequirement = "organization_admin"
)

type actionPolicy struct {
	requirements map[string]actionRequirement
}

type actionPolicyDecision struct {
	Allowed       bool
	Reason        string
	Message       string
	RequiredRoles []string
}

func defaultActionPolicy() actionPolicy {
	return actionPolicy{requirements: map[string]actionRequirement{
		"chat.create":                         actionRequirementWrite,
		"chat.archive":                        actionRequirementWrite,
		"chat.restore":                        actionRequirementWrite,
		"message.create":                      actionRequirementWrite,
		"run.cancel":                          actionRequirementWrite,
		"skill.enable":                        actionRequirementWrite,
		"skill.disable":                       actionRequirementWrite,
		"skill.approve":                       actionRequirementWrite,
		"skill.import.preview":                actionRequirementWrite,
		"skill.import.mcp.preview":            actionRequirementWrite,
		"skill.import.create":                 actionRequirementWrite,
		"skill.import.mcp.create":             actionRequirementWrite,
		"skill.create":                        actionRequirementWrite,
		"skill.update":                        actionRequirementWrite,
		"project.usage.read":                  actionRequirementProjectAdmin,
		"project.quota.update":                actionRequirementProjectAdmin,
		"project.runtime_policy.update":       actionRequirementProjectAdmin,
		"project.member.upsert":               actionRequirementProjectAdmin,
		"project.member.remove":               actionRequirementProjectAdmin,
		"invitation.list":                     actionRequirementOrganizationAdmin,
		"invitation.create":                   actionRequirementOrganizationAdmin,
		"invitation.email.resend":             actionRequirementOrganizationAdmin,
		"invitation.email_event.list":         actionRequirementOrganizationAdmin,
		"invitation.email_suppression.list":   actionRequirementOrganizationAdmin,
		"invitation.email_suppression.delete": actionRequirementOrganizationAdmin,
		"invitation.email_event.record":       actionRequirementOrganizationAdmin,
		"organization.member.upsert":          actionRequirementOrganizationAdmin,
		"organization.member.remove":          actionRequirementOrganizationAdmin,
	}}
}

func loadActionPolicyFile(path string) (actionPolicy, error) {
	policy := defaultActionPolicy()
	path = strings.TrimSpace(path)
	if path == "" {
		return policy, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return actionPolicy{}, fmt.Errorf("read action policy file: %w", err)
	}
	var input struct {
		Actions map[string]string `json:"actions"`
	}
	if err := json.Unmarshal(data, &input); err != nil {
		return actionPolicy{}, fmt.Errorf("decode action policy file: %w", err)
	}
	for action, requirement := range input.Actions {
		action = strings.TrimSpace(action)
		if action == "" {
			return actionPolicy{}, fmt.Errorf("action policy file contains an empty action")
		}
		parsed, err := parseActionRequirement(requirement)
		if err != nil {
			return actionPolicy{}, fmt.Errorf("action %q: %w", action, err)
		}
		policy.requirements[action] = parsed
	}
	return policy, nil
}

func ValidateActionPolicyFile(path string) error {
	_, err := loadActionPolicyFile(path)
	return err
}

func parseActionRequirement(value string) (actionRequirement, error) {
	switch actionRequirement(strings.ToLower(strings.TrimSpace(value))) {
	case actionRequirementWrite:
		return actionRequirementWrite, nil
	case actionRequirementProjectAdmin:
		return actionRequirementProjectAdmin, nil
	case actionRequirementOrganizationAdmin:
		return actionRequirementOrganizationAdmin, nil
	default:
		return "", fmt.Errorf("requirement must be one of write, project_admin, or organization_admin")
	}
}

func (p actionPolicy) Authorize(actor app.ActorContext, action string, fallback actionRequirement) actionPolicyDecision {
	requirement := fallback
	if configured, ok := p.requirements[strings.TrimSpace(action)]; ok {
		requirement = configured
	}
	switch requirement {
	case actionRequirementWrite:
		if actorCanWrite(actor) {
			return actionPolicyDecision{Allowed: true}
		}
		return actionPolicyDecision{
			Reason:        "action requires write role",
			Message:       "当前角色没有执行该操作的权限。",
			RequiredRoles: []string{"owner", "admin", "member", "editor", "writer"},
		}
	case actionRequirementProjectAdmin:
		if actorCanAdmin(actor) {
			return actionPolicyDecision{Allowed: true}
		}
		return actionPolicyDecision{
			Reason:        "action requires project admin role",
			Message:       "当前角色没有管理项目的权限。",
			RequiredRoles: []string{"owner", "admin"},
		}
	case actionRequirementOrganizationAdmin:
		if actorCanAdmin(actor) {
			return actionPolicyDecision{Allowed: true}
		}
		return actionPolicyDecision{
			Reason:        "action requires organization admin role",
			Message:       "当前角色没有管理组织的权限。",
			RequiredRoles: []string{"owner", "admin"},
		}
	default:
		return actionPolicyDecision{
			Reason:        "unsupported action requirement",
			Message:       "当前角色没有执行该操作的权限。",
			RequiredRoles: nil,
		}
	}
}
