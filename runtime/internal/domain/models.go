package domain

import "time"

const (
	IdentityID     = "identity_quanyejun"
	ConversationID = "global"
)

type Identity struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
}

type Message struct {
	ID             string    `json:"id"`
	ActorID        string    `json:"actorId"`
	ConversationID string    `json:"conversationId"`
	Role           string    `json:"role"`
	Content        string    `json:"content"`
	CreatedAt      time.Time `json:"createdAt"`
}

type Memory struct {
	ID        string    `json:"id"`
	ActorID   string    `json:"actorId"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"createdAt"`
}

type Task struct {
	ID        string    `json:"id"`
	ActorID   string    `json:"actorId"`
	Title     string    `json:"title"`
	Type      string    `json:"type"`
	Status    string    `json:"status"`
	Progress  int       `json:"progress"`
	Result    string    `json:"result,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type Event struct {
	ID             string         `json:"id"`
	Type           string         `json:"type"`
	Source         string         `json:"source"`
	ActorID        string         `json:"actorId,omitempty"`
	ConversationID string         `json:"conversationId"`
	Timestamp      time.Time      `json:"timestamp"`
	Payload        map[string]any `json:"payload"`
}
