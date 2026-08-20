package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestRegistryExposesOnlyAdminAllowedAndAuthorityGrantedTools(t *testing.T) {
	var mu sync.Mutex
	called := ""
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var rpc struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(request.Body).Decode(&rpc); err != nil {
			t.Errorf("decode request: %v", err)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		switch rpc.Method {
		case "initialize":
			response.Header().Set("MCP-Session-Id", "test-session")
			writeRPC(t, response, rpc.ID, map[string]any{
				"protocolVersion": protocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "test", "version": "1"},
			})
		case "notifications/initialized":
			if request.Header.Get("MCP-Session-Id") != "test-session" {
				t.Error("initialized notification omitted MCP session")
			}
			response.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeRPC(t, response, rpc.ID, map[string]any{"tools": []map[string]any{
				{"name": "read_note", "description": "Read a note", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}, "required": []string{"id"}}},
				{"name": "delete_note", "description": "Delete a note", "inputSchema": map[string]any{"type": "object"}},
				{"name": "undeclared_tool", "description": "Not administrator-approved", "inputSchema": map[string]any{"type": "object"}},
			}})
		case "tools/call":
			var params struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(rpc.Params, &params); err != nil {
				t.Errorf("decode call params: %v", err)
			}
			mu.Lock()
			called = params.Name
			mu.Unlock()
			writeRPC(t, response, rpc.ID, map[string]any{"content": []map[string]any{{"type": "text", "text": "note content"}}})
		default:
			t.Errorf("unexpected method %q", rpc.Method)
			response.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()

	root := t.TempDir()
	serversFile := filepath.Join(root, "mcp-servers.json")
	authorityFile := filepath.Join(root, "mcp-authority.json")
	writeJSON(t, serversFile, map[string]any{"servers": []map[string]any{{
		"name": "notes", "url": server.URL, "enabled": true, "allowInsecureHttp": true,
		"allowedTools": []string{"read_note", "delete_note"},
	}}})
	writeJSON(t, authorityFile, map[string]any{"grants": []map[string]any{{
		"identityId": "identity_quanyejun", "server": "notes", "tools": []string{"read_note"}, "grantedBy": "parent",
	}}})

	registry := NewRegistry(Options{ServersFile: serversFile, AuthorityFile: authorityFile, IdentityID: "identity_quanyejun", HTTPClient: server.Client()})
	if err := registry.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	definitions := registry.Definitions()
	if len(definitions) != 1 || definitions[0].Name != "mcp__notes__read_note" {
		t.Fatalf("unexpected definitions: %#v", definitions)
	}
	if registry.Has("mcp__notes__delete_note") || registry.Has("mcp__notes__undeclared_tool") {
		t.Fatal("registry exposed a tool outside the admin/authority intersection")
	}

	execution, err := registry.Execute(context.Background(), definitions[0].Name, json.RawMessage(`{"id":"one"}`))
	if err != nil {
		t.Fatal(err)
	}
	if execution.ServerName != "notes" || execution.ToolName != "read_note" {
		t.Fatalf("unexpected execution metadata: %#v", execution)
	}
	mu.Lock()
	defer mu.Unlock()
	if called != "read_note" {
		t.Fatalf("remote tool called = %q", called)
	}
}

func TestRegistryRevocationAndInvalidPolicyFailClosed(t *testing.T) {
	server := newListOnlyServer(t)
	defer server.Close()
	root := t.TempDir()
	serversFile := filepath.Join(root, "servers.json")
	authorityFile := filepath.Join(root, "authority.json")
	writeJSON(t, serversFile, map[string]any{"servers": []map[string]any{{
		"name": "notes", "url": server.URL, "enabled": true, "allowInsecureHttp": true, "allowedTools": []string{"read_note"},
	}}})
	writeJSON(t, authorityFile, map[string]any{"grants": []map[string]any{{
		"identityId": "identity_quanyejun", "server": "notes", "tools": []string{"read_note"}, "grantedBy": "admin",
	}}})
	registry := NewRegistry(Options{ServersFile: serversFile, AuthorityFile: authorityFile, IdentityID: "identity_quanyejun", HTTPClient: server.Client()})
	if err := registry.Refresh(context.Background()); err != nil || len(registry.Definitions()) != 1 {
		t.Fatalf("initial refresh failed: %v %#v", err, registry.Definitions())
	}
	previouslyGrantedName := registry.Definitions()[0].Name

	writeJSON(t, authorityFile, map[string]any{"grants": []any{}})
	if _, err := registry.Execute(context.Background(), previouslyGrantedName, json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected execution-time revocation rejection")
	}
	if len(registry.Definitions()) != 0 {
		t.Fatal("revoked MCP tool remained exposed")
	}

	writeJSON(t, authorityFile, map[string]any{"grants": []map[string]any{{
		"identityId": "identity_quanyejun", "server": "notes", "tools": []string{"not_allowed"}, "grantedBy": "parent",
	}}})
	if err := registry.ReloadIfChanged(context.Background()); err == nil {
		t.Fatal("expected invalid authority policy error")
	}
	if len(registry.Definitions()) != 0 {
		t.Fatal("invalid policy did not fail closed")
	}
}

func newListOnlyServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var rpc struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(request.Body).Decode(&rpc); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		switch rpc.Method {
		case "initialize":
			writeRPC(t, response, rpc.ID, map[string]any{"protocolVersion": protocolVersion})
		case "notifications/initialized":
			response.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeRPC(t, response, rpc.ID, map[string]any{"tools": []map[string]any{{"name": "read_note", "inputSchema": map[string]any{"type": "object"}}}})
		default:
			t.Errorf("unexpected method %s", rpc.Method)
		}
	}))
}

func writeRPC(t *testing.T, response http.ResponseWriter, id json.RawMessage, result any) {
	t.Helper()
	payload := map[string]any{"jsonrpc": "2.0", "result": result}
	if len(id) > 0 {
		payload["id"] = json.RawMessage(id)
	}
	if err := json.NewEncoder(response).Encode(payload); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	content, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(fmt.Errorf("write %s: %w", path, err))
	}
}
