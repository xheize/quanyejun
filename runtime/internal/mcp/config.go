package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
)

const maxConfigBytes = 256 * 1024

var (
	validServerName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
	validEnvName    = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
)

type ServersConfig struct {
	Servers []ServerConfig `json:"servers"`
}

type ServerConfig struct {
	Name              string            `json:"name"`
	Description       string            `json:"description,omitempty"`
	URL               string            `json:"url"`
	Enabled           bool              `json:"enabled"`
	AllowedTools      []string          `json:"allowedTools"`
	HeaderEnv         map[string]string `json:"headerEnv,omitempty"`
	AllowInsecureHTTP bool              `json:"allowInsecureHttp,omitempty"`
	TimeoutSeconds    int               `json:"timeoutSeconds,omitempty"`
}

type AuthorityConfig struct {
	Grants []Grant `json:"grants"`
}

type Grant struct {
	IdentityID string   `json:"identityId"`
	Server     string   `json:"server"`
	Tools      []string `json:"tools"`
	GrantedBy  string   `json:"grantedBy"`
}

func loadConfig(path string, target any) ([]byte, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(content) > maxConfigBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", path, maxConfigBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return content, fmt.Errorf("decode %s: %w", path, err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return content, fmt.Errorf("decode %s: trailing content", path)
	}
	return content, nil
}

func validateServer(server ServerConfig) error {
	if !validServerName.MatchString(server.Name) {
		return fmt.Errorf("invalid MCP server name %q", server.Name)
	}
	parsed, err := url.Parse(server.URL)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("MCP server %q has an invalid URL", server.Name)
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && server.AllowInsecureHTTP) {
		return fmt.Errorf("MCP server %q must use HTTPS unless allowInsecureHttp is explicitly enabled", server.Name)
	}
	if len(server.AllowedTools) == 0 {
		return fmt.Errorf("MCP server %q requires an explicit allowedTools list", server.Name)
	}
	if server.TimeoutSeconds != 0 && (server.TimeoutSeconds < 1 || server.TimeoutSeconds > 120) {
		return fmt.Errorf("MCP server %q timeoutSeconds must be between 1 and 120", server.Name)
	}
	seen := make(map[string]struct{}, len(server.AllowedTools))
	for _, tool := range server.AllowedTools {
		tool = strings.TrimSpace(tool)
		if tool == "" || tool == "*" {
			return fmt.Errorf("MCP server %q must allow tools by exact name", server.Name)
		}
		if _, exists := seen[tool]; exists {
			return fmt.Errorf("MCP server %q repeats allowed tool %q", server.Name, tool)
		}
		seen[tool] = struct{}{}
	}
	for header, environmentName := range server.HeaderEnv {
		canonical := http.CanonicalHeaderKey(strings.TrimSpace(header))
		if canonical == "" || isReservedHeader(canonical) || !validEnvName.MatchString(environmentName) {
			return fmt.Errorf("MCP server %q has an invalid headerEnv entry", server.Name)
		}
	}
	return nil
}

func validateAuthority(authority AuthorityConfig, servers map[string]ServerConfig) error {
	for _, grant := range authority.Grants {
		if grant.IdentityID == "" || grant.Server == "" || len(grant.Tools) == 0 {
			return fmt.Errorf("MCP authority grants require identityId, server, and tools")
		}
		if grant.GrantedBy != "parent" && grant.GrantedBy != "admin" {
			return fmt.Errorf("MCP authority grant for %q must be grantedBy parent or admin", grant.Server)
		}
		server, exists := servers[grant.Server]
		if !exists {
			return fmt.Errorf("MCP authority references unknown server %q", grant.Server)
		}
		allowed := stringSet(server.AllowedTools)
		for _, tool := range grant.Tools {
			if _, exists := allowed[tool]; !exists {
				return fmt.Errorf("MCP authority grants tool %q outside server %q allowedTools", tool, grant.Server)
			}
		}
	}
	return nil
}

func readConfigBytes(path string) ([]byte, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil {
		return nil, err
	}
	if len(content) > maxConfigBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", path, maxConfigBytes)
	}
	return content, nil
}

func resolveHeaders(server ServerConfig, getenv func(string) string) (http.Header, error) {
	headers := make(http.Header, len(server.HeaderEnv))
	for header, environmentName := range server.HeaderEnv {
		value := strings.TrimSpace(getenv(environmentName))
		if value == "" {
			return nil, fmt.Errorf("MCP server %q requires environment variable %s", server.Name, environmentName)
		}
		headers.Set(header, value)
	}
	return headers, nil
}

func isReservedHeader(header string) bool {
	switch strings.ToLower(header) {
	case "accept", "content-length", "content-type", "host", "mcp-protocol-version", "mcp-session-id":
		return true
	default:
		return false
	}
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[strings.TrimSpace(value)] = struct{}{}
	}
	return result
}
