package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/quanyejun/quanyejun/runtime/internal/domain"
	"github.com/quanyejun/quanyejun/runtime/internal/environment"
	"github.com/quanyejun/quanyejun/runtime/internal/eventhub"
	"github.com/quanyejun/quanyejun/runtime/internal/llm"
	"github.com/quanyejun/quanyejun/runtime/internal/store"
	runtimetools "github.com/quanyejun/quanyejun/runtime/internal/tools"
)

type toolBurstProvider struct{}

func (toolBurstProvider) Name() string { return "test" }
func (toolBurstProvider) Info() llm.ProviderInfo {
	return llm.ProviderInfo{ID: "test", Label: "Test", Source: "test"}
}
func (toolBurstProvider) Turn(context.Context, llm.Request) (llm.Result, error) {
	return llm.Result{ToolCalls: []llm.ToolCall{
		{ID: "one", Name: "memory_remember", Arguments: json.RawMessage(`{"content":"one"}`)},
		{ID: "two", Name: "memory_remember", Arguments: json.RawMessage(`{"content":"two"}`)},
	}}, nil
}

func TestRunnerRejectsToolCallsBeyondBudget(t *testing.T) {
	dataStore := store.NewMemoryStore()
	run := domain.AgentRun{
		ID: "run", IdentityID: domain.IdentityID, ActorID: "actor", ConversationID: domain.ConversationID,
		Status: "queued", Goal: "test", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := dataStore.CreateAgentRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}

	executionEnvironment := testEnvironment(t)
	registry := runtimetools.NewRegistry(dataStore, executionEnvironment)
	runner := NewRunnerWithConfig(dataStore, eventhub.New(), toolBurstProvider{}, registry, "Test", HarnessConfig{MaxTurns: 2, MaxToolCalls: 1, RunTimeout: time.Minute})
	err := runner.execute(context.Background(), run)
	if err == nil || !strings.Contains(err.Error(), "tool call budget of 1") {
		t.Fatalf("expected tool call budget error, got %v", err)
	}
	steps, err := dataStore.ListRunSteps(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range steps {
		if step.Kind == "tool_call" {
			t.Fatal("tool execution step was persisted after budget rejection")
		}
	}
}

func TestRunStepEventRedactsInputAndOutput(t *testing.T) {
	dataStore := store.NewMemoryStore()
	hub := eventhub.New()
	events, unsubscribe := hub.Subscribe()
	defer unsubscribe()
	runner := &Runner{store: dataStore, hub: hub}
	run := domain.AgentRun{ID: "run", ActorID: "actor", ConversationID: domain.ConversationID}
	if err := dataStore.CreateAgentRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	step := domain.RunStep{ID: "step", RunID: run.ID, Kind: "tool_result", Input: json.RawMessage(`{"secret":"input"}`), Output: json.RawMessage(`{"secret":"output"}`)}
	if err := runner.appendStep(context.Background(), run, step); err != nil {
		t.Fatal(err)
	}
	event := <-events
	publicStep, ok := event.Payload["step"].(domain.RunStep)
	if !ok {
		t.Fatalf("unexpected event payload: %#v", event.Payload)
	}
	if len(publicStep.Input) != 0 || len(publicStep.Output) != 0 {
		t.Fatalf("public step contains tool data: %#v", publicStep)
	}
	persisted, err := dataStore.ListRunSteps(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 1 || len(persisted[0].Input) == 0 || len(persisted[0].Output) == 0 {
		t.Fatalf("persisted step lost tool data: %#v", persisted)
	}
}

// Only inspection is expected in these unit tests. Other environment methods
// deliberately remain unavailable; real filesystem/CLI behavior is tested in environment.
type staticTestEnvironment struct{ environment.Environment }

func (staticTestEnvironment) Inspect(context.Context) (environment.Info, error) {
	return environment.Info{Kind: "test", Profile: "test", Authority: environment.Authority{Mode: "test"}}, nil
}
func testEnvironment(t *testing.T) environment.Environment {
	t.Helper()
	return staticTestEnvironment{}
}
