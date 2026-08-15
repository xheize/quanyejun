CREATE TABLE IF NOT EXISTS identities (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  status TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS messages (
  id TEXT PRIMARY KEY,
  actor_id TEXT NOT NULL,
  conversation_id TEXT NOT NULL,
  role TEXT NOT NULL CHECK (role IN ('user', 'assistant', 'system')),
  content TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS messages_actor_created_idx ON messages (actor_id, created_at DESC);

CREATE TABLE IF NOT EXISTS memories (
  id TEXT PRIMARY KEY,
  actor_id TEXT NOT NULL,
  content TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS memories_actor_created_idx ON memories (actor_id, created_at DESC);

CREATE TABLE IF NOT EXISTS tasks (
  id TEXT PRIMARY KEY,
  actor_id TEXT NOT NULL,
  title TEXT NOT NULL,
  type TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'completed', 'failed')),
  progress INTEGER NOT NULL CHECK (progress BETWEEN 0 AND 100),
  result TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS tasks_actor_updated_idx ON tasks (actor_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS events (
  seq BIGSERIAL PRIMARY KEY,
  id TEXT NOT NULL UNIQUE,
  type TEXT NOT NULL,
  source TEXT NOT NULL,
  actor_id TEXT NOT NULL DEFAULT '',
  conversation_id TEXT NOT NULL,
  timestamp TIMESTAMPTZ NOT NULL,
  payload JSONB NOT NULL
);
CREATE INDEX IF NOT EXISTS events_timestamp_idx ON events (timestamp DESC);
