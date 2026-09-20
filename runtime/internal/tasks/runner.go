package tasks

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/quanyejun/quanyejun/runtime/internal/domain"
	"github.com/quanyejun/quanyejun/runtime/internal/eventhub"
	"github.com/quanyejun/quanyejun/runtime/internal/store"
)

type Runner struct {
	slots chan struct{}
	store store.Store
	hub   *eventhub.Hub
}

func NewRunner(store store.Store, hub *eventhub.Hub) *Runner {
	return &Runner{slots: make(chan struct{}, 32), store: store, hub: hub}
}

var ErrCapacity = errors.New("task concurrency limit reached")

func (r *Runner) StartDemo(actorID string, duration time.Duration) (domain.Task, error) {
	select {
	case r.slots <- struct{}{}:
	default:
		return domain.Task{}, ErrCapacity
	}
	launched := false
	defer func() {
		if !launched {
			<-r.slots
		}
	}()
	if duration < 4*time.Second {
		duration = 4 * time.Second
	}
	now := time.Now().UTC()
	task := domain.Task{ID: uuid.NewString(), ActorID: actorID, Title: "연결 독립성 확인", Type: "demo.continuity", Status: "queued", Progress: 0, CreatedAt: now, UpdatedAt: now, ExpiresAt: domain.Expiry(context.Background(), actorID, now, nil)}
	if err := r.store.CreateTask(context.Background(), task); err != nil {
		return domain.Task{}, err
	}
	if err := r.emit("task.created", actorID, map[string]any{"task": task}); err != nil {
		return domain.Task{}, err
	}
	go func() { defer func() { <-r.slots }(); r.execute(task, duration) }()
	launched = true
	return task, nil
}

func (r *Runner) execute(task domain.Task, duration time.Duration) {
	steps := []int{18, 43, 68, 87, 100}
	interval := duration / time.Duration(len(steps))
	task.Status = "running"
	for _, progress := range steps {
		time.Sleep(interval)
		task.Progress = progress
		task.UpdatedAt = time.Now().UTC()
		eventType := "task.progress"
		if progress == 100 {
			task.Status = "completed"
			task.Result = "연결과 독립적으로 작업을 완료했습니다."
			eventType = "task.completed"
		}
		if err := r.store.UpdateTask(context.Background(), task); err != nil {
			r.fail(task, err)
			return
		}
		if err := r.emit(eventType, task.ActorID, map[string]any{"task": task}); err != nil {
			r.fail(task, err)
			return
		}
	}
}

func (r *Runner) fail(task domain.Task, cause error) {
	task.Status = "failed"
	task.Result = cause.Error()
	task.UpdatedAt = time.Now().UTC()
	_ = r.store.UpdateTask(context.Background(), task)
	_ = r.emit("task.failed", task.ActorID, map[string]any{"task": task})
}
func (r *Runner) emit(eventType, actorID string, payload map[string]any) error {
	event := domain.Event{ID: uuid.NewString(), Type: eventType, Source: "runtime", ActorID: actorID, ConversationID: domain.ConversationID, Timestamp: time.Now().UTC(), Payload: payload}
	event.ExpiresAt = domain.EventExpiry(context.Background(), event)
	if domain.Expired(event.ExpiresAt) {
		return nil
	}
	if err := r.store.AppendEvent(context.Background(), event); err != nil {
		return fmt.Errorf("persist %s: %w", eventType, err)
	}
	r.hub.Publish(event)
	return nil
}
