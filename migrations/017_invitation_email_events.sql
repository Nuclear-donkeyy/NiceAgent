CREATE TABLE IF NOT EXISTS invitation_email_events (
  id TEXT PRIMARY KEY,
  invitation_id TEXT NOT NULL REFERENCES invitations(id) ON DELETE CASCADE,
  delivery_id TEXT REFERENCES invitation_email_outbox(id) ON DELETE SET NULL,
  provider TEXT,
  provider_message_id TEXT,
  type TEXT NOT NULL,
  reason TEXT,
  payload JSONB NOT NULL DEFAULT '{}'::jsonb,
  occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_invitation_email_events_invitation
  ON invitation_email_events(invitation_id, occurred_at DESC);

CREATE INDEX IF NOT EXISTS idx_invitation_email_events_delivery
  ON invitation_email_events(delivery_id, occurred_at DESC);

CREATE INDEX IF NOT EXISTS idx_invitation_email_events_provider_message
  ON invitation_email_events(provider, provider_message_id)
  WHERE provider_message_id IS NOT NULL AND provider_message_id <> '';
