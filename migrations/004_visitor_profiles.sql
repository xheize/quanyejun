CREATE TABLE IF NOT EXISTS visitor_profiles (actor_id TEXT PRIMARY KEY, display_name TEXT NOT NULL, expires_at TIMESTAMPTZ);
CREATE INDEX IF NOT EXISTS visitor_profiles_expires_idx ON visitor_profiles (expires_at) WHERE expires_at IS NOT NULL;
ALTER TABLE agent_runs ADD COLUMN IF NOT EXISTS visitor JSONB NOT NULL DEFAULT '{}';
