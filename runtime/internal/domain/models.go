package domain

import (
	"encoding/json"
	"strings"
	"time"
)

const (
	IdentityID         = "identity_quanyejun"
	ConversationID     = "global"
	DefaultDisplayName = "QuanYejun"
)

func NormalizeDisplayName(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return DefaultDisplayName
	}
	runes := []rune(value)
	if len(runes) > 40 {
		value = string(runes[:40])
	}
	return value
}

type Identity struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
}

type Message struct {
	ExpiresAt      *time.Time `json:"expiresAt,omitempty"`
	ID             string     `json:"id"`
	ActorID        string     `json:"actorId"`
	ConversationID string     `json:"conversationId"`
	Role           string     `json:"role"`
	Content        string     `json:"content"`
	CreatedAt      time.Time  `json:"createdAt"`
}

type Memory struct {
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	ID        string     `json:"id"`
	ActorID   string     `json:"actorId"`
	Content   string     `json:"content"`
	CreatedAt time.Time  `json:"createdAt"`
}

type Task struct {
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	ID        string     `json:"id"`
	ActorID   string     `json:"actorId"`
	Title     string     `json:"title"`
	Type      string     `json:"type"`
	Status    string     `json:"status"`
	Progress  int        `json:"progress"`
	Result    string     `json:"result,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
}

type Event struct {
	ExpiresAt      *time.Time     `json:"expiresAt,omitempty"`
	ID             string         `json:"id"`
	Type           string         `json:"type"`
	Source         string         `json:"source"`
	ActorID        string         `json:"actorId,omitempty"`
	ConversationID string         `json:"conversationId"`
	Timestamp      time.Time      `json:"timestamp"`
	Payload        map[string]any `json:"payload"`
}

type AgentRun struct {
	Visitor        VisitorContext `json:"visitor"`
	ExpiresAt      *time.Time     `json:"expiresAt,omitempty"`
	ID             string         `json:"id"`
	IdentityID     string         `json:"identityId"`
	ActorID        string         `json:"actorId"`
	ConversationID string         `json:"conversationId"`
	ParentRunID    string         `json:"parentRunId,omitempty"`
	InputMessageID string         `json:"inputMessageId,omitempty"`
	Kind           string         `json:"kind"`
	Provider       string         `json:"provider"`
	Model          string         `json:"model,omitempty"`
	Status         string         `json:"status"`
	Goal           string         `json:"goal"`
	Result         string         `json:"result,omitempty"`
	Error          string         `json:"error,omitempty"`
	Attempt        int            `json:"attempt"`
	CreatedAt      time.Time      `json:"createdAt"`
	UpdatedAt      time.Time      `json:"updatedAt"`
	StartedAt      time.Time      `json:"startedAt,omitempty"`
	CompletedAt    time.Time      `json:"completedAt,omitempty"`
}

type RunStep struct {
	ExpiresAt          *time.Time      `json:"expiresAt,omitempty"`
	ID                 string          `json:"id"`
	RunID              string          `json:"runId"`
	Sequence           int             `json:"sequence"`
	Kind               string          `json:"kind"`
	Status             string          `json:"status"`
	ProviderResponseID string          `json:"providerResponseId,omitempty"`
	CallID             string          `json:"callId,omitempty"`
	ToolName           string          `json:"toolName,omitempty"`
	Input              json.RawMessage `json:"input,omitempty"`
	Output             json.RawMessage `json:"output,omitempty"`
	Error              string          `json:"error,omitempty"`
	CreatedAt          time.Time       `json:"createdAt"`
	CompletedAt        time.Time       `json:"completedAt,omitempty"`
}
