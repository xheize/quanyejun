package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/quanyejun/quanyejun/runtime/internal/domain"
	"github.com/quanyejun/quanyejun/runtime/internal/eventhub"
	"github.com/quanyejun/quanyejun/runtime/internal/llm"
	"github.com/quanyejun/quanyejun/runtime/internal/store"
	runtimetools "github.com/quanyejun/quanyejun/runtime/internal/tools"
)

type Runner struct {
	store       store.Store
	hub         *eventhub.Hub
	provider    llm.Provider
	tools       *runtimetools.Registry
	displayName string
	config      HarnessConfig
	active      sync.Map
}

func NewRunner(dataStore store.Store, hub *eventhub.Hub, provider llm.Provider, tools *runtimetools.Registry, displayName string) *Runner {
	return NewRunnerWithConfig(dataStore, hub, provider, tools, displayName, DefaultHarnessConfig())
}

func NewRunnerWithConfig(dataStore store.Store, hub *eventhub.Hub, provider llm.Provider, tools *runtimetools.Registry, displayName string, config HarnessConfig) *Runner {
	return &Runner{store: dataStore, hub: hub, provider: provider, tools: tools, displayName: domain.NormalizeDisplayName(displayName), config: normalizeHarnessConfig(config)}
}

func (r *Runner) Start(actorID, messageID, goal string) (domain.AgentRun, error) {
	now := time.Now().UTC()
	info := r.provider.Info()
	run := domain.AgentRun{
		ID: uuid.NewString(), IdentityID: domain.IdentityID, ActorID: actorID, ConversationID: domain.ConversationID,
		InputMessageID: messageID, Kind: "parent", Provider: info.ID, Model: info.Model, Status: "queued", Goal: goal,
		CreatedAt: now, UpdatedAt: now,
	}
	messages, err := r.store.ListMessages(context.Background(), actorID, 50)
	if err != nil {
		return domain.AgentRun{}, err
	}
	memories, err := r.store.ListMemories(context.Background(), actorID, 8)
	if err != nil {
		return domain.AgentRun{}, err
	}
	run.ExpiresAt = domain.Expiry(context.Background(), actorID, now, nil)
	for _, m := range throughMessage(messages, messageID) {
		run.ExpiresAt = domain.Earliest(run.ExpiresAt, m.ExpiresAt)
	}
	for _, m := range memories {
		run.ExpiresAt = domain.Earliest(run.ExpiresAt, m.ExpiresAt)
	}
	if domain.Expired(run.ExpiresAt) {
		return domain.AgentRun{}, fmt.Errorf("context expired; retry with fresh context")
	}
	if err := r.store.CreateAgentRun(context.Background(), run); err != nil {
		return domain.AgentRun{}, err
	}
	if err := r.emit(context.Background(), run, "run.created", map[string]any{"run": run}); err != nil {
		return domain.AgentRun{}, err
	}
	r.launch(run)
	return run, nil
}

func (r *Runner) Recover(ctx context.Context) error {
	runs, err := r.store.ListAgentRunsByStatus(ctx, []string{"queued", "running"}, 100)
	if err != nil {
		return err
	}
	for _, run := range runs {
		if run.Status == "running" {
			run.Status = "queued"
			run.UpdatedAt = time.Now().UTC()
			if err := r.store.UpdateAgentRun(ctx, run); err != nil {
				return err
			}
		}
		r.launch(run)
	}
	return nil
}

func (r *Runner) launch(run domain.AgentRun) {
	if _, loaded := r.active.LoadOrStore(run.ID, struct{}{}); loaded {
		return
	}
	go func() {
		defer r.active.Delete(run.ID)
		ctx, cancel := context.WithTimeout(context.Background(), r.config.RunTimeout)
		defer cancel()
		ctx = domain.WithExpiry(ctx, run.ExpiresAt)
		if run.ExpiresAt != nil {
			var expiryCancel context.CancelFunc
			ctx, expiryCancel = context.WithDeadline(ctx, *run.ExpiresAt)
			defer expiryCancel()
		}
		if err := r.execute(ctx, run); err != nil {
			slog.Error("agent run failed", "runId", run.ID, "status", "failed")
			// Execution can shorten the context lifetime. Never publish a failure
			// using the older queued deadline, or resurrect an expired run.
			if current, readErr := r.store.GetAgentRun(context.Background(), run.ID); readErr == nil {
				r.fail(current, err)
			}
		}
	}()
}

func (r *Runner) execute(ctx context.Context, run domain.AgentRun) error {
	now := time.Now().UTC()
	run.Status, run.StartedAt, run.UpdatedAt, run.Attempt = "running", now, now, run.Attempt+1
	if err := r.store.UpdateAgentRun(ctx, run); err != nil {
		return err
	}
	if err := r.emit(ctx, run, "run.started", map[string]any{"run": run}); err != nil {
		return err
	}

	messages, err := r.store.ListMessages(ctx, run.ActorID, 50)
	if err != nil {
		return err
	}
	messages = throughMessage(messages, run.InputMessageID)
	memories, err := r.store.ListMemories(ctx, run.ActorID, 8)
	if err != nil {
		return err
	}
	// Context may have changed between queuing and execution. Shorten the entire
	// derived result's lifetime to include every record actually sent to the model.
	for _, message := range messages {
		run.ExpiresAt = domain.Earliest(run.ExpiresAt, message.ExpiresAt)
	}
	for _, memory := range memories {
		run.ExpiresAt = domain.Earliest(run.ExpiresAt, memory.ExpiresAt)
	}
	ctx = domain.WithExpiry(ctx, run.ExpiresAt)
	if run.ExpiresAt != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, *run.ExpiresAt)
		defer cancel()
		if err := r.store.UpdateAgentRun(ctx, run); err != nil {
			return err
		}
	}
	exchanges := make([]llm.ToolExchange, 0)
	steps, err := r.store.ListRunSteps(ctx, run.ID)
	if err != nil {
		return err
	}
	sequence := len(steps)
	toolCallCount := 0

	for turn := 0; turn < r.config.MaxTurns; turn++ {
		sequence++
		requestInput := mustJSON(map[string]any{"turn": turn + 1, "messageCount": len(messages), "memoryCount": len(memories), "toolCount": len(r.tools.DefinitionsFor(run.ActorID)), "toolCallsUsed": toolCallCount, "toolCallBudget": r.config.MaxToolCalls})
		if err := r.appendStep(ctx, run, domain.RunStep{ID: uuid.NewString(), RunID: run.ID, Sequence: sequence, Kind: "model_request", Status: "completed", Input: requestInput, CreatedAt: time.Now().UTC(), CompletedAt: time.Now().UTC()}); err != nil {
			return err
		}

		result, err := r.provider.Turn(ctx, llm.Request{ActorID: run.ActorID, IdentityName: r.displayName, Messages: messages, Memories: memories, Tools: r.tools.DefinitionsFor(run.ActorID), Exchanges: exchanges})
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return fmt.Errorf("provider turn: %w", err)
		}
		sequence++
		resultOutput := mustJSON(map[string]any{"text": result.Text, "toolCalls": result.ToolCalls, "usage": result.Usage, "stopReason": result.StopReason})
		if err := r.appendStep(ctx, run, domain.RunStep{ID: uuid.NewString(), RunID: run.ID, Sequence: sequence, Kind: "model_response", Status: "completed", ProviderResponseID: result.ProviderResponseID, Output: resultOutput, CreatedAt: time.Now().UTC(), CompletedAt: time.Now().UTC()}); err != nil {
			return err
		}

		if len(result.ToolCalls) == 0 {
			if result.Text == "" {
				return fmt.Errorf("provider completed without text")
			}
			message := domain.Message{ID: uuid.NewString(), ActorID: run.ActorID, ConversationID: run.ConversationID, Role: "assistant", Content: result.Text, CreatedAt: time.Now().UTC(), ExpiresAt: run.ExpiresAt}
			if err := r.store.AddMessage(ctx, message); err != nil {
				return err
			}
			if err := r.emit(ctx, run, "chat.message", map[string]any{"message": message, "runId": run.ID}); err != nil {
				return err
			}
			run.Status, run.Result, run.UpdatedAt, run.CompletedAt = "completed", result.Text, time.Now().UTC(), time.Now().UTC()
			if err := r.store.UpdateAgentRun(ctx, run); err != nil {
				return err
			}
			return r.emit(ctx, run, "run.completed", map[string]any{"run": run})
		}
		if toolCallCount+len(result.ToolCalls) > r.config.MaxToolCalls {
			return fmt.Errorf("agent run exceeded tool call budget of %d", r.config.MaxToolCalls)
		}
		toolCallCount += len(result.ToolCalls)

		exchange := llm.ToolExchange{Calls: result.ToolCalls, ProviderItems: result.ProviderItems}
		for _, call := range result.ToolCalls {
			sequence++
			if err := r.appendStep(ctx, run, domain.RunStep{ID: uuid.NewString(), RunID: run.ID, Sequence: sequence, Kind: "tool_call", Status: "completed", CallID: call.ID, ToolName: call.Name, Input: call.Arguments, CreatedAt: time.Now().UTC(), CompletedAt: time.Now().UTC()}); err != nil {
				return err
			}
			if err := r.emit(ctx, run, "tool.started", map[string]any{"runId": run.ID, "callId": call.ID, "tool": call.Name}); err != nil {
				return err
			}
			toolResult, toolErr := r.tools.Execute(ctx, run.ActorID, call)
			output := toolResult.Output
			status, errorText := "completed", ""
			if toolErr != nil {
				status, errorText = "failed", toolErr.Error()
				output = string(mustJSON(map[string]any{"error": errorText}))
			}
			sequence++
			if err := r.appendStep(ctx, run, domain.RunStep{ID: uuid.NewString(), RunID: run.ID, Sequence: sequence, Kind: "tool_result", Status: status, CallID: call.ID, ToolName: call.Name, Output: json.RawMessage(output), Error: errorText, CreatedAt: time.Now().UTC(), CompletedAt: time.Now().UTC()}); err != nil {
				return err
			}
			exchange.Outputs = append(exchange.Outputs, llm.ToolOutput{CallID: call.ID, Output: output})
			if toolResult.EventType != "" {
				if err := r.emit(ctx, run, toolResult.EventType, toolResult.Payload); err != nil {
					return err
				}
			}
			eventType := "tool.completed"
			if toolErr != nil {
				eventType = "tool.failed"
			}
			if err := r.emit(ctx, run, eventType, map[string]any{"runId": run.ID, "callId": call.ID, "tool": call.Name, "error": errorText}); err != nil {
				return err
			}
		}
		exchanges = append(exchanges, exchange)
	}
	return fmt.Errorf("agent run exceeded %d model turns", r.config.MaxTurns)
}

func (r *Runner) fail(run domain.AgentRun, cause error) {
	now := time.Now().UTC()
	run.Status, run.Error, run.UpdatedAt, run.CompletedAt = "failed", cause.Error(), now, now
	_ = r.store.UpdateAgentRun(context.Background(), run)
	_ = r.emit(context.Background(), run, "run.failed", map[string]any{"run": run})
}

func (r *Runner) appendStep(ctx context.Context, run domain.AgentRun, step domain.RunStep) error {
	step.ExpiresAt = domain.Earliest(step.ExpiresAt, run.ExpiresAt)
	if err := r.store.AppendRunStep(ctx, step); err != nil {
		return err
	}
	publicStep := step
	publicStep.Input = nil
	publicStep.Output = nil
	return r.emit(ctx, run, "run.step", map[string]any{"step": publicStep})
}

func (r *Runner) emit(ctx context.Context, run domain.AgentRun, eventType string, payload map[string]any) error {
	event := domain.Event{ID: uuid.NewString(), Type: eventType, Source: "agent-runtime", ActorID: run.ActorID, ConversationID: run.ConversationID, Timestamp: time.Now().UTC(), Payload: payload}
	event.ExpiresAt = domain.Earliest(run.ExpiresAt, domain.EventExpiry(ctx, event))
	if domain.Expired(event.ExpiresAt) {
		return nil
	}
	if err := r.store.AppendEvent(ctx, event); err != nil {
		return err
	}
	r.hub.Publish(event)
	return nil
}

func throughMessage(messages []domain.Message, id string) []domain.Message {
	if id == "" {
		return messages
	}
	for index, message := range messages {
		if message.ID == id {
			return messages[:index+1]
		}
	}
	return messages
}

func mustJSON(value any) json.RawMessage {
	encoded, _ := json.Marshal(value)
	return encoded
}
