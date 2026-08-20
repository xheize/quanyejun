package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	protocolVersion   = "2025-06-18"
	maxResponseBytes  = 1024 * 1024
	maxToolsPerServer = 128
)

type RemoteTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations,omitempty"`
}

type Client struct {
	mu         sync.Mutex
	server     ServerConfig
	headers    http.Header
	httpClient *http.Client
	sessionID  string
	requestID  int64
	ready      bool
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type httpStatusError struct {
	server string
	status int
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("MCP server %q returned HTTP %d", e.server, e.status)
}

func NewClient(server ServerConfig, headers http.Header, httpClient *http.Client) *Client {
	if httpClient == nil {
		timeout := 30 * time.Second
		if server.TimeoutSeconds > 0 {
			timeout = time.Duration(server.TimeoutSeconds) * time.Second
		}
		httpClient = &http.Client{Timeout: timeout}
	}
	return &Client{server: server, headers: headers.Clone(), httpClient: httpClient}
}

func (c *Client) ListTools(ctx context.Context) ([]RemoteTool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.initialize(ctx); err != nil {
		return nil, err
	}
	tools := make([]RemoteTool, 0)
	cursor := ""
	for page := 0; page < 10; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		result, err := c.call(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		var response struct {
			Tools      []RemoteTool `json:"tools"`
			NextCursor string       `json:"nextCursor,omitempty"`
		}
		if err := json.Unmarshal(result, &response); err != nil {
			return nil, fmt.Errorf("decode MCP tools/list result: %w", err)
		}
		tools = append(tools, response.Tools...)
		if len(tools) > maxToolsPerServer {
			return nil, fmt.Errorf("MCP server %q returned more than %d tools", c.server.Name, maxToolsPerServer)
		}
		if response.NextCursor == "" {
			return tools, nil
		}
		cursor = response.NextCursor
	}
	return nil, fmt.Errorf("MCP server %q tools/list exceeded pagination limit", c.server.Name)
}

func (c *Client) CallTool(ctx context.Context, name string, arguments json.RawMessage) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.initialize(ctx); err != nil {
		return nil, err
	}
	var decoded any
	if len(arguments) == 0 {
		decoded = map[string]any{}
	} else if err := json.Unmarshal(arguments, &decoded); err != nil {
		return nil, fmt.Errorf("invalid MCP tool arguments: %w", err)
	}
	result, err := c.call(ctx, "tools/call", map[string]any{"name": name, "arguments": decoded})
	var statusError *httpStatusError
	if err != nil && c.sessionID != "" && errors.As(err, &statusError) && statusError.status == http.StatusNotFound {
		c.sessionID = ""
		c.ready = false
		if initializeErr := c.initialize(ctx); initializeErr != nil {
			return nil, initializeErr
		}
		return c.call(ctx, "tools/call", map[string]any{"name": name, "arguments": decoded})
	}
	return result, err
}

func (c *Client) initialize(ctx context.Context) error {
	if c.ready {
		return nil
	}
	result, err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "quanyejun-runtime", "version": "0.1.0"},
	})
	if err != nil {
		return fmt.Errorf("initialize MCP server %q: %w", c.server.Name, err)
	}
	var initialized struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(result, &initialized); err != nil || initialized.ProtocolVersion == "" {
		return fmt.Errorf("MCP server %q returned an invalid initialize result", c.server.Name)
	}
	if err := c.notify(ctx, "notifications/initialized", map[string]any{}); err != nil {
		return fmt.Errorf("notify MCP server %q: %w", c.server.Name, err)
	}
	c.ready = true
	return nil
}

func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.requestID++
	payload := map[string]any{"jsonrpc": "2.0", "id": c.requestID, "method": method, "params": params}
	response, err := c.post(ctx, payload, false)
	if err != nil {
		return nil, err
	}
	if response.Error != nil {
		message := strings.TrimSpace(response.Error.Message)
		if len(message) > 500 {
			message = message[:500]
		}
		return nil, fmt.Errorf("MCP error %d: %s", response.Error.Code, message)
	}
	if len(response.Result) == 0 {
		return nil, fmt.Errorf("MCP method %s returned no result", method)
	}
	return response.Result, nil
}

func (c *Client) notify(ctx context.Context, method string, params any) error {
	payload := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	_, err := c.post(ctx, payload, true)
	return err
}

func (c *Client) post(ctx context.Context, payload any, notification bool) (rpcResponse, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return rpcResponse{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.server.URL, bytes.NewReader(body))
	if err != nil {
		return rpcResponse{}, fmt.Errorf("create MCP request: %w", err)
	}
	for name, values := range c.headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("MCP-Protocol-Version", protocolVersion)
	if c.sessionID != "" {
		request.Header.Set("MCP-Session-Id", c.sessionID)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return rpcResponse{}, fmt.Errorf("call MCP server %q: %w", c.server.Name, err)
	}
	defer response.Body.Close()
	if sessionID := strings.TrimSpace(response.Header.Get("MCP-Session-Id")); sessionID != "" {
		c.sessionID = sessionID
	}
	if notification && response.StatusCode >= 200 && response.StatusCode < 300 {
		return rpcResponse{}, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return rpcResponse{}, &httpStatusError{server: c.server.Name, status: response.StatusCode}
	}
	mediaType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	limited := io.LimitReader(response.Body, maxResponseBytes+1)
	var decoded rpcResponse
	if mediaType == "text/event-stream" {
		decoded, err = decodeSSE(limited)
	} else {
		content, readErr := io.ReadAll(limited)
		if readErr != nil {
			return rpcResponse{}, fmt.Errorf("read MCP response: %w", readErr)
		}
		if len(content) > maxResponseBytes {
			return rpcResponse{}, fmt.Errorf("MCP server %q response exceeded %d bytes", c.server.Name, maxResponseBytes)
		}
		err = json.Unmarshal(content, &decoded)
	}
	if err != nil {
		return rpcResponse{}, fmt.Errorf("decode MCP response: %w", err)
	}
	return decoded, nil
}

func decodeSSE(reader io.Reader) (rpcResponse, error) {
	scanner := bufio.NewScanner(io.LimitReader(reader, maxResponseBytes+1))
	scanner.Buffer(make([]byte, 4096), maxResponseBytes)
	var data strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if data.Len() > 0 {
				var response rpcResponse
				if err := json.Unmarshal([]byte(strings.TrimSpace(data.String())), &response); err == nil && isRPCResponse(response) {
					return response, nil
				}
				data.Reset()
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return rpcResponse{}, err
	}
	if data.Len() > 0 {
		var response rpcResponse
		if err := json.Unmarshal([]byte(strings.TrimSpace(data.String())), &response); err == nil && isRPCResponse(response) {
			return response, nil
		}
	}
	return rpcResponse{}, fmt.Errorf("SSE stream contained no JSON-RPC response")
}

func isRPCResponse(response rpcResponse) bool {
	return len(response.ID) > 0 && (len(response.Result) > 0 || response.Error != nil)
}
