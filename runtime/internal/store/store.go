package store

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/quanyejun/quanyejun/runtime/internal/domain"
)

type Store interface {
	GetVisitorProfile(context.Context, string) (domain.VisitorProfile, error)
	SetVisitorProfile(context.Context, string, string) (domain.VisitorProfile, error)
	EnsureIdentity(context.Context) (domain.Identity, error)
	AddMessage(context.Context, domain.Message) error
	ListMessages(context.Context, string, int) ([]domain.Message, error)
	AddMemory(context.Context, domain.Memory) error
	ListMemories(context.Context, string, int) ([]domain.Memory, error)
	CreateTask(context.Context, domain.Task) error
	UpdateTask(context.Context, domain.Task) error
	ListTasks(context.Context, string, int) ([]domain.Task, error)
	CreateAgentRun(context.Context, domain.AgentRun) error
	UpdateAgentRun(context.Context, domain.AgentRun) error
	GetAgentRun(context.Context, string) (domain.AgentRun, error)
	ListAgentRuns(context.Context, string, int) ([]domain.AgentRun, error)
	ListAgentRunsByStatus(context.Context, []string, int) ([]domain.AgentRun, error)
	AppendRunStep(context.Context, domain.RunStep) error
	ListRunSteps(context.Context, string) ([]domain.RunStep, error)
	AppendEvent(context.Context, domain.Event) error
	ListEventsAfter(context.Context, string, string, int) ([]domain.Event, error)
	PurgeExpired(context.Context) error
	Close()
}

type MemoryStore struct {
	profiles map[string]domain.VisitorProfile
	mu       sync.RWMutex
	identity domain.Identity
	messages []domain.Message
	memories []domain.Memory
	tasks    []domain.Task
	runs     []domain.AgentRun
	steps    []domain.RunStep
	events   []domain.Event
}

func NewMemoryStore() *MemoryStore {
	now := time.Now().UTC()
	return &MemoryStore{
		identity: domain.Identity{ID: domain.IdentityID, Name: domain.DefaultDisplayName, Status: "active", CreatedAt: now},
		memories: []domain.Memory{{ID: uuid.NewString(), ActorID: "person_example", Content: "사용자가 명시적으로 제공한 선호만 기억한다.", CreatedAt: now}},
	}
}

func (s *MemoryStore) EnsureIdentity(context.Context) (domain.Identity, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.identity, nil
}
func (s *MemoryStore) AddMessage(ctx context.Context, message domain.Message) error {
	message.ExpiresAt = domain.Expiry(ctx, message.ActorID, message.CreatedAt, message.ExpiresAt)
	if domain.Expired(message.ExpiresAt) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, message)
	return nil
}
func (s *MemoryStore) ListMessages(_ context.Context, actorID string, limit int) ([]domain.Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	filtered := make([]domain.Message, 0, limit)
	for i := len(s.messages) - 1; i >= 0 && len(filtered) < limit; i-- {
		if s.messages[i].ActorID == actorID && !domain.Expired(s.messages[i].ExpiresAt) {
			filtered = append(filtered, s.messages[i])
		}
	}
	reverseMessages(filtered)
	return filtered, nil
}
func (s *MemoryStore) AddMemory(ctx context.Context, memory domain.Memory) error {
	memory.ExpiresAt = domain.Expiry(ctx, memory.ActorID, memory.CreatedAt, memory.ExpiresAt)
	if domain.Expired(memory.ExpiresAt) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.memories = append(s.memories, memory)
	return nil
}
func (s *MemoryStore) ListMemories(_ context.Context, actorID string, limit int) ([]domain.Memory, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	filtered := make([]domain.Memory, 0, limit)
	for i := len(s.memories) - 1; i >= 0 && len(filtered) < limit; i-- {
		if s.memories[i].ActorID == actorID && !domain.Expired(s.memories[i].ExpiresAt) {
			filtered = append(filtered, s.memories[i])
		}
	}
	for left, right := 0, len(filtered)-1; left < right; left, right = left+1, right-1 {
		filtered[left], filtered[right] = filtered[right], filtered[left]
	}
	return filtered, nil
}
func (s *MemoryStore) CreateTask(ctx context.Context, task domain.Task) error {
	task.ExpiresAt = domain.Expiry(ctx, task.ActorID, task.CreatedAt, task.ExpiresAt)
	if domain.Expired(task.ExpiresAt) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks = append(s.tasks, task)
	return nil
}
func (s *MemoryStore) UpdateTask(ctx context.Context, task domain.Task) error {
	task.ExpiresAt = domain.Expiry(ctx, task.ActorID, task.CreatedAt, task.ExpiresAt)
	if domain.Expired(task.ExpiresAt) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.tasks {
		if s.tasks[i].ID == task.ID {
			if domain.Expired(s.tasks[i].ExpiresAt) {
				return nil
			}
			task.ExpiresAt = domain.Earliest(task.ExpiresAt, s.tasks[i].ExpiresAt)
			s.tasks[i] = task
			return nil
		}
	}
	return fmt.Errorf("task not found")
}
func (s *MemoryStore) ListTasks(_ context.Context, actorID string, limit int) ([]domain.Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	filtered := make([]domain.Task, 0, limit)
	for i := len(s.tasks) - 1; i >= 0 && len(filtered) < limit; i-- {
		if s.tasks[i].ActorID == actorID && !domain.Expired(s.tasks[i].ExpiresAt) {
			filtered = append(filtered, s.tasks[i])
		}
	}
	for left, right := 0, len(filtered)-1; left < right; left, right = left+1, right-1 {
		filtered[left], filtered[right] = filtered[right], filtered[left]
	}
	return filtered, nil
}
func (s *MemoryStore) CreateAgentRun(ctx context.Context, run domain.AgentRun) error {
	run.ExpiresAt = domain.Expiry(ctx, run.ActorID, run.CreatedAt, run.ExpiresAt)
	if domain.Expired(run.ExpiresAt) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs = append(s.runs, run)
	return nil
}
func (s *MemoryStore) UpdateAgentRun(ctx context.Context, run domain.AgentRun) error {
	run.ExpiresAt = domain.Expiry(ctx, run.ActorID, run.CreatedAt, run.ExpiresAt)
	if domain.Expired(run.ExpiresAt) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.runs {
		if s.runs[i].ID == run.ID {
			if domain.Expired(s.runs[i].ExpiresAt) {
				return nil
			}
			run.ExpiresAt = domain.Earliest(run.ExpiresAt, s.runs[i].ExpiresAt)
			s.runs[i] = run
			return nil
		}
	}
	return fmt.Errorf("agent run %s not found", run.ID)
}
func (s *MemoryStore) GetAgentRun(_ context.Context, id string) (domain.AgentRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, run := range s.runs {
		if run.ID == id && !domain.Expired(run.ExpiresAt) {
			return run, nil
		}
	}
	return domain.AgentRun{}, fmt.Errorf("agent run %s not found", id)
}
func (s *MemoryStore) ListAgentRuns(_ context.Context, actorID string, limit int) ([]domain.AgentRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]domain.AgentRun, 0, limit)
	for i := len(s.runs) - 1; i >= 0 && len(result) < limit; i-- {
		if s.runs[i].ActorID == actorID && !domain.Expired(s.runs[i].ExpiresAt) {
			result = append(result, s.runs[i])
		}
	}
	reverseRuns(result)
	return result, nil
}
func (s *MemoryStore) ListAgentRunsByStatus(_ context.Context, statuses []string, limit int) ([]domain.AgentRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	wanted := make(map[string]struct{}, len(statuses))
	for _, status := range statuses {
		wanted[status] = struct{}{}
	}
	result := make([]domain.AgentRun, 0, limit)
	for _, run := range s.runs {
		if _, ok := wanted[run.Status]; ok && len(result) < limit && !domain.Expired(run.ExpiresAt) {
			result = append(result, run)
		}
	}
	return result, nil
}
func (s *MemoryStore) AppendRunStep(_ context.Context, step domain.RunStep) error {
	run, err := s.GetAgentRun(context.Background(), step.RunID)
	if err != nil {
		return err
	}
	step.ExpiresAt = domain.Earliest(step.ExpiresAt, run.ExpiresAt)
	if domain.Expired(step.ExpiresAt) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.steps = append(s.steps, step)
	return nil
}
func (s *MemoryStore) ListRunSteps(_ context.Context, runID string) ([]domain.RunStep, error) {
	if _, err := s.GetAgentRun(context.Background(), runID); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]domain.RunStep, 0)
	for _, step := range s.steps {
		if step.RunID == runID && !domain.Expired(step.ExpiresAt) {
			result = append(result, step)
		}
	}
	return result, nil
}
func (s *MemoryStore) AppendEvent(ctx context.Context, event domain.Event) error {
	event.ExpiresAt = domain.EventExpiry(ctx, event)
	if domain.Expired(event.ExpiresAt) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}
func (s *MemoryStore) ListEventsAfter(_ context.Context, actorID, after string, limit int) ([]domain.Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	filtered := make([]domain.Event, 0)
	for _, event := range s.events {
		if event.ActorID == actorID && !domain.Expired(event.ExpiresAt) {
			filtered = append(filtered, event)
		}
	}
	start := max(0, len(filtered)-limit)
	if after != "" {
		start = 0
		for i, event := range filtered {
			if event.ID == after {
				start = i + 1
				break
			}
		}
	}
	end := min(len(filtered), start+limit)
	return filtered[start:end], nil
}
func (s *MemoryStore) PurgeExpired(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for actor, profile := range s.profiles {
		if domain.Expired(profile.ExpiresAt) {
			delete(s.profiles, actor)
		}
	}
	s.messages = slices.DeleteFunc(s.messages, func(v domain.Message) bool { return domain.Expired(v.ExpiresAt) })
	s.memories = slices.DeleteFunc(s.memories, func(v domain.Memory) bool { return domain.Expired(v.ExpiresAt) })
	s.tasks = slices.DeleteFunc(s.tasks, func(v domain.Task) bool { return domain.Expired(v.ExpiresAt) })
	s.runs = slices.DeleteFunc(s.runs, func(v domain.AgentRun) bool { return domain.Expired(v.ExpiresAt) })
	liveRuns := make(map[string]bool, len(s.runs))
	for _, run := range s.runs {
		liveRuns[run.ID] = true
	}
	s.steps = slices.DeleteFunc(s.steps, func(v domain.RunStep) bool { return domain.Expired(v.ExpiresAt) || !liveRuns[v.RunID] })
	s.events = slices.DeleteFunc(s.events, func(v domain.Event) bool { return domain.Expired(v.ExpiresAt) })
	return nil
}
func (s *MemoryStore) Close() {}

func reverseMessages(items []domain.Message) {
	for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
		items[left], items[right] = items[right], items[left]
	}
}

func reverseRuns(items []domain.AgentRun) {
	for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
		items[left], items[right] = items[right], items[left]
	}
}
