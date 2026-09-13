package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/quanyejun/quanyejun/runtime/internal/domain"
	"github.com/quanyejun/quanyejun/runtime/internal/eventhub"
	"github.com/quanyejun/quanyejun/runtime/internal/llm"
	"github.com/quanyejun/quanyejun/runtime/internal/store"
)

func testRequest(handler http.Handler, method, path string, cookie *http.Cookie, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if cookie != nil {
		req.AddCookie(cookie)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "QuanYejun")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	return res
}
func cookieNamed(t *testing.T, response *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatal("cookie not found")
	return nil
}
func sessionPrincipal(t *testing.T, response *httptest.ResponseRecorder) Principal {
	t.Helper()
	var p Principal
	if err := json.Unmarshal(response.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAnonymousIdentityAndIsolation(t *testing.T) {
	db := store.NewMemoryStore()
	s := newTestServer(t, db, eventhub.New(), llm.NewBuiltinProvider(), []string{"http://localhost:3000"}, "")
	h := s.Handler()
	a := testRequest(h, "GET", "/api/session", nil, "")
	ca := cookieNamed(t, a, sessionCookie)
	pa := sessionPrincipal(t, a)
	b := testRequest(h, "GET", "/api/session", nil, "")
	cb := cookieNamed(t, b, sessionCookie)
	pb := sessionPrincipal(t, b)
	if pa.ActorID == pb.ActorID || !domain.Anonymous(pa.ActorID) || !ca.HttpOnly {
		t.Fatal("anonymous identity missing")
	}
	if got := sessionPrincipal(t, testRequest(h, "GET", "/api/session?actorId=person_example", ca, "")); got.ActorID != pa.ActorID {
		t.Fatal("anonymous identity changed")
	}
	created := testRequest(h, "POST", "/api/messages", ca, `{"actorId":"person_example","content":"private message"}`)
	if created.Code != 202 {
		t.Fatal(created.Body.String())
	}
	var result struct {
		RunID       string         `json:"runId"`
		UserMessage domain.Message `json:"userMessage"`
	}
	_ = json.Unmarshal(created.Body.Bytes(), &result)
	if result.UserMessage.ActorID != pa.ActorID || result.UserMessage.ExpiresAt == nil {
		t.Fatal("client actor was trusted or TTL missing")
	}
	waitForRun(t, db, pa.ActorID)
	for _, path := range []string{"/api/bootstrap?actorId=" + pa.ActorID, "/api/events?actorId=" + pa.ActorID} {
		res := testRequest(h, "GET", path, cb, "")
		if strings.Contains(res.Body.String(), "private message") {
			t.Fatalf("other actor leaked at %s", path)
		}
	}
	if res := testRequest(h, "GET", "/api/runs/"+result.RunID, cb, ""); res.Code != 404 {
		t.Fatal("cross-actor run access")
	}
	tampered := *ca
	tampered.Value = "x" + ca.Value
	if got := sessionPrincipal(t, testRequest(h, "GET", "/api/session", &tampered, "")); got.ActorID == pa.ActorID {
		t.Fatal("tampered cookie accepted")
	}
	request := httptest.NewRequest("POST", "/api/messages", strings.NewReader(`{"content":"csrf"}`))
	request.AddCookie(ca)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != 403 {
		t.Fatal("missing CSRF header accepted")
	}
	request = httptest.NewRequest("GET", "/api/bootstrap", nil)
	request.Header.Set("Origin", "https://untrusted.example")
	response = httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != 403 {
		t.Fatal("untrusted origin accepted")
	}
}

func TestSSOCodePKCEUserInfoAndRevocation(t *testing.T) {
	var challenge string
	revoked := false
	sso := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/oauth2/token":
			_ = r.ParseForm()
			hash := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if base64.RawURLEncoding.EncodeToString(hash[:]) != challenge || r.Form.Get("redirect_uri") != "http://localhost:8080/auth/callback" || r.Form.Get("code") != "valid-code" {
				t.Error("bad code exchange")
				w.WriteHeader(400)
				return
			}
			id, secret, ok := r.BasicAuth()
			if !ok || id != "client" || secret != "secret" {
				t.Error("bad client authentication")
			}
			_, _ = w.Write([]byte(`{"access_token":"server-secret-token","token_type":"Bearer","expires_in":1800}`))
		case "/oauth2/userinfo":
			if revoked || r.Header.Get("Authorization") != "Bearer server-secret-token" {
				w.WriteHeader(401)
				return
			}
			_, _ = w.Write([]byte(`{"sub":"stable-subject","email":"ignored@example.com"}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer sso.Close()
	a, err := NewAuth(AuthConfig{Key: []byte(strings.Repeat("k", 32)), Issuer: sso.URL, ClientID: "client", ClientSecret: "secret", RedirectURL: "http://localhost:8080/auth/callback", WebURL: "http://localhost:3000"})
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.GET("/auth/login", a.login)
	r.GET("/auth/callback", a.callback)
	r.POST("/auth/logout", a.logout)
	r.Use(a.identify)
	r.GET("/api/session", func(c *gin.Context) { c.JSON(200, principal(c)) })
	login := testRequest(r, "GET", "/auth/login", nil, "")
	flow := cookieNamed(t, login, flowCookie)
	location, _ := url.Parse(login.Header().Get("Location"))
	q := location.Query()
	challenge = q.Get("code_challenge")
	if q.Get("code_challenge_method") != "S256" || q.Get("scope") != "openid" || challenge == "" {
		t.Fatal("missing PKCE")
	}
	bad := testRequest(r, "GET", "/auth/callback?state=forged&code=valid-code", flow, "")
	if bad.Code != 400 {
		t.Fatal("forged state accepted")
	}
	callback := testRequest(r, "GET", "/auth/callback?state="+url.QueryEscape(q.Get("state"))+"&code=valid-code", flow, "")
	if callback.Code != 303 || callback.Header().Get("Location") != "http://localhost:3000" {
		t.Fatal("login failed", callback.Body.String())
	}
	cookie := cookieNamed(t, callback, sessionCookie)
	if strings.Contains(cookie.Value, "server-secret-token") {
		t.Fatal("token not encrypted")
	}
	res := testRequest(r, "GET", "/api/session", cookie, "")
	p := sessionPrincipal(t, res)
	if res.Code != 200 || p.Role != "authenticated" || p.ActorID != a.actorFor("stable-subject") {
		t.Fatal("wrong SSO identity")
	}
	if p.ActorID == a.actorFor("different-subject") {
		t.Fatal("subject collision")
	}
	revoked = true
	if res := testRequest(r, "GET", "/api/session", cookie, ""); res.Code != 401 {
		t.Fatal("revoked SSO token accepted")
	}
	expired, _ := a.seal(sessionCookie, browserSession{ActorID: p.ActorID, AccessToken: "server-secret-token", Expires: time.Now().Add(-time.Minute).Unix()})
	if p := sessionPrincipal(t, testRequest(r, "GET", "/api/session", &http.Cookie{Name: sessionCookie, Value: expired}, "")); p.Role != "anonymous" {
		t.Fatal("expired login remained authenticated")
	}
	logout := testRequest(r, "POST", "/auth/logout", cookie, "")
	if cookieNamed(t, logout, sessionCookie).MaxAge != -1 {
		t.Fatal("logout did not clear cookie")
	}
}

func TestExpiredEventsNeverReachOtherActor(t *testing.T) {
	db := store.NewMemoryStore()
	ctx := context.Background()
	for _, actor := range []string{"anon:a", "anon:b"} {
		_ = db.AppendEvent(ctx, domain.Event{ID: actor, ActorID: actor, Timestamp: time.Now(), Payload: map[string]any{"private": actor}})
	}
	events, err := db.ListEventsAfter(ctx, "anon:a", "anon:b", 10)
	if err != nil || len(events) != 1 || events[0].ActorID != "anon:a" {
		t.Fatal("event cursor bypassed scope", events, err)
	}
}

func TestWebSocketScopesReplayAndLiveEvents(t *testing.T) {
	db := store.NewMemoryStore()
	hub := eventhub.New()
	s := newTestServer(t, db, hub, llm.NewBuiltinProvider(), []string{"http://localhost:3000"}, "")
	h := s.Handler()
	response := testRequest(h, "GET", "/api/session", nil, "")
	cookie := cookieNamed(t, response, sessionCookie)
	p := sessionPrincipal(t, response)
	now := time.Now()
	_ = db.AppendEvent(context.Background(), domain.Event{ID: "foreign", ActorID: "anon:other", Type: "foreign", Timestamp: now})
	_ = db.AppendEvent(context.Background(), domain.Event{ID: "own", ActorID: p.ActorID, Type: "own", Timestamp: now})
	ts := httptest.NewServer(h)
	defer ts.Close()
	header := http.Header{"Origin": []string{"http://localhost:3000"}, "Cookie": []string{cookie.Name + "=" + cookie.Value}}
	ws, _, err := websocket.DefaultDialer.Dial(strings.Replace(ts.URL, "http", "ws", 1)+"/ws?actorId=anon:other", header)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	var event domain.Event
	if err = ws.ReadJSON(&event); err != nil || event.ID != "own" {
		t.Fatal("replay isolation failed", event, err)
	}
	past := now.Add(-time.Second)
	hub.Publish(domain.Event{ID: "expired", ActorID: p.ActorID, Timestamp: now, ExpiresAt: &past})
	hub.Publish(domain.Event{ID: "foreign-live", ActorID: "anon:other", Timestamp: now})
	hub.Publish(domain.Event{ID: "own-live", ActorID: p.ActorID, Timestamp: now})
	if err = ws.ReadJSON(&event); err != nil || event.ID != "own-live" {
		t.Fatal("live isolation failed", event, err)
	}
}

func TestIssuerDiscoveryRejectsAliases(t *testing.T) {
	var base string
	sso := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": base, "authorization_endpoint": base + "/oauth2/auth", "token_endpoint": base + "/oauth2/token", "userinfo_endpoint": base + "/oauth2/userinfo"})
	}))
	defer sso.Close()
	base = sso.URL
	for _, suffix := range []string{"", "/alias"} {
		a, err := NewAuth(AuthConfig{Key: []byte(strings.Repeat("k", 32)), Issuer: base + suffix, ClientID: "client", RedirectURL: "http://localhost:8080/auth/callback", WebURL: "http://localhost:3000"})
		if err != nil {
			t.Fatal(err)
		}
		err = a.ValidateIssuer(context.Background())
		if (suffix == "") != (err == nil) {
			t.Fatal("issuer mismatch handling", suffix, err)
		}
	}
}
