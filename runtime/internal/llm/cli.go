package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/quanyejun/quanyejun/runtime/internal/environment"
)

type CLIProvider struct {
	info        ProviderInfo
	path        string
	kind        string
	environment environment.Environment
}

func NewClaudeCodeProvider(executionEnvironment environment.Environment, path, model, source string) *CLIProvider {
	return &CLIProvider{info: ProviderInfo{ID: "claude-code", Label: "Claude Code", Model: model, Source: source}, path: path, kind: "claude-code", environment: executionEnvironment}
}

func NewCodexCLIProvider(executionEnvironment environment.Environment, path, model, source string) *CLIProvider {
	return &CLIProvider{info: ProviderInfo{ID: "codex-cli", Label: "Codex CLI", Model: model, Source: source}, path: path, kind: "codex-cli", environment: executionEnvironment}
}

func (p *CLIProvider) Name() string       { return p.info.ID }
func (p *CLIProvider) Info() ProviderInfo { return p.info }

func (p *CLIProvider) Generate(ctx context.Context, input Request) (string, error) {
	args := make([]string, 0, 16)
	if p.kind == "claude-code" {
		args = append(args, "-p", "--output-format", "text", "--max-turns", "1", "--no-session-persistence", "--safe-mode", "--tools", "", "--disallowedTools", "mcp__*")
		if p.info.Model != "" {
			args = append(args, "--model", p.info.Model)
		}
	} else {
		args = append(args, "exec", "--ephemeral", "--sandbox", "read-only", "--skip-git-repo-check")
		if p.info.Model != "" {
			args = append(args, "--model", p.info.Model)
		}
		args = append(args, "-")
	}

	result, err := p.environment.Run(ctx, environment.RunSpec{
		Command:  p.path,
		Args:     args,
		Stdin:    cliPrompt(input),
		Isolated: true,
		Requires: []environment.Capability{environment.CanShell},
	})
	if err != nil {
		return "", commandError(p.info.Label, err, result.Stderr)
	}
	if text := strings.TrimSpace(result.Stdout); text != "" {
		return text, nil
	}
	return "", fmt.Errorf("%s returned an empty response", p.info.Label)
}

func commandError(label string, err error, stderr string) error {
	detail := strings.TrimSpace(stderr)
	if len(detail) > 1200 {
		detail = detail[:1200]
	}
	if detail != "" {
		return fmt.Errorf("%s failed: %w: %s", label, err, detail)
	}
	return fmt.Errorf("%s failed: %w", label, err)
}
