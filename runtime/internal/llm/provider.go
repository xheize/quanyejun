package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

type Provider interface {
	Generate(context.Context, Request) (string, error)
	Name() string
}

type LocalProvider struct{}

func NewLocalProvider() *LocalProvider { return &LocalProvider{} }
func (*LocalProvider) Name() string    { return "local" }
func (*LocalProvider) Generate(_ context.Context, request Request) (string, error) {
	if len(request.Messages) == 0 {
		return "무엇을 함께 해볼까요?", nil
	}
	latest := request.Messages[len(request.Messages)-1].Content
	if strings.Contains(latest, "기억해") {
		return "알겠어요. 이 내용은 대화 기록과 별도로 기억에 남겨두었습니다.", nil
	}
	return fmt.Sprintf("말씀하신 내용을 이어서 이해했어요: “%s”\n\n지금은 로컬 추론 엔진으로 응답하고 있습니다. OPENAI_API_KEY와 LLM_MODEL을 설정하면 Identity와 기억은 그대로 둔 채 추론 엔진만 교체됩니다.", latest), nil
}

type OpenAICompatibleProvider struct {
	apiKey string
	model  string
	url    string
	client *http.Client
}

func NewOpenAICompatibleProvider(apiKey, model, baseURL string) *OpenAICompatibleProvider {
	return &OpenAICompatibleProvider{apiKey: apiKey, model: model, url: strings.TrimRight(baseURL, "/") + "/chat/completions", client: &http.Client{Timeout: 45 * time.Second}}
}
func (p *OpenAICompatibleProvider) Name() string { return "openai-compatible" }

func (p *OpenAICompatibleProvider) Generate(ctx context.Context, input Request) (string, error) {
	messages := []map[string]string{{"role": "system", "content": systemPrompt(input.Memories)}}
	for _, message := range input.Messages {
		messages = append(messages, map[string]string{"role": message.Role, "content": message.Content})
	}
	payload, err := json.Marshal(map[string]any{"model": p.model, "messages": messages, "temperature": 0.4})
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+p.apiKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := p.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("invoke provider: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("provider returned %s", response.Status)
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return "", err
	}
	if len(result.Choices) == 0 || strings.TrimSpace(result.Choices[0].Message.Content) == "" {
		return "", fmt.Errorf("provider returned an empty response")
	}
	return strings.TrimSpace(result.Choices[0].Message.Content), nil
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
