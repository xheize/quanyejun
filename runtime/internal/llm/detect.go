package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/quanyejun/quanyejun/runtime/internal/environment"
)

const defaultPriority = "local,claude-api,claude-code,openai-api,codex-cli,builtin"

type DetectOptions struct {
	Getenv      func(string) string
	Environment environment.Environment
	HTTPClient  *http.Client
}

func DetectProvider(ctx context.Context, options DetectOptions) (Provider, error) {
	options = detectionDefaults(options)
	requested := normalizeProvider(options.Getenv("LLM_PROVIDER"))
	if requested == "" {
		requested = "auto"
	}
	if requested != "auto" {
		provider, available, reason := detectOne(ctx, requested, "explicit", options)
		if !available {
			return nil, fmt.Errorf("LLM_PROVIDER %q is unavailable: %s", requested, reason)
		}
		return provider, nil
	}

	priority, err := providerPriority(options.Getenv("LLM_PROVIDER_PRIORITY"))
	if err != nil {
		return nil, err
	}
	for _, candidate := range priority {
		provider, available, _ := detectOne(ctx, candidate, "auto", options)
		if available {
			return provider, nil
		}
	}
	return NewBuiltinProvider(), nil
}

func detectOne(ctx context.Context, id, source string, options DetectOptions) (Provider, bool, string) {
	getenv := options.Getenv
	switch id {
	case "local":
		model := firstValue(getenv("LOCAL_LLM_MODEL"), getenv("OLLAMA_MODEL"))
		baseURL := getenv("LOCAL_LLM_BASE_URL")
		if model != "" {
			if baseURL == "" {
				baseURL = normalizeOllamaBase(getenv("OLLAMA_HOST"))
			}
			if baseURL == "" {
				baseURL = "http://localhost:11434/v1"
			}
			return NewLocalOpenAIProvider(getenv("LOCAL_LLM_API_KEY"), model, baseURL, source+":config"), true, ""
		}
		if provider := detectOllama(ctx, source, options); provider != nil {
			return provider, true, ""
		}
		if provider := detectLMStudio(ctx, source, options); provider != nil {
			return provider, true, ""
		}
		return nil, false, "no configured model and no running Ollama or LM Studio model"
	case "claude-api":
		apiKey := getenv("ANTHROPIC_API_KEY")
		model := getenv("ANTHROPIC_MODEL")
		if apiKey == "" || model == "" {
			return nil, false, "ANTHROPIC_API_KEY and ANTHROPIC_MODEL are required"
		}
		maxTokens, _ := strconv.Atoi(getenv("ANTHROPIC_MAX_TOKENS"))
		return NewAnthropicProvider(apiKey, model, firstValue(getenv("ANTHROPIC_BASE_URL"), "https://api.anthropic.com"), source+":credentials", maxTokens), true, ""
	case "claude-code":
		path, err := options.Environment.LookPath(ctx, firstValue(getenv("CLAUDE_CODE_PATH"), "claude"))
		if err != nil {
			return nil, false, "claude executable not found"
		}
		if err := probeWithTimeout(ctx, options.Environment, path, "--version"); err != nil {
			return nil, false, "claude executable is not runnable"
		}
		if err := probeWithTimeout(ctx, options.Environment, path, "auth", "status"); err != nil {
			return nil, false, "Claude Code is not authenticated"
		}
		return NewClaudeCodeProvider(options.Environment, path, getenv("CLAUDE_CODE_MODEL"), source+":cli"), true, ""
	case "openai-api":
		apiKey := getenv("OPENAI_API_KEY")
		model := firstValue(getenv("OPENAI_MODEL"), getenv("LLM_MODEL"))
		if apiKey == "" || model == "" {
			return nil, false, "OPENAI_API_KEY and OPENAI_MODEL (or LLM_MODEL) are required"
		}
		return NewOpenAIProvider(apiKey, model, firstValue(getenv("OPENAI_BASE_URL"), "https://api.openai.com/v1"), source+":credentials"), true, ""
	case "codex-cli":
		path, err := options.Environment.LookPath(ctx, firstValue(getenv("CODEX_CLI_PATH"), "codex"))
		if err != nil {
			return nil, false, "codex executable not found"
		}
		if err := probeWithTimeout(ctx, options.Environment, path, "--version"); err != nil {
			return nil, false, "codex executable is not runnable"
		}
		if err := probeWithTimeout(ctx, options.Environment, path, "login", "status"); err != nil {
			return nil, false, "Codex CLI is not authenticated"
		}
		return NewCodexCLIProvider(options.Environment, path, getenv("CODEX_MODEL"), source+":cli"), true, ""
	case "builtin":
		return NewBuiltinProvider(), true, ""
	default:
		return nil, false, "unknown provider; use local, claude-api, claude-code, openai-api, codex-cli, builtin, or auto"
	}
}

func detectionDefaults(options DetectOptions) DetectOptions {
	if options.Getenv == nil {
		options.Getenv = os.Getenv
	}
	if options.Environment == nil {
		options.Environment = environment.NewLocal(environment.ConfigFromEnvironment(options.Getenv))
	}
	if options.HTTPClient == nil {
		options.HTTPClient = &http.Client{Timeout: 650 * time.Millisecond}
	}
	return options
}

func providerPriority(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		value = defaultPriority
	}
	seen := make(map[string]struct{})
	providers := make([]string, 0, 6)
	for _, raw := range strings.Split(value, ",") {
		id := normalizeProvider(raw)
		if id == "" || id == "auto" {
			continue
		}
		switch id {
		case "local", "claude-api", "claude-code", "openai-api", "codex-cli", "builtin":
		default:
			return nil, fmt.Errorf("unknown provider %q in LLM_PROVIDER_PRIORITY", strings.TrimSpace(raw))
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		providers = append(providers, id)
	}
	if _, ok := seen["builtin"]; !ok {
		providers = append(providers, "builtin")
	}
	return providers, nil
}

func normalizeProvider(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", "-")
	switch value {
	case "openai", "openai-compatible":
		return "openai-api"
	case "anthropic", "claude":
		return "claude-api"
	case "codex":
		return "codex-cli"
	case "ollama", "lm-studio", "lmstudio", "local-openai":
		return "local"
	case "preview", "deterministic":
		return "builtin"
	default:
		return value
	}
}

func detectOllama(ctx context.Context, source string, options DetectOptions) Provider {
	host := strings.TrimRight(firstValue(normalizeHost(options.Getenv("OLLAMA_HOST")), "http://localhost:11434"), "/")
	var response struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if !getJSON(ctx, options.HTTPClient, host+"/api/tags", &response) || len(response.Models) == 0 || response.Models[0].Name == "" {
		return nil
	}
	return NewLocalOpenAIProvider("", response.Models[0].Name, host+"/v1", source+":ollama")
}

func detectLMStudio(ctx context.Context, source string, options DetectOptions) Provider {
	baseURL := strings.TrimRight(firstValue(options.Getenv("LM_STUDIO_BASE_URL"), "http://localhost:1234/v1"), "/")
	var response struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if !getJSON(ctx, options.HTTPClient, baseURL+"/models", &response) || len(response.Data) == 0 || response.Data[0].ID == "" {
		return nil
	}
	return NewLocalOpenAIProvider(options.Getenv("LOCAL_LLM_API_KEY"), response.Data[0].ID, baseURL, source+":lm-studio")
}

func getJSON(ctx context.Context, client *http.Client, address string, result any) bool {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return false
	}
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return false
	}
	return json.NewDecoder(response.Body).Decode(result) == nil
}

func probeWithTimeout(parent context.Context, executionEnvironment environment.Environment, path string, args ...string) error {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	_, err := executionEnvironment.Run(ctx, environment.RunSpec{Command: path, Args: args, Isolated: true, Requires: []environment.Capability{environment.CanShell}})
	return err
}

func normalizeOllamaBase(host string) string {
	host = normalizeHost(host)
	if host == "" {
		return ""
	}
	return strings.TrimRight(host, "/") + "/v1"
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if parsed, err := url.Parse(host); err == nil && parsed.Scheme != "" {
		return host
	}
	return "http://" + host
}

func firstValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
