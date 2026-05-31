package protocol

type SandboxCommand struct {
	RunID          string            `json:"run_id"`
	WorkspaceID    string            `json:"workspace_id"`
	Command        []string          `json:"command"`
	Env            map[string]string `json:"env,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds"`
	Network        bool              `json:"network"`
	MaxOutputBytes int               `json:"max_output_bytes,omitempty"`
	WorkspaceRoot  string            `json:"workspace_root,omitempty"`
}

type SandboxResult struct {
	RunID            string   `json:"run_id"`
	ExitCode         int      `json:"exit_code"`
	Stdout           string   `json:"stdout"`
	Stderr           string   `json:"stderr"`
	Duration         string   `json:"duration"`
	Truncated        bool     `json:"truncated"`
	ApprovalRequired bool     `json:"approval_required"`
	Reason           string   `json:"reason,omitempty"`
	Policy           string   `json:"policy,omitempty"`
	Command          []string `json:"command,omitempty"`
	Error            string   `json:"error,omitempty"`
}
