package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDetectsExplicitOpenAIAPI(t *testing.T) {
	values := map[string]string{
		"LLM_PROVIDER":   "openai-api",
		"OPENAI_API_KEY": "secret",
		"OPENAI_MODEL":   "test-model",
	}
	provider, err := DetectProvider(context.Background(), DetectOptions{Getenv: mapEnv(values)})
	if err != nil {
		t.Fatal(err)
	}
	if info := provider.Info(); info.ID != "openai-api" || info.Model != "test-model" || info.Source != "explicit:credentials" {
		t.Fatalf("unexpected provider info: %#v", info)
	}
}

func TestAutoPriorityIsConfigurable(t *testing.T) {
	values := map[string]string{
		"LLM_PROVIDER_PRIORITY": "openai-api,claude-api,builtin",
		"OPENAI_API_KEY":        "openai-secret",
		"OPENAI_MODEL":          "openai-test",
		"ANTHROPIC_API_KEY":     "anthropic-secret",
		"ANTHROPIC_MODEL":       "claude-test",
	}
	provider, err := DetectProvider(context.Background(), DetectOptions{Getenv: mapEnv(values)})
	if err != nil {
		t.Fatal(err)
	}
	if provider.Name() != "openai-api" {
		t.Fatalf("provider = %q, want openai-api", provider.Name())
	}
}

func TestCodexCLIRequiresRunnableAuthenticatedCommand(t *testing.T) {
	values := map[string]string{"LLM_PROVIDER": "codex-cli", "CODEX_MODEL": "test-codex"}
	var probes [][]string
	provider, err := DetectProvider(context.Background(), DetectOptions{
		Getenv: mapEnv(values),
		LookupPath: func(name string) (string, error) {
			if name != "codex" {
				t.Fatalf("lookup = %q", name)
			}
			return "C:/tools/codex.exe", nil
		},
		Probe: func(_ context.Context, _ string, args ...string) error {
			probes = append(probes, append([]string(nil), args...))
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if provider.Name() != "codex-cli" || len(probes) != 2 {
		t.Fatalf("provider = %q, probes = %#v", provider.Name(), probes)
	}
	if probes[1][0] != "login" || probes[1][1] != "status" {
		t.Fatalf("auth probe = %#v", probes[1])
	}
}

func TestExplicitUnavailableProviderReturnsUsefulError(t *testing.T) {
	_, err := DetectProvider(context.Background(), DetectOptions{
		Getenv:     mapEnv(map[string]string{"LLM_PROVIDER": "claude-code"}),
		LookupPath: func(string) (string, error) { return "", errors.New("missing") },
	})
	if err == nil {
		t.Fatal("expected detection error")
	}
}

func TestAutoDetectsRunningOllamaModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/tags" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"models":[{"name":"qwen-test:latest"}]}`))
	}))
	defer server.Close()

	values := map[string]string{"LLM_PROVIDER_PRIORITY": "local,builtin", "OLLAMA_HOST": server.URL}
	provider, err := DetectProvider(context.Background(), DetectOptions{Getenv: mapEnv(values), HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	info := provider.Info()
	if info.ID != "local" || info.Model != "qwen-test:latest" || info.Source != "auto:ollama" {
		t.Fatalf("unexpected provider info: %#v", info)
	}
}

func mapEnv(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}
