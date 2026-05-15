package main

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestCommandBookCoversDispatchedCommands(t *testing.T) {
	repo := testRepoRoot(t)
	mainRaw := readTestFile(t, filepath.Join(repo, "cmd", "supercdnctl", "main.go"))
	commandBook := readTestFile(t, filepath.Join(repo, "docs", "commands.md"))
	commands := dispatchedCommandsFromMain(t, mainRaw)

	var missing []string
	for _, command := range commands {
		if !strings.Contains(commandBook, "`"+command+"`") {
			missing = append(missing, command)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("docs/commands.md is missing supercdnctl commands: %s", strings.Join(missing, ", "))
	}
}

func TestUsageCoversDispatchedCommands(t *testing.T) {
	repo := testRepoRoot(t)
	mainRaw := readTestFile(t, filepath.Join(repo, "cmd", "supercdnctl", "main.go"))
	commands := dispatchedCommandsFromMain(t, mainRaw)
	usageText := captureStdout(t, usage)

	var missing []string
	for _, command := range commands {
		if !strings.Contains(usageText, " "+command+" ") && !strings.Contains(usageText, " "+command+"\n") {
			missing = append(missing, command)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("usage() is missing supercdnctl commands: %s", strings.Join(missing, ", "))
	}
}

func dispatchedCommandsFromMain(t *testing.T, mainRaw string) []string {
	t.Helper()
	start := strings.Index(mainRaw, "switch args[0] {")
	if start < 0 {
		t.Fatal("main.go command switch not found")
	}
	body := mainRaw[start:]
	end := strings.Index(body, "\n\tdefault:")
	if end < 0 {
		t.Fatal("main.go command switch default not found")
	}
	body = body[:end]

	commandRE := regexp.MustCompile(`case\s+"([^"]+)":`)
	matches := commandRE.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		t.Fatal("no supercdnctl command cases found in main.go")
	}
	seen := map[string]bool{}
	var commands []string
	for _, match := range matches {
		command := match[1]
		if seen[command] {
			continue
		}
		seen[command] = true
		commands = append(commands, command)
	}
	sort.Strings(commands)
	return commands
}

func testRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
