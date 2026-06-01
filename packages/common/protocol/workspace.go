package protocol

import "time"

type Workspace struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	ProjectID string    `json:"project_id,omitempty"`
	ChatID    string    `json:"chat_id,omitempty"`
	RunID     string    `json:"run_id,omitempty"`
	RootPath  string    `json:"root_path"`
	CreatedAt time.Time `json:"created_at"`
}

type Artifact struct {
	ID             string     `json:"id"`
	RunID          string     `json:"run_id"`
	ChatID         string     `json:"chat_id,omitempty"`
	UserID         string     `json:"user_id,omitempty"`
	ProjectID      string     `json:"project_id,omitempty"`
	WorkspaceID    string     `json:"workspace_id,omitempty"`
	Path           string     `json:"path"`
	Name           string     `json:"name,omitempty"`
	MimeType       string     `json:"mime_type"`
	SizeBytes      int64      `json:"size_bytes"`
	SHA256         string     `json:"sha256,omitempty"`
	StorageBackend string     `json:"storage_backend,omitempty"`
	StorageKey     string     `json:"storage_key,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	DeletedAt      *time.Time `json:"deleted_at,omitempty"`
}

type ArtifactListResponse struct {
	Artifacts []Artifact `json:"artifacts"`
}

type ArtifactWriteRequest struct {
	AttemptID string     `json:"attempt_id,omitempty"`
	Artifacts []Artifact `json:"artifacts"`
}

type ArtifactCleanupRequest struct {
	Limit       int  `json:"limit,omitempty"`
	DeleteFiles bool `json:"delete_files,omitempty"`
}

type ArtifactCleanupResponse struct {
	Artifacts    []Artifact `json:"artifacts"`
	DeletedFiles int        `json:"deleted_files"`
	FileErrors   []string   `json:"file_errors,omitempty"`
}

type ArtifactTextResponse struct {
	Artifact  Artifact `json:"artifact"`
	Content   string   `json:"content"`
	Truncated bool     `json:"truncated"`
	BytesRead int      `json:"bytes_read"`
}
