export type SkillRiskPolicy = "allow" | "block-high" | "block-destructive" | "read-only";

export interface ProjectRuntimePolicy {
  project_id: string;
  skill_risk_policy: SkillRiskPolicy;
  created_at?: string;
  updated_at?: string;
}

export interface ProjectRuntimePolicyInput {
  skill_risk_policy: SkillRiskPolicy;
}

export interface ProjectRuntimePolicyResponse {
  policy: ProjectRuntimePolicy;
}
