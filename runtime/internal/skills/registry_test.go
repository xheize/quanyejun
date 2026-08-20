package skills

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRegistryListsAndLoadsSkill(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "workspace-summary", `---
name: workspace-summary
description: Summarize a workspace safely.
---
Read the relevant files and return a concise summary.
`)
	registry := NewRegistry(root)
	items, err := registry.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Name != "workspace-summary" || items[0].Version == "" {
		t.Fatalf("unexpected skills: %#v", items)
	}
	skill, err := registry.Load(context.Background(), "workspace-summary")
	if err != nil {
		t.Fatal(err)
	}
	if skill.Instructions != "Read the relevant files and return a concise summary." {
		t.Fatalf("instructions = %q", skill.Instructions)
	}
}

func TestRegistryRejectsTraversalAndMismatchedName(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry(root)
	if _, err := registry.Load(context.Background(), "../outside"); err == nil {
		t.Fatal("expected invalid name rejection")
	}
	writeSkill(t, root, "expected", `---
name: different
description: invalid fixture
---
Do something.
`)
	if _, err := registry.Load(context.Background(), "expected"); err == nil {
		t.Fatal("expected directory and frontmatter mismatch")
	}
}

func writeSkill(t *testing.T, root, name, content string) {
	t.Helper()
	directory := filepath.Join(root, name)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
