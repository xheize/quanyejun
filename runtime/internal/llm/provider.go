package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/quanyejun/quanyejun/runtime/internal/domain"
)

type Request struct {
	ActorID  string
	Messages []domain.Message
	Memories []domain.Memory
}

type ProviderInfo struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Model  string `json:"model,omitempty"`
	Source string `json:"source"`
}

type Provider interface {
	Generate(context.Context, Request) (string, error)
	Name() string
	Info() ProviderInfo
}

type BuiltinProvider struct {
	info ProviderInfo
}

func NewBuiltinProvider() *BuiltinProvider {
	return &BuiltinProvider{info: ProviderInfo{ID: "builtin", Label: "Built-in preview", Source: "fallback"}}
}

// NewLocalProvider is kept for callers that used the original deterministic
// preview provider. A real local model is represented by local-openai.
func NewLocalProvider() *BuiltinProvider { return NewBuiltinProvider() }
func (p *BuiltinProvider) Name() string  { return p.info.ID }
func (p *BuiltinProvider) Info() ProviderInfo {
	return p.info
}
func (*BuiltinProvider) Generate(_ context.Context, request Request) (string, error) {
	if len(request.Messages) == 0 {
		return "무엇을 함께 해볼까요?", nil
	}
	latest := request.Messages[len(request.Messages)-1].Content
	if strings.Contains(latest, "기억해") {
		return "알겠어요. 이 내용은 대화 기록과 별도로 기억에 남겨두었습니다.", nil
	}
	return fmt.Sprintf("말씀하신 내용을 이어서 이해했어요: “%s”\n\n현재는 내장 미리보기 응답기를 사용 중입니다. 로컬 모델, Claude API/Code, OpenAI API 또는 Codex CLI가 준비되면 Identity와 기억은 그대로 둔 채 자동으로 추론 엔진을 교체합니다.", latest), nil
}

type ChatCompletionsProvider struct {
	info   ProviderInfo
	apiKey string
	url    string
	client *http.Client
}

func NewLocalOpenAIProvider(apiKey, model, baseURL, source string) *ChatCompletionsProvider {
	return &ChatCompletionsProvider{
		info:   ProviderInfo{ID: "local", Label: "Local LLM", Model: model, Source: source},
		apiKey: apiKey,
		url:    endpoint(baseURL, "chat/completions"),
		client: defaultHTTPClient(),
	}
}

func (p *ChatCompletionsProvider) Name() string       { return p.info.ID }
func (p *ChatCompletionsProvider) Info() ProviderInfo { return p.info }

func (p *ChatCompletionsProvider) Generate(ctx context.Context, input Request) (string, error) {
	messages := []map[string]string{{"role": "system", "content": systemPrompt(input.Memories)}}
	for _, message := range input.Messages {
		messages = append(messages, map[string]string{"role": message.Role, "content": message.Content})
	}
	payload := map[string]any{"model": p.info.Model, "messages": messages, "temperature": 0.4}
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := postJSON(ctx, p.client, p.url, p.apiKey, nil, payload, &result); err != nil {
		return "", err
	}
	if len(result.Choices) == 0 || strings.TrimSpace(result.Choices[0].Message.Content) == "" {
		return "", fmt.Errorf("provider returned an empty response")
	}
	return strings.TrimSpace(result.Choices[0].Message.Content), nil
}

type OpenAIProvider struct {
	info   ProviderInfo
	apiKey string
	url    string
	client *http.Client
}

func NewOpenAIProvider(apiKey, model, baseURL, source string) *OpenAIProvider {
	return &OpenAIProvider{
		info:   ProviderInfo{ID: "openai-api", Label: "OpenAI API", Model: model, Source: source},
		apiKey: apiKey,
		url:    endpoint(baseURL, "responses"),
		client: defaultHTTPClient(),
	}
}

func (p *OpenAIProvider) Name() string       { return p.info.ID }
func (p *OpenAIProvider) Info() ProviderInfo { return p.info }

func (p *OpenAIProvider) Generate(ctx context.Context, input Request) (string, error) {
	messages := make([]map[string]string, 0, len(input.Messages))
	for _, message := range input.Messages {
		messages = append(messages, map[string]string{"role": message.Role, "content": message.Content})
	}
	payload := map[string]any{
		"model":        p.info.Model,
		"instructions": systemPrompt(input.Memories),
		"input":        messages,
	}
	var result struct {
		OutputText string `json:"output_text"`
		Output     []struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := postJSON(ctx, p.client, p.url, p.apiKey, nil, payload, &result); err != nil {
		return "", err
	}
	if text := strings.TrimSpace(result.OutputText); text != "" {
		return text, nil
	}
	for _, output := range result.Output {
		for _, content := range output.Content {
			if text := strings.TrimSpace(content.Text); text != "" {
				return text, nil
			}
		}
	}
	return "", fmt.Errorf("provider returned an empty response")
}

type AnthropicProvider struct {
	info       ProviderInfo
	apiKey     string
	url        string
	maxTokens  int
	apiVersion string
	client     *http.Client
}

func NewAnthropicProvider(apiKey, model, baseURL, source string, maxTokens int) *AnthropicProvider {
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(baseURL, "/v1") {
		baseURL += "/v1"
	}
	return &AnthropicProvider{
		info:       ProviderInfo{ID: "claude-api", Label: "Claude API", Model: model, Source: source},
		apiKey:     apiKey,
		url:        baseURL + "/messages",
		maxTokens:  maxTokens,
		apiVersion: "2023-06-01",
		client:     defaultHTTPClient(),
	}
}

func (p *AnthropicProvider) Name() string       { return p.info.ID }
func (p *AnthropicProvider) Info() ProviderInfo { return p.info }

func (p *AnthropicProvider) Generate(ctx context.Context, input Request) (string, error) {
	messages := make([]map[string]string, 0, len(input.Messages))
	for _, message := range input.Messages {
		if message.Role == "user" || message.Role == "assistant" {
			messages = append(messages, map[string]string{"role": message.Role, "content": message.Content})
		}
	}
	payload := map[string]any{
		"model":      p.info.Model,
		"max_tokens": p.maxTokens,
		"system":     systemPrompt(input.Memories),
		"messages":   messages,
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	headers := map[string]string{"x-api-key": p.apiKey, "anthropic-version": p.apiVersion}
	if err := postJSON(ctx, p.client, p.url, "", headers, payload, &result); err != nil {
		return "", err
	}
	for _, content := range result.Content {
		if content.Type == "text" && strings.TrimSpace(content.Text) != "" {
			return strings.TrimSpace(content.Text), nil
		}
	}
	return "", fmt.Errorf("provider returned an empty response")
}

func defaultHTTPClient() *http.Client {
	return &http.Client{Timeout: 90 * time.Second}
}

func endpoint(baseURL, path string) string {
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(path, "/")
}

func postJSON(ctx context.Context, client *http.Client, url, bearer string, headers map[string]string, payload, result any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode provider request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create provider request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("invoke provider: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		if text := strings.TrimSpace(string(detail)); text != "" {
			return fmt.Errorf("provider returned %s: %s", response.Status, text)
		}
		return fmt.Errorf("provider returned %s", response.Status)
	}
	if err := json.NewDecoder(response.Body).Decode(result); err != nil {
		return fmt.Errorf("decode provider response: %w", err)
	}
	return nil
}

func systemPrompt(memories []domain.Memory) string {
	var builder strings.Builder
	builder.WriteString("You are the current reasoning engine for QuanYejun, one persistent identity. Reply in the user's language. Be concise and natural. You are not a separate chat persona. Relevant persistent memories:\n")
	for _, memory := range memories {
		builder.WriteString("- ")
		builder.WriteString(memory.Content)
		builder.WriteByte('\n')
	}
	return builder.String()
}

func cliPrompt(input Request) string {
	var builder strings.Builder
	builder.WriteString(systemPrompt(input.Memories))
	builder.WriteString("\nThis is a text-generation request. Do not inspect or modify files, call tools, browse, or execute commands. Return only the reply to the user.\n\nConversation:\n")
	for _, message := range input.Messages {
		builder.WriteString(message.Role)
		builder.WriteString(": ")
		builder.WriteString(message.Content)
		builder.WriteByte('\n')
	}
	return builder.String()
}
