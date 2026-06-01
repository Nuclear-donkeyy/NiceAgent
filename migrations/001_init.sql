CREATE TABLE users (
  id TEXT PRIMARY KEY,
  email TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  status TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE organizations (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE projects (
  id TEXT PRIMARY KEY,
  organization_id TEXT NOT NULL REFERENCES organizations(id),
  name TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE chat_sessions (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id),
  project_id TEXT NOT NULL REFERENCES projects(id),
  title TEXT NOT NULL,
  archived BOOLEAN NOT NULL DEFAULT false,
  last_run_id TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE messages (
  id TEXT PRIMARY KEY,
  chat_id TEXT NOT NULL REFERENCES chat_sessions(id),
  run_id TEXT,
  role TEXT NOT NULL,
  content TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE runs (
  id TEXT PRIMARY KEY,
  chat_id TEXT NOT NULL REFERENCES chat_sessions(id),
  user_id TEXT NOT NULL REFERENCES users(id),
  workspace_id TEXT NOT NULL,
  status TEXT NOT NULL,
  error TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  started_at TIMESTAMPTZ,
  finished_at TIMESTAMPTZ
);

CREATE TABLE run_events (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL REFERENCES runs(id),
  chat_id TEXT NOT NULL REFERENCES chat_sessions(id),
  seq BIGINT NOT NULL,
  type TEXT NOT NULL,
  message TEXT,
  payload JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (run_id, seq)
);

CREATE TABLE skills (
  id TEXT PRIMARY KEY,
  slug TEXT NOT NULL UNIQUE,
  scope TEXT NOT NULL,
  kind TEXT NOT NULL,
  owner_user_id TEXT REFERENCES users(id),
  project_id TEXT REFERENCES projects(id),
  status TEXT NOT NULL DEFAULT 'enabled',
  current_version_id TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE skill_versions (
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

CREATE TABLE skill_secrets (
  id TEXT PRIMARY KEY,
  skill_id TEXT NOT NULL REFERENCES skills(id),
  secret_key TEXT NOT NULL,
  secret_ref TEXT,
  encrypted_value TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (skill_id, secret_key)
);

CREATE TABLE workspaces (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id),
  root_path TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_chat_sessions_user_updated ON chat_sessions(user_id, updated_at DESC);
CREATE INDEX idx_messages_chat_created ON messages(chat_id, created_at);
CREATE INDEX idx_runs_chat_created ON runs(chat_id, created_at DESC);
CREATE INDEX idx_run_events_run_seq ON run_events(run_id, seq);

INSERT INTO users (id, email, name, status)
VALUES ('demo-user', 'demo@niceagent.local', 'Demo User', 'active')
ON CONFLICT (id) DO NOTHING;

INSERT INTO organizations (id, name)
VALUES ('demo-org', 'Demo Organization')
ON CONFLICT (id) DO NOTHING;

INSERT INTO projects (id, organization_id, name)
VALUES ('demo-project', 'demo-org', 'Demo Project')
ON CONFLICT (id) DO NOTHING;

INSERT INTO skills (id, slug, scope, kind, status, current_version_id)
VALUES
  ('cli.exec', 'cli.exec', 'system', 'builtin', 'enabled', 'skv_cli_exec_001'),
  ('workspace.read', 'workspace.read', 'system', 'builtin', 'enabled', 'skv_workspace_read_001')
ON CONFLICT (id) DO NOTHING;

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
