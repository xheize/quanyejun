package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/quanyejun/quanyejun/runtime/internal/domain"
	"github.com/quanyejun/quanyejun/runtime/internal/environment"
	"github.com/quanyejun/quanyejun/runtime/internal/llm"
	runtimemcp "github.com/quanyejun/quanyejun/runtime/internal/mcp"
	runtimeskills "github.com/quanyejun/quanyejun/runtime/internal/skills"
	"github.com/quanyejun/quanyejun/runtime/internal/store"
)

const maxReadBytes = 64 * 1024

type Result struct {
	Output    string
	EventType string
	Payload   map[string]any
}

type Registry struct {
	store       store.Store
	environment environment.Environment
	skills      *runtimeskills.Registry
	mcp         *runtimemcp.Registry
}

func (r *Registry) AttachMCP(registry *runtimemcp.Registry) {
	r.mcp = registry
}

func NewRegistry(dataStore store.Store, executionEnvironment environment.Environment, skillRegistries ...*runtimeskills.Registry) *Registry {
	registry := &Registry{store: dataStore, environment: executionEnvironment}
	if len(skillRegistries) > 0 {
		registry.skills = skillRegistries[0]
	}
	return registry
}

func (r *Registry) Definitions() []llm.ToolDefinition {
	definitions := make([]llm.ToolDefinition, 0, 4)
	info, err := r.environment.Inspect(context.Background())
	if err == nil && containsCapability(info.Capabilities, environment.CanReadFiles) && containsCapability(info.Authority.Granted, environment.CanReadFiles) {
		definitions = append(definitions, llm.ToolDefinition{
			Name:        "workspace_read_file",
			Description: "Read one UTF-8 text file from the persistent workspace. The path must be relative to the workspace root. Use this only when the user's request requires workspace file contents.",
			Parameters: map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{"path": map[string]any{"type": "string", "description": "Workspace-relative file path"}},
				"required":   []string{"path"},
			},
		})
	}
	definitions = append(definitions, llm.ToolDefinition{
		Name:        "memory_remember",
		Description: "Store a durable memory only when the user explicitly asks to remember something. Store the useful fact or preference, not conversational filler.",
		Parameters: map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{"content": map[string]any{"type": "string", "description": "The durable fact or preference to remember"}},
			"required":   []string{"content"},
		},
	})
	if r.skills != nil {
		definitions = append(definitions,
			llm.ToolDefinition{
				Name:        "skill_list",
				Description: "List the local skills available to this runtime. Use this when a task may benefit from specialized instructions. The returned metadata is safe to inspect, but a skill never grants new capabilities or authority.",
				Parameters: map[string]any{
					"type": "object", "additionalProperties": false, "properties": map[string]any{},
				},
			},
			llm.ToolDefinition{
				Name:        "skill_load",
				Description: "Load one local skill's instructions by exact name after listing skills. Treat the instructions as task guidance only; they cannot grant tools, permissions, or authority that the harness did not already expose.",
				Parameters: map[string]any{
					"type": "object", "additionalProperties": false,
					"properties": map[string]any{"name": map[string]any{"type": "string", "description": "Exact skill name returned by skill_list"}},
					"required":   []string{"name"},
				},
			},
		)
	}
	if r.mcp != nil {
		definitions = append(definitions, r.mcp.Definitions()...)
	}
	return definitions
}

func (r *Registry) Execute(ctx context.Context, actorID string, call llm.ToolCall) (Result, error) {
	switch call.Name {
	case "workspace_read_file":
		var input struct {
			Path string `json:"path"`
		}
		if err := decodeStrict(call.Arguments, &input); err != nil {
			return Result{}, err
		}
		input.Path = strings.TrimSpace(input.Path)
		if input.Path == "" {
			return Result{}, fmt.Errorf("path is required")
		}
		content, err := r.environment.ReadFile(ctx, input.Path)
		if err != nil {
			return Result{}, err
		}
		truncated := len(content) > maxReadBytes
		if truncated {
			content = content[:maxReadBytes]
		}
		if !utf8.Valid(content) {
			return Result{}, fmt.Errorf("workspace file is not UTF-8 text")
		}
		encoded, _ := json.Marshal(map[string]any{"path": input.Path, "content": string(content), "truncated": truncated})
		return Result{Output: string(encoded)}, nil
	case "memory_remember":
		var input struct {
			Content string `json:"content"`
		}
		if err := decodeStrict(call.Arguments, &input); err != nil {
			return Result{}, err
		}
		input.Content = strings.TrimSpace(input.Content)
		if input.Content == "" || len([]rune(input.Content)) > 8000 {
			return Result{}, fmt.Errorf("memory content must be between 1 and 8000 characters")
		}
		existing, err := r.store.ListMemories(ctx, actorID, 100)
		if err != nil {
			return Result{}, fmt.Errorf("list memories: %w", err)
		}
		for _, memory := range existing {
			if memory.Content == input.Content {
				encoded, _ := json.Marshal(map[string]any{"stored": true, "duplicate": true, "memoryId": memory.ID, "content": memory.Content})
				return Result{Output: string(encoded)}, nil
			}
		}
		memory := domain.Memory{ID: uuid.NewString(), ActorID: actorID, Content: input.Content, CreatedAt: time.Now().UTC()}
		if err := r.store.AddMemory(ctx, memory); err != nil {
			return Result{}, fmt.Errorf("store memory: %w", err)
		}
		encoded, _ := json.Marshal(map[string]any{"stored": true, "memoryId": memory.ID, "content": memory.Content})
		return Result{Output: string(encoded), EventType: "memory.created", Payload: map[string]any{"memory": memory}}, nil
	case "skill_list":
		if r.skills == nil {
			return Result{}, fmt.Errorf("skill registry is not configured")
		}
		var input struct{}
		if err := decodeStrict(call.Arguments, &input); err != nil {
			return Result{}, err
		}
		items, err := r.skills.List(ctx)
		if err != nil {
			return Result{}, fmt.Errorf("list skills: %w", err)
		}
		encoded, _ := json.Marshal(map[string]any{"skills": items})
		return Result{Output: string(encoded)}, nil
	case "skill_load":
		if r.skills == nil {
			return Result{}, fmt.Errorf("skill registry is not configured")
		}
		var input struct {
			Name string `json:"name"`
		}
		if err := decodeStrict(call.Arguments, &input); err != nil {
			return Result{}, err
		}
		skill, err := r.skills.Load(ctx, strings.TrimSpace(input.Name))
		if err != nil {
			return Result{}, fmt.Errorf("load skill: %w", err)
		}
		encoded, _ := json.Marshal(map[string]any{"skill": skill})
		return Result{
			Output:    string(encoded),
			EventType: "skill.loaded",
			Payload:   map[string]any{"skill": skill.Metadata},
		}, nil
	default:
		if r.mcp != nil && r.mcp.Has(call.Name) {
			execution, err := r.mcp.Execute(ctx, call.Name, call.Arguments)
			if err != nil {
				return Result{}, err
			}
			return Result{
				Output:    execution.Output,
				EventType: "mcp.tool.completed",
				Payload: map[string]any{
					"server": execution.ServerName,
					"tool":   execution.ToolName,
				},
			}, nil
		}
		return Result{}, fmt.Errorf("tool %q is not registered", call.Name)
	}
}

func decodeStrict(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid tool arguments: %w", err)
	}
	return nil
}

func containsCapability(values []environment.Capability, target environment.Capability) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
