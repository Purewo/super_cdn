package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func zipDirectory(dir string) (string, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", dir)
	}
	tmp, err := os.CreateTemp("", "supercdn-site-*.zip")
	if err != nil {
		return "", err
	}
	defer tmp.Close()
	zw := zip.NewWriter(tmp)
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = rel
		header.Method = zip.Deflate
		entry, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(entry, f)
		return err
	})
	if closeErr := zw.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

type directorySummary struct {
	FileCount       int
	TotalSize       int64
	LargestFileSize int64
	SHA256          string
}

func summarizeDirectory(dir string) (directorySummary, error) {
	return summarizeDirectoryFiltered(dir, nil)
}

func summarizeCloudflareStaticDirectory(dir string) (directorySummary, error) {
	return summarizeDirectoryFiltered(dir, func(rel string) bool {
		rel = strings.TrimPrefix(filepath.ToSlash(rel), "/")
		return rel == "_headers" || rel == "_redirects"
	})
}

func summarizeDirectoryFiltered(dir string, skip func(rel string) bool) (directorySummary, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return directorySummary{}, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return directorySummary{}, err
	}
	if !info.IsDir() {
		return directorySummary{}, fmt.Errorf("%s is not a directory", dir)
	}
	var summary directorySummary
	var files []string
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if skip != nil && skip(rel) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		summary.FileCount++
		summary.TotalSize += info.Size()
		if info.Size() > summary.LargestFileSize {
			summary.LargestFileSize = info.Size()
		}
		files = append(files, p)
		return nil
	})
	if err != nil {
		return directorySummary{}, err
	}
	if summary.FileCount == 0 {
		return directorySummary{}, fmt.Errorf("%s contains no files", dir)
	}
	sort.Strings(files)
	h := sha256.New()
	for _, file := range files {
		rel, err := filepath.Rel(root, file)
		if err != nil {
			return directorySummary{}, err
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			return directorySummary{}, err
		}
		_, _ = h.Write([]byte(filepath.ToSlash(rel)))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(raw)
		_, _ = h.Write([]byte{0})
	}
	summary.SHA256 = hex.EncodeToString(h.Sum(nil))
	return summary, nil
}
