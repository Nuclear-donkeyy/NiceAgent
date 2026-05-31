ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS project_id TEXT REFERENCES projects(id);
ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS chat_id TEXT REFERENCES chat_sessions(id);
ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS run_id TEXT REFERENCES runs(id);

CREATE TABLE IF NOT EXISTS artifacts (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL REFERENCES runs(id),
  chat_id TEXT REFERENCES chat_sessions(id),
  user_id TEXT REFERENCES users(id),
  project_id TEXT REFERENCES projects(id),
  workspace_id TEXT REFERENCES workspaces(id),
  path TEXT NOT NULL,
  name TEXT,
  mime_type TEXT NOT NULL,
  size_bytes BIGINT NOT NULL DEFAULT 0,
  sha256 TEXT,
  storage_backend TEXT,
  storage_key TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_artifacts_run_created ON artifacts(run_id, created_at);
CREATE INDEX IF NOT EXISTS idx_artifacts_workspace ON artifacts(workspace_id);
CREATE INDEX IF NOT EXISTS idx_workspaces_run ON workspaces(run_id);
