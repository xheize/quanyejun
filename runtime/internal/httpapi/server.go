package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/quanyejun/quanyejun/runtime/internal/domain"
	"github.com/quanyejun/quanyejun/runtime/internal/environment"
	"github.com/quanyejun/quanyejun/runtime/internal/eventhub"
	"github.com/quanyejun/quanyejun/runtime/internal/llm"
	"github.com/quanyejun/quanyejun/runtime/internal/store"
	"github.com/quanyejun/quanyejun/runtime/internal/tasks"
)

type Server struct {
	store          store.Store
	hub            *eventhub.Hub
	provider       llm.Provider
	runner         *tasks.Runner
	displayName    string
	environment    environment.Environment
	allowedOrigins map[string]struct{}
}

func New(store store.Store, hub *eventhub.Hub, provider llm.Provider, runner *tasks.Runner, origins []string, displayName string, executionEnvironment environment.Environment) *Server {
	allowed := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		allowed[strings.TrimSpace(origin)] = struct{}{}
	}
	return &Server{store: store, hub: hub, provider: provider, runner: runner, displayName: domain.NormalizeDisplayName(displayName), environment: executionEnvironment, allowedOrigins: allowed}
}

func (s *Server) Handler() http.Handler {
	router := gin.New()
	router.Use(gin.Logger(), gin.Recovery(), s.cors())
	router.GET("/healthz", s.health)
	router.GET("/api/bootstrap", s.bootstrap)
	router.GET("/api/environment", s.environmentInfo)
	router.POST("/api/messages", s.createMessage)
	router.POST("/api/tasks/demo", s.createDemoTask)
	router.GET("/api/events", s.listEvents)
	router.GET("/ws", s.websocket)
	return router
}

func (s *Server) cors() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if s.originAllowed(origin) {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
		}
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
		c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func (s *Server) originAllowed(origin string) bool {
	if origin == "" {
		return true
	}
	if _, ok := s.allowedOrigins["*"]; ok {
		return true
	}
	_, ok := s.allowedOrigins[origin]
	return ok
}

func (s *Server) health(c *gin.Context) {
	info, err := s.environment.Inspect(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "identity": domain.IdentityID, "displayName": s.displayName, "environment": info, "llmProvider": s.provider.Name(), "provider": s.provider.Info()})
}

func (s *Server) environmentInfo(c *gin.Context) {
	info, err := s.environment.Inspect(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, info)
}

func (s *Server) bootstrap(c *gin.Context) {
	actorID := actor(c)
	identity, err := s.store.EnsureIdentity(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	identity.Name = s.displayName
	messages, err := s.store.ListMessages(c.Request.Context(), actorID, 20)
	if err != nil {
		fail(c, err)
		return
	}
	memories, err := s.store.ListMemories(c.Request.Context(), actorID, 8)
	if err != nil {
		fail(c, err)
		return
	}
	tasksList, err := s.store.ListTasks(c.Request.Context(), actorID, 8)
	if err != nil {
		fail(c, err)
		return
	}
	events, err := s.store.ListEventsAfter(c.Request.Context(), "", 30)
	if err != nil {
		fail(c, err)
		return
	}
	environmentInfo, err := s.environment.Inspect(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"identity": identity, "environment": environmentInfo, "provider": s.provider.Info(), "messages": messages, "memories": memories, "tasks": tasksList, "events": events})
}

type messageInput struct {
	ActorID string `json:"actorId"`
	Content string `json:"content"`
}

func (s *Server) createMessage(c *gin.Context) {
	var input messageInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
		return
	}
	input.ActorID = strings.TrimSpace(input.ActorID)
	input.Content = strings.TrimSpace(input.Content)
	if input.ActorID == "" {
		input.ActorID = "person_example"
	}
	if input.Content == "" || len([]rune(input.Content)) > 8000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "content must be between 1 and 8000 characters"})
		return
	}
	now := time.Now().UTC()
	userMessage := domain.Message{ID: uuid.NewString(), ActorID: input.ActorID, ConversationID: domain.ConversationID, Role: "user", Content: input.Content, CreatedAt: now}
	if err := s.store.AddMessage(c.Request.Context(), userMessage); err != nil {
		fail(c, err)
		return
	}
	if err := s.emit(c.Request.Context(), "chat.message", "http", input.ActorID, map[string]any{"message": userMessage}); err != nil {
		fail(c, err)
		return
	}
	if strings.Contains(strings.ToLower(input.Content), "기억해") || strings.Contains(strings.ToLower(input.Content), "remember") {
		memory := domain.Memory{ID: uuid.NewString(), ActorID: input.ActorID, Content: input.Content, CreatedAt: now}
		if err := s.store.AddMemory(c.Request.Context(), memory); err != nil {
			fail(c, err)
			return
		}
		if err := s.emit(c.Request.Context(), "memory.created", "runtime", input.ActorID, map[string]any{"memory": memory}); err != nil {
			fail(c, err)
			return
		}
	}
	messages, err := s.store.ListMessages(c.Request.Context(), input.ActorID, 10)
	if err != nil {
		fail(c, err)
		return
	}
	memories, err := s.store.ListMemories(c.Request.Context(), input.ActorID, 5)
	if err != nil {
		fail(c, err)
		return
	}
	responseText, err := s.provider.Generate(c.Request.Context(), llm.Request{ActorID: input.ActorID, IdentityName: s.displayName, Messages: messages, Memories: memories})
	if err != nil {
		slog.Error("reasoning engine failed", "provider", s.provider.Name(), "error", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "reasoning engine failed"})
		return
	}
	assistantMessage := domain.Message{ID: uuid.NewString(), ActorID: input.ActorID, ConversationID: domain.ConversationID, Role: "assistant", Content: responseText, CreatedAt: time.Now().UTC()}
	if err := s.store.AddMessage(c.Request.Context(), assistantMessage); err != nil {
		fail(c, err)
		return
	}
	if err := s.emit(c.Request.Context(), "chat.message", "llm", input.ActorID, map[string]any{"message": assistantMessage}); err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"userMessage": userMessage, "assistantMessage": assistantMessage})
}

type taskInput struct {
	ActorID         string `json:"actorId"`
	DurationSeconds int    `json:"durationSeconds"`
}

func (s *Server) createDemoTask(c *gin.Context) {
	var input taskInput
	_ = c.ShouldBindJSON(&input)
	if input.ActorID == "" {
		input.ActorID = "person_example"
	}
	if input.DurationSeconds <= 0 {
		input.DurationSeconds = 12
	}
	if input.DurationSeconds > 120 {
		input.DurationSeconds = 120
	}
	task, err := s.runner.StartDemo(input.ActorID, time.Duration(input.DurationSeconds)*time.Second)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusAccepted, task)
}
func (s *Server) listEvents(c *gin.Context) {
	events, err := s.store.ListEventsAfter(c.Request.Context(), c.Query("after"), 100)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, events)
}

func (s *Server) websocket(c *gin.Context) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return s.originAllowed(r.Header.Get("Origin")) }}
	connection, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer connection.Close()
	connection.SetReadLimit(4096)
	_ = connection.SetReadDeadline(time.Now().Add(70 * time.Second))
	connection.SetPongHandler(func(string) error { return connection.SetReadDeadline(time.Now().Add(70 * time.Second)) })
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := connection.ReadMessage(); err != nil {
				return
			}
		}
	}()
	replay, err := s.store.ListEventsAfter(c.Request.Context(), c.Query("lastEventId"), 100)
	if err != nil {
		return
	}
	for _, event := range replay {
		if err := connection.WriteJSON(event); err != nil {
			return
		}
	}
	events, unsubscribe := s.hub.Subscribe()
	defer unsubscribe()
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return
			}
			if err := connection.WriteJSON(event); err != nil {
				return
			}
		case <-ticker.C:
			if err := connection.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(5*time.Second)); err != nil {
				return
			}
		case <-done:
			return
		case <-c.Request.Context().Done():
			return
		}
	}
}

func (s *Server) emit(ctx context.Context, eventType, source, actorID string, payload map[string]any) error {
	event := domain.Event{ID: uuid.NewString(), Type: eventType, Source: source, ActorID: actorID, ConversationID: domain.ConversationID, Timestamp: time.Now().UTC(), Payload: payload}
	if err := s.store.AppendEvent(ctx, event); err != nil {
		return err
	}
	s.hub.Publish(event)
	return nil
}
func actor(c *gin.Context) string {
	value := strings.TrimSpace(c.Query("actorId"))
	if value == "" {
		return "person_example"
	}
	return value
}
func fail(c *gin.Context, _ error) {
	c.JSON(http.StatusInternalServerError, gin.H{"error": "runtime operation failed"})
}
