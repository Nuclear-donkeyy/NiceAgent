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
  timeout_seconds?: number;
  retry_max_attempts?: number;
  rate_limit_per_minute?: number;
  auth_type: "none" | "bearer";
  bearer_token?: string;
  bearer_token_secret_ref?: string;
}

export interface OpenAPIImportPreviewInput {
  document: string;
  base_url?: string;
}

export interface HTTPSkillImportCandidate {
  name: string;
  description?: string;
  method: "GET" | "POST";
  url: string;
  path: string;
  operation_id?: string;
  input_schema?: string;
  output_schema?: string;
  auth_type?: "none" | "bearer";
  requires_secret?: boolean;
  security_scheme?: string;
  unsupported_auth?: boolean;
}

export interface OpenAPIImportPreviewResponse {
  candidates: HTTPSkillImportCandidate[];
}

export interface OpenAPIImportCreateInput extends OpenAPIImportPreviewInput {
  operation_id?: string;
  method?: "GET" | "POST";
  path?: string;
  name?: string;
  description?: string;
  auth_type?: "none" | "bearer";
  bearer_token?: string;
  bearer_token_secret_ref?: string;
  timeout_seconds?: number;
  retry_max_attempts?: number;
  rate_limit_per_minute?: number;
}
