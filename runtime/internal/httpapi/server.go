package httpapi

import (
	"context"
	"crypto/rand"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/quanyejun/quanyejun/runtime/internal/agent"
	"github.com/quanyejun/quanyejun/runtime/internal/domain"
	"github.com/quanyejun/quanyejun/runtime/internal/environment"
	"github.com/quanyejun/quanyejun/runtime/internal/eventhub"
	"github.com/quanyejun/quanyejun/runtime/internal/llm"
	"github.com/quanyejun/quanyejun/runtime/internal/store"
	"github.com/quanyejun/quanyejun/runtime/internal/tasks"
)

type Server struct {
	auth           *Auth
	store          store.Store
	hub            *eventhub.Hub
	provider       llm.Provider
	agentRunner    *agent.Runner
	taskRunner     *tasks.Runner
	displayName    string
	environment    environment.Environment
	allowedOrigins map[string]struct{}
}

func New(store store.Store, hub *eventhub.Hub, provider llm.Provider, agentRunner *agent.Runner, taskRunner *tasks.Runner, origins []string, displayName string, executionEnvironment environment.Environment) *Server {
	allowed := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		allowed[strings.TrimSpace(origin)] = struct{}{}
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	auth, err := NewAuth(AuthConfig{Key: key})
	if err != nil {
		panic(err)
	}
	return &Server{auth: auth, store: store, hub: hub, provider: provider, agentRunner: agentRunner, taskRunner: taskRunner, displayName: domain.NormalizeDisplayName(displayName), environment: executionEnvironment, allowedOrigins: allowed}
}

func (s *Server) Handler() http.Handler {
	router := gin.New()
	router.Use(gin.Recovery(), s.cors())
	// Do not log callback codes, cookies, or anonymous request contents.
	router.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Header("Referrer-Policy", "no-referrer")
		c.Next()
	})
	router.GET("/auth/login", s.auth.login)
	router.GET("/auth/callback", s.auth.callback)
	router.POST("/auth/logout", s.auth.logout)
	router.GET("/healthz", s.health)
	router.Use(s.auth.identify, s.visitorProfile)
	router.GET("/api/session", func(c *gin.Context) { c.JSON(200, principal(c)) })
	router.POST("/api/session/display-name", s.setDisplayName)
	router.GET("/api/bootstrap", s.bootstrap)
	router.GET("/api/environment", s.environmentInfo)
	router.POST("/api/messages", s.createMessage)
	router.GET("/api/runs/:id", s.getRun)
	router.POST("/api/tasks/demo", s.createDemoTask)
	router.GET("/api/events", s.listEvents)
	router.GET("/ws", s.websocket)
	return router
}

func (s *Server) cors() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" && !s.originAllowed(origin) {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		if origin != "" {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Credentials", "true")
		}
		c.Header("Access-Control-Allow-Headers", "Content-Type, X-Requested-With")
		c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead && c.GetHeader("X-Requested-With") != "QuanYejun" {
			c.AbortWithStatus(http.StatusForbidden)
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
	runs, err := s.store.ListAgentRuns(c.Request.Context(), actorID, 8)
	if err != nil {
		fail(c, err)
		return
	}
	events, err := s.store.ListEventsAfter(c.Request.Context(), actorID, "", 30)
	if err != nil {
		fail(c, err)
		return
	}
	environmentInfo, err := s.environment.Inspect(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"session": principal(c), "identity": identity, "environment": environmentInfo, "provider": s.provider.Info(), "messages": messages, "memories": memories, "tasks": tasksList, "runs": runs, "events": events})
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
	input.ActorID = actor(c)
	input.Content = strings.TrimSpace(input.Content)
	if input.Content == "" || len([]rune(input.Content)) > 8000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "content must be between 1 and 8000 characters"})
		return
	}
	now := time.Now().UTC()
	userMessage := domain.Message{ID: uuid.NewString(), ActorID: input.ActorID, ConversationID: domain.ConversationID, Role: "user", Content: input.Content, CreatedAt: now, ExpiresAt: domain.Expiry(c.Request.Context(), input.ActorID, now, nil)}
	if err := s.store.AddMessage(c.Request.Context(), userMessage); err != nil {
		fail(c, err)
		return
	}
	if err := s.emit(c.Request.Context(), "chat.message", "http", input.ActorID, map[string]any{"message": userMessage}); err != nil {
		fail(c, err)
		return
	}
	run, err := s.agentRunner.StartForVisitor(input.ActorID, userMessage.ID, input.Content, domain.VisitorContext{Role: principal(c).Role, DisplayName: principal(c).DisplayName}, principal(c).DisplayNameExpiresAt)
	if err != nil {
		slog.Error("agent run creation failed", "provider", s.provider.Name(), "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "agent run creation failed"})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"userMessage": userMessage, "runId": run.ID, "run": run})
}

func (s *Server) getRun(c *gin.Context) {
	run, err := s.store.GetAgentRun(c.Request.Context(), c.Param("id"))
	if err != nil || run.ActorID != actor(c) {
		c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
		return
	}
	steps, err := s.store.ListRunSteps(c.Request.Context(), run.ID)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"run": run, "steps": steps})
}

type taskInput struct {
	ActorID         string `json:"actorId"`
	DurationSeconds int    `json:"durationSeconds"`
}

func (s *Server) createDemoTask(c *gin.Context) {
	var input taskInput
	_ = c.ShouldBindJSON(&input)
	input.ActorID = actor(c)
	if input.DurationSeconds <= 0 {
		input.DurationSeconds = 12
	}
	if input.DurationSeconds > 120 {
		input.DurationSeconds = 120
	}
	task, err := s.taskRunner.StartDemo(input.ActorID, time.Duration(input.DurationSeconds)*time.Second)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusAccepted, task)
}
func (s *Server) listEvents(c *gin.Context) {
	events, err := s.store.ListEventsAfter(c.Request.Context(), actor(c), c.Query("after"), 100)
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
	events, unsubscribe := s.hub.Subscribe()
	defer unsubscribe()
	replay, err := s.store.ListEventsAfter(c.Request.Context(), actor(c), c.Query("lastEventId"), 100)
	if err != nil {
		return
	}
	for _, event := range replay {
		if event.ActorID != actor(c) || domain.Expired(event.ExpiresAt) {
			continue
		}
		if err := connection.WriteJSON(event); err != nil {
			return
		}
	}
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	sessionExpiry := time.Unix(c.GetInt64("sessionExpires"), 0)
	expiryTimer := time.NewTimer(time.Until(sessionExpiry))
	defer expiryTimer.Stop()
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return
			}
			if !sessionExpiry.After(time.Now()) {
				return
			}
			if event.ActorID != actor(c) || domain.Expired(event.ExpiresAt) {
				continue
			}
			if err := connection.WriteJSON(event); err != nil {
				return
			}
		case <-ticker.C:
			var session browserSession
			if !s.auth.open(c, sessionCookie, &session) || session.Expires <= time.Now().Unix() {
				return
			}
			if session.AccessToken != "" {
				if _, err := s.auth.userInfo(c, session.AccessToken); err != nil {
					return
				}
			}
			if err := connection.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(5*time.Second)); err != nil {
				return
			}
		case <-done:
			return
		case <-expiryTimer.C:
			return
		case <-c.Request.Context().Done():
			return
		}
	}
}

func (s *Server) emit(ctx context.Context, eventType, source, actorID string, payload map[string]any) error {
	event := domain.Event{ID: uuid.NewString(), Type: eventType, Source: source, ActorID: actorID, ConversationID: domain.ConversationID, Timestamp: time.Now().UTC(), Payload: payload}
	event.ExpiresAt = domain.EventExpiry(ctx, event)
	if domain.Expired(event.ExpiresAt) {
		return nil
	}
	if err := s.store.AppendEvent(ctx, event); err != nil {
		return err
	}
	s.hub.Publish(event)
	return nil
}
func actor(c *gin.Context) string { return principal(c).ActorID }
func fail(c *gin.Context, _ error) {
	c.JSON(http.StatusInternalServerError, gin.H{"error": "runtime operation failed"})
}

func (s *Server) SetAuth(auth *Auth) { s.auth = auth }
