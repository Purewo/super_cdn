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

func TestMarkdownLanguagePairs(t *testing.T) {
	repo := repoRoot(t)
	var englishFiles []string
	englishFiles = append(englishFiles, filepath.Join(repo, "README.md"))
	docsDir := filepath.Join(repo, "docs")
	if err := filepath.WalkDir(docsDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || strings.ToLower(filepath.Ext(path)) != ".md" || strings.HasSuffix(path, ".zh-CN.md") {
			return nil
		}
		englishFiles = append(englishFiles, path)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	var missing []string
	for _, english := range englishFiles {
		zh := strings.TrimSuffix(english, ".md") + ".zh-CN.md"
		if _, err := os.Stat(zh); err != nil {
			missing = append(missing, filepath.ToSlash(mustRel(t, repo, english))+" -> missing zh-CN pair")
			continue
		}
		englishRaw, err := os.ReadFile(english)
		if err != nil {
			t.Fatal(err)
		}
		zhRaw, err := os.ReadFile(zh)
		if err != nil {
			t.Fatal(err)
		}
		englishName := filepath.Base(english)
		zhName := filepath.Base(zh)
		englishSwitch := "[English](" + englishName + ") | [简体中文](" + zhName + ")"
		zhSwitch := "[English](" + englishName + ") | 简体中文"
		if !strings.Contains(string(englishRaw), englishSwitch) {
			missing = append(missing, filepath.ToSlash(mustRel(t, repo, english))+" -> missing language switch")
		}
		if !strings.Contains(string(zhRaw), zhSwitch) {
			missing = append(missing, filepath.ToSlash(mustRel(t, repo, zh))+" -> missing language switch")
		}
	}

	zhFiles, err := filepath.Glob(filepath.Join(repo, "README*.zh-CN.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := filepath.WalkDir(docsDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".zh-CN.md") {
			return nil
		}
		zhFiles = append(zhFiles, path)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, zh := range zhFiles {
		english := strings.TrimSuffix(zh, ".zh-CN.md") + ".md"
		if _, err := os.Stat(english); err != nil {
			missing = append(missing, filepath.ToSlash(mustRel(t, repo, zh))+" -> missing English pair")
		}
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("broken bilingual documentation coverage:\n%s", strings.Join(missing, "\n"))
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
