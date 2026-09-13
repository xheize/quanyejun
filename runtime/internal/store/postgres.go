package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanyejun/quanyejun/runtime/internal/domain"
)

type PostgresStore struct{ pool *pgxpool.Pool }

func NewPostgresStore(ctx context.Context, databaseURL string) (*PostgresStore, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("create postgres pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	store := &PostgresStore{pool: pool}
	if err := store.ensureSchema(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return store, nil
}

func (s *PostgresStore) ensureSchema(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS identities (id TEXT PRIMARY KEY, name TEXT NOT NULL, status TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS messages (id TEXT PRIMARY KEY, actor_id TEXT NOT NULL, conversation_id TEXT NOT NULL, role TEXT NOT NULL, content TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS messages_actor_created_idx ON messages (actor_id, created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS memories (id TEXT PRIMARY KEY, actor_id TEXT NOT NULL, content TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS memories_actor_created_idx ON memories (actor_id, created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS tasks (id TEXT PRIMARY KEY, actor_id TEXT NOT NULL, title TEXT NOT NULL, type TEXT NOT NULL, status TEXT NOT NULL, progress INTEGER NOT NULL, result TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS tasks_actor_updated_idx ON tasks (actor_id, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS agent_runs (id TEXT PRIMARY KEY, identity_id TEXT NOT NULL, actor_id TEXT NOT NULL, conversation_id TEXT NOT NULL, parent_run_id TEXT NOT NULL DEFAULT '', input_message_id TEXT NOT NULL DEFAULT '', kind TEXT NOT NULL, provider TEXT NOT NULL, model TEXT NOT NULL DEFAULT '', status TEXT NOT NULL, goal TEXT NOT NULL, result TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '', attempt INTEGER NOT NULL DEFAULT 0, created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL, started_at TIMESTAMPTZ NOT NULL, completed_at TIMESTAMPTZ NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS agent_runs_actor_updated_idx ON agent_runs (actor_id, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS agent_runs_status_created_idx ON agent_runs (status, created_at)`,
		`CREATE TABLE IF NOT EXISTS run_steps (id TEXT PRIMARY KEY, run_id TEXT NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE, sequence INTEGER NOT NULL, kind TEXT NOT NULL, status TEXT NOT NULL, provider_response_id TEXT NOT NULL DEFAULT '', call_id TEXT NOT NULL DEFAULT '', tool_name TEXT NOT NULL DEFAULT '', input JSONB NOT NULL DEFAULT 'null', output JSONB NOT NULL DEFAULT 'null', error TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL, completed_at TIMESTAMPTZ NOT NULL, UNIQUE(run_id, sequence))`,
		`CREATE INDEX IF NOT EXISTS run_steps_run_sequence_idx ON run_steps (run_id, sequence)`,
		`CREATE TABLE IF NOT EXISTS events (seq BIGSERIAL PRIMARY KEY, id TEXT NOT NULL UNIQUE, type TEXT NOT NULL, source TEXT NOT NULL, actor_id TEXT NOT NULL DEFAULT '', conversation_id TEXT NOT NULL, timestamp TIMESTAMPTZ NOT NULL, payload JSONB NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS events_timestamp_idx ON events (timestamp DESC)`,
		`ALTER TABLE messages ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ`,
		`CREATE INDEX IF NOT EXISTS messages_expires_idx ON messages (expires_at) WHERE expires_at IS NOT NULL`,
		`ALTER TABLE memories ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ`,
		`CREATE INDEX IF NOT EXISTS memories_expires_idx ON memories (expires_at) WHERE expires_at IS NOT NULL`,
		`ALTER TABLE tasks ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ`,
		`CREATE INDEX IF NOT EXISTS tasks_expires_idx ON tasks (expires_at) WHERE expires_at IS NOT NULL`,
		`ALTER TABLE agent_runs ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ`,
		`CREATE INDEX IF NOT EXISTS agent_runs_expires_idx ON agent_runs (expires_at) WHERE expires_at IS NOT NULL`,
		`ALTER TABLE run_steps ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ`,
		`CREATE INDEX IF NOT EXISTS run_steps_expires_idx ON run_steps (expires_at) WHERE expires_at IS NOT NULL`,
		`ALTER TABLE events ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ`,
		`CREATE INDEX IF NOT EXISTS events_expires_idx ON events (expires_at) WHERE expires_at IS NOT NULL`,
		`UPDATE messages SET expires_at=created_at + interval '72 hours' WHERE actor_id LIKE 'anon:%' AND expires_at IS NULL`,
		`UPDATE memories SET expires_at=created_at + interval '72 hours' WHERE actor_id LIKE 'anon:%' AND expires_at IS NULL`,
		`UPDATE tasks SET expires_at=created_at + interval '72 hours' WHERE actor_id LIKE 'anon:%' AND expires_at IS NULL`,
		`UPDATE agent_runs SET expires_at=created_at + interval '72 hours' WHERE actor_id LIKE 'anon:%' AND expires_at IS NULL`,
		`UPDATE events SET expires_at=timestamp + interval '72 hours' WHERE actor_id LIKE 'anon:%' AND expires_at IS NULL`,
		`UPDATE run_steps SET expires_at=r.expires_at FROM agent_runs r WHERE run_steps.run_id=r.id AND run_steps.expires_at IS NULL AND r.expires_at IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS events_actor_seq_idx ON events (actor_id,seq)`,
	}
	for _, statement := range statements {
		if _, err := s.pool.Exec(ctx, statement); err != nil {
			return fmt.Errorf("ensure schema: %w", err)
		}
	}
	return nil
}

func (s *PostgresStore) EnsureIdentity(ctx context.Context) (domain.Identity, error) {
	identity := domain.Identity{ID: domain.IdentityID, Name: domain.DefaultDisplayName, Status: "active", CreatedAt: time.Now().UTC()}
	_, err := s.pool.Exec(ctx, `INSERT INTO identities (id, name, status, created_at) VALUES ($1,$2,$3,$4) ON CONFLICT (id) DO NOTHING`, identity.ID, identity.Name, identity.Status, identity.CreatedAt)
	if err != nil {
		return domain.Identity{}, err
	}
	err = s.pool.QueryRow(ctx, `SELECT id, name, status, created_at FROM identities WHERE id=$1`, domain.IdentityID).Scan(&identity.ID, &identity.Name, &identity.Status, &identity.CreatedAt)
	return identity, err
}
func (s *PostgresStore) AddMessage(ctx context.Context, message domain.Message) error {
	message.ExpiresAt = domain.Expiry(ctx, message.ActorID, message.CreatedAt, message.ExpiresAt)
	if domain.Expired(message.ExpiresAt) {
		return nil
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO messages (id, actor_id, conversation_id, role, content, created_at,expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`, message.ID, message.ActorID, message.ConversationID, message.Role, message.Content, message.CreatedAt, message.ExpiresAt)
	return err
}
func (s *PostgresStore) ListMessages(ctx context.Context, actorID string, limit int) ([]domain.Message, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, actor_id, conversation_id, role, content, created_at,expires_at FROM (SELECT * FROM messages WHERE actor_id=$1 AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP) ORDER BY created_at DESC LIMIT $2) recent ORDER BY created_at`, actorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.Message, 0, limit)
	for rows.Next() {
		var item domain.Message
		if err := rows.Scan(&item.ID, &item.ActorID, &item.ConversationID, &item.Role, &item.Content, &item.CreatedAt, &item.ExpiresAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func (s *PostgresStore) AddMemory(ctx context.Context, memory domain.Memory) error {
	memory.ExpiresAt = domain.Expiry(ctx, memory.ActorID, memory.CreatedAt, memory.ExpiresAt)
	if domain.Expired(memory.ExpiresAt) {
		return nil
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO memories (id, actor_id, content, created_at,expires_at) VALUES ($1,$2,$3,$4,$5)`, memory.ID, memory.ActorID, memory.Content, memory.CreatedAt, memory.ExpiresAt)
	return err
}
func (s *PostgresStore) ListMemories(ctx context.Context, actorID string, limit int) ([]domain.Memory, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, actor_id, content, created_at,expires_at FROM (SELECT * FROM memories WHERE actor_id=$1 AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP) ORDER BY created_at DESC LIMIT $2) recent ORDER BY created_at`, actorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.Memory, 0, limit)
	for rows.Next() {
		var item domain.Memory
		if err := rows.Scan(&item.ID, &item.ActorID, &item.Content, &item.CreatedAt, &item.ExpiresAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func (s *PostgresStore) CreateTask(ctx context.Context, task domain.Task) error {
	task.ExpiresAt = domain.Expiry(ctx, task.ActorID, task.CreatedAt, task.ExpiresAt)
	if domain.Expired(task.ExpiresAt) {
		return nil
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO tasks (id,actor_id,title,type,status,progress,result,created_at,updated_at,expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, task.ID, task.ActorID, task.Title, task.Type, task.Status, task.Progress, task.Result, task.CreatedAt, task.UpdatedAt, task.ExpiresAt)
	return err
}
func (s *PostgresStore) UpdateTask(ctx context.Context, task domain.Task) error {
	task.ExpiresAt = domain.Expiry(ctx, task.ActorID, task.CreatedAt, task.ExpiresAt)
	if domain.Expired(task.ExpiresAt) {
		return nil
	}
	_, err := s.pool.Exec(ctx, `UPDATE tasks SET status=$2, progress=$3, result=$4, updated_at=$5,expires_at=LEAST(expires_at,$6) WHERE id=$1 AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)`, task.ID, task.Status, task.Progress, task.Result, task.UpdatedAt, task.ExpiresAt)
	return err
}
func (s *PostgresStore) ListTasks(ctx context.Context, actorID string, limit int) ([]domain.Task, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,actor_id,title,type,status,progress,result,created_at,updated_at,expires_at FROM (SELECT * FROM tasks WHERE actor_id=$1 AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP) ORDER BY updated_at DESC LIMIT $2) recent ORDER BY updated_at`, actorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.Task, 0, limit)
	for rows.Next() {
		var item domain.Task
		if err := rows.Scan(&item.ID, &item.ActorID, &item.Title, &item.Type, &item.Status, &item.Progress, &item.Result, &item.CreatedAt, &item.UpdatedAt, &item.ExpiresAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func (s *PostgresStore) CreateAgentRun(ctx context.Context, run domain.AgentRun) error {
	run.ExpiresAt = domain.Expiry(ctx, run.ActorID, run.CreatedAt, run.ExpiresAt)
	if domain.Expired(run.ExpiresAt) {
		return nil
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO agent_runs (id,identity_id,actor_id,conversation_id,parent_run_id,input_message_id,kind,provider,model,status,goal,result,error,attempt,created_at,updated_at,started_at,completed_at,expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`, run.ID, run.IdentityID, run.ActorID, run.ConversationID, run.ParentRunID, run.InputMessageID, run.Kind, run.Provider, run.Model, run.Status, run.Goal, run.Result, run.Error, run.Attempt, run.CreatedAt, run.UpdatedAt, run.StartedAt, run.CompletedAt, run.ExpiresAt)
	return err
}
func (s *PostgresStore) UpdateAgentRun(ctx context.Context, run domain.AgentRun) error {
	run.ExpiresAt = domain.Expiry(ctx, run.ActorID, run.CreatedAt, run.ExpiresAt)
	if domain.Expired(run.ExpiresAt) {
		return nil
	}
	_, err := s.pool.Exec(ctx, `UPDATE agent_runs SET status=$2,result=$3,error=$4,attempt=$5,updated_at=$6,started_at=$7,completed_at=$8,expires_at=LEAST(expires_at,$9) WHERE id=$1 AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)`, run.ID, run.Status, run.Result, run.Error, run.Attempt, run.UpdatedAt, run.StartedAt, run.CompletedAt, run.ExpiresAt)
	return err
}
func (s *PostgresStore) GetAgentRun(ctx context.Context, id string) (domain.AgentRun, error) {
	var run domain.AgentRun
	err := s.pool.QueryRow(ctx, `SELECT id,identity_id,actor_id,conversation_id,parent_run_id,input_message_id,kind,provider,model,status,goal,result,error,attempt,created_at,updated_at,started_at,completed_at,expires_at FROM agent_runs WHERE id=$1 AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)`, id).Scan(&run.ID, &run.IdentityID, &run.ActorID, &run.ConversationID, &run.ParentRunID, &run.InputMessageID, &run.Kind, &run.Provider, &run.Model, &run.Status, &run.Goal, &run.Result, &run.Error, &run.Attempt, &run.CreatedAt, &run.UpdatedAt, &run.StartedAt, &run.CompletedAt, &run.ExpiresAt)
	return run, err
}
func (s *PostgresStore) ListAgentRuns(ctx context.Context, actorID string, limit int) ([]domain.AgentRun, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,identity_id,actor_id,conversation_id,parent_run_id,input_message_id,kind,provider,model,status,goal,result,error,attempt,created_at,updated_at,started_at,completed_at,expires_at FROM (SELECT * FROM agent_runs WHERE actor_id=$1 AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP) ORDER BY updated_at DESC LIMIT $2) recent ORDER BY updated_at`, actorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAgentRuns(rows, limit)
}
func (s *PostgresStore) ListAgentRunsByStatus(ctx context.Context, statuses []string, limit int) ([]domain.AgentRun, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,identity_id,actor_id,conversation_id,parent_run_id,input_message_id,kind,provider,model,status,goal,result,error,attempt,created_at,updated_at,started_at,completed_at,expires_at FROM agent_runs WHERE status = ANY($1) AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP) ORDER BY created_at LIMIT $2`, statuses, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAgentRuns(rows, limit)
}
func (s *PostgresStore) AppendRunStep(ctx context.Context, step domain.RunStep) error {
	run, err := s.GetAgentRun(ctx, step.RunID)
	if err != nil {
		return err
	}
	step.ExpiresAt = domain.Earliest(step.ExpiresAt, run.ExpiresAt)
	if domain.Expired(step.ExpiresAt) {
		return nil
	}
	input, output := rawOrNull(step.Input), rawOrNull(step.Output)
	_, err = s.pool.Exec(ctx, `INSERT INTO run_steps (id,run_id,sequence,kind,status,provider_response_id,call_id,tool_name,input,output,error,created_at,completed_at,expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, step.ID, step.RunID, step.Sequence, step.Kind, step.Status, step.ProviderResponseID, step.CallID, step.ToolName, input, output, step.Error, step.CreatedAt, step.CompletedAt, step.ExpiresAt)
	return err
}
func (s *PostgresStore) ListRunSteps(ctx context.Context, runID string) ([]domain.RunStep, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,run_id,sequence,kind,status,provider_response_id,call_id,tool_name,input,output,error,created_at,completed_at,expires_at FROM run_steps WHERE run_id=$1 AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP) AND EXISTS (SELECT 1 FROM agent_runs r WHERE r.id=run_id AND (r.expires_at IS NULL OR r.expires_at > CURRENT_TIMESTAMP)) ORDER BY sequence`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	steps := make([]domain.RunStep, 0)
	for rows.Next() {
		var step domain.RunStep
		if err := rows.Scan(&step.ID, &step.RunID, &step.Sequence, &step.Kind, &step.Status, &step.ProviderResponseID, &step.CallID, &step.ToolName, &step.Input, &step.Output, &step.Error, &step.CreatedAt, &step.CompletedAt, &step.ExpiresAt); err != nil {
			return nil, err
		}
		steps = append(steps, step)
	}
	return steps, rows.Err()
}
func (s *PostgresStore) AppendEvent(ctx context.Context, event domain.Event) error {
	event.ExpiresAt = domain.EventExpiry(ctx, event)
	if domain.Expired(event.ExpiresAt) {
		return nil
	}
	payload, err := json.Marshal(event.Payload)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO events (id,type,source,actor_id,conversation_id,timestamp,payload,expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, event.ID, event.Type, event.Source, event.ActorID, event.ConversationID, event.Timestamp, payload, event.ExpiresAt)
	return err
}
func (s *PostgresStore) ListEventsAfter(ctx context.Context, actorID, after string, limit int) ([]domain.Event, error) {
	query := `SELECT id,type,source,actor_id,conversation_id,timestamp,payload,expires_at FROM events WHERE actor_id=$1 AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP) ORDER BY seq DESC LIMIT $2`
	args := []any{actorID, limit}
	if after != "" {
		query = `SELECT id,type,source,actor_id,conversation_id,timestamp,payload,expires_at FROM events WHERE actor_id=$1 AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP) AND seq > COALESCE((SELECT seq FROM events WHERE id=$2 AND actor_id=$1),0) ORDER BY seq LIMIT $3`
		args = []any{actorID, after, limit}
	}
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.Event, 0, limit)
	for rows.Next() {
		var item domain.Event
		var payload []byte
		if err := rows.Scan(&item.ID, &item.Type, &item.Source, &item.ActorID, &item.ConversationID, &item.Timestamp, &payload, &item.ExpiresAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(payload, &item.Payload); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if after == "" {
		for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
			items[left], items[right] = items[right], items[left]
		}
	}
	return items, rows.Err()
}
func (s *PostgresStore) Close() { s.pool.Close() }

type agentRunRows interface {
	Next() bool
	Scan(...any) error
	Err() error
}

func scanAgentRuns(rows agentRunRows, capacity int) ([]domain.AgentRun, error) {
	items := make([]domain.AgentRun, 0, capacity)
	for rows.Next() {
		var run domain.AgentRun
		if err := rows.Scan(&run.ID, &run.IdentityID, &run.ActorID, &run.ConversationID, &run.ParentRunID, &run.InputMessageID, &run.Kind, &run.Provider, &run.Model, &run.Status, &run.Goal, &run.Result, &run.Error, &run.Attempt, &run.CreatedAt, &run.UpdatedAt, &run.StartedAt, &run.CompletedAt, &run.ExpiresAt); err != nil {
			return nil, err
		}
		items = append(items, run)
	}
	return items, rows.Err()
}

func rawOrNull(value json.RawMessage) []byte {
	if len(value) == 0 {
		return []byte("null")
	}
	return value
}

func (s *PostgresStore) PurgeExpired(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, table := range []string{"events", "run_steps", "agent_runs", "tasks", "memories", "messages"} {
		if _, err := tx.Exec(ctx, "DELETE FROM "+table+" WHERE expires_at <= CURRENT_TIMESTAMP"); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
