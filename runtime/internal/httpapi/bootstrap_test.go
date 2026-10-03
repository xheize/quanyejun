package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/quanyejun/quanyejun/runtime/internal/domain"
	"github.com/quanyejun/quanyejun/runtime/internal/eventhub"
	"github.com/quanyejun/quanyejun/runtime/internal/llm"
	"github.com/quanyejun/quanyejun/runtime/internal/store"
)

// Inject writes at the exact race boundary; no sleeps or scheduler assumptions.
type bootstrapRaceStore struct {
	store.Store
	count    int
	inserted bool
}

func (s *bootstrapRaceStore) ListMessages(ctx context.Context, actor string, limit int) ([]domain.Message, error) {
	snapshot, err := s.Store.ListMessages(ctx, actor, limit)
	if err != nil {
		return nil, err
	}
	if s.inserted {
		return snapshot, nil
	}
	s.inserted = true
	for i := 0; i < s.count; i++ {
		id := fmt.Sprintf("during-bootstrap-%d", i)
		message := domain.Message{ID: id, ActorID: actor, Role: "assistant", Content: "concurrent reply", CreatedAt: time.Now()}
		if err := s.Store.AddMessage(ctx, message); err != nil {
			return nil, err
		}
		if err := s.Store.AppendEvent(ctx, domain.Event{ID: id, ActorID: actor, Type: "chat.message", Timestamp: time.Now(), Payload: map[string]any{"message": message}}); err != nil {
			return nil, err
		}
	}
	return snapshot, nil
}
func TestBootstrapWebSocketReplaysConcurrentMessages(t *testing.T) {
	for _, tc := range []struct {
		name  string
		prior bool
		count int
	}{
		{"first visit", false, 1},
		{"reconnect with existing cursor", true, 1},
		{"more than bootstrap event page", true, 35},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := &bootstrapRaceStore{Store: store.NewMemoryStore(), count: tc.count}
			handler := newTestServer(t, db, eventhub.New(), llm.NewBuiltinProvider(), nil, "").Handler()
			session := testRequest(handler, "GET", "/api/session", nil, "")
			actor := sessionPrincipal(t, session).ActorID
			cookie := cookieNamed(t, session, sessionCookie)
			wantCursor := store.EventStreamStart
			if tc.prior {
				wantCursor = "before-bootstrap"
				if err := db.AppendEvent(context.Background(), domain.Event{ID: wantCursor, ActorID: actor, Type: "existing", Timestamp: time.Now(), Payload: map[string]any{}}); err != nil {
					t.Fatal(err)
				}
			}
			response := testRequest(handler, "GET", "/api/bootstrap", cookie, "")
			if response.Code != http.StatusOK {
				t.Fatalf("bootstrap status=%d: %s", response.Code, response.Body.String())
			}
			var data struct {
				Messages    []domain.Message `json:"messages"`
				Events      []domain.Event   `json:"events"`
				EventCursor string           `json:"eventCursor"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &data); err != nil {
				t.Fatal(err)
			}
			if len(data.Messages) != 0 {
				t.Fatal("race fixture must omit concurrent messages from snapshot")
			}
			if data.EventCursor != wantCursor {
				t.Fatalf("cursor=%q, want %q", data.EventCursor, wantCursor)
			}
			server := httptest.NewServer(handler)
			defer server.Close()
			headers := http.Header{"Cookie": []string{cookie.String()}}
			conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/ws?lastEventId="+url.QueryEscape(data.EventCursor), headers)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			// A deadlock guard, not a timing assertion: every expected event must arrive.
			if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < tc.count; i++ {
				var event domain.Event
				if err := conn.ReadJSON(&event); err != nil {
					t.Fatalf("event %d: %v", i, err)
				}
				want := fmt.Sprintf("during-bootstrap-%d", i)
				if event.ID != want || event.ActorID != actor || event.Type != "chat.message" {
					t.Fatalf("event %d = %+v, want %s", i, event, want)
				}
			}
		})
	}
}
