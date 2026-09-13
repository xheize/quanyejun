package store

import (
	"context"
	"testing"
	"time"

	"github.com/quanyejun/quanyejun/runtime/internal/domain"
)

func TestEventsCanReplayAfterCursor(t *testing.T) {
	dataStore := NewMemoryStore()
	for _, event := range []domain.Event{{ID: "one", Timestamp: time.Now()}, {ID: "two", Timestamp: time.Now()}, {ID: "three", Timestamp: time.Now()}} {
		if err := dataStore.AppendEvent(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	events, err := dataStore.ListEventsAfter(context.Background(), "", "one", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].ID != "two" || events[1].ID != "three" {
		t.Fatalf("unexpected replay: %#v", events)
	}
}
