package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/quanyejun/quanyejun/runtime/internal/domain"
)

func testVisitorProfiles(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()
	actor := "anon:" + uuid.NewString()
	expiry := time.Now().Add(100 * time.Millisecond)
	p, err := s.SetVisitorProfile(domain.WithExpiry(ctx, &expiry), actor, "소유자")
	if err != nil || p.ExpiresAt == nil {
		t.Fatal(p, err)
	}
	p, err = s.SetVisitorProfile(ctx, actor, "관리자")
	if err != nil || p.ExpiresAt.After(expiry.Add(time.Microsecond)) {
		t.Fatal("edit extended retention", p, err)
	}
	time.Sleep(120 * time.Millisecond)
	p, err = s.GetVisitorProfile(ctx, actor)
	if err != nil || p.DisplayName != "" {
		t.Fatal("expired alias visible", p, err)
	}
	if err = s.PurgeExpired(ctx); err != nil {
		t.Fatal(err)
	}
	member := "sso:" + uuid.NewString()
	p, err = s.SetVisitorProfile(ctx, member, "소유자")
	if err != nil || p.ExpiresAt != nil {
		t.Fatal(p, err)
	}
	p, err = s.GetVisitorProfile(ctx, member)
	if err != nil || p.DisplayName != "소유자" {
		t.Fatal(p, err)
	}
	_, err = s.SetVisitorProfile(ctx, member, "")
	if err != nil {
		t.Fatal(err)
	}
}

func TestMemoryVisitorProfiles(t *testing.T) { testVisitorProfiles(t, NewMemoryStore()) }
func TestPostgresVisitorProfiles(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	s, err := NewPostgresStore(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	testVisitorProfiles(t, s)
	now := time.Now().UTC()
	run := domain.AgentRun{ID: uuid.NewString(), ActorID: "sso:test", IdentityID: domain.IdentityID, ConversationID: domain.ConversationID, Kind: "parent", Status: "queued", CreatedAt: now, UpdatedAt: now, Visitor: domain.VisitorContext{Role: "authenticated", DisplayName: "소유자"}}
	if err = s.CreateAgentRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	saved, err := s.GetAgentRun(context.Background(), run.ID)
	if err != nil || saved.Visitor != run.Visitor {
		t.Fatal(saved, err)
	}
}
