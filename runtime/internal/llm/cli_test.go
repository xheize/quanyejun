package llm

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestCodexCLIUsesEphemeralReadOnlyExecution(t *testing.T) {
	provider := NewCodexCLIProvider("codex", "test-model", "test")
	provider.runner = func(_ context.Context, _ string, args []string, stdin, workDir string) ([]byte, []byte, error) {
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "--ephemeral") || !strings.Contains(joined, "--sandbox read-only") || !strings.Contains(stdin, "Do not inspect or modify files") {
			t.Fatalf("unsafe invocation: args=%q prompt=%q", joined, stdin)
		}
		for index, arg := range args {
			if arg == "--output-last-message" {
				if err := os.WriteFile(args[index+1], []byte("Codex response"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
		}
		if workDir == "" {
			t.Fatal("missing isolated work directory")
		}
		return nil, nil, nil
	}

	text, err := provider.Generate(context.Background(), sampleRequest())
	if err != nil {
		t.Fatal(err)
	}
	if text != "Codex response" {
		t.Fatalf("response = %q", text)
	}
}

func TestClaudeCodeDisablesToolsAndPersistence(t *testing.T) {
	provider := NewClaudeCodeProvider("claude", "sonnet", "test")
	provider.runner = func(_ context.Context, _ string, args []string, _ string, _ string) ([]byte, []byte, error) {
		joined := strings.Join(args, "|")
		if !strings.Contains(joined, "--no-session-persistence") || !strings.Contains(joined, "--safe-mode") || !strings.Contains(joined, "--tools||") {
			t.Fatalf("unsafe invocation: %q", joined)
		}
		return []byte("Claude Code response"), nil, nil
	}

	text, err := provider.Generate(context.Background(), sampleRequest())
	if err != nil {
		t.Fatal(err)
	}
	if text != "Claude Code response" {
		t.Fatalf("response = %q", text)
	}
}
