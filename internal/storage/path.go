package storage

import (
	"errors"
	"path"
	"strings"
)

func CleanObjectPath(p string) (string, error) {
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.TrimSpace(p)
	p = strings.TrimPrefix(p, "/")
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return "", errors.New("path traversal is not allowed")
		}
	}
	p = path.Clean("/" + p)
	if p == "/" || strings.Contains(p, "\x00") {
		return "", errors.New("path must point to a file")
	}
	return strings.TrimPrefix(p, "/"), nil
}

func CleanDirectoryPath(p string) (string, error) {
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.TrimSpace(p)
	p = strings.TrimPrefix(p, "/")
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return "", errors.New("path traversal is not allowed")
		}
	}
	p = path.Clean("/" + p)
	if strings.Contains(p, "\x00") {
		return "", errors.New("path contains NUL byte")
	}
	if p == "/" {
		return "", nil
	}
	return strings.TrimPrefix(p, "/"), nil
}

func JoinKey(parts ...string) string {
	cleaned := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.ReplaceAll(p, "\\", "/")
		p = strings.Trim(p, "/")
		if p != "" {
			cleaned = append(cleaned, p)
		}
	}
	return path.Join(cleaned...)
}
