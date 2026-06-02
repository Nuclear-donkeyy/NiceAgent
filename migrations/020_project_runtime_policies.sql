CREATE TABLE IF NOT EXISTS project_runtime_policies (
  project_id TEXT PRIMARY KEY REFERENCES projects(id),
  skill_risk_policy TEXT NOT NULL DEFAULT 'allow' CHECK (skill_risk_policy IN ('allow', 'block-high', 'block-destructive', 'read-only')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
