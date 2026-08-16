package environment

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type Config struct {
	Kind      string
	Profile   string
	Paths     Paths
	Authority Authority
}

type Local struct {
	config  Config
	granted map[Capability]struct{}
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
	return Info{
		Kind:         l.config.Kind,
		Profile:      l.config.Profile,
		OS:           runtime.GOOS,
		Arch:         runtime.GOARCH,
		Paths:        l.config.Paths,
		Capabilities: capabilities,
		Authority:    l.config.Authority,
		Tools:        tools,
	}, nil
}

func (l *Local) LookPath(_ context.Context, name string) (string, error) {
	return exec.LookPath(name)
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
		isolated, err := os.MkdirTemp(l.config.Paths.Cache, "run-*")
		if err != nil {
			return RunResult{ExitCode: -1}, fmt.Errorf("create isolated environment workspace: %w", err)
		}
		workDir = isolated
		cleanup = func() { _ = os.RemoveAll(isolated) }
	} else if workDir == "" {
		workDir = l.config.Paths.Workspace
	}
	defer cleanup()

	command := exec.CommandContext(ctx, spec.Command, spec.Args...)
	command.Dir = workDir
	command.Stdin = strings.NewReader(spec.Stdin)
	command.Env = append(os.Environ(),
		"QUANYEJUN_HOME="+l.config.Paths.Home,
		"QUANYEJUN_WORKSPACE="+l.config.Paths.Workspace,
		"QUANYEJUN_CONFIG_DIR="+l.config.Paths.Config,
		"QUANYEJUN_CACHE_DIR="+l.config.Paths.Cache,
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err = command.Run()
	result := RunResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: 0}
	if err != nil {
		result.ExitCode = -1
		if exitError, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitError.ExitCode()
		}
	}
	return result, err
}

var discoverableTools = []string{
	"sh", "bash", "git", "ssh", "curl", "wget", "jq", "tar", "gzip", "find", "grep", "sed", "awk",
	"go", "node", "pnpm", "python", "docker", "gh", "claude", "codex",
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
