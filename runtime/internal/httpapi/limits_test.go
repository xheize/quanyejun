package httpapi

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/quanyejun/quanyejun/runtime/internal/domain"
	"github.com/quanyejun/quanyejun/runtime/internal/eventhub"
	"github.com/quanyejun/quanyejun/runtime/internal/llm"
	"github.com/quanyejun/quanyejun/runtime/internal/store"
)

func TestWorkBodyLimits(t *testing.T) {
	for _, path := range []string{"/api/messages", "/api/tasks/demo"} {
		for _, knownLength := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/known=%v", path, knownLength), func(t *testing.T) {
				db := store.NewMemoryStore()
				s := newTestServer(t, db, eventhub.New(), llm.NewBuiltinProvider(), nil, "")
				body := `{"content":"hello","padding":"` + strings.Repeat("x", 2*1024*1024) + `"}`
				req := httptest.NewRequest("POST", path, strings.NewReader(body))
				if !knownLength {
					req.ContentLength = -1
				}
				req.Header.Set("X-Requested-With", "QuanYejun")
				res := httptest.NewRecorder()
				s.Handler().ServeHTTP(res, req)
				if res.Code != 413 {
					t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
				}
				if runs, _ := db.ListAgentRunsByStatus(context.Background(), []string{"queued", "running", "completed"}, 100); len(runs) != 0 {
					t.Fatal("oversized request created work")
				}
			})
		}
		for _, body := range []string{`{`, `{} {}`, `{} ` + strings.Repeat(" ", maxRequestBytes)} {
			s := newTestServer(t, store.NewMemoryStore(), eventhub.New(), llm.NewBuiltinProvider(), nil, "")
			res := testRequest(s.Handler(), "POST", path, nil, body)
			if res.Code != 400 && res.Code != 413 {
				t.Fatalf("malformed request accepted: %d", res.Code)
			}
		}
	}
}

func TestIPRateLimitCannotBeBypassedWithNewCookiesOrForwardedHeaders(t *testing.T) {
	t.Setenv("TRUSTED_PROXIES", "")
	s := newTestServer(t, store.NewMemoryStore(), eventhub.New(), llm.NewBuiltinProvider(), nil, "")
	handler := s.Handler()
	for i := 0; i < 21; i++ {
		path := "/api/messages"
		if i%2 == 0 {
			path = "/api/tasks/demo"
		}
		req := httptest.NewRequest("POST", path, strings.NewReader(`{`))
		req.RemoteAddr = "192.0.2.1:1234"
		req.Header.Set("X-Requested-With", "QuanYejun")
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i+1))
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		want := 400
		if i == 20 {
			want = 429
		}
		if res.Code != want {
			t.Fatalf("request %d: status=%d want=%d", i, res.Code, want)
		}
		if want == 429 && res.Header().Get("Retry-After") == "" {
			t.Fatal("missing Retry-After")
		}
	}
}

func TestActorRateLimitAndTrustedProxy(t *testing.T) {
	t.Setenv("TRUSTED_PROXIES", "192.0.2.1/32")
	s := newTestServer(t, store.NewMemoryStore(), eventhub.New(), llm.NewBuiltinProvider(), nil, "")
	handler := s.Handler()
	cookie := cookieNamed(t, testRequest(handler, "GET", "/api/session", nil, ""), sessionCookie)
	for i := 0; i < 11; i++ {
		req := httptest.NewRequest("POST", "/api/messages", strings.NewReader(`{`))
		req.RemoteAddr = "192.0.2.1:1234"
		req.AddCookie(cookie)
		req.Header.Set("X-Requested-With", "QuanYejun")
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i+1))
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		want := 400
		if i == 10 {
			want = 429
		}
		if res.Code != want {
			t.Fatalf("request %d: status=%d want=%d", i, res.Code, want)
		}
	}
	// A new actor behind the explicitly trusted proxy gets its own client-IP budget.
	req := httptest.NewRequest("POST", "/api/messages", strings.NewReader(`{`))
	req.RemoteAddr = "192.0.2.1:1234"
	req.Header.Set("X-Requested-With", "QuanYejun")
	req.Header.Set("X-Forwarded-For", "198.51.100.200")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != 400 {
		t.Fatalf("new client status=%d", res.Code)
	}
}

func TestBudgetExpiryAndBoundedStorage(t *testing.T) {
	b := &requestBudget{}
	now := time.Now()
	if !b.allow("actor", 1, now) || b.allow("actor", 1, now) {
		t.Fatal("limit not enforced")
	}
	if !b.allow("actor", 1, now.Add(time.Minute)) {
		t.Fatal("window did not reset")
	}
	for i := 0; i < 9999; i++ {
		if !b.allow(fmt.Sprint(i), 1, now) {
			t.Fatal("unexpected rejection")
		}
	}
	if b.allow("overflow", 1, now) || len(b.entries) != 10000 {
		t.Fatal("map is unbounded")
	}
	if !b.allow("overflow", 1, now.Add(2*time.Minute)) {
		t.Fatal("expired entries not reclaimed")
	}
}

type blockingProvider struct {
	entered chan struct{}
	release chan struct{}
}

func (p *blockingProvider) Name() string           { return "test" }
func (p *blockingProvider) Info() llm.ProviderInfo { return llm.ProviderInfo{ID: "test"} }
func (p *blockingProvider) Turn(ctx context.Context, _ llm.Request) (llm.Result, error) {
	p.entered <- struct{}{}
	select {
	case <-p.release:
		return llm.Result{Text: "done"}, nil
	case <-ctx.Done():
		return llm.Result{}, ctx.Err()
	}
}

func TestConcurrencyRejectsBeforePersistingAndReleasesCapacity(t *testing.T) {
	p := &blockingProvider{entered: make(chan struct{}, 12), release: make(chan struct{})}
	t.Cleanup(func() {
		select {
		case <-p.release:
		default:
			close(p.release)
		}
	})
	db := store.NewMemoryStore()
	s := newTestServer(t, db, eventhub.New(), p, nil, "")
	handler := s.Handler()
	var actors []string
	for i := 0; i < 12; i++ {
		res := testRequest(handler, "POST", "/api/messages", nil, `{"content":"hello"}`)
		want := 202
		if i >= 4 {
			want = 429
		}
		if res.Code != want {
			t.Fatalf("request %d status=%d want=%d", i, res.Code, want)
		}
		cookie := cookieNamed(t, res, sessionCookie)
		actor := sessionPrincipal(t, testRequest(handler, "GET", "/api/session", cookie, "")).ActorID
		messages, _ := db.ListMessages(context.Background(), actor, 100)
		if want == 429 && len(messages) != 0 {
			t.Fatal("rejected request persisted a message")
		}
		if want == 202 {
			actors = append(actors, actor)
		}
	}
	for i := 0; i < 4; i++ {
		select {
		case <-p.entered:
		case <-time.After(3 * time.Second):
			t.Fatal("provider did not start")
		}
	}
	close(p.release)
	for _, actor := range actors {
		waitForRun(t, db, actor)
	}
	deadline := time.Now().Add(time.Second)
	for {
		_, err := s.agentRunner.StartForVisitor("anon:after-release", "", "hello", domain.VisitorContext{}, nil)
		if err == nil {
			waitForRun(t, db, "anon:after-release")
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("capacity was not released", err)
		}
		time.Sleep(time.Millisecond)
	}
}
