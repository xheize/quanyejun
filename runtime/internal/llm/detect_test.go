package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/quanyejun/quanyejun/runtime/internal/environment"
)

type fakeEnvironment struct {
	paths map[string]string
	run   func(environment.RunSpec) (environment.RunResult, error)
	runs  []environment.RunSpec
}

func (*fakeEnvironment) Prepare(context.Context) error { return nil }
func (*fakeEnvironment) Inspect(context.Context) (environment.Info, error) {
	return environment.Info{Kind: "test"}, nil
}
func (f *fakeEnvironment) LookPath(_ context.Context, name string) (string, error) {
	if path, ok := f.paths[name]; ok {
		return path, nil
	}
	return "", errors.New("missing")
}
func (f *fakeEnvironment) Run(_ context.Context, spec environment.RunSpec) (environment.RunResult, error) {
	f.runs = append(f.runs, spec)
	if f.run != nil {
		return f.run(spec)
	}
	return environment.RunResult{}, nil
}

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
	executionEnvironment := &fakeEnvironment{paths: map[string]string{"codex": "C:/tools/codex.exe"}}
	provider, err := DetectProvider(context.Background(), DetectOptions{
		Getenv:      mapEnv(values),
		Environment: executionEnvironment,
	})
	if err != nil {
		t.Fatal(err)
	}
	if provider.Name() != "codex-cli" || len(executionEnvironment.runs) != 2 {
		t.Fatalf("provider = %q, probes = %#v", provider.Name(), executionEnvironment.runs)
	}
	if args := executionEnvironment.runs[1].Args; args[0] != "login" || args[1] != "status" {
		t.Fatalf("auth probe = %#v", args)
	}
}

func TestExplicitUnavailableProviderReturnsUsefulError(t *testing.T) {
	_, err := DetectProvider(context.Background(), DetectOptions{
		Getenv:      mapEnv(map[string]string{"LLM_PROVIDER": "claude-code"}),
		Environment: &fakeEnvironment{},
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
