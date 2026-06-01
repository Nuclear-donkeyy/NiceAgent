ALTER TABLE skills ADD COLUMN IF NOT EXISTS slug TEXT;
ALTER TABLE skills ADD COLUMN IF NOT EXISTS scope TEXT;
ALTER TABLE skills ADD COLUMN IF NOT EXISTS kind TEXT;
ALTER TABLE skills ADD COLUMN IF NOT EXISTS owner_user_id TEXT REFERENCES users(id);
ALTER TABLE skills ADD COLUMN IF NOT EXISTS project_id TEXT REFERENCES projects(id);
ALTER TABLE skills ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'enabled';
ALTER TABLE skills ADD COLUMN IF NOT EXISTS current_version_id TEXT;
ALTER TABLE skills ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT now();
ALTER TABLE skills ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

UPDATE skills SET slug = id WHERE slug IS NULL;
UPDATE skills SET scope = 'system' WHERE scope IS NULL;
UPDATE skills SET kind = 'builtin' WHERE kind IS NULL;
UPDATE skills SET status = 'enabled' WHERE status IS NULL;

CREATE TABLE IF NOT EXISTS skill_versions (
  id TEXT PRIMARY KEY,
  skill_id TEXT NOT NULL REFERENCES skills(id),
  version TEXT NOT NULL,
  name TEXT NOT NULL,
  description TEXT NOT NULL,
  risk TEXT NOT NULL,
  input_schema JSONB,
  output_schema JSONB,
  annotations JSONB,
  runtime_config JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS skill_secrets (
  id TEXT PRIMARY KEY,
  skill_id TEXT NOT NULL REFERENCES skills(id),
  secret_key TEXT NOT NULL,
  secret_ref TEXT,
  encrypted_value TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (skill_id, secret_key)
);

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

UPDATE skills
SET current_version_id = CASE
  WHEN id = 'cli.exec' THEN 'skv_cli_exec_001'
  WHEN id = 'workspace.read' THEN 'skv_workspace_read_001'
  ELSE current_version_id
END
WHERE current_version_id IS NULL;

ALTER TABLE skill_grants ADD COLUMN IF NOT EXISTS enabled BOOLEAN NOT NULL DEFAULT true;
