package agent

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/quanyejun/quanyejun/runtime/internal/domain"
	"github.com/quanyejun/quanyejun/runtime/internal/eventhub"
	"github.com/quanyejun/quanyejun/runtime/internal/llm"
	"github.com/quanyejun/quanyejun/runtime/internal/store"
	runtimetools "github.com/quanyejun/quanyejun/runtime/internal/tools"
)

type capacityProvider struct {
	entered chan struct{}
	release chan struct{}
}

func (p *capacityProvider) Name() string           { return "capacity-test" }
func (p *capacityProvider) Info() llm.ProviderInfo { return llm.ProviderInfo{ID: p.Name()} }
func (p *capacityProvider) Turn(ctx context.Context, _ llm.Request) (llm.Result, error) {
	p.entered <- struct{}{}
	select {
	case <-p.release:
		return llm.Result{Text: "done"}, nil
	case <-ctx.Done():
		return llm.Result{}, ctx.Err()
	}
}

func TestRecoverySharesConcurrencyLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		db := store.NewMemoryStore()
		p := &capacityProvider{entered: make(chan struct{}, 8), release: make(chan struct{})}
		t.Cleanup(func() {
			select {
			case <-p.release:
			default:
				close(p.release)
			}
		})
		for i := 0; i < 8; i++ {
			err := db.CreateAgentRun(context.Background(), domain.AgentRun{ID: fmt.Sprint(i), ActorID: "actor", Status: "queued", CreatedAt: time.Now()})
			if err != nil {
				t.Fatal(err)
			}
		}
		r := NewRunnerWithConfig(db, eventhub.New(), p, runtimetools.NewRegistry(db, testEnvironment(t)), "", HarnessConfig{MaxConcurrent: 2})
		if err := r.Recover(context.Background()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if got := len(p.entered); got != 2 {
			t.Fatalf("active model calls = %d, want 2", got)
		}
		if _, err := r.Start("actor", "", "new"); !errors.Is(err, ErrCapacity) {
			t.Fatalf("new run bypassed recovery capacity: %v", err)
		}
		close(p.release)
		synctest.Wait()
		runs, err := db.ListAgentRunsByStatus(context.Background(), []string{"completed"}, 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(runs) != 8 || len(r.slots) != 0 {
			t.Fatalf("recovery incomplete: runs=%d slots=%d", len(runs), len(r.slots))
		}

	})
}

type failingMessageStore struct{ store.Store }

func (s failingMessageStore) AddMessage(context.Context, domain.Message) error {
	return errors.New("write failed")
}
func TestAdmissionReleasedAfterStorageFailure(t *testing.T) {
	db := failingMessageStore{store.NewMemoryStore()}
	r := NewRunnerWithConfig(db, eventhub.New(), llm.NewBuiltinProvider(), runtimetools.NewRegistry(db, testEnvironment(t)), "", HarnessConfig{MaxConcurrent: 1})
	for i := 0; i < 2; i++ {
		_, err := r.StartMessage(context.Background(), domain.Message{ActorID: "actor", Content: "hello"}, domain.VisitorContext{}, nil)
		if err == nil || errors.Is(err, ErrCapacity) {
			t.Fatalf("slot leaked: %v", err)
		}
	}
}
