package llm

import (
	"context"
	"testing"
)

func TestDetectsExplicitOpenAIAPI(t *testing.T) {
	provider, err := DetectProvider(context.Background(), DetectOptions{Getenv: mapEnv(map[string]string{
		"LLM_PROVIDER": "openai", "OPENAI_API_KEY": "secret", "OPENAI_MODEL": "test-model", "OPENAI_STORE": "false",
	})})
	if err != nil {
		t.Fatal(err)
	}
	if info := provider.Info(); info.ID != "openai" || info.Model != "test-model" || info.Source != "explicit:credentials" {
		t.Fatalf("unexpected provider info: %#v", info)
	}
}

func TestProviderMustBeExplicitlyConfigured(t *testing.T) {
	provider, err := DetectProvider(context.Background(), DetectOptions{Getenv: mapEnv(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if provider.Name() != "builtin" {
		t.Fatalf("provider = %q", provider.Name())
	}

	_, err = DetectProvider(context.Background(), DetectOptions{Getenv: mapEnv(map[string]string{"LLM_PROVIDER": "openai"})})
	if err == nil {
		t.Fatal("expected missing API configuration error")
	}
}

func TestLegacyAPINamesNormalizeWithoutCLIFallback(t *testing.T) {
	provider, err := DetectProvider(context.Background(), DetectOptions{Getenv: mapEnv(map[string]string{
		"LLM_PROVIDER": "claude-api", "ANTHROPIC_API_KEY": "secret", "ANTHROPIC_MODEL": "claude-test",
	})})
	if err != nil {
		t.Fatal(err)
	}
	if provider.Name() != "anthropic" {
		t.Fatalf("provider = %q", provider.Name())
	}
	if _, err := DetectProvider(context.Background(), DetectOptions{Getenv: mapEnv(map[string]string{"LLM_PROVIDER": "codex-cli"})}); err == nil {
		t.Fatal("expected CLI provider rejection")
	}
}

func mapEnv(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}
