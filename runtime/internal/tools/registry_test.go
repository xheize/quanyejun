package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/quanyejun/quanyejun/runtime/internal/environment"
	"github.com/quanyejun/quanyejun/runtime/internal/llm"
	runtimeskills "github.com/quanyejun/quanyejun/runtime/internal/skills"
	"github.com/quanyejun/quanyejun/runtime/internal/store"
)

func TestDefinitionsExposeOnlyAuthorizedEnvironmentTools(t *testing.T) {
	restricted := testEnvironment(t, []environment.Capability{environment.CanWriteFiles})
	definitions := NewRegistry(store.NewMemoryStore(), restricted).Definitions()
	if hasDefinition(definitions, "workspace_read_file") || hasDefinition(definitions, "environment_exec") || !hasDefinition(definitions, "environment_inspect") || !hasDefinition(definitions, "memory_remember") {
		t.Fatalf("unexpected restricted definitions: %#v", definitions)
	}

	reader := testEnvironment(t, []environment.Capability{environment.CanReadFiles})
	definitions = NewRegistry(store.NewMemoryStore(), reader).Definitions()
	if !hasDefinition(definitions, "workspace_read_file") || !hasDefinition(definitions, "memory_remember") {
		t.Fatalf("unexpected reader definitions: %#v", definitions)
	}

	shell := testEnvironment(t, []environment.Capability{environment.CanShell})
	definitions = NewRegistry(store.NewMemoryStore(), shell).Definitions()
	if !hasDefinition(definitions, "environment_exec") || !hasDefinition(definitions, "environment_inspect") {
		t.Fatalf("shell tools were not exposed: %#v", definitions)
	}
	for _, definition := range definitions {
		if definition.Name == "environment_exec" && (definition.Strict == nil || *definition.Strict) {
			t.Fatal("environment_exec has optional inputs and must disable provider strict schema mode")
		}
	}
}

func TestVisitorsCannotUseOwnerTools(t *testing.T) {
	r := NewRegistry(store.NewMemoryStore(), testEnvironment(t, []environment.Capability{environment.CanShell, environment.CanReadFiles}))
	for _, actor := range []string{"anon:visitor", "sso:member"} {
		definitions := r.DefinitionsFor(actor)
		if len(definitions) != 1 || definitions[0].Name != "memory_remember" {
			t.Fatal("owner tools exposed to visitor", definitions)
		}
		for _, name := range []string{"environment_exec", "workspace_read_file", "skill_load", "mcp_remote_tool"} {
			if _, err := r.Execute(context.Background(), actor, llm.ToolCall{Name: name, Arguments: json.RawMessage(`{}`)}); err == nil {
				t.Fatal("visitor executed owner tool", name)
			}
		}
	}
}

func TestEnvironmentExecRunsAuthorizedCommandAndEmitsAuditMetadata(t *testing.T) {
	registry := NewRegistry(store.NewMemoryStore(), testEnvironment(t, []environment.Capability{environment.CanShell}))
	arguments, err := json.Marshal(map[string]any{
		"command": os.Args[0],
		"args":    []string{"-test.run=^TestEnvironmentExecHelperProcess$", "--", "success"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := registry.Execute(context.Background(), "actor", llm.ToolCall{Name: "environment_exec", Arguments: arguments})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(result.Output, `"exitCode":0`) || !contains(result.Output, "helper output") {
		t.Fatalf("unexpected command result: %s", result.Output)
	}
	if result.EventType != "environment.command.completed" || result.Payload["command"] != os.Args[0] {
		t.Fatalf("unexpected command event: %#v", result)
	}
}

func TestEnvironmentExecRejectsWorkspaceEscape(t *testing.T) {
	registry := NewRegistry(store.NewMemoryStore(), testEnvironment(t, []environment.Capability{environment.CanShell}))
	arguments, _ := json.Marshal(map[string]any{"command": os.Args[0], "workdir": "../outside"})
	if _, err := registry.Execute(context.Background(), "actor", llm.ToolCall{Name: "environment_exec", Arguments: arguments}); err == nil {
		t.Fatal("expected workspace escape rejection")
	}
}

func TestEnvironmentExecHelperProcess(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-1] != "success" {
		return
	}
	_, _ = os.Stdout.WriteString("helper output")
}

func TestSkillToolsUseProgressiveDisclosure(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "workspace-summary")
	if err := os.MkdirAll(directory, 0o750); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: workspace-summary\ndescription: Summarize a workspace safely.\n---\nRead only the requested file.\n"
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	registry := NewRegistry(store.NewMemoryStore(), testEnvironment(t, nil), runtimeskills.NewRegistry(root))
	definitions := registry.Definitions()
	if !hasDefinition(definitions, "skill_list") || !hasDefinition(definitions, "skill_load") {
		t.Fatalf("skill tools were not exposed: %#v", definitions)
	}

	listed, err := registry.Execute(context.Background(), "actor", llm.ToolCall{Name: "skill_list", Arguments: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if contains(listed.Output, "Read only the requested file") {
		t.Fatalf("skill_list leaked instructions: %s", listed.Output)
	}
	loaded, err := registry.Execute(context.Background(), "actor", llm.ToolCall{Name: "skill_load", Arguments: json.RawMessage(`{"name":"workspace-summary"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(loaded.Output, "Read only the requested file") || loaded.EventType != "skill.loaded" {
		t.Fatalf("unexpected skill load result: %#v", loaded)
	}
}

func testEnvironment(t *testing.T, granted []environment.Capability) environment.Environment {
	t.Helper()
	root := t.TempDir()
	local := environment.NewLocal(environment.Config{
		Kind: "test", Profile: "test",
		Paths:     environment.Paths{Home: filepath.Join(root, "home"), Workspace: filepath.Join(root, "workspace"), Config: filepath.Join(root, "config"), Cache: filepath.Join(root, "cache")},
		Authority: environment.Authority{Mode: "test", Granted: granted},
	})
	if err := local.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	return local
}

func hasDefinition(definitions []llm.ToolDefinition, name string) bool {
	for _, definition := range definitions {
		if definition.Name == name {
			return true
		}
	}
	return false
}

func contains(value, fragment string) bool {
	return len(value) >= len(fragment) && strings.Contains(value, fragment)
}
