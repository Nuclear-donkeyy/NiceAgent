ALTER TABLE project_quota_policies ADD COLUMN IF NOT EXISTS max_tool_calls_per_day INTEGER NOT NULL DEFAULT 0 CHECK (max_tool_calls_per_day >= 0);
ALTER TABLE project_quota_policies ADD COLUMN IF NOT EXISTS max_sandbox_seconds_per_day INTEGER NOT NULL DEFAULT 0 CHECK (max_sandbox_seconds_per_day >= 0);
