package llm

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type DetectOptions struct{ Getenv func(string) string }

func DetectProvider(_ context.Context, options DetectOptions) (Provider, error) {
	getenv := options.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	requested := normalizeProvider(getenv("LLM_PROVIDER"))
	if requested == "" {
		requested = "builtin"
	}
	switch requested {
	case "openai":
		apiKey, model := strings.TrimSpace(getenv("OPENAI_API_KEY")), firstValue(getenv("OPENAI_MODEL"), getenv("LLM_MODEL"))
		if apiKey == "" || model == "" {
			return nil, fmt.Errorf("LLM_PROVIDER openai requires OPENAI_API_KEY and OPENAI_MODEL")
		}
		store, _ := strconv.ParseBool(getenv("OPENAI_STORE"))
		return NewOpenAIProvider(apiKey, model, firstValue(getenv("OPENAI_BASE_URL"), "https://api.openai.com/v1"), "explicit:credentials", store), nil
	case "anthropic":
		apiKey, model := strings.TrimSpace(getenv("ANTHROPIC_API_KEY")), strings.TrimSpace(getenv("ANTHROPIC_MODEL"))
		if apiKey == "" || model == "" {
			return nil, fmt.Errorf("LLM_PROVIDER anthropic requires ANTHROPIC_API_KEY and ANTHROPIC_MODEL")
		}
		maxTokens, _ := strconv.Atoi(getenv("ANTHROPIC_MAX_TOKENS"))
		return NewAnthropicProvider(apiKey, model, firstValue(getenv("ANTHROPIC_BASE_URL"), "https://api.anthropic.com"), "explicit:credentials", maxTokens), nil
	case "local":
		model := firstValue(getenv("LOCAL_LLM_MODEL"), getenv("OLLAMA_MODEL"))
		if model == "" {
			return nil, fmt.Errorf("LLM_PROVIDER local requires LOCAL_LLM_MODEL")
		}
		return NewLocalOpenAIProvider(getenv("LOCAL_LLM_API_KEY"), model, firstValue(getenv("LOCAL_LLM_BASE_URL"), "http://localhost:11434/v1"), "explicit:http"), nil
	case "builtin":
		return NewBuiltinProvider(), nil
	default:
		return nil, fmt.Errorf("unknown LLM_PROVIDER %q; use openai, anthropic, local, or builtin", requested)
	}
}

func normalizeProvider(value string) string {
	value = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(value, "_", "-")))
	switch value {
	case "openai-api":
		return "openai"
	case "claude", "claude-api":
		return "anthropic"
	case "local-openai", "ollama", "lm-studio", "lmstudio":
		return "local"
	case "preview", "deterministic":
		return "builtin"
	default:
		return value
	}
}

func firstValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
