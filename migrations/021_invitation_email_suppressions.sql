CREATE TABLE IF NOT EXISTS invitation_email_suppressions (
  id TEXT PRIMARY KEY,
  organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  email TEXT NOT NULL,
  reason TEXT NOT NULL,
  source_event_id TEXT REFERENCES invitation_email_events(id) ON DELETE SET NULL,
  provider TEXT,
  provider_message_id TEXT,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  UNIQUE (organization_id, email)
);

CREATE INDEX IF NOT EXISTS idx_invitation_email_suppressions_org_created
  ON invitation_email_suppressions(organization_id, created_at DESC);
