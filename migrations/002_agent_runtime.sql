CREATE TABLE IF NOT EXISTS agent_runs (
  id TEXT PRIMARY KEY,
  identity_id TEXT NOT NULL,
  actor_id TEXT NOT NULL,
  conversation_id TEXT NOT NULL,
  parent_run_id TEXT NOT NULL DEFAULT '',
  input_message_id TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL CHECK (kind IN ('parent', 'worker')),
  provider TEXT NOT NULL,
  model TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'waiting_approval', 'completed', 'failed', 'cancelled')),
  goal TEXT NOT NULL,
  result TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '',
  attempt INTEGER NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  started_at TIMESTAMPTZ NOT NULL,
  completed_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS agent_runs_actor_updated_idx ON agent_runs (actor_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS agent_runs_status_created_idx ON agent_runs (status, created_at);

CREATE TABLE IF NOT EXISTS run_steps (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
  sequence INTEGER NOT NULL,
  kind TEXT NOT NULL,
  status TEXT NOT NULL,
  provider_response_id TEXT NOT NULL DEFAULT '',
  call_id TEXT NOT NULL DEFAULT '',
  tool_name TEXT NOT NULL DEFAULT '',
  input JSONB NOT NULL DEFAULT 'null',
  output JSONB NOT NULL DEFAULT 'null',
  error TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL,
  completed_at TIMESTAMPTZ NOT NULL,
  UNIQUE (run_id, sequence)
);
CREATE INDEX IF NOT EXISTS run_steps_run_sequence_idx ON run_steps (run_id, sequence);
