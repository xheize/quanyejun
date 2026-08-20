package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/quanyejun/quanyejun/runtime/internal/agent"
	"github.com/quanyejun/quanyejun/runtime/internal/environment"
	"github.com/quanyejun/quanyejun/runtime/internal/eventhub"
	"github.com/quanyejun/quanyejun/runtime/internal/llm"
	"github.com/quanyejun/quanyejun/runtime/internal/store"
	"github.com/quanyejun/quanyejun/runtime/internal/tasks"
	runtimetools "github.com/quanyejun/quanyejun/runtime/internal/tools"
)

func TestMessageRoundTripAppearsInBootstrap(t *testing.T) {
	dataStore := store.NewMemoryStore()
	hub := eventhub.New()
	provider := &recordingProvider{}
	server := newTestServer(t, dataStore, hub, provider, []string{"http://localhost:3000"}, "루멘")

	body, _ := json.Marshal(map[string]string{"actorId": "person_example", "content": "이 문장을 기억해"})
	request := httptest.NewRequest(http.MethodPost, "/api/messages", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("create message status = %d, body = %s", response.Code, response.Body.String())
	}
	waitForRun(t, dataStore)

	bootstrapRequest := httptest.NewRequest(http.MethodGet, "/api/bootstrap?actorId=person_example", nil)
	bootstrapResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(bootstrapResponse, bootstrapRequest)
	if bootstrapResponse.Code != http.StatusOK {
		t.Fatalf("bootstrap status = %d", bootstrapResponse.Code)
	}

	var payload struct {
		Identity struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"identity"`
		Environment struct {
			Kind    string `json:"kind"`
			Profile string `json:"profile"`
		} `json:"environment"`
		Messages []any `json:"messages"`
		Memories []any `json:"memories"`
		Events   []any `json:"events"`
	}
	if err := json.Unmarshal(bootstrapResponse.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(payload.Messages))
	}
	if payload.Identity.ID != "identity_quanyejun" || payload.Identity.Name != "루멘" {
		t.Fatalf("identity = %#v", payload.Identity)
	}
	if payload.Environment.Kind != "local" || payload.Environment.Profile != "developer" {
		t.Fatalf("environment = %#v", payload.Environment)
	}
	if provider.lastRequest.IdentityName != "루멘" {
		t.Fatalf("provider identity name = %q", provider.lastRequest.IdentityName)
	}
	if len(payload.Memories) != 2 {
		t.Fatalf("memories = %d, want 2", len(payload.Memories))
	}
	if len(payload.Events) < 8 {
		t.Fatalf("events = %d, want persisted run and tool events", len(payload.Events))
	}
}

type recordingProvider struct {
	lastRequest llm.Request
}

func (p *recordingProvider) Turn(_ context.Context, request llm.Request) (llm.Result, error) {
	p.lastRequest = request
	if len(request.Exchanges) == 0 {
		return llm.Result{ToolCalls: []llm.ToolCall{{ID: "remember-1", Name: "memory_remember", Arguments: json.RawMessage(`{"content":"이 문장을 기억해"}`)}}}, nil
	}
	return llm.Result{Text: "recorded", StopReason: "completed"}, nil
}

func (*recordingProvider) Name() string { return "recording" }
func (*recordingProvider) Info() llm.ProviderInfo {
	return llm.ProviderInfo{ID: "recording", Label: "Recording", Source: "test"}
}

func TestRejectsEmptyMessage(t *testing.T) {
	dataStore := store.NewMemoryStore()
	hub := eventhub.New()
	server := newTestServer(t, dataStore, hub, llm.NewLocalProvider(), []string{"*"}, "")
	request := httptest.NewRequest(http.MethodPost, "/api/messages", bytes.NewBufferString(`{"content":"  "}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
}

func TestWebSocketReceivesPersistedEvents(t *testing.T) {
	dataStore := store.NewMemoryStore()
	hub := eventhub.New()
	server := newTestServer(t, dataStore, hub, llm.NewLocalProvider(), []string{"http://localhost:3000"}, "")
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	header := http.Header{"Origin": []string{"http://localhost:3000"}}
	connection, _, err := websocket.DefaultDialer.Dial(strings.Replace(httpServer.URL, "http", "ws", 1)+"/ws", header)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()

	body := bytes.NewBufferString(`{"actorId":"person_example","content":"웹소켓 확인"}`)
	response, err := http.Post(httpServer.URL+"/api/messages", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("message status = %d", response.StatusCode)
	}

	if err := connection.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var event struct {
		Type string `json:"type"`
	}
	if err := connection.ReadJSON(&event); err != nil {
		t.Fatal(err)
	}
	if event.Type != "chat.message" {
		t.Fatalf("event type = %q", event.Type)
	}
}

func newTestServer(t *testing.T, dataStore store.Store, hub *eventhub.Hub, provider llm.Provider, origins []string, displayName string) *Server {
	t.Helper()
	executionEnvironment := newTestEnvironment(t)
	registry := runtimetools.NewRegistry(dataStore, executionEnvironment)
	agentRunner := agent.NewRunner(dataStore, hub, provider, registry, displayName)
	return New(dataStore, hub, provider, agentRunner, tasks.NewRunner(dataStore, hub), origins, displayName, executionEnvironment)
}

func waitForRun(t *testing.T, dataStore store.Store) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		runs, err := dataStore.ListAgentRuns(context.Background(), "person_example", 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(runs) == 1 && (runs[0].Status == "completed" || runs[0].Status == "failed") {
			if runs[0].Status == "failed" {
				t.Fatalf("run failed: %s", runs[0].Error)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("agent run did not complete")
}

func newTestEnvironment(t *testing.T) environment.Environment {
	t.Helper()
	root := t.TempDir()
	executionEnvironment := environment.NewLocal(environment.ConfigFromEnvironment(func(key string) string {
		if key == "QUANYEJUN_RUNTIME_ROOT" {
			return root
		}
		return ""
	}))
	if err := executionEnvironment.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	return executionEnvironment
}
