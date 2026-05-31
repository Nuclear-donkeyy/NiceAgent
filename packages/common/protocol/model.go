package protocol

type ModelProvider struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	BaseURL   string `json:"base_url,omitempty"`
	Model     string `json:"model"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at,omitempty"`
}
