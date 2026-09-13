package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/quanyejun/quanyejun/runtime/internal/domain"
)

// Run only against a disposable database explicitly selected by the test runner.
func TestPostgresRetentionAndIsolation(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	s, err := NewPostgresStore(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	expiry := now.Add(time.Hour)
	actor := "anon:" + uuid.NewString()
	ctx = domain.WithExpiry(ctx, &expiry)
	message := domain.Message{ID: uuid.NewString(), ActorID: actor, ConversationID: domain.ConversationID, Role: "user", Content: "private", CreatedAt: now}
	if err = s.AddMessage(ctx, message); err != nil {
		t.Fatal(err)
	}
	if err = s.AddMemory(ctx, domain.Memory{ID: uuid.NewString(), ActorID: actor, Content: "derived", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	task := domain.Task{ID: uuid.NewString(), ActorID: actor, Title: "test", Type: "demo", Status: "queued", CreatedAt: now, UpdatedAt: now}
	if err = s.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	task.Status = "completed"
	if err = s.UpdateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	run := domain.AgentRun{ID: uuid.NewString(), ActorID: actor, IdentityID: domain.IdentityID, ConversationID: domain.ConversationID, Kind: "parent", Status: "queued", CreatedAt: now, UpdatedAt: now}
	if err = s.CreateAgentRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	run.Status = "completed"
	if err = s.UpdateAgentRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if err = s.AppendRunStep(ctx, domain.RunStep{ID: uuid.NewString(), RunID: run.ID, Sequence: 1, Kind: "tool_result", Status: "completed", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err = s.AppendEvent(ctx, domain.Event{ID: uuid.NewString(), ActorID: actor, ConversationID: domain.ConversationID, Type: "test", Timestamp: now, Payload: map[string]any{"private": "data"}}); err != nil {
		t.Fatal(err)
	}
	ms, err := s.ListMessages(ctx, actor, 10)
	if err != nil || len(ms) != 1 || ms[0].ExpiresAt == nil {
		t.Fatal(ms, err)
	}
	mem, err := s.ListMemories(ctx, actor, 10)
	if err != nil || len(mem) != 1 || mem[0].ExpiresAt == nil {
		t.Fatal(mem, err)
	}
	ts, err := s.ListTasks(ctx, actor, 10)
	if err != nil || len(ts) != 1 || ts[0].ExpiresAt.Sub(expiry) > time.Microsecond {
		t.Fatal(ts, err)
	}
	rs, err := s.ListAgentRuns(ctx, actor, 10)
	if err != nil || len(rs) != 1 || rs[0].ExpiresAt.Sub(expiry) > time.Microsecond {
		t.Fatal(rs, err)
	}
	steps, err := s.ListRunSteps(ctx, run.ID)
	if err != nil || len(steps) != 1 || steps[0].ExpiresAt == nil {
		t.Fatal(steps, err)
	}
	events, err := s.ListEventsAfter(ctx, actor, "", 10)
	if err != nil || len(events) != 1 {
		t.Fatal(events, err)
	}
	foreign, err := s.ListEventsAfter(ctx, "anon:other", events[0].ID, 10)
	if err != nil || len(foreign) != 0 {
		t.Fatal("events leaked", foreign, err)
	}
	// Simulate the passage of 72h in SQL; reads must hide data before the sweeper runs.
	for _, table := range []string{"messages", "memories", "tasks", "agent_runs", "events"} {
		if _, err = s.pool.Exec(ctx, "UPDATE "+table+" SET expires_at=CURRENT_TIMESTAMP-interval '1 second' WHERE actor_id=$1", actor); err != nil {
			t.Fatal(err)
		}
	}
	if rows, err := s.ListMessages(ctx, actor, 10); err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
	if rows, err := s.ListMemories(ctx, actor, 10); err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
	if rows, err := s.ListTasks(ctx, actor, 10); err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
	if _, err := s.GetAgentRun(ctx, run.ID); err == nil {
		t.Fatal("expired run returned")
	}
	if rows, err := s.ListRunSteps(ctx, run.ID); err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
	if rows, err := s.ListEventsAfter(ctx, actor, "", 10); err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
	if err = s.PurgeExpired(ctx); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"messages", "memories", "tasks", "agent_runs", "events"} {
		var count int
		if err = s.pool.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE actor_id=$1", actor).Scan(&count); err != nil || count != 0 {
			t.Fatal("not deleted", table, count, err)
		}
	}
	var count int
	if err = s.pool.QueryRow(ctx, "SELECT count(*) FROM run_steps WHERE run_id=$1", run.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("steps not cascaded", count, err)
	}
}
