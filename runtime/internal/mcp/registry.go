package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/quanyejun/quanyejun/runtime/internal/llm"
)

type Options struct {
	ServersFile   string
	AuthorityFile string
	IdentityID    string
	Getenv        func(string) string
	HTTPClient    *http.Client
}

type Registry struct {
	mu          sync.RWMutex
	refreshMu   sync.Mutex
	options     Options
	definitions []llm.ToolDefinition
	bindings    map[string]binding
	signature   string
}

type binding struct {
	serverName string
	remoteName string
	client     *Client
}

type Execution struct {
	Output     string
	ServerName string
	ToolName   string
}

func NewRegistry(options Options) *Registry {
	if options.Getenv == nil {
		options.Getenv = os.Getenv
	}
	return &Registry{options: options, bindings: make(map[string]binding)}
}

func (r *Registry) Definitions() []llm.ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]llm.ToolDefinition, len(r.definitions))
	copy(result, r.definitions)
	return result
}

func (r *Registry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, exists := r.bindings[name]
	return exists
}

func (r *Registry) Refresh(ctx context.Context) error {
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()

	var serversConfig ServersConfig
	serverBytes, err := loadConfig(r.options.ServersFile, &serversConfig)
	if err != nil {
		r.replace(nil, nil, digestPair(serverBytes, nil))
		return err
	}
	var authorityConfig AuthorityConfig
	authorityBytes, err := loadConfig(r.options.AuthorityFile, &authorityConfig)
	if err != nil {
		r.replace(nil, nil, digestPair(serverBytes, authorityBytes))
		return err
	}
	signature := digestPair(serverBytes, authorityBytes)
	if len(serverBytes) == 0 && len(authorityBytes) == 0 {
		r.replace(nil, nil, signature)
		return nil
	}

	servers := make(map[string]ServerConfig, len(serversConfig.Servers))
	for _, server := range serversConfig.Servers {
		if err := validateServer(server); err != nil {
			r.replace(nil, nil, signature)
			return err
		}
		if _, exists := servers[server.Name]; exists {
			r.replace(nil, nil, signature)
			return fmt.Errorf("duplicate MCP server %q", server.Name)
		}
		servers[server.Name] = server
	}
	if err := validateAuthority(authorityConfig, servers); err != nil {
		r.replace(nil, nil, signature)
		return err
	}

	granted := make(map[string]map[string]struct{})
	for _, grant := range authorityConfig.Grants {
		if grant.IdentityID != r.options.IdentityID {
			continue
		}
		if granted[grant.Server] == nil {
			granted[grant.Server] = make(map[string]struct{})
		}
		for _, tool := range grant.Tools {
			granted[grant.Server][tool] = struct{}{}
		}
	}

	definitions := make([]llm.ToolDefinition, 0)
	bindings := make(map[string]binding)
	var refreshErrors []error
	for _, server := range serversConfig.Servers {
		if !server.Enabled || len(granted[server.Name]) == 0 {
			continue
		}
		headers, err := resolveHeaders(server, r.options.Getenv)
		if err != nil {
			refreshErrors = append(refreshErrors, err)
			continue
		}
		client := NewClient(server, headers, r.options.HTTPClient)
		remoteTools, err := client.ListTools(ctx)
		if err != nil {
			refreshErrors = append(refreshErrors, err)
			continue
		}
		allowed := stringSet(server.AllowedTools)
		for _, tool := range remoteTools {
			if _, ok := allowed[tool.Name]; !ok {
				continue
			}
			if _, ok := granted[server.Name][tool.Name]; !ok {
				continue
			}
			externalName := externalToolName(server.Name, tool.Name)
			if _, collision := bindings[externalName]; collision {
				refreshErrors = append(refreshErrors, fmt.Errorf("MCP tool name collision for %s/%s", server.Name, tool.Name))
				continue
			}
			schema := tool.InputSchema
			if schema == nil {
				schema = map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{}}
			}
			if schemaType, ok := schema["type"].(string); !ok || schemaType != "object" {
				refreshErrors = append(refreshErrors, fmt.Errorf("MCP tool %s/%s must provide an object inputSchema", server.Name, tool.Name))
				continue
			}
			description := strings.TrimSpace(tool.Description)
			if len(description) > 1000 {
				description = description[:1000]
			}
			description = fmt.Sprintf("MCP tool from administrator-configured server %q. %s", server.Name, description)
			strict := false
			definitions = append(definitions, llm.ToolDefinition{Name: externalName, Description: description, Parameters: schema, Strict: &strict})
			bindings[externalName] = binding{serverName: server.Name, remoteName: tool.Name, client: client}
		}
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].Name < definitions[j].Name })
	if len(refreshErrors) > 0 {
		// Keep retrying transient discovery/auth failures even when config files do not change.
		signature = ""
	}
	r.replace(definitions, bindings, signature)
	return errors.Join(refreshErrors...)
}

func (r *Registry) ReloadIfChanged(ctx context.Context) error {
	serverBytes, serverErr := readConfigBytes(r.options.ServersFile)
	authorityBytes, authorityErr := readConfigBytes(r.options.AuthorityFile)
	if serverErr != nil || authorityErr != nil {
		return errors.Join(serverErr, authorityErr)
	}
	signature := digestPair(serverBytes, authorityBytes)
	r.mu.RLock()
	unchanged := signature == r.signature
	r.mu.RUnlock()
	if unchanged {
		return nil
	}
	return r.Refresh(ctx)
}

func (r *Registry) Execute(ctx context.Context, name string, arguments json.RawMessage) (Execution, error) {
	if err := r.ReloadIfChanged(ctx); err != nil {
		return Execution{}, fmt.Errorf("reload MCP policy: %w", err)
	}
	r.mu.RLock()
	bound, exists := r.bindings[name]
	r.mu.RUnlock()
	if !exists {
		return Execution{}, fmt.Errorf("MCP tool %q is not granted", name)
	}
	result, err := bound.client.CallTool(ctx, bound.remoteName, arguments)
	if err != nil {
		return Execution{}, err
	}
	encoded, err := json.Marshal(map[string]any{"server": bound.serverName, "tool": bound.remoteName, "result": json.RawMessage(result)})
	if err != nil {
		return Execution{}, fmt.Errorf("encode MCP result: %w", err)
	}
	return Execution{Output: string(encoded), ServerName: bound.serverName, ToolName: bound.remoteName}, nil
}

func (r *Registry) replace(definitions []llm.ToolDefinition, bindings map[string]binding, signature string) {
	if definitions == nil {
		definitions = []llm.ToolDefinition{}
	}
	if bindings == nil {
		bindings = make(map[string]binding)
	}
	r.mu.Lock()
	r.definitions = definitions
	r.bindings = bindings
	r.signature = signature
	r.mu.Unlock()
}

func externalToolName(server, tool string) string {
	raw := server + "__" + tool
	var builder strings.Builder
	for _, char := range raw {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' || char == '-' {
			builder.WriteRune(char)
		} else {
			builder.WriteByte('_')
		}
	}
	base := "mcp__" + builder.String()
	if base != "mcp__"+raw || len(base) > 64 {
		digest := sha256.Sum256([]byte(raw))
		suffix := "_" + hex.EncodeToString(digest[:4])
		limit := 64 - len(suffix)
		if len(base) > limit {
			base = base[:limit]
		}
		base += suffix
	}
	return base
}

func digestPair(first, second []byte) string {
	digest := sha256.New()
	digest.Write(first)
	digest.Write([]byte{0})
	digest.Write(second)
	return hex.EncodeToString(digest.Sum(nil))
}
