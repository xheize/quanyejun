package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/quanyejun/quanyejun/runtime/internal/domain"
	"github.com/quanyejun/quanyejun/runtime/internal/eventhub"
	"github.com/quanyejun/quanyejun/runtime/internal/llm"
	"github.com/quanyejun/quanyejun/runtime/internal/store"
)

// Publish during the replay query to deterministically exercise the handover.
type replayStore struct {
	store.Store
	once         sync.Once
	duringReplay func()
}

func (s *replayStore) ListEventsAfter(ctx context.Context, actor, after string, limit int) ([]domain.Event, error) {
	if after == "event-0" {
		s.once.Do(s.duringReplay)
	}
	return s.Store.ListEventsAfter(ctx, actor, after, limit)
}

func TestWebSocketReplaysAllPagesWithoutLiveDuplicates(t *testing.T) {
	db := store.NewMemoryStore()
	hub := eventhub.New()
	wrapped := &replayStore{Store: db}
	s := newTestServer(t, wrapped, hub, llm.NewBuiltinProvider(), nil, "")
	handler := s.Handler()
	response := testRequest(handler, "GET", "/api/session", nil, "")
	cookie := cookieNamed(t, response, sessionCookie)
	actor := sessionPrincipal(t, response).ActorID
	appendEvent := func(i int) domain.Event {
		ev := domain.Event{ID: fmt.Sprintf("event-%d", i), ActorID: actor, Timestamp: time.Now(), Type: "test"}
		if err := db.AppendEvent(context.Background(), ev); err != nil {
			t.Fatal(err)
		}
		return ev
	}
	for i := 0; i <= 105; i++ {
		appendEvent(i)
	}
	wrapped.duringReplay = func() {
		// More than the hub's 32-event buffer; the persisted feed must fill the gap.
		for i := 106; i <= 155; i++ {
			ev := domain.Event{ID: fmt.Sprintf("event-%d", i), ActorID: actor, Timestamp: time.Now(), Type: "test"}
			_ = db.AppendEvent(context.Background(), ev)
			hub.Publish(ev)
		}
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()
	headers := http.Header{"Cookie": []string{cookie.String()}}
	ws, _, err := websocket.DefaultDialer.Dial(strings.Replace(srv.URL, "http", "ws", 1)+"/ws?lastEventId=event-0", headers)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	for i := 1; i <= 155; i++ {
		var ev domain.Event
		if err := ws.ReadJSON(&ev); err != nil {
			t.Fatal(err)
		}
		if ev.ID != fmt.Sprintf("event-%d", i) {
			t.Fatalf("event %d: got %s", i, ev.ID)
		}
	}
	hub.Publish(appendEvent(156))
	var ev domain.Event
	if err := ws.ReadJSON(&ev); err != nil {
		t.Fatal(err)
	}
	if ev.ID != "event-156" {
		t.Fatalf("replay/live duplicate: %s", ev.ID)
	}
}

func TestWebSocketWithoutCursorDoesNotSkipFirstPage(t *testing.T) {
	db := store.NewMemoryStore()
	s := newTestServer(t, db, eventhub.New(), llm.NewBuiltinProvider(), nil, "")
	handler := s.Handler()
	response := testRequest(handler, "GET", "/api/session", nil, "")
	cookie := cookieNamed(t, response, sessionCookie)
	actor := sessionPrincipal(t, response).ActorID
	for i := 0; i < 150; i++ {
		if err := db.AppendEvent(context.Background(), domain.Event{ID: fmt.Sprintf("event-%d", i), ActorID: actor, Timestamp: time.Now(), Type: "test"}); err != nil {
			t.Fatal(err)
		}
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()
	ws, _, err := websocket.DefaultDialer.Dial(strings.Replace(srv.URL, "http", "ws", 1)+"/ws", http.Header{"Cookie": []string{cookie.String()}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	for i := 0; i < 150; i++ {
		var event domain.Event
		if err := ws.ReadJSON(&event); err != nil {
			t.Fatal(err)
		}
		if event.ID != fmt.Sprintf("event-%d", i) {
			t.Fatalf("event %d: %s", i, event.ID)
		}
	}
}
