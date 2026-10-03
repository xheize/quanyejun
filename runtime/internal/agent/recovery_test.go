package agent

import (
	"context"
	"encoding/json"
	"github.com/quanyejun/quanyejun/runtime/internal/domain"
	"github.com/quanyejun/quanyejun/runtime/internal/eventhub"
	"github.com/quanyejun/quanyejun/runtime/internal/llm"
	"github.com/quanyejun/quanyejun/runtime/internal/store"
	tools "github.com/quanyejun/quanyejun/runtime/internal/tools"
	"strings"
	"testing"
	"time"
)

func completedExchange() []domain.RunStep {
	return []domain.RunStep{
		{ID: "request", Sequence: 1, Kind: "model_request"},
		{ID: "response", Sequence: 2, Kind: "model_response", Output: json.RawMessage(`{"toolCalls":[{"ID":"call","Name":"memory_remember","Arguments":{"content":"saved"}}],"providerItems":[{"type":"function_call","call_id":"call","name":"memory_remember","arguments":"{\"content\":\"saved\"}"}]}`)},
		{ID: "call", Sequence: 3, Kind: "tool_call", CallID: "call", ToolName: "memory_remember"},
		{ID: "result", Sequence: 4, Kind: "tool_result", CallID: "call", ToolName: "memory_remember", Output: json.RawMessage(`{"stored":true}`)},
	}
}
func TestRestoreCompletedExchange(t *testing.T) {
	exchanges, turns, calls, seq, err := restoreExchanges(completedExchange(), "openai")
	if err != nil {
		t.Fatal(err)
	}
	if turns != 1 || calls != 1 || seq != 4 || len(exchanges) != 1 || len(exchanges[0].ProviderItems) != 1 || exchanges[0].Outputs[0].Output != `{"stored":true}` {
		t.Fatalf("bad restoration: %+v %d %d %d", exchanges, turns, calls, seq)
	}
}
func TestRestoreIncompleteHistoryFailsClosed(t *testing.T) {
	for _, n := range []int{1, 2, 3} {
		if _, _, _, _, err := restoreExchanges(completedExchange()[:n], "openai"); err == nil {
			t.Fatalf("accepted incomplete history of %d steps", n)
		}
	}
	steps := completedExchange()
	steps[1].Output = json.RawMessage(`{"toolCalls":[{"ID":"call","Name":"memory_remember"}]}`)
	if _, _, _, _, err := restoreExchanges(steps, "openai"); err == nil {
		t.Fatal("accepted missing provider continuation")
	}
	steps = completedExchange()
	steps[3].CallID = "other"
	if _, _, _, _, err := restoreExchanges(steps, "local"); err == nil {
		t.Fatal("accepted mismatched result")
	}
}

type restoredProvider struct{ requests []llm.Request }

func (p *restoredProvider) Name() string           { return "test" }
func (p *restoredProvider) Info() llm.ProviderInfo { return llm.ProviderInfo{ID: "test"} }
func (p *restoredProvider) Turn(_ context.Context, r llm.Request) (llm.Result, error) {
	p.requests = append(p.requests, r)
	return llm.Result{Text: "continued"}, nil
}
func TestRecoveredExecutionUsesSavedResultsAndBudgets(t *testing.T) {
	for _, limit := range []int{1, 2} {
		db := store.NewMemoryStore()
		ctx := context.Background()
		now := time.Now()
		run := domain.AgentRun{ID: "run", ActorID: "sso:visitor", Provider: "test", Status: "running", StartedAt: now, CreatedAt: now}
		if err := db.CreateAgentRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		for _, step := range completedExchange() {
			step.RunID = run.ID
			if err := db.AppendRunStep(ctx, step); err != nil {
				t.Fatal(err)
			}
		}
		p := &restoredProvider{}
		r := NewRunnerWithConfig(db, eventhub.New(), p, tools.NewRegistry(db, testEnvironment(t)), "", HarnessConfig{MaxTurns: limit, MaxToolCalls: 1})
		err := r.execute(ctx, run)
		if limit == 1 {
			if err == nil || !strings.Contains(err.Error(), "model turns") || len(p.requests) != 0 {
				t.Fatalf("reset turn budget: %v", err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(p.requests) != 1 || len(p.requests[0].Exchanges) != 1 {
			t.Fatal("saved result not forwarded")
		}
		memories, _ := db.ListMemories(ctx, run.ActorID, 10)
		if len(memories) != 0 {
			t.Fatal("completed memory tool was executed again")
		}
		steps, _ := db.ListRunSteps(ctx, run.ID)
		results := 0
		for _, step := range steps {
			if step.Kind == "tool_result" {
				results++
			}
		}
		if results != 1 {
			t.Fatal("duplicate tool result")
		}
	}
}
func TestRecoveredToolBudgetCannotReset(t *testing.T) {
	db := store.NewMemoryStore()
	ctx := context.Background()
	now := time.Now()
	run := domain.AgentRun{ID: "run", ActorID: "actor", CreatedAt: now}
	if err := db.CreateAgentRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	for _, step := range completedExchange() {
		step.RunID = run.ID
		if err := db.AppendRunStep(ctx, step); err != nil {
			t.Fatal(err)
		}
	}
	r := NewRunnerWithConfig(db, eventhub.New(), toolBurstProvider{}, tools.NewRegistry(db, testEnvironment(t)), "", HarnessConfig{MaxTurns: 3, MaxToolCalls: 2})
	if err := r.execute(ctx, run); err == nil || !strings.Contains(err.Error(), "tool call budget") {
		t.Fatalf("reset tool budget: %v", err)
	}
	memories, _ := db.ListMemories(ctx, run.ActorID, 10)
	if len(memories) != 0 {
		t.Fatal("over-budget tools executed")
	}
}

func TestExpiredRecoveredExecutionDoesNotInvokeProvider(t *testing.T) {
	db := store.NewMemoryStore()
	p := &restoredProvider{}
	r := NewRunner(db, eventhub.New(), p, tools.NewRegistry(db, testEnvironment(t)), "")
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if err := r.execute(ctx, domain.AgentRun{ID: "expired"}); err != context.DeadlineExceeded {
		t.Fatalf("expected deadline error, got %v", err)
	}
	if len(p.requests) != 0 {
		t.Fatal("expired execution invoked provider")
	}
}
