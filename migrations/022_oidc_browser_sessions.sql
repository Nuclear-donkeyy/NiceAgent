CREATE TABLE IF NOT EXISTS oidc_browser_sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    project_id TEXT NOT NULL,
    refresh_token_hash TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_oidc_browser_sessions_user ON oidc_browser_sessions (user_id);
CREATE INDEX IF NOT EXISTS idx_oidc_browser_sessions_active ON oidc_browser_sessions (user_id, expires_at) WHERE revoked_at IS NULL;
