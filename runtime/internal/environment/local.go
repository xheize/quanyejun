package environment

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultMaxCommandOutputBytes = 128 * 1024
	maximumMaxCommandOutputBytes = 1024 * 1024
)

type Config struct {
	Kind      string
	Profile   string
	Paths     Paths
	Authority Authority
}

type Local struct {
	config          Config
	granted         map[Capability]struct{}
	inspectMu       sync.Mutex
	inspectCachedAt time.Time
	inspectCache    Info
}

func ConfigFromEnvironment(getenv func(string) string) Config {
	if getenv == nil {
		getenv = os.Getenv
	}
	root := strings.TrimSpace(getenv("QUANYEJUN_RUNTIME_ROOT"))
	if root == "" {
		root = ".runtime"
	}
	root, _ = filepath.Abs(root)
	paths := Paths{
		Home:      pathOrDefault(getenv("QUANYEJUN_HOME"), filepath.Join(root, "home")),
		Workspace: pathOrDefault(getenv("QUANYEJUN_WORKSPACE"), filepath.Join(root, "workspace")),
		Config:    pathOrDefault(getenv("QUANYEJUN_CONFIG_DIR"), filepath.Join(root, "config")),
		Cache:     pathOrDefault(getenv("QUANYEJUN_CACHE_DIR"), filepath.Join(root, "cache")),
	}
	granted := parseCapabilities(getenv("QUANYEJUN_ENVIRONMENT_ALLOW"))
	if len(granted) == 0 {
		granted = []Capability{CanShell, CanReadFiles, CanWriteFiles}
	}
	return Config{
		Kind:    valueOrDefault(getenv("QUANYEJUN_ENVIRONMENT_KIND"), "local"),
		Profile: valueOrDefault(getenv("QUANYEJUN_ENVIRONMENT_PROFILE"), "developer"),
		Paths:   paths,
		Authority: Authority{
			Mode:    valueOrDefault(getenv("QUANYEJUN_AUTHORITY_MODE"), "internal-only"),
			Granted: granted,
		},
	}
}

func NewLocal(config Config) *Local {
	granted := make(map[Capability]struct{}, len(config.Authority.Granted))
	for _, capability := range config.Authority.Granted {
		granted[capability] = struct{}{}
	}
	return &Local{config: config, granted: granted}
}

func (l *Local) Prepare(context.Context) error {
	for _, path := range []string{l.config.Paths.Home, l.config.Paths.Workspace, l.config.Paths.Config, l.config.Paths.Cache} {
		if err := os.MkdirAll(path, 0o750); err != nil {
			return fmt.Errorf("prepare environment directory %s: %w", path, err)
		}
	}
	return nil
}

func (l *Local) Inspect(context.Context) (Info, error) {
	l.inspectMu.Lock()
	defer l.inspectMu.Unlock()
	if !l.inspectCachedAt.IsZero() && time.Since(l.inspectCachedAt) < 5*time.Second {
		return l.inspectCache, nil
	}
	tools := make([]Tool, 0, len(discoverableTools))
	available := make(map[string]bool, len(discoverableTools))
	for _, name := range discoverableTools {
		path, err := exec.LookPath(name)
		tool := Tool{Name: name, Available: err == nil}
		if err == nil {
			absolute, absoluteErr := filepath.Abs(path)
			if absoluteErr == nil {
				path = absolute
			}
			tool.Path = path
			available[name] = true
		}
		tools = append(tools, tool)
	}
	capabilities := []Capability{CanShell, CanReadFiles, CanWriteFiles}
	if available["git"] {
		capabilities = append(capabilities, CanGit)
	}
	if available["ssh"] {
		capabilities = append(capabilities, CanSSH)
	}
	if available["go"] || available["node"] || available["python"] {
		capabilities = append(capabilities, CanBuild)
	}
	if available["docker"] {
		capabilities = append(capabilities, CanUseDocker)
	}
	info := Info{
		Kind:         l.config.Kind,
		Profile:      l.config.Profile,
		OS:           runtime.GOOS,
		Arch:         runtime.GOARCH,
		Paths:        l.config.Paths,
		Capabilities: capabilities,
		Authority:    l.config.Authority,
		Tools:        tools,
	}
	l.inspectCache = info
	l.inspectCachedAt = time.Now()
	return info, nil
}

func (l *Local) LookPath(_ context.Context, name string) (string, error) {
	return exec.LookPath(name)
}

func (l *Local) ReadFile(ctx context.Context, relativePath string) ([]byte, error) {
	if _, ok := l.granted[CanReadFiles]; !ok {
		return nil, fmt.Errorf("environment authority does not grant %q", CanReadFiles)
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	target, err := secureWorkspacePath(l.config.Paths.Workspace, relativePath)
	if err != nil {
		return nil, err
	}
	content, err := os.ReadFile(target)
	if err != nil {
		return nil, fmt.Errorf("read workspace file: %w", err)
	}
	return content, nil
}

func (l *Local) Run(ctx context.Context, spec RunSpec) (RunResult, error) {
	if strings.TrimSpace(spec.Command) == "" {
		return RunResult{ExitCode: -1}, fmt.Errorf("environment command is required")
	}
	required := spec.Requires
	if len(required) == 0 {
		required = []Capability{CanShell}
	}
	info, err := l.Inspect(ctx)
	if err != nil {
		return RunResult{ExitCode: -1}, fmt.Errorf("inspect environment capabilities: %w", err)
	}
	available := make(map[Capability]struct{}, len(info.Capabilities))
	for _, capability := range info.Capabilities {
		available[capability] = struct{}{}
	}
	for _, capability := range required {
		if _, ok := available[capability]; !ok {
			return RunResult{ExitCode: -1}, fmt.Errorf("environment does not provide %q", capability)
		}
		if _, ok := l.granted[capability]; !ok {
			return RunResult{ExitCode: -1}, fmt.Errorf("environment authority does not grant %q", capability)
		}
	}

	workDir := strings.TrimSpace(spec.WorkDir)
	cleanup := func() {}
	if spec.Isolated {
		if workDir != "" {
			return RunResult{ExitCode: -1}, fmt.Errorf("isolated runs cannot specify a workspace directory")
		}
		isolated, err := os.MkdirTemp(l.config.Paths.Cache, "run-*")
		if err != nil {
			return RunResult{ExitCode: -1}, fmt.Errorf("create isolated environment workspace: %w", err)
		}
		workDir = isolated
		cleanup = func() { _ = os.RemoveAll(isolated) }
	} else {
		if workDir == "" {
			workDir = l.config.Paths.Workspace
		} else {
			resolved, resolveErr := secureWorkspacePath(l.config.Paths.Workspace, workDir)
			if resolveErr != nil {
				return RunResult{ExitCode: -1}, fmt.Errorf("resolve command working directory: %w", resolveErr)
			}
			workDir = resolved
		}
	}
	defer cleanup()
	if stat, statErr := os.Stat(workDir); statErr != nil || !stat.IsDir() {
		if statErr == nil {
			statErr = fmt.Errorf("not a directory")
		}
		return RunResult{ExitCode: -1}, fmt.Errorf("command working directory %s: %w", workDir, statErr)
	}

	maxOutputBytes := spec.MaxOutputBytes
	if maxOutputBytes <= 0 {
		maxOutputBytes = defaultMaxCommandOutputBytes
	}
	if maxOutputBytes > maximumMaxCommandOutputBytes {
		maxOutputBytes = maximumMaxCommandOutputBytes
	}

	command := exec.CommandContext(ctx, spec.Command, spec.Args...)
	command.Dir = workDir
	command.Stdin = strings.NewReader(spec.Stdin)
	overrides := map[string]string{
		"HOME":                 l.config.Paths.Home,
		"XDG_CONFIG_HOME":      l.config.Paths.Config,
		"XDG_CACHE_HOME":       l.config.Paths.Cache,
		"QUANYEJUN_HOME":       l.config.Paths.Home,
		"QUANYEJUN_WORKSPACE":  l.config.Paths.Workspace,
		"QUANYEJUN_CONFIG_DIR": l.config.Paths.Config,
		"QUANYEJUN_CACHE_DIR":  l.config.Paths.Cache,
	}
	if runtime.GOOS == "windows" {
		overrides["USERPROFILE"] = l.config.Paths.Home
	}
	command.Env = environmentWithOverrides(os.Environ(), overrides)
	stdout := newLimitedBuffer(maxOutputBytes)
	stderr := newLimitedBuffer(maxOutputBytes)
	command.Stdout = &stdout
	command.Stderr = &stderr
	err = command.Run()
	l.invalidateInspection()
	result := RunResult{
		Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: 0,
		StdoutTruncated: stdout.Truncated(), StderrTruncated: stderr.Truncated(),
	}
	if err != nil {
		result.ExitCode = -1
		if exitError, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitError.ExitCode()
		}
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return result, nil
		}
	}
	return result, err
}

func (l *Local) invalidateInspection() {
	l.inspectMu.Lock()
	l.inspectCachedAt = time.Time{}
	l.inspectCache = Info{}
	l.inspectMu.Unlock()
}

var discoverableTools = []string{
	"sh", "bash", "pwsh", "powershell", "git", "ssh", "curl", "wget", "jq", "tar", "gzip", "find", "grep", "sed", "awk",
	"apt", "apt-get", "apk", "dnf", "yum", "brew", "winget", "go", "node", "npm", "pnpm", "python", "python3", "pip", "pip3", "uv", "docker", "kubectl", "gh", "gws", "claude", "codex",
}

type limitedBuffer struct {
	buffer    bytes.Buffer
	remaining int
	truncated bool
}

func newLimitedBuffer(limit int) limitedBuffer {
	return limitedBuffer{remaining: limit}
}

func (b *limitedBuffer) Write(content []byte) (int, error) {
	originalLength := len(content)
	if b.remaining <= 0 {
		b.truncated = b.truncated || originalLength > 0
		return originalLength, nil
	}
	toWrite := content
	if len(toWrite) > b.remaining {
		toWrite = toWrite[:b.remaining]
		b.truncated = true
	}
	_, _ = b.buffer.Write(toWrite)
	b.remaining -= len(toWrite)
	return originalLength, nil
}

func (b *limitedBuffer) String() string {
	return b.buffer.String()
}

func (b *limitedBuffer) Truncated() bool {
	return b.truncated
}

func environmentWithOverrides(current []string, overrides map[string]string) []string {
	result := make([]string, 0, len(current)+len(overrides))
	for _, entry := range current {
		separator := strings.IndexByte(entry, '=')
		if separator <= 0 {
			continue
		}
		name := entry[:separator]
		if _, replaced := overrides[name]; replaced {
			continue
		}
		if runtime.GOOS == "windows" {
			replaced := false
			for overrideName := range overrides {
				if strings.EqualFold(name, overrideName) {
					replaced = true
					break
				}
			}
			if replaced {
				continue
			}
		}
		result = append(result, entry)
	}
	for name, value := range overrides {
		result = append(result, name+"="+value)
	}
	return result
}

func pathOrDefault(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return filepath.Clean(value)
	}
	return absolute
}

func valueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func parseCapabilities(value string) []Capability {
	seen := make(map[Capability]struct{})
	result := make([]Capability, 0)
	for _, item := range strings.Split(value, ",") {
		capability := Capability(strings.TrimSpace(item))
		if capability == "" {
			continue
		}
		if _, ok := seen[capability]; ok {
			continue
		}
		seen[capability] = struct{}{}
		result = append(result, capability)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func secureWorkspacePath(workspace, relativePath string) (string, error) {
	relativePath = strings.TrimSpace(relativePath)
	if relativePath == "" || filepath.IsAbs(relativePath) {
		return "", fmt.Errorf("workspace path must be relative")
	}
	root, err := filepath.Abs(workspace)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	target, err := filepath.Abs(filepath.Join(root, filepath.Clean(relativePath)))
	if err != nil {
		return "", fmt.Errorf("resolve workspace path: %w", err)
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("workspace path escapes the workspace")
	}
	return target, nil
}
