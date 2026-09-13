package store

import (
	"context"
	"testing"
	"time"

	"github.com/quanyejun/quanyejun/runtime/internal/domain"
)

func TestRetentionIsPerRecordAndDoesNotSlide(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	now := time.Now().UTC()
	old := now.Add(-71 * time.Hour)
	expected := old.Add(72 * time.Hour)
	if err := s.AddMessage(ctx, domain.Message{ID: "old", ActorID: "anon:a", CreatedAt: old}); err != nil {
		t.Fatal(err)
	}
	_ = s.AddMessage(ctx, domain.Message{ID: "new", ActorID: "anon:a", CreatedAt: now})
	_ = s.AddMessage(ctx, domain.Message{ID: "member", ActorID: "sso:member", CreatedAt: old.Add(-100 * time.Hour)})
	items, _ := s.ListMessages(ctx, "anon:a", 10)
	if len(items) != 2 || !items[0].ExpiresAt.Equal(expected) || !items[1].ExpiresAt.Equal(now.Add(72*time.Hour)) {
		t.Fatal("expiry slid", items)
	}
	members, _ := s.ListMessages(ctx, "sso:member", 10)
	if len(members) != 1 || members[0].ExpiresAt != nil {
		t.Fatal("authenticated data expired")
	}
	past := now.Add(-time.Second)
	s.mu.Lock()
	s.messages[0].ExpiresAt = &past
	s.mu.Unlock()
	items, _ = s.ListMessages(ctx, "anon:a", 10)
	if len(items) != 1 || items[0].ID != "new" {
		t.Fatal("expired message returned before cleanup")
	}
	if err := s.PurgeExpired(ctx); err != nil {
		t.Fatal(err)
	}
	if len(s.messages) != 2 {
		t.Fatal("physical cleanup failed")
	}
}

func TestDerivedDataAndCopiesInheritEarliestExpiry(t *testing.T) {
	s := NewMemoryStore()
	now := time.Now().UTC()
	expiry := now.Add(time.Hour)
	ctx := domain.WithExpiry(context.Background(), &expiry)
	_ = s.AddMemory(ctx, domain.Memory{ID: "memory", ActorID: "anon:a", Content: "derived", CreatedAt: now})
	_ = s.CreateAgentRun(ctx, domain.AgentRun{ID: "run", ActorID: "anon:a", CreatedAt: now, Status: "running"})
	_ = s.AppendRunStep(context.Background(), domain.RunStep{ID: "step", RunID: "run", CreatedAt: now})
	_ = s.CreateTask(ctx, domain.Task{ID: "task", ActorID: "anon:a", CreatedAt: now})
	_ = s.UpdateTask(context.Background(), domain.Task{ID: "task", ActorID: "anon:a", CreatedAt: now, UpdatedAt: now.Add(time.Minute)})
	memories, _ := s.ListMemories(ctx, "anon:a", 10)
	run, _ := s.GetAgentRun(ctx, "run")
	steps, _ := s.ListRunSteps(ctx, "run")
	tasks, _ := s.ListTasks(ctx, "anon:a", 10)
	for _, got := range []*time.Time{memories[0].ExpiresAt, run.ExpiresAt, steps[0].ExpiresAt, tasks[0].ExpiresAt} {
		if got == nil || !got.Equal(expiry) {
			t.Fatal("derived data extended lifetime", got)
		}
	}
	event := domain.Event{ID: "copy", ActorID: "anon:a", Timestamp: now, Payload: map[string]any{"memory": memories[0]}}
	_ = s.AppendEvent(context.Background(), event)
	events, _ := s.ListEventsAfter(ctx, "anon:a", "", 10)
	if !events[0].ExpiresAt.Equal(expiry) {
		t.Fatal("event copy outlives source")
	}
	past := now.Add(-time.Second)
	s.mu.Lock()
	s.memories[len(s.memories)-1].ExpiresAt = &past
	s.runs[0].ExpiresAt = &past
	s.steps[0].ExpiresAt = &past
	s.tasks[0].ExpiresAt = &past
	s.events[0].ExpiresAt = &past
	s.mu.Unlock()
	if _, err := s.GetAgentRun(ctx, "run"); err == nil {
		t.Fatal("expired run returned")
	}
	if rows, _ := s.ListMemories(ctx, "anon:a", 10); len(rows) != 0 {
		t.Fatal("expired memory returned")
	}
	if rows, _ := s.ListEventsAfter(ctx, "anon:a", "", 10); len(rows) != 0 {
		t.Fatal("expired event returned")
	}
	if err := s.PurgeExpired(ctx); err != nil {
		t.Fatal(err)
	}
	if len(s.runs)+len(s.steps)+len(s.tasks)+len(s.events) != 0 {
		t.Fatal("derived rows were not deleted")
	}
}
