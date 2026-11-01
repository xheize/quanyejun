package transfer

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const testKey = "test-only-transfer-key-0123456789abcdef"

func newService(t *testing.T) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	s, err := FromEnvironment(func(key string) string {
		if key == "QUANYEJUN_TRANSFER_API_KEY" {
			return testKey
		}
		return ""
	}, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, root
}

func request(t *testing.T, query string, contents []byte, extra bool) *http.Request {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	p, err := w.CreateFormFile("file", "ignored-client-name")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Write(contents); err != nil {
		t.Fatal(err)
	}
	if extra {
		if err := w.WriteField("path", "unexpected"); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/admin/files?"+query, bytes.NewReader(body.Bytes()))
	r.Header.Set("Content-Type", w.FormDataContentType())
	return r
}

func wantStatus(t *testing.T, err error, code int) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Status != code {
		t.Fatalf("got %v, want status %d", err, code)
	}
}

func noStages(t *testing.T, directory string) {
	t.Helper()
	err := filepath.WalkDir(directory, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(d.Name(), stagePrefix) {
			t.Errorf("staging residue: %s", name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestFileUploadReplacementAndRollback(t *testing.T) {
	s, directory := newService(t)
	for _, tc := range []struct {
		query, content string
		extra          bool
		status         int
		want           string
	}{
		{"path=nested/config", "original", false, 0, "original"},
		{"path=nested/config", "accidental", false, 409, "original"},
		{"path=nested/config&overwrite=true", "incomplete", true, 400, "original"},
		{"path=nested/config&overwrite=true", "replacement", false, 0, "replacement"},
	} {
		result, err := s.Upload(request(t, tc.query, []byte(tc.content), tc.extra), httptest.NewRecorder())
		if tc.status != 0 {
			wantStatus(t, err, tc.status)
		} else {
			if err != nil {
				t.Fatal(err)
			}
			if result.Bytes != int64(len(tc.content)) || result.Files != 1 {
				t.Fatalf("bad result: %+v", result)
			}
			if result.Replaced != strings.Contains(tc.query, "overwrite=true") {
				t.Fatalf("bad replaced status: %+v", result)
			}
		}
		got, err := os.ReadFile(filepath.Join(directory, "nested", "config"))
		if err != nil || string(got) != tc.want {
			t.Fatalf("content=%q error=%v", got, err)
		}
		noStages(t, directory)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(directory, "nested", "config"))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("unexpected file permissions: %v %v", info, err)
		}
	}
}

type zipItem struct {
	name, content string
	mode          os.FileMode
}

func makeZIP(t *testing.T, items ...zipItem) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for _, item := range items {
		header := &zip.FileHeader{Name: item.name, Method: zip.Deflate}
		if item.mode != 0 {
			header.SetMode(item.mode)
		}
		out, err := w.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.WriteString(out, item.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestZIPDirectoryUpload(t *testing.T) {
	s, directory := newService(t)
	z := makeZIP(t, zipItem{"nested/문서.txt", "hello", 0}, zipItem{"nested/", "", os.ModeDir | 0o755}, zipItem{"empty/", "", os.ModeDir | 0o755})
	result, err := s.Upload(request(t, "path=project&format=zip", z, false), httptest.NewRecorder())
	if err != nil {
		t.Fatal(err)
	}
	if result.Files != 1 || result.Directories != 2 || result.Bytes != 5 {
		t.Fatalf("bad result: %+v", result)
	}
	got, err := os.ReadFile(filepath.Join(directory, "project", "nested", "문서.txt"))
	if err != nil || string(got) != "hello" {
		t.Fatalf("content=%q error=%v", got, err)
	}
	info, err := os.Stat(filepath.Join(directory, "project", "empty"))
	if err != nil || !info.IsDir() {
		t.Fatalf("missing empty directory: %v", err)
	}
	_, err = s.Upload(request(t, "path=project&format=zip", z, false), httptest.NewRecorder())
	wantStatus(t, err, 409)
	_, err = s.Upload(request(t, "path=project&format=zip&overwrite=true", z, false), httptest.NewRecorder())
	wantStatus(t, err, 400)
	noStages(t, directory)
}

func TestRejectUnsafeAndConflictingZIPsWithoutPublishing(t *testing.T) {
	cases := [][]zipItem{
		{{"../escape", "bad", 0}}, {{"/absolute", "bad", 0}}, {{"C:/config", "bad", 0}},
		{{"a\\b", "bad", 0}}, {{"a/../../escape", "bad", 0}}, {{"NUL", "bad", 0}},
		{{"link", "../outside", os.ModeSymlink | 0o777}}, {{"pipe", "", os.ModeNamedPipe | 0o600}},
		{{"a", "one", 0}, {"a", "two", 0}}, {{"a", "one", 0}, {"a/b", "two", 0}},
		{{"a/b", "one", 0}, {"a", "two", 0}}, {{"A/b", "one", 0}, {"a/c", "two", 0}},
		{{"A/b", "one", 0}, {"a/", "", os.ModeDir}},
	}
	for _, items := range cases {
		t.Run(items[0].name, func(t *testing.T) {
			s, directory := newService(t)
			_, err := s.Upload(request(t, "path=result&format=zip", makeZIP(t, items...), false), httptest.NewRecorder())
			wantStatus(t, err, 400)
			entries, err := os.ReadDir(directory)
			if err != nil || len(entries) != 0 {
				t.Fatalf("failed upload left files: %v %v", entries, err)
			}
		})
	}
}

func TestZIPChecksumFailureDoesNotPublish(t *testing.T) {
	s, directory := newService(t)
	z := makeZIP(t, zipItem{"first", "valid", 0}, zipItem{"second", "also valid", 0})
	central := bytes.LastIndex(z, []byte("PK\x01\x02"))
	if central < 0 {
		t.Fatal("no central directory")
	}
	z[central+16] ^= 1 // corrupt CRC in central directory, leaving manifest valid
	_, err := s.Upload(request(t, "path=result&format=zip", z, false), httptest.NewRecorder())
	wantStatus(t, err, 400)
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("partial ZIP was published: %v %v", entries, err)
	}
}

func TestUploadLimitsAndIncompleteBodies(t *testing.T) {
	for _, chunked := range []bool{false, true} {
		s, directory := newService(t)
		s.maxBytes = 300
		r := request(t, "path=result", bytes.Repeat([]byte("x"), 400), false)
		if chunked {
			r.ContentLength = -1
		}
		_, err := s.Upload(r, httptest.NewRecorder())
		wantStatus(t, err, 413)
		noStages(t, directory)
		if _, err = os.Stat(filepath.Join(directory, "result")); !errors.Is(err, fs.ErrNotExist) {
			t.Fatal("oversize file published")
		}
	}
	s, directory := newService(t)
	s.maxExpandedBytes = 8
	_, err := s.Upload(request(t, "path=result&format=zip", makeZIP(t, zipItem{"a", "12345", 0}, zipItem{"b", "12345", 0}), false), httptest.NewRecorder())
	wantStatus(t, err, 413)
	r := request(t, "path=result", []byte("data"), false)
	data, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(data[:len(data)-15]))
	r.ContentLength = -1
	_, err = s.Upload(r, httptest.NewRecorder())
	wantStatus(t, err, 400)
	r = request(t, "path=result", []byte("data"), false)
	ctx, cancel := context.WithCancel(r.Context())
	cancel()
	_, err = s.Upload(r.WithContext(ctx), httptest.NewRecorder())
	if err == nil {
		t.Fatal("cancelled upload accepted")
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed upload left files: %v %v", entries, err)
	}
}

func TestPathsOptionsAndBusy(t *testing.T) {
	s, directory := newService(t)
	for _, name := range []string{"", "..", "../x", "a/../x", "/x", "C:/x", "a\\b", "a//b", "a/", "a.", "a ", "COM1", "COM¹", "a\x00b", stagePrefix + "guess"} {
		_, err := s.Upload(request(t, "path="+url.QueryEscape(name), []byte("x"), false), httptest.NewRecorder())
		wantStatus(t, err, 400)
	}
	for _, query := range []string{"path=x&root=missing", "path=x&format=tar", "path=x&overwrite=yes", "path=x&path=y", "path=x&unknown=1", "path=%ZZ"} {
		_, err := s.Upload(request(t, query, []byte("x"), false), httptest.NewRecorder())
		wantStatus(t, err, 400)
	}
	s.busy <- struct{}{}
	_, err := s.Upload(request(t, "path=x", []byte("x"), false), httptest.NewRecorder())
	wantStatus(t, err, 429)
	<-s.busy
	noStages(t, directory)
}

func TestSymlinkParentsAndDestinations(t *testing.T) {
	s, directory := newService(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(directory, "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err := s.Upload(request(t, "path=link/nested/file", []byte("bad"), false), httptest.NewRecorder())
	if err == nil {
		t.Fatal("symlink parent accepted")
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatal("wrote outside root")
	}
	if err := os.WriteFile(filepath.Join(directory, "original"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("original", filepath.Join(directory, "alias")); err != nil {
		t.Fatal(err)
	}
	_, err = s.Upload(request(t, "path=alias&overwrite=true", []byte("bad"), false), httptest.NewRecorder())
	wantStatus(t, err, 409)
	got, _ := os.ReadFile(filepath.Join(directory, "original"))
	if string(got) != "keep" {
		t.Fatal("symlink target overwritten")
	}
}

func TestConfigurationAndNamedRoots(t *testing.T) {
	directory := t.TempDir()
	for _, values := range []map[string]string{
		{"QUANYEJUN_TRANSFER_API_KEY": "short"},
		{"QUANYEJUN_TRANSFER_API_KEY": testKey + "\n"},
		{"QUANYEJUN_TRANSFER_API_KEY": testKey, "QUANYEJUN_TRANSFER_ROOTS": "{}"},
		{"QUANYEJUN_TRANSFER_API_KEY": testKey, "QUANYEJUN_TRANSFER_ROOTS": `{"bad":"relative"}`},
		{"QUANYEJUN_TRANSFER_API_KEY": testKey, "QUANYEJUN_TRANSFER_MAX_BYTES": "-1"},
		{"QUANYEJUN_TRANSFER_API_KEY": testKey, "QUANYEJUN_TRANSFER_MAX_EXPANDED_BYTES": "broken"},
	} {
		s, err := FromEnvironment(func(k string) string { return values[k] }, directory)
		if s != nil {
			s.Close()
		}
		if err == nil {
			t.Fatal("invalid configuration accepted")
		}
		if strings.Contains(err.Error(), testKey) {
			t.Fatal("key leaked in error")
		}
	}
	s, err := FromEnvironment(func(string) string { return "" }, "")
	if s != nil || err != nil {
		t.Fatal("missing key must disable API")
	}
	roots, _ := json.Marshal(map[string]string{"credentials": directory})
	s, err = FromEnvironment(func(k string) string {
		switch k {
		case "QUANYEJUN_TRANSFER_API_KEY":
			return testKey
		case "QUANYEJUN_TRANSFER_ROOTS":
			return string(roots)
		}
		return ""
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.Upload(request(t, "root=credentials&path=kubeconfig", []byte("example"), false), httptest.NewRecorder())
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Upload(request(t, "path=x", []byte("example"), false), httptest.NewRecorder())
	wantStatus(t, err, 400) // explicit roots replace, rather than extend, defaults
}
