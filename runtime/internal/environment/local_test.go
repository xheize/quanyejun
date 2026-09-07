package environment

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalPrepareCreatesPersistentDirectories(t *testing.T) {
	root := t.TempDir()
	local := NewLocal(ConfigFromEnvironment(func(key string) string {
		values := map[string]string{
			"QUANYEJUN_RUNTIME_ROOT":        root,
			"QUANYEJUN_ENVIRONMENT_KIND":    "test-local",
			"QUANYEJUN_ENVIRONMENT_PROFILE": "test",
		}
		return values[key]
	}))
	if err := local.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	info, err := local.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Kind != "test-local" || info.Profile != "test" {
		t.Fatalf("unexpected environment identity: %#v", info)
	}
	for _, path := range []string{info.Paths.Home, info.Paths.Workspace, info.Paths.Config, info.Paths.Cache} {
		stat, statErr := os.Stat(path)
		if statErr != nil || !stat.IsDir() {
			t.Fatalf("environment path was not prepared: %s (%v)", path, statErr)
		}
		if filepath.Dir(path) != root {
			t.Fatalf("path %s is outside root %s", path, root)
		}
	}
}

func TestLocalRunEnforcesAuthoritySeparatelyFromCapability(t *testing.T) {
	root := t.TempDir()
	local := NewLocal(Config{
		Kind: "test", Profile: "restricted",
		Paths:     Paths{Home: filepath.Join(root, "home"), Workspace: filepath.Join(root, "workspace"), Config: filepath.Join(root, "config"), Cache: filepath.Join(root, "cache")},
		Authority: Authority{Mode: "deny", Granted: []Capability{CanReadFiles}},
	})
	if err := local.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := local.Run(context.Background(), RunSpec{Command: "ignored"}); err == nil {
		t.Fatal("expected shell authority denial")
	}
}

func TestLocalReadFileStaysInsideWorkspace(t *testing.T) {
	root := t.TempDir()
	local := NewLocal(Config{
		Kind: "test", Profile: "reader",
		Paths:     Paths{Home: filepath.Join(root, "home"), Workspace: filepath.Join(root, "workspace"), Config: filepath.Join(root, "config"), Cache: filepath.Join(root, "cache")},
		Authority: Authority{Mode: "internal", Granted: []Capability{CanReadFiles}},
	})
	if err := local.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workspace", "note.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	content, err := local.ReadFile(context.Background(), "note.txt")
	if err != nil || string(content) != "hello" {
		t.Fatalf("content=%q err=%v", content, err)
	}
	if _, err := local.ReadFile(context.Background(), "../outside.txt"); err == nil {
		t.Fatal("expected workspace escape rejection")
	}
}

func TestLocalRunCapsOutputAndReturnsProcessExitCode(t *testing.T) {
	root := t.TempDir()
	local := NewLocal(Config{
		Kind: "test", Profile: "runner",
		Paths:     Paths{Home: filepath.Join(root, "home"), Workspace: filepath.Join(root, "workspace"), Config: filepath.Join(root, "config"), Cache: filepath.Join(root, "cache")},
		Authority: Authority{Mode: "internal", Granted: []Capability{CanShell}},
	})
	if err := local.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := local.Run(context.Background(), RunSpec{
		Command: os.Args[0], Args: []string{"-test.run=^TestLocalRunHelperProcess$", "--", "exit-seven"}, MaxOutputBytes: 32,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 7 || len(result.Stdout) != 32 || !result.StdoutTruncated {
		t.Fatalf("unexpected limited result: %#v", result)
	}
}

func TestLocalRunRejectsWorkspaceEscape(t *testing.T) {
	root := t.TempDir()
	local := NewLocal(Config{
		Kind: "test", Profile: "runner",
		Paths:     Paths{Home: filepath.Join(root, "home"), Workspace: filepath.Join(root, "workspace"), Config: filepath.Join(root, "config"), Cache: filepath.Join(root, "cache")},
		Authority: Authority{Mode: "internal", Granted: []Capability{CanShell}},
	})
	if err := local.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := local.Run(context.Background(), RunSpec{Command: os.Args[0], WorkDir: "../outside"}); err == nil {
		t.Fatal("expected command workspace escape rejection")
	}
}

func TestLocalRunHelperProcess(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-1] != "exit-seven" {
		return
	}
	_, _ = fmt.Fprint(os.Stdout, "0123456789abcdefghijklmnopqrstuvwxyz")
	os.Exit(7)
}
