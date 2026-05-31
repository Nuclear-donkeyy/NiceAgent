package protocol

type ModelProvider struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	BaseURL   string `json:"base_url,omitempty"`
	Model     string `json:"model"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at,omitempty"`
}

type ModelProviderHealth struct {
	Provider        string                      `json:"provider,omitempty"`
	Model           string                      `json:"model,omitempty"`
	Status          string                      `json:"status"`
	ProbeEnabled    bool                        `json:"probe_enabled,omitempty"`
	ProbeStatus     string                      `json:"probe_status,omitempty"`
	ProbeCount      int64                       `json:"probe_count,omitempty"`
	ProbeSuccess    int64                       `json:"probe_success,omitempty"`
	ProbeError      int64                       `json:"probe_error,omitempty"`
	LastProbeAt     string                      `json:"last_probe_at,omitempty"`
	RequestCount    int64                       `json:"request_count,omitempty"`
	SuccessCount    int64                       `json:"success_count,omitempty"`
	ErrorCount      int64                       `json:"error_count,omitempty"`
	RetryCount      int                         `json:"retry_count,omitempty"`
	FallbackEnabled bool                        `json:"fallback_enabled,omitempty"`
	LastFallback    bool                        `json:"last_fallback,omitempty"`
	FallbackFrom    string                      `json:"fallback_from,omitempty"`
	FallbackTo      string                      `json:"fallback_to,omitempty"`
	LastErrorClass  string                      `json:"last_error_class,omitempty"`
	LastError       string                      `json:"last_error,omitempty"`
	LastLatencyMs   int64                       `json:"last_latency_ms,omitempty"`
	LastSuccessAt   string                      `json:"last_success_at,omitempty"`
	LastFailureAt   string                      `json:"last_failure_at,omitempty"`
	Targets         []ModelProviderTargetHealth `json:"targets,omitempty"`
}

type ModelProviderTargetHealth struct {
	Name           string `json:"name"`
	Status         string `json:"status"`
	ProbeEnabled   bool   `json:"probe_enabled,omitempty"`
	ProbeStatus    string `json:"probe_status,omitempty"`
	ProbeCount     int64  `json:"probe_count,omitempty"`
	ProbeSuccess   int64  `json:"probe_success,omitempty"`
	ProbeError     int64  `json:"probe_error,omitempty"`
	LastProbeAt    string `json:"last_probe_at,omitempty"`
	RequestCount   int64  `json:"request_count,omitempty"`
	SuccessCount   int64  `json:"success_count,omitempty"`
	ErrorCount     int64  `json:"error_count,omitempty"`
	RetryCount     int    `json:"retry_count,omitempty"`
	LastErrorClass string `json:"last_error_class,omitempty"`
	LastError      string `json:"last_error,omitempty"`
	LastLatencyMs  int64  `json:"last_latency_ms,omitempty"`
	LastSuccessAt  string `json:"last_success_at,omitempty"`
	LastFailureAt  string `json:"last_failure_at,omitempty"`
}
