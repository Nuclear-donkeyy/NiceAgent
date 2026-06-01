package protocol

import "time"

type SkillRisk string

const (
	SkillRiskLow    SkillRisk = "low"
	SkillRiskMedium SkillRisk = "medium"
	SkillRiskHigh   SkillRisk = "high"
)

type SkillScope string

const (
	SkillScopeSystem SkillScope = "system"
	SkillScopeUser   SkillScope = "user"
)

type SkillKind string

const (
	SkillKindBuiltin SkillKind = "builtin"
	SkillKindHTTP    SkillKind = "http"
)

type SkillStatus string

const (
	SkillStatusEnabled  SkillStatus = "enabled"
	SkillStatusDisabled SkillStatus = "disabled"
	SkillStatusArchived SkillStatus = "archived"
)

type Skill struct {
	ID               string      `json:"id"`
	Slug             string      `json:"slug,omitempty"`
	Scope            SkillScope  `json:"scope,omitempty"`
	Kind             SkillKind   `json:"kind,omitempty"`
	OwnerUserID      string      `json:"owner_user_id,omitempty"`
	ProjectID        string      `json:"project_id,omitempty"`
	Status           SkillStatus `json:"status,omitempty"`
	CurrentVersionID string      `json:"current_version_id,omitempty"`
	Name             string      `json:"name"`
	Version          string      `json:"version"`
	Description      string      `json:"description"`
	Risk             SkillRisk   `json:"risk"`
	RequiresAuth     bool        `json:"requires_auth"`
	InputSchema      string      `json:"input_schema,omitempty"`
	OutputSchema     string      `json:"output_schema,omitempty"`
	Annotations      string      `json:"annotations,omitempty"`
	RuntimeConfig    string      `json:"runtime_config,omitempty"`
	Enabled          bool        `json:"enabled"`
}

type RuntimeSkill struct {
	Skill           Skill                    `json:"skill"`
	Secrets         map[string]string        `json:"secrets,omitempty"`
	SecretMaterials map[string]RuntimeSecret `json:"secret_materials,omitempty"`
}

type RuntimeSecret struct {
	EncryptedValue string `json:"encrypted_value,omitempty"`
	SecretRef      string `json:"secret_ref,omitempty"`
}

func (s RuntimeSkill) SecretMaterial(key string) RuntimeSecret {
	if material, ok := s.SecretMaterials[key]; ok {
		return material
	}
	if value := s.Secrets[key]; value != "" {
		return RuntimeSecret{EncryptedValue: value}
	}
	return RuntimeSecret{}
}

type SkillGroups struct {
	System []Skill `json:"system"`
	User   []Skill `json:"user"`
}

type SkillsResponse struct {
	Skills []Skill     `json:"skills"`
	Groups SkillGroups `json:"groups"`
}

type HTTPSkillInput struct {
	Name                 string `json:"name"`
	Description          string `json:"description"`
	Method               string `json:"method"`
	URL                  string `json:"url"`
	InputSchema          string `json:"input_schema,omitempty"`
	OutputSchema         string `json:"output_schema,omitempty"`
	TimeoutSeconds       int    `json:"timeout_seconds,omitempty"`
	RetryMaxAttempts     int    `json:"retry_max_attempts,omitempty"`
	RateLimitPerMinute   int    `json:"rate_limit_per_minute,omitempty"`
	AuthType             string `json:"auth_type,omitempty"`
	BearerToken          string `json:"bearer_token,omitempty"`
	BearerTokenSecretRef string `json:"bearer_token_secret_ref,omitempty"`
}

type OpenAPIImportPreviewInput struct {
	Document string `json:"document"`
	BaseURL  string `json:"base_url,omitempty"`
}

type HTTPSkillImportCandidate struct {
	Name            string `json:"name"`
	Description     string `json:"description,omitempty"`
	Method          string `json:"method"`
	URL             string `json:"url"`
	Path            string `json:"path"`
	OperationID     string `json:"operation_id,omitempty"`
	InputSchema     string `json:"input_schema,omitempty"`
	OutputSchema    string `json:"output_schema,omitempty"`
	AuthType        string `json:"auth_type,omitempty"`
	RequiresSecret  bool   `json:"requires_secret,omitempty"`
	SecurityScheme  string `json:"security_scheme,omitempty"`
	UnsupportedAuth bool   `json:"unsupported_auth,omitempty"`
}

type OpenAPIImportPreviewResponse struct {
	Candidates []HTTPSkillImportCandidate `json:"candidates"`
}

type SkillInvocation struct {
	ID             string     `json:"id"`
	RunID          string     `json:"run_id"`
	SkillID        string     `json:"skill_id"`
	SkillVersion   string     `json:"skill_version"`
	Input          any        `json:"input"`
	TimeoutSeconds int        `json:"timeout_seconds"`
	ApprovalID     string     `json:"approval_id,omitempty"`
	StartedAt      time.Time  `json:"started_at"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
	Status         string     `json:"status"`
	Output         any        `json:"output,omitempty"`
	Error          string     `json:"error,omitempty"`
}
