package httpapi

import (
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
