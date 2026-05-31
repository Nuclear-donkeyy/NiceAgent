CREATE TABLE IF NOT EXISTS organization_members (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id),
  organization_id TEXT NOT NULL REFERENCES organizations(id),
  role TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (user_id, organization_id)
);

CREATE TABLE IF NOT EXISTS project_members (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id),
  project_id TEXT NOT NULL REFERENCES projects(id),
  role TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (user_id, project_id)
);

CREATE INDEX IF NOT EXISTS idx_project_members_project ON project_members(project_id);
CREATE INDEX IF NOT EXISTS idx_organization_members_organization ON organization_members(organization_id);

INSERT INTO organization_members (id, user_id, organization_id, role)
VALUES ('orgmem_demo_owner', 'demo-user', 'demo-org', 'owner')
ON CONFLICT (user_id, organization_id) DO UPDATE SET role = EXCLUDED.role, updated_at = now();

INSERT INTO project_members (id, user_id, project_id, role)
VALUES ('prjmem_demo_owner', 'demo-user', 'demo-project', 'owner')
ON CONFLICT (user_id, project_id) DO UPDATE SET role = EXCLUDED.role, updated_at = now();
