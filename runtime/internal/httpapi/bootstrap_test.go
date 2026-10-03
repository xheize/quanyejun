package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/quanyejun/quanyejun/runtime/internal/domain"
	"github.com/quanyejun/quanyejun/runtime/internal/eventhub"
	"github.com/quanyejun/quanyejun/runtime/internal/llm"
	"github.com/quanyejun/quanyejun/runtime/internal/store"
	"testing"
	"time"
)

type bootstrapRaceStore struct {
	store.Store
	inserted bool
}

func (s *bootstrapRaceStore) ListMessages(ctx context.Context, actor string, limit int) ([]domain.Message, error) {
	snapshot, err := s.Store.ListMessages(ctx, actor, limit)
	if !s.inserted {
		s.inserted = true
		// More than the bootstrap event page must still be replayable.
		for i := 0; i < 35; i++ {
			id := fmt.Sprintf("event-%d", i)
			m := domain.Message{ID: id, ActorID: actor, Role: "assistant", Content: "reply", CreatedAt: time.Now()}
			if e := s.Store.AddMessage(ctx, m); e != nil {
				return nil, e
			}
			if e := s.Store.AppendEvent(ctx, domain.Event{ID: id, ActorID: actor, Type: "chat.message", Timestamp: time.Now(), Payload: map[string]any{"message": m}}); e != nil {
				return nil, e
			}
		}
	}
	return snapshot, err
}
func TestBootstrapCursorReplaysConcurrentMessages(t *testing.T) {
	db := &bootstrapRaceStore{Store: store.NewMemoryStore()}
	h := newTestServer(t, db, eventhub.New(), llm.NewBuiltinProvider(), nil, "").Handler()
	res := testRequest(h, "GET", "/api/bootstrap", nil, "")
	if res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
	var data struct {
		Messages    []domain.Message `json:"messages"`
		Events      []domain.Event   `json:"events"`
		EventCursor string           `json:"eventCursor"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Messages) != 0 || len(data.Events) != 30 {
		t.Fatalf("unexpected snapshot: %+v", data)
	}
	replay, err := db.ListEventsAfter(context.Background(), data.Events[0].ActorID, data.EventCursor, 100)
	if err != nil {
		t.Fatal(err)
	}
	if data.EventCursor != store.EventStreamStart || len(replay) != 35 {
		t.Fatalf("concurrent messages skipped: cursor=%q replay=%d", data.EventCursor, len(replay))
	}
}
