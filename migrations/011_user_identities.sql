CREATE TABLE IF NOT EXISTS user_identities (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id),
  provider TEXT NOT NULL,
  issuer TEXT NOT NULL,
  subject TEXT NOT NULL,
  email TEXT,
  name TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (provider, issuer, subject),
  UNIQUE (provider, user_id)
);

CREATE INDEX IF NOT EXISTS idx_user_identities_user ON user_identities(user_id);
