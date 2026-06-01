CREATE TABLE IF NOT EXISTS skill_grants (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id),
  project_id TEXT REFERENCES projects(id),
  skill_id TEXT NOT NULL REFERENCES skills(id),
  enabled BOOLEAN NOT NULL DEFAULT true,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (user_id, project_id, skill_id)
);

UPDATE skills
SET slug = 'cli.exec',
    scope = 'system',
    kind = 'builtin',
    status = 'enabled',
    current_version_id = 'skv_cli_exec_001'
WHERE id = 'cli.exec';

UPDATE skills
SET slug = 'workspace.read',
    scope = 'system',
    kind = 'builtin',
    status = 'enabled',
    current_version_id = 'skv_workspace_read_001'
WHERE id = 'workspace.read';

INSERT INTO skill_versions (id, skill_id, version, name, description, risk, input_schema, annotations, runtime_config)
VALUES
  (
    'skv_cli_exec_001',
    'cli.exec',
    '0.1.0',
    'System CLI',
    'Fetch external information through a read-only sandboxed CLI.',
    'medium',
    '{"type":"object","required":["command"],"properties":{"command":{"type":"array","items":{"type":"string"}}}}',
    '{"readOnlyHint":true,"destructiveHint":false,"idempotentHint":false,"openWorldHint":true}',
    '{"type":"builtin","executor":"sandbox"}'
  ),
  (
    'skv_workspace_read_001',
    'workspace.read',
    '0.1.0',
    'Workspace Reader',
    'Inspect files and artifacts attached to a run workspace.',
    'low',
    '{"type":"object","properties":{"action":{"type":"string","enum":["summary","list","read"],"description":"summary returns workspace artifact metadata; list returns artifacts; read returns text artifact content"},"artifact_id":{"type":"string"},"max_bytes":{"type":"integer","minimum":1,"maximum":262144}}}',
    '{"readOnlyHint":true,"destructiveHint":false,"idempotentHint":true,"openWorldHint":false}',
    '{"type":"builtin"}'
  )
ON CONFLICT (id) DO NOTHING;

INSERT INTO skill_grants (id, user_id, project_id, skill_id)
VALUES
  ('grant_demo_cli_exec', 'demo-user', 'demo-project', 'cli.exec'),
  ('grant_demo_workspace_read', 'demo-user', 'demo-project', 'workspace.read')
ON CONFLICT (user_id, project_id, skill_id) DO NOTHING;
