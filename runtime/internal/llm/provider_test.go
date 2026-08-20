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
		_, _ = writer.Write([]byte(`{"id":"resp_test","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"OpenAI response"}]}]}`))
	}))
	defer server.Close()

	provider := NewOpenAIProvider("secret", "test-model", server.URL+"/v1", "test", false)
	provider.client = server.Client()
	result, err := provider.Turn(context.Background(), sampleRequest())
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "OpenAI response" {
		t.Fatalf("response = %q", result.Text)
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
	result, err := provider.Turn(context.Background(), sampleRequest())
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "Claude response" {
		t.Fatalf("response = %q", result.Text)
	}
}

func TestOpenAIProviderContinuesFunctionCallWithOutput(t *testing.T) {
	requestNumber := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestNumber++
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		writer.Header().Set("Content-Type", "application/json")
		if requestNumber == 1 {
			if _, ok := body["tools"]; !ok {
				t.Fatalf("tools missing: %#v", body)
			}
			_, _ = writer.Write([]byte(`{"id":"resp_1","status":"completed","output":[{"type":"function_call","call_id":"call_1","name":"memory_remember","arguments":"{\"content\":\"remember this\"}"}]}`))
			return
		}
		encoded, _ := json.Marshal(body["input"])
		if !strings.Contains(string(encoded), `function_call_output`) || !strings.Contains(string(encoded), `call_1`) {
			t.Fatalf("function output missing from continuation: %s", encoded)
		}
		_, _ = writer.Write([]byte(`{"id":"resp_2","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"remembered"}]}]}`))
	}))
	defer server.Close()

	provider := NewOpenAIProvider("secret", "test-model", server.URL, "test", false)
	provider.client = server.Client()
	request := sampleRequest()
	request.Tools = []ToolDefinition{{Name: "memory_remember", Description: "remember", Parameters: map[string]any{"type": "object"}}}
	first, err := provider.Turn(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.ToolCalls) != 1 || string(first.ToolCalls[0].Arguments) != `{"content":"remember this"}` {
		t.Fatalf("unexpected tool call: %#v", first.ToolCalls)
	}
	request.Exchanges = []ToolExchange{{Calls: first.ToolCalls, ProviderItems: first.ProviderItems, Outputs: []ToolOutput{{CallID: "call_1", Output: `{"stored":true}`}}}}
	second, err := provider.Turn(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if second.Text != "remembered" {
		t.Fatalf("response = %q", second.Text)
	}
}

func TestToolStrictDefaultsToTrueAndCanBeDisabled(t *testing.T) {
	if !toolStrict(ToolDefinition{}) {
		t.Fatal("local tools should default to strict schemas")
	}
	strict := false
	if toolStrict(ToolDefinition{Strict: &strict}) {
		t.Fatal("remote MCP schemas must be able to disable strict mode")
	}
}

func sampleRequest() Request {
	return Request{
		IdentityName: "루멘",
		Messages:     []domain.Message{{Role: "user", Content: "hello"}},
		Memories:     []domain.Memory{{Content: "prefers concise replies"}},
	}
}
