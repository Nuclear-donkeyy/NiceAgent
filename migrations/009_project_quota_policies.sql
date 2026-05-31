CREATE TABLE IF NOT EXISTS project_quota_policies (
  project_id TEXT PRIMARY KEY REFERENCES projects(id),
  max_concurrent_runs INTEGER NOT NULL DEFAULT 0 CHECK (max_concurrent_runs >= 0),
  max_runs_per_hour INTEGER NOT NULL DEFAULT 0 CHECK (max_runs_per_hour >= 0),
  max_model_tokens_per_day INTEGER NOT NULL DEFAULT 0 CHECK (max_model_tokens_per_day >= 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
