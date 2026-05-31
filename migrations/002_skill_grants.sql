CREATE TABLE IF NOT EXISTS skill_grants (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id),
  project_id TEXT REFERENCES projects(id),
  skill_id TEXT NOT NULL REFERENCES skills(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (user_id, project_id, skill_id)
);

UPDATE skills
SET
  name = 'System CLI',
  description = 'Fetch external information through a read-only sandboxed CLI.',
  risk = 'medium',
  requires_auth = false
WHERE id = 'cli.exec';

INSERT INTO skill_grants (id, user_id, project_id, skill_id)
VALUES
  ('grant_demo_cli_exec', 'demo-user', 'demo-project', 'cli.exec'),
  ('grant_demo_workspace_read', 'demo-user', 'demo-project', 'workspace.read')
ON CONFLICT (user_id, project_id, skill_id) DO NOTHING;
