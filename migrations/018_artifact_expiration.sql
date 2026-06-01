ALTER TABLE artifacts
  ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_artifacts_expiration
  ON artifacts (expires_at)
  WHERE deleted_at IS NULL AND expires_at IS NOT NULL;
