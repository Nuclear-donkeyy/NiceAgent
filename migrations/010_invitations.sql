CREATE TABLE IF NOT EXISTS invitations (
  id TEXT PRIMARY KEY,
  token TEXT NOT NULL UNIQUE,
  organization_id TEXT NOT NULL REFERENCES organizations(id),
  project_id TEXT REFERENCES projects(id),
  email TEXT NOT NULL,
  role TEXT NOT NULL,
  status TEXT NOT NULL,
  invited_by_user_id TEXT NOT NULL REFERENCES users(id),
  accepted_by_user_id TEXT REFERENCES users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ NOT NULL,
  accepted_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_invitations_organization ON invitations(organization_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_invitations_token ON invitations(token);
