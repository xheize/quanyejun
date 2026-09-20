package tasks

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/quanyejun/quanyejun/runtime/internal/domain"
	"github.com/quanyejun/quanyejun/runtime/internal/eventhub"
	"github.com/quanyejun/quanyejun/runtime/internal/store"
)

type failingStore struct{ store.Store }

func (s failingStore) CreateTask(context.Context, domain.Task) error {
	return errors.New("write failed")
}

func TestTaskCapacityAndStorageFailureRelease(t *testing.T) {
	r := NewRunner(failingStore{store.NewMemoryStore()}, eventhub.New())
	for i := 0; i < cap(r.slots); i++ {
		r.slots <- struct{}{}
	}
	if _, err := r.StartDemo("anon:test", time.Second); !errors.Is(err, ErrCapacity) {
		t.Fatalf("limit bypassed: %v", err)
	}
	for len(r.slots) > 0 {
		<-r.slots
	}
	for i := 0; i < 2; i++ {
		if _, err := r.StartDemo("anon:test", time.Second); err == nil || errors.Is(err, ErrCapacity) {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(r.slots) != 0 {
			t.Fatal("capacity leaked after storage failure")
		}
	}
}
