package agent

import (
	"context"
	"testing"
	"time"

	"github.com/quanyejun/quanyejun/runtime/internal/domain"
	"github.com/quanyejun/quanyejun/runtime/internal/eventhub"
	"github.com/quanyejun/quanyejun/runtime/internal/llm"
	"github.com/quanyejun/quanyejun/runtime/internal/store"
	runtimetools "github.com/quanyejun/quanyejun/runtime/internal/tools"
)

type visitorProvider struct {
	toolBurstProvider
	requests chan llm.Request
}

func (p visitorProvider) Turn(_ context.Context, request llm.Request) (llm.Result, error) {
	p.requests <- request
	return llm.Result{Text: "hello"}, nil
}

func TestRunPreservesVisitorSnapshotAndRetention(t *testing.T) {
	db := store.NewMemoryStore()
	p := visitorProvider{requests: make(chan llm.Request, 1)}
	runner := NewRunner(db, eventhub.New(), p, runtimetools.NewRegistry(db, testEnvironment(t)), "AI")
	visitor := domain.VisitorContext{Role: "anonymous", DisplayName: "관리자"}
	expiry := time.Now().Add(time.Hour)
	run, err := runner.StartForVisitor("anon:test", "", "hello", visitor, &expiry)
	if err != nil {
		t.Fatal(err)
	}
	if run.ExpiresAt == nil || !run.ExpiresAt.Equal(expiry) {
		t.Fatal("alias retention missing", run)
	}
	_, _ = db.SetVisitorProfile(context.Background(), "anon:test", "different")
	select {
	case request := <-p.requests:
		if request.Visitor != visitor || request.ActorID != "anon:test" {
			t.Fatal(request)
		}
	case <-time.After(time.Second):
		t.Fatal("provider was not called")
	}
	// Wait for persistence before the environment is cleaned up.
	for i := 0; i < 100; i++ {
		current, _ := db.GetAgentRun(context.Background(), run.ID)
		if current.Status == "completed" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("run did not complete")
}
