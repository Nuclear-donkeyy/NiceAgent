package protocol

import (
	"strings"
	"time"
)

const (
	SkillRiskPolicyAllow            = "allow"
	SkillRiskPolicyBlockHigh        = "block-high"
	SkillRiskPolicyBlockDestructive = "block-destructive"
	SkillRiskPolicyReadOnly         = "read-only"
)

type ProjectRuntimePolicy struct {
	ProjectID       string    `json:"project_id"`
	SkillRiskPolicy string    `json:"skill_risk_policy"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type ProjectRuntimePolicyInput struct {
	SkillRiskPolicy string `json:"skill_risk_policy"`
}

type ProjectRuntimePolicyResponse struct {
	Policy ProjectRuntimePolicy `json:"policy"`
}

func NormalizeSkillRiskPolicy(policy string) string {
	policy = strings.TrimSpace(policy)
	switch policy {
	case "", SkillRiskPolicyAllow:
		return SkillRiskPolicyAllow
	case SkillRiskPolicyBlockHigh:
		return SkillRiskPolicyBlockHigh
	case SkillRiskPolicyBlockDestructive:
		return SkillRiskPolicyBlockDestructive
	case SkillRiskPolicyReadOnly:
		return SkillRiskPolicyReadOnly
	default:
		return ""
	}
}
