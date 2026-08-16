package llm

import (
	"context"
	"strings"
	"testing"

	"github.com/quanyejun/quanyejun/runtime/internal/environment"
)

func TestCodexCLIUsesEphemeralReadOnlyExecution(t *testing.T) {
	executionEnvironment := &fakeEnvironment{run: func(spec environment.RunSpec) (environment.RunResult, error) {
		joined := strings.Join(spec.Args, " ")
		if !spec.Isolated || !strings.Contains(joined, "--ephemeral") || !strings.Contains(joined, "--sandbox read-only") || !strings.Contains(spec.Stdin, "Do not inspect or modify files") {
			t.Fatalf("unsafe invocation: args=%q prompt=%q", joined, spec.Stdin)
		}
		return environment.RunResult{Stdout: "Codex response"}, nil
	}}
	provider := NewCodexCLIProvider(executionEnvironment, "codex", "test-model", "test")

	text, err := provider.Generate(context.Background(), sampleRequest())
	if err != nil {
		t.Fatal(err)
	}
	if text != "Codex response" {
		t.Fatalf("response = %q", text)
	}
}

func TestClaudeCodeDisablesToolsAndPersistence(t *testing.T) {
	executionEnvironment := &fakeEnvironment{run: func(spec environment.RunSpec) (environment.RunResult, error) {
		joined := strings.Join(spec.Args, "|")
		if !strings.Contains(joined, "--no-session-persistence") || !strings.Contains(joined, "--safe-mode") || !strings.Contains(joined, "--tools||") {
			t.Fatalf("unsafe invocation: %q", joined)
		}
		return environment.RunResult{Stdout: "Claude Code response"}, nil
	}}
	provider := NewClaudeCodeProvider(executionEnvironment, "claude", "sonnet", "test")

	text, err := provider.Generate(context.Background(), sampleRequest())
	if err != nil {
		t.Fatal(err)
	}
	if text != "Claude Code response" {
		t.Fatalf("response = %q", text)
	}
}
