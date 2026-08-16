package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/quanyejun/quanyejun/runtime/internal/domain"
)

func TestOpenAIProviderUsesResponsesAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/responses" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("authorization header missing")
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["model"] != "test-model" || body["instructions"] == "" {
			t.Fatalf("unexpected request: %#v", body)
		}
		if !strings.Contains(body["instructions"].(string), `"루멘"`) {
			t.Fatalf("display name missing from instructions: %q", body["instructions"])
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"output":[{"content":[{"type":"output_text","text":"OpenAI response"}]}]}`))
	}))
	defer server.Close()

	provider := NewOpenAIProvider("secret", "test-model", server.URL+"/v1", "test")
	provider.client = server.Client()
	text, err := provider.Generate(context.Background(), sampleRequest())
	if err != nil {
		t.Fatal(err)
	}
	if text != "OpenAI response" {
		t.Fatalf("response = %q", text)
	}
}

func TestAnthropicProviderUsesMessagesAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/messages" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		if request.Header.Get("x-api-key") != "secret" || request.Header.Get("anthropic-version") == "" {
			t.Fatalf("Anthropic headers missing")
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["model"] != "claude-test" || body["system"] == "" {
			t.Fatalf("unexpected request: %#v", body)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"content":[{"type":"text","text":"Claude response"}]}`))
	}))
	defer server.Close()

	provider := NewAnthropicProvider("secret", "claude-test", server.URL, "test", 512)
	provider.client = server.Client()
	text, err := provider.Generate(context.Background(), sampleRequest())
	if err != nil {
		t.Fatal(err)
	}
	if text != "Claude response" {
		t.Fatalf("response = %q", text)
	}
}

func sampleRequest() Request {
	return Request{
		IdentityName: "루멘",
		Messages:     []domain.Message{{Role: "user", Content: "hello"}},
		Memories:     []domain.Memory{{Content: "prefers concise replies"}},
	}
}
