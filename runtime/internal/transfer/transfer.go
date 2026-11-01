// Package transfer implements operator-owned file ingress independently of chat
// identities and model tool authority. Roots are explicit deployment resources.
package transfer

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	defaultMaxBytes         = 256 << 20
	defaultMaxExpandedBytes = 512 << 20
	maxEntries              = 10000
	stagePrefix             = ".quanyejun-transfer-"
)

type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string                { return e.Message }
func reject(status int, message string) error { return &Error{status, message} }

type Service struct {
	digest           [32]byte
	roots            map[string]*os.Root
	maxBytes         int64
	maxExpandedBytes int64
	busy             chan struct{}
}

type Result struct {
	Root        string `json:"root"`
	Path        string `json:"path"`
	Format      string `json:"format"`
	Files       int    `json:"files"`
	Directories int    `json:"directories"`
	Bytes       int64  `json:"bytes"`
	Replaced    bool   `json:"replaced"`
}

// FromEnvironment returns nil when no key is configured. Roots must already
// exist; opening them once pins the administrator-selected directories.
func FromEnvironment(getenv func(string) string, workspace string) (*Service, error) {
	key := getenv("QUANYEJUN_TRANSFER_API_KEY")
	if key == "" {
		return nil, nil
	}
	if len(key) < 32 || len(key) > 512 || strings.IndexFunc(key, func(r rune) bool { return r < 33 || r > 126 }) >= 0 {
		return nil, fmt.Errorf("QUANYEJUN_TRANSFER_API_KEY must contain 32-512 printable non-space ASCII characters")
	}
	roots := map[string]string{"workspace": workspace}
	if raw := getenv("QUANYEJUN_TRANSFER_ROOTS"); strings.TrimSpace(raw) != "" {
		roots = nil
		if err := json.Unmarshal([]byte(raw), &roots); err != nil || len(roots) == 0 || len(roots) > 16 {
			return nil, fmt.Errorf("QUANYEJUN_TRANSFER_ROOTS must be a JSON object with 1-16 named absolute directories")
		}
	}
	maxBytes, err := byteLimit(getenv("QUANYEJUN_TRANSFER_MAX_BYTES"), defaultMaxBytes, 1<<30)
	if err != nil {
		return nil, fmt.Errorf("invalid QUANYEJUN_TRANSFER_MAX_BYTES")
	}
	maxExpanded, err := byteLimit(getenv("QUANYEJUN_TRANSFER_MAX_EXPANDED_BYTES"), defaultMaxExpandedBytes, 4<<30)
	if err != nil {
		return nil, fmt.Errorf("invalid QUANYEJUN_TRANSFER_MAX_EXPANDED_BYTES")
	}
	s := &Service{digest: sha256.Sum256([]byte(key)), roots: make(map[string]*os.Root), maxBytes: maxBytes, maxExpandedBytes: maxExpanded, busy: make(chan struct{}, 1)}
	for name, directory := range roots {
		if !validRootName(name) || !filepath.IsAbs(directory) {
			s.Close()
			return nil, fmt.Errorf("transfer roots require simple names and absolute directory paths")
		}
		root, err := os.OpenRoot(directory)
		if err != nil {
			s.Close()
			return nil, fmt.Errorf("transfer root %q cannot be opened; provision its directory first", name)
		}
		s.roots[name] = root
	}
	return s, nil
}

func byteLimit(raw string, fallback, maximum int64) (int64, error) {
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 || n > maximum {
		return 0, fmt.Errorf("invalid byte limit")
	}
	return n, nil
}

func validRootName(name string) bool {
	if len(name) == 0 || len(name) > 32 {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func (s *Service) KeyDigest() [32]byte { return s.digest }
func (s *Service) Close() {
	for _, root := range s.roots {
		_ = root.Close()
	}
}

func (s *Service) Upload(r *http.Request, w http.ResponseWriter) (Result, error) {
	select {
	case s.busy <- struct{}{}:
		defer func() { <-s.busy }()
	default:
		return Result{}, reject(http.StatusTooManyRequests, "another transfer is in progress")
	}
	result, overwrite, err := options(r)
	if err != nil {
		return result, err
	}
	root := s.roots[result.Root]
	if root == nil {
		return result, reject(http.StatusBadRequest, "unknown transfer root")
	}
	if r.ContentLength > s.maxBytes {
		return result, reject(http.StatusRequestEntityTooLarge, "upload exceeds byte limit")
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.maxBytes)
	reader, err := r.MultipartReader()
	if err != nil {
		return result, reject(http.StatusBadRequest, "expected multipart/form-data with one file field")
	}
	part, err := reader.NextRawPart()
	if err != nil {
		return result, bodyError(err)
	}
	if part.FormName() != "file" || part.FileName() == "" {
		return result, reject(http.StatusBadRequest, "expected one file field named file")
	}
	// All mutations stay anchored to os.Root, including staging and cleanup.
	// Reject existing symlinks in parents; os.Root also prevents escape if paths
	// change concurrently after these checks.
	parent, err := openParent(root, path.Dir(result.Path))
	if err != nil {
		return result, err
	}
	defer parent.Close()
	base := path.Base(result.Path)
	info, err := parent.Lstat(base)
	if err == nil {
		if !overwrite || result.Format == "zip" || !info.Mode().IsRegular() {
			return result, reject(http.StatusConflict, "destination exists; only regular files support overwrite=true")
		}
		result.Replaced = true
	} else if !errors.Is(err, fs.ErrNotExist) {
		return result, err
	}
	stage := stagePrefix + rand.Text()
	file, err := parent.OpenFile(stage, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return result, err
	}
	defer parent.Remove(stage)
	defer file.Close()
	n, err := io.Copy(file, contextReader{r.Context(), part})
	if err != nil {
		return result, bodyError(err)
	}
	if _, err = reader.NextRawPart(); err != io.EOF {
		if err == nil {
			return result, reject(http.StatusBadRequest, "only one file field is allowed; put options in the URL query")
		}
		return result, bodyError(err)
	}
	// Consume the epilogue too, so chunked bodies cannot bypass the request cap.
	if _, err = io.Copy(io.Discard, contextReader{r.Context(), r.Body}); err != nil {
		return result, bodyError(err)
	}
	if err = r.Context().Err(); err != nil {
		return result, err
	}
	if result.Format == "zip" {
		return s.installZIP(r.Context(), parent, file, n, base, result)
	}
	if err = file.Sync(); err != nil {
		return result, err
	}
	if err = file.Close(); err != nil {
		return result, err
	}
	if overwrite {
		// Rename replaces the directory entry, never truncates a symlink target.
		err = parent.Rename(stage, base)
	} else {
		// Linking is atomic and fails if any destination entry already exists.
		err = parent.Link(stage, base)
	}
	if errors.Is(err, fs.ErrExist) {
		return result, reject(http.StatusConflict, "destination exists")
	}
	if err != nil {
		return result, err
	}
	result.Files, result.Bytes = 1, n
	return result, nil
}

func options(r *http.Request) (Result, bool, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return Result{}, false, reject(http.StatusBadRequest, "invalid query parameters")
	}
	for key, values := range q {
		if len(values) != 1 || (key != "root" && key != "path" && key != "format" && key != "overwrite") {
			return Result{}, false, reject(http.StatusBadRequest, "unknown or repeated query parameter")
		}
	}
	result := Result{Root: q.Get("root"), Path: q.Get("path"), Format: q.Get("format")}
	if result.Root == "" {
		result.Root = "workspace"
	}
	if result.Format == "" {
		result.Format = "file"
	}
	if !validRelativePath(result.Path) {
		return result, false, reject(http.StatusBadRequest, "path must be a portable relative path without traversal")
	}
	if result.Format != "file" && result.Format != "zip" {
		return result, false, reject(http.StatusBadRequest, "format must be file or zip")
	}
	overwrite := q.Get("overwrite") == "true"
	if value := q.Get("overwrite"); value != "" && value != "true" && value != "false" {
		return result, false, reject(http.StatusBadRequest, "overwrite must be true or false")
	}
	if overwrite && result.Format == "zip" {
		return result, false, reject(http.StatusBadRequest, "ZIP uploads require a new destination directory")
	}
	return result, overwrite, nil
}

func validRelativePath(name string) bool {
	if name == "" || len(name) > 1024 || !utf8.ValidString(name) || strings.ContainsAny(name, "\\:<>\"|?*") || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return false
	}
	parts := strings.Split(name, "/")
	if len(parts) > 32 {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || len(part) > 255 || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || strings.HasPrefix(strings.ToLower(part), stagePrefix) {
			return false
		}
		// Keep archive paths portable, including on Windows validation hosts.
		stem := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		stemRunes := []rune(stem)
		if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || stem == "CONIN$" || stem == "CONOUT$" ||
			len(stemRunes) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && strings.ContainsRune("123456789¹²³", stemRunes[3]) {
			return false
		}
	}
	return true
}

func openParent(root *os.Root, directory string) (*os.Root, error) {
	if directory != "." {
		current := ""
		for _, segment := range strings.Split(directory, "/") {
			current = path.Join(current, segment)
			if err := root.Mkdir(current, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
				return nil, err
			}
			info, err := root.Lstat(current)
			if err != nil {
				return nil, err
			}
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return nil, reject(http.StatusBadRequest, "destination parent must be a directory without symlinks")
			}
		}
	}
	return root.OpenRoot(directory)
}

func bodyError(err error) error {
	var filesystemError *fs.PathError
	if errors.As(err, &filesystemError) {
		return err
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return reject(http.StatusRequestEntityTooLarge, "upload exceeds byte limit")
	}
	return reject(http.StatusBadRequest, "incomplete or invalid upload body")
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func (s *Service) installZIP(ctx context.Context, parent *os.Root, file *os.File, size int64, base string, result Result) (Result, error) {
	archive, err := zip.NewReader(file, size)
	if err != nil {
		return result, reject(http.StatusBadRequest, "invalid ZIP archive")
	}
	if len(archive.File) > maxEntries {
		return result, reject(http.StatusRequestEntityTooLarge, "ZIP exceeds 10000 entries")
	}
	result.Directories, err = validateZIP(archive, s.maxExpandedBytes)
	if err != nil {
		return result, err
	}
	stage := stagePrefix + rand.Text()
	if err := parent.Mkdir(stage, 0o700); err != nil {
		return result, err
	}
	defer parent.RemoveAll(stage)
	tree, err := parent.OpenRoot(stage)
	if err != nil {
		return result, err
	}
	defer tree.Close()
	for _, entry := range archive.File {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		name := strings.TrimSuffix(entry.Name, "/")
		if entry.Mode().IsDir() {
			if err := tree.MkdirAll(name, 0o700); err != nil {
				return result, err
			}
			continue
		}
		if entry.UncompressedSize64 > uint64(s.maxExpandedBytes-result.Bytes) {
			return result, reject(http.StatusRequestEntityTooLarge, "ZIP exceeds expanded byte limit")
		}
		if err := tree.MkdirAll(path.Dir(name), 0o700); err != nil {
			return result, err
		}
		output, err := tree.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return result, err
		}
		input, err := entry.Open()
		if err != nil {
			output.Close()
			return result, reject(http.StatusBadRequest, "invalid ZIP entry")
		}
		n, copyErr := io.Copy(output, io.LimitReader(contextReader{ctx, input}, s.maxExpandedBytes-result.Bytes+1))
		input.Close()
		syncErr := output.Sync()
		closeErr := output.Close()
		result.Bytes += n
		if result.Bytes > s.maxExpandedBytes {
			return result, reject(http.StatusRequestEntityTooLarge, "ZIP exceeds expanded byte limit")
		}
		if copyErr != nil {
			var filesystemError *fs.PathError
			if errors.As(copyErr, &filesystemError) {
				return result, copyErr
			}
			return result, reject(http.StatusBadRequest, "invalid or interrupted ZIP entry")
		}
		if syncErr != nil {
			return result, syncErr
		}
		if closeErr != nil {
			return result, closeErr
		}
		result.Files++
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := tree.Close(); err != nil {
		return result, err
	}
	if _, err := parent.Lstat(base); err == nil {
		return result, reject(http.StatusConflict, "destination exists")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return result, err
	}
	if err := parent.Rename(stage, base); err != nil {
		return result, err
	}
	return result, nil
}

// Validate the entire manifest before extraction, including implicit directory
// entries and case aliases. A directory may be explicitly listed after its files.
func validateZIP(archive *zip.Reader, maximum int64) (int, error) {
	type node struct {
		name                string
		directory, explicit bool
	}
	nodes := make(map[string]node)
	var size uint64
	for _, entry := range archive.File {
		name := strings.TrimSuffix(entry.Name, "/")
		mode := entry.Mode()
		if !validRelativePath(name) || (!mode.IsRegular() && !mode.IsDir()) || entry.NonUTF8 {
			return 0, reject(http.StatusBadRequest, "ZIP contains an unsafe path, special file, or non-UTF-8 filename")
		}
		if !mode.IsDir() {
			if entry.UncompressedSize64 > uint64(maximum)-size {
				return 0, reject(http.StatusRequestEntityTooLarge, "ZIP exceeds expanded byte limit")
			}
			size += entry.UncompressedSize64
		}
		key := strings.ToLower(name)
		if existing, found := nodes[key]; found && (existing.name != name || existing.explicit || !mode.IsDir() || !existing.directory) {
			return 0, reject(http.StatusBadRequest, "ZIP contains duplicate or conflicting paths")
		}
		nodes[key] = node{name, mode.IsDir(), true}
		for ancestor := path.Dir(name); ancestor != "."; ancestor = path.Dir(ancestor) {
			key := strings.ToLower(ancestor)
			if existing, found := nodes[key]; found {
				if existing.name != ancestor || !existing.directory {
					return 0, reject(http.StatusBadRequest, "ZIP contains conflicting paths")
				}
			} else {
				nodes[key] = node{ancestor, true, false}
			}
		}
		if len(nodes) > maxEntries {
			return 0, reject(http.StatusRequestEntityTooLarge, "ZIP exceeds 10000 files and directories")
		}
	}
	directories := 0
	for _, n := range nodes {
		if n.directory {
			directories++
		}
	}
	return directories, nil
}
