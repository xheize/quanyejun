package store

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/quanyejun/quanyejun/runtime/internal/domain"
)

type Store interface {
	EnsureIdentity(context.Context) (domain.Identity, error)
	AddMessage(context.Context, domain.Message) error
	ListMessages(context.Context, string, int) ([]domain.Message, error)
	AddMemory(context.Context, domain.Memory) error
	ListMemories(context.Context, string, int) ([]domain.Memory, error)
	CreateTask(context.Context, domain.Task) error
	UpdateTask(context.Context, domain.Task) error
	ListTasks(context.Context, string, int) ([]domain.Task, error)
	AppendEvent(context.Context, domain.Event) error
	ListEventsAfter(context.Context, string, int) ([]domain.Event, error)
	Close()
}

type MemoryStore struct {
	mu       sync.RWMutex
	identity domain.Identity
	messages []domain.Message
	memories []domain.Memory
	tasks    []domain.Task
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
func (s *MemoryStore) AddMessage(_ context.Context, message domain.Message) error {
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
		if s.messages[i].ActorID == actorID {
			filtered = append(filtered, s.messages[i])
		}
	}
	reverseMessages(filtered)
	return filtered, nil
}
func (s *MemoryStore) AddMemory(_ context.Context, memory domain.Memory) error {
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
		if s.memories[i].ActorID == actorID {
			filtered = append(filtered, s.memories[i])
		}
	}
	for left, right := 0, len(filtered)-1; left < right; left, right = left+1, right-1 {
		filtered[left], filtered[right] = filtered[right], filtered[left]
	}
	return filtered, nil
}
func (s *MemoryStore) CreateTask(_ context.Context, task domain.Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks = append(s.tasks, task)
	return nil
}
func (s *MemoryStore) UpdateTask(_ context.Context, task domain.Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.tasks {
		if s.tasks[i].ID == task.ID {
			s.tasks[i] = task
			return nil
		}
	}
	s.tasks = append(s.tasks, task)
	return nil
}
func (s *MemoryStore) ListTasks(_ context.Context, actorID string, limit int) ([]domain.Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	filtered := make([]domain.Task, 0, limit)
	for i := len(s.tasks) - 1; i >= 0 && len(filtered) < limit; i-- {
		if s.tasks[i].ActorID == actorID {
			filtered = append(filtered, s.tasks[i])
		}
	}
	for left, right := 0, len(filtered)-1; left < right; left, right = left+1, right-1 {
		filtered[left], filtered[right] = filtered[right], filtered[left]
	}
	return filtered, nil
}
func (s *MemoryStore) AppendEvent(_ context.Context, event domain.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}
func (s *MemoryStore) ListEventsAfter(_ context.Context, after string, limit int) ([]domain.Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	start := len(s.events) - limit
	if start < 0 {
		start = 0
	}
	if after != "" {
		for i := range s.events {
			if s.events[i].ID == after {
				start = i + 1
				break
			}
		}
	}
	end := start + limit
	if end > len(s.events) {
		end = len(s.events)
	}
	result := make([]domain.Event, end-start)
	copy(result, s.events[start:end])
	return result, nil
}
func (s *MemoryStore) Close() {}

func reverseMessages(items []domain.Message) {
	for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
		items[left], items[right] = items[right], items[left]
	}
}
