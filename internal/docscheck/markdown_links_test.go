package docscheck

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestMarkdownLocalLinksResolve(t *testing.T) {
	repo := repoRoot(t)
	files, err := filepath.Glob(filepath.Join(repo, "README*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no root README markdown files found")
	}
	docsDir := filepath.Join(repo, "docs")
	if err := filepath.WalkDir(docsDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || strings.ToLower(filepath.Ext(path)) != ".md" {
			return nil
		}
		files = append(files, path)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)

	var missing []string
	linkRE := regexp.MustCompile(`\[[^\]]+\]\(([^)]+)\)`)
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range linkRE.FindAllStringSubmatch(string(raw), -1) {
			target := localLinkTarget(match[1])
			if target == "" {
				continue
			}
			resolved := target
			if filepath.IsAbs(target) {
				resolved = filepath.Join(repo, strings.TrimLeft(target, `/\`))
			} else {
				resolved = filepath.Join(filepath.Dir(file), filepath.FromSlash(target))
			}
			if _, err := os.Stat(resolved); err != nil {
				relFile := filepath.ToSlash(mustRel(t, repo, file))
				missing = append(missing, relFile+" -> "+target)
			}
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("broken local documentation links:\n%s", strings.Join(missing, "\n"))
	}
}

func localLinkTarget(raw string) string {
	target := strings.Trim(strings.TrimSpace(raw), "<>")
	if target == "" || strings.HasPrefix(target, "#") {
		return ""
	}
	lower := strings.ToLower(target)
	if strings.Contains(lower, "://") || strings.HasPrefix(lower, "mailto:") {
		return ""
	}
	if i := strings.IndexAny(target, "#?"); i >= 0 {
		target = target[:i]
	}
	return strings.TrimSpace(target)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func mustRel(t *testing.T, base, path string) string {
	t.Helper()
	rel, err := filepath.Rel(base, path)
	if err != nil {
		t.Fatal(err)
	}
	return rel
}
