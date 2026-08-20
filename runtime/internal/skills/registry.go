package skills

import (
	"bufio"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const maxSkillBytes = 64 * 1024

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

type Metadata struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
}

type Skill struct {
	Metadata
	Instructions string `json:"instructions"`
}

type Registry struct{ root string }

func NewRegistry(root string) *Registry {
	root = strings.TrimSpace(root)
	if root != "" {
		root, _ = filepath.Abs(root)
	}
	return &Registry{root: root}
}

func (r *Registry) Root() string { return r.root }

func (r *Registry) List(ctx context.Context) ([]Metadata, error) {
	if r == nil || r.root == "" {
		return []Metadata{}, nil
	}
	entries, err := os.ReadDir(r.root)
	if errors.Is(err, os.ErrNotExist) {
		return []Metadata{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read skills directory: %w", err)
	}
	items := make([]Metadata, 0, len(entries))
	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !validName.MatchString(entry.Name()) {
			continue
		}
		skill, err := r.Load(ctx, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("load skill %q: %w", entry.Name(), err)
		}
		items = append(items, skill.Metadata)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items, nil
}

func (r *Registry) Load(ctx context.Context, name string) (Skill, error) {
	if r == nil || r.root == "" {
		return Skill{}, fmt.Errorf("skills directory is not configured")
	}
	name = strings.TrimSpace(name)
	if !validName.MatchString(name) {
		return Skill{}, fmt.Errorf("invalid skill name")
	}
	path := filepath.Join(r.root, name, "SKILL.md")
	if err := ensureInsideRoot(r.root, path); err != nil {
		return Skill{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return Skill{}, fmt.Errorf("open SKILL.md: %w", err)
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return Skill{}, fmt.Errorf("stat SKILL.md: %w", err)
	}
	if !stat.Mode().IsRegular() || stat.Size() > maxSkillBytes {
		return Skill{}, fmt.Errorf("SKILL.md must be a regular file no larger than %d bytes", maxSkillBytes)
	}
	select {
	case <-ctx.Done():
		return Skill{}, ctx.Err()
	default:
	}
	content, err := io.ReadAll(io.LimitReader(file, maxSkillBytes+1))
	if err != nil {
		return Skill{}, fmt.Errorf("read SKILL.md: %w", err)
	}
	metadata, instructions, err := parse(content)
	if err != nil {
		return Skill{}, err
	}
	if metadata.Name != name {
		return Skill{}, fmt.Errorf("frontmatter name %q must match directory %q", metadata.Name, name)
	}
	digest := sha256.Sum256(content)
	metadata.Version = fmt.Sprintf("sha256:%x", digest[:8])
	return Skill{Metadata: metadata, Instructions: instructions}, nil
}

func parse(content []byte) (Metadata, string, error) {
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return Metadata{}, "", fmt.Errorf("SKILL.md must start with YAML frontmatter")
	}
	end := strings.Index(text[4:], "\n---\n")
	if end < 0 {
		return Metadata{}, "", fmt.Errorf("SKILL.md frontmatter is not closed")
	}
	frontmatter := text[4 : 4+end]
	instructions := strings.TrimSpace(text[4+end+5:])
	metadata := Metadata{}
	scanner := bufio.NewScanner(strings.NewReader(frontmatter))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return Metadata{}, "", fmt.Errorf("invalid frontmatter line %q", line)
		}
		value = unquote(strings.TrimSpace(value))
		switch strings.TrimSpace(key) {
		case "name":
			metadata.Name = value
		case "description":
			metadata.Description = value
		}
	}
	if err := scanner.Err(); err != nil {
		return Metadata{}, "", err
	}
	if !validName.MatchString(metadata.Name) {
		return Metadata{}, "", fmt.Errorf("frontmatter requires a valid name")
	}
	if metadata.Description == "" || instructions == "" {
		return Metadata{}, "", fmt.Errorf("frontmatter description and skill instructions are required")
	}
	return metadata, instructions, nil
}

func unquote(value string) string {
	if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
		if decoded, err := strconv.Unquote(value); err == nil {
			return decoded
		}
		return strings.Trim(value, "'\"")
	}
	return value
}

func ensureInsideRoot(root, target string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve skills root: %w", err)
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("resolve skill path: %w", err)
	}
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("skill path escapes the skills root")
	}
	resolvedRoot, rootErr := filepath.EvalSymlinks(root)
	resolvedTarget, targetErr := filepath.EvalSymlinks(target)
	if rootErr == nil && targetErr == nil {
		relative, err = filepath.Rel(resolvedRoot, resolvedTarget)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("skill symlink escapes the skills root")
		}
	}
	return nil
}
