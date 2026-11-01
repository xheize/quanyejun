package httpapi

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/quanyejun/quanyejun/runtime/internal/eventhub"
	"github.com/quanyejun/quanyejun/runtime/internal/llm"
	"github.com/quanyejun/quanyejun/runtime/internal/store"
	"github.com/quanyejun/quanyejun/runtime/internal/transfer"
)

const managementTestKey = "test-only-http-transfer-key-0123456789abcdef"

func managementServer(t *testing.T, enabled bool) (http.Handler, string) {
	t.Helper()
	s := newTestServer(t, store.NewMemoryStore(), eventhub.New(), llm.NewBuiltinProvider(), []string{"http://localhost:3000"}, "")
	directory := t.TempDir()
	if enabled {
		service, err := transfer.FromEnvironment(func(k string) string {
			if k == "QUANYEJUN_TRANSFER_API_KEY" {
				return managementTestKey
			}
			return ""
		}, directory)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(service.Close)
		s.SetTransfers(service)
	}
	return s.Handler(), directory
}

func TestManagementUploadAuthAndBrowserIsolation(t *testing.T) {
	for _, tc := range []struct {
		name, auth, origin string
		enabled            bool
		status             int
	}{
		{"disabled", "Bearer " + managementTestKey, "", false, 404},
		{"missing", "", "", true, 401},
		{"wrong", "Bearer wrong", "", true, 401},
		{"basic", "Basic " + managementTestKey, "", true, 401},
		{"browser", "Bearer " + managementTestKey, "http://localhost:3000", true, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, directory := managementServer(t, tc.enabled)
			cookie := cookieNamed(t, testRequest(h, "GET", "/api/session", nil, ""), sessionCookie)
			r := httptest.NewRequest("POST", "/api/admin/files?path=config&api_key="+managementTestKey, strings.NewReader("must not read"))
			r.Header.Set("Authorization", tc.auth)
			r.Header.Set("Origin", tc.origin)
			r.AddCookie(cookie)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if len(w.Result().Cookies()) != 0 {
				t.Fatal("management route created browser session")
			}
			entries, _ := os.ReadDir(directory)
			if len(entries) != 0 {
				t.Fatal("unauthorized write")
			}
		})
	}
}

func TestManagementUploadOverHTTPWithoutChatHeaders(t *testing.T) {
	h, directory := managementServer(t, true)
	server := httptest.NewServer(h)
	defer server.Close()
	var body bytes.Buffer
	m := multipart.NewWriter(&body)
	p, err := m.CreateFormFile("file", "client-name.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.WriteString(p, "Postman-compatible payload"); err != nil {
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := http.NewRequest("POST", server.URL+"/api/admin/files?root=workspace&path=imports/server-name.txt", &body)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+managementTestKey)
	r.Header.Set("Content-Type", m.FormDataContentType())
	response, err := server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 201 {
		b, _ := io.ReadAll(response.Body)
		t.Fatalf("status=%d body=%s", response.StatusCode, b)
	}
	if len(response.Cookies()) != 0 {
		t.Fatal("unexpected session cookie")
	}
	got, err := os.ReadFile(filepath.Join(directory, "imports", "server-name.txt"))
	if err != nil || string(got) != "Postman-compatible payload" {
		t.Fatalf("content=%q err=%v", got, err)
	}
	// Existing chat CSRF behavior must remain unchanged.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/api/messages", strings.NewReader(`{"content":"hello"}`)))
	if w.Code != 403 {
		t.Fatal("chat CSRF requirement was bypassed")
	}
}

func TestManagementAuthenticationRateLimit(t *testing.T) {
	h, _ := managementServer(t, true)
	for i := 0; i < 31; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/api/admin/files", nil))
		want := 401
		if i == 30 {
			want = 429
		}
		if w.Code != want {
			t.Fatalf("request %d got %d want %d", i, w.Code, want)
		}
	}
}
