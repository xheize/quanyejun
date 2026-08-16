package llm

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type commandRunner func(context.Context, string, []string, string, string) ([]byte, []byte, error)

type CLIProvider struct {
	info   ProviderInfo
	path   string
	kind   string
	runner commandRunner
}

func NewClaudeCodeProvider(path, model, source string) *CLIProvider {
	return &CLIProvider{info: ProviderInfo{ID: "claude-code", Label: "Claude Code", Model: model, Source: source}, path: path, kind: "claude-code", runner: executeCommand}
}

func NewCodexCLIProvider(path, model, source string) *CLIProvider {
	return &CLIProvider{info: ProviderInfo{ID: "codex-cli", Label: "Codex CLI", Model: model, Source: source}, path: path, kind: "codex-cli", runner: executeCommand}
}

func (p *CLIProvider) Name() string       { return p.info.ID }
func (p *CLIProvider) Info() ProviderInfo { return p.info }

func (p *CLIProvider) Generate(ctx context.Context, input Request) (string, error) {
	workDir, err := os.MkdirTemp("", "quanyejun-llm-*")
	if err != nil {
		return "", fmt.Errorf("create isolated CLI workspace: %w", err)
	}
	defer os.RemoveAll(workDir)

	args := make([]string, 0, 16)
	if p.kind == "claude-code" {
		args = append(args, "-p", "--output-format", "text", "--max-turns", "1", "--no-session-persistence", "--safe-mode", "--tools", "", "--disallowedTools", "mcp__*")
		if p.info.Model != "" {
			args = append(args, "--model", p.info.Model)
		}
	} else {
		outputFile := workDir + string(os.PathSeparator) + "last-message.txt"
		args = append(args, "exec", "--ephemeral", "--sandbox", "read-only", "--skip-git-repo-check", "--output-last-message", outputFile)
		if p.info.Model != "" {
			args = append(args, "--model", p.info.Model)
		}
		args = append(args, "-")
		_, stderr, runErr := p.runner(ctx, p.path, args, cliPrompt(input), workDir)
		if runErr != nil {
			return "", commandError(p.info.Label, runErr, stderr)
		}
		result, readErr := os.ReadFile(outputFile)
		if readErr != nil {
			return "", fmt.Errorf("read %s final response: %w", p.info.Label, readErr)
		}
		if text := strings.TrimSpace(string(result)); text != "" {
			return text, nil
		}
		return "", fmt.Errorf("%s returned an empty response", p.info.Label)
	}

	stdout, stderr, err := p.runner(ctx, p.path, args, cliPrompt(input), workDir)
	if err != nil {
		return "", commandError(p.info.Label, err, stderr)
	}
	if text := strings.TrimSpace(string(stdout)); text != "" {
		return text, nil
	}
	return "", fmt.Errorf("%s returned an empty response", p.info.Label)
}

func executeCommand(ctx context.Context, path string, args []string, stdin, workDir string) ([]byte, []byte, error) {
	command := exec.CommandContext(ctx, path, args...)
	command.Dir = workDir
	command.Stdin = strings.NewReader(stdin)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func commandError(label string, err error, stderr []byte) error {
	detail := strings.TrimSpace(string(stderr))
	if len(detail) > 1200 {
		detail = detail[:1200]
	}
	if detail != "" {
		return fmt.Errorf("%s failed: %w: %s", label, err, detail)
	}
	return fmt.Errorf("%s failed: %w", label, err)
}
