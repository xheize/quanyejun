package agent

import (
	"encoding/json"
	"fmt"

	"github.com/quanyejun/quanyejun/runtime/internal/domain"
	"github.com/quanyejun/quanyejun/runtime/internal/llm"
)

// Only resume at a fully persisted model/tool exchange boundary. A missing
// result cannot distinguish an unexecuted operation from a completed external
// side effect whose result was not saved. Never retry that operation blindly.
func restoreExchanges(steps []domain.RunStep, provider string) ([]llm.ToolExchange, int, int, int, error) {
	exchanges := make([]llm.ToolExchange, 0)
	turns, calls, sequence := 0, 0, 0
	awaitingResponse, awaitingResult := false, false
	var pending *llm.ToolExchange
	next := 0
	uncertain := func() ([]llm.ToolExchange, int, int, int, error) {
		return nil, 0, 0, 0, fmt.Errorf("interrupted run has incomplete or incompatible execution history; manual reconciliation required")
	}
	for _, step := range steps {
		if step.Sequence <= sequence {
			return uncertain()
		}
		sequence = step.Sequence
		switch step.Kind {
		case "model_request":
			if awaitingResponse || pending != nil {
				return uncertain()
			}
			turns++
			awaitingResponse = true
		case "model_response":
			if !awaitingResponse {
				return uncertain()
			}
			var result struct {
				ToolCalls     []llm.ToolCall    `json:"toolCalls"`
				ProviderItems []json.RawMessage `json:"providerItems"`
			}
			if json.Unmarshal(step.Output, &result) != nil || len(result.ToolCalls) == 0 {
				return uncertain()
			}
			// Older Responses API records did not retain provider continuation items.
			if provider == "openai" && len(result.ProviderItems) == 0 {
				return uncertain()
			}
			calls += len(result.ToolCalls)
			pending = &llm.ToolExchange{Calls: result.ToolCalls, ProviderItems: result.ProviderItems}
			awaitingResponse = false
			next = 0
		case "tool_call":
			if pending == nil || awaitingResult || next >= len(pending.Calls) {
				return uncertain()
			}
			call := pending.Calls[next]
			if step.CallID != call.ID || step.ToolName != call.Name {
				return uncertain()
			}
			awaitingResult = true
		case "tool_result":
			if pending == nil || !awaitingResult {
				return uncertain()
			}
			call := pending.Calls[next]
			if step.CallID != call.ID || step.ToolName != call.Name || !json.Valid(step.Output) {
				return uncertain()
			}
			pending.Outputs = append(pending.Outputs, llm.ToolOutput{CallID: step.CallID, Output: string(step.Output)})
			awaitingResult = false
			next++
			if next == len(pending.Calls) {
				exchanges = append(exchanges, *pending)
				pending = nil
			}
		default:
			return uncertain()
		}
	}
	if awaitingResponse || pending != nil || awaitingResult {
		return uncertain()
	}
	return exchanges, turns, calls, sequence, nil
}
