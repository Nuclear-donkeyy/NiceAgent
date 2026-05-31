export type SkillScope = "system" | "user";
export type SkillKind = "builtin" | "http";
export type SkillStatus = "enabled" | "disabled" | "archived";
export type SkillRisk = "low" | "medium" | "high";

export interface Skill {
  id: string;
  slug?: string;
  scope?: SkillScope;
  kind?: SkillKind;
  owner_user_id?: string;
  project_id?: string;
  status?: SkillStatus;
  current_version_id?: string;
  name: string;
  version: string;
  description: string;
  risk: SkillRisk;
  requires_auth: boolean;
  input_schema?: string;
  output_schema?: string;
  annotations?: string;
  runtime_config?: string;
  enabled: boolean;
}

export interface SkillGroups {
  system: Skill[];
  user: Skill[];
}

export interface SkillsResponse {
  skills: Skill[];
  groups?: Partial<SkillGroups> | null;
}

export interface HTTPSkillInput {
  name: string;
  description: string;
  method: "GET" | "POST";
  url: string;
  input_schema?: string;
  output_schema?: string;
  auth_type: "none" | "bearer";
  bearer_token?: string;
}
