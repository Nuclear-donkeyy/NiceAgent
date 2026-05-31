ALTER TABLE runs ADD COLUMN IF NOT EXISTS active_attempt_id TEXT;
ALTER TABLE runs ADD COLUMN IF NOT EXISTS claimed_by TEXT;
ALTER TABLE runs ADD COLUMN IF NOT EXISTS lease_expires_at TIMESTAMPTZ;
ALTER TABLE runs ADD COLUMN IF NOT EXISTS attempt_count INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_runs_active_attempt ON runs(active_attempt_id);
CREATE INDEX IF NOT EXISTS idx_runs_lease_expires ON runs(lease_expires_at);
