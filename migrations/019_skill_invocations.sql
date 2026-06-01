CREATE TABLE IF NOT EXISTS skill_invocations (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  chat_id TEXT NOT NULL REFERENCES chat_sessions(id) ON DELETE CASCADE,
  user_id TEXT NOT NULL REFERENCES users(id),
  project_id TEXT NOT NULL REFERENCES projects(id),
  skill_id TEXT NOT NULL,
  tool_name TEXT,
  status TEXT NOT NULL,
  decision TEXT NOT NULL,
  reason TEXT,
  request_id TEXT,
  trace_id TEXT,
  started_event_id TEXT,
  started_event_seq BIGINT,
  finished_event_id TEXT,
  finished_event_seq BIGINT,
  duration_ms BIGINT NOT NULL DEFAULT 0,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  finished_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_skill_invocations_actor_started ON skill_invocations(user_id, project_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_skill_invocations_run_started ON skill_invocations(run_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_skill_invocations_skill_started ON skill_invocations(skill_id, started_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_skill_invocations_started_event ON skill_invocations(started_event_id) WHERE started_event_id IS NOT NULL;
