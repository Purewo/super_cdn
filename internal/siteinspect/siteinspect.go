package siteinspect

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const (
	SiteConfigFile  = "supercdn.site.json"
	maxInspectBytes = 1 << 20
)

type File struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type Warning struct {
	Code    string `json:"code"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

type Report struct {
	FileCount       int       `json:"file_count"`
	TotalSize       int64     `json:"total_size"`
	LargestFileSize int64     `json:"largest_file_size"`
	HasIndex        bool      `json:"has_index"`
	Features        []string  `json:"features,omitempty"`
	Warnings        []Warning `json:"warnings,omitempty"`
}

type ReadFunc func(filePath string, maxBytes int64) ([]byte, error)

func InspectFiles(files []File, read ReadFunc) Report {
	report := Report{FileCount: len(files)}
	features := map[string]bool{}
	warnings := map[string]Warning{}
	addFeature := func(feature string) {
		features[feature] = true
	}
	addWarning := func(code, filePath, message string) {
		key := code + "\x00" + filePath
		if _, ok := warnings[key]; ok {
			return
		}
		warnings[key] = Warning{Code: code, Path: filePath, Message: message}
	}
	for _, file := range files {
		report.TotalSize += file.Size
		if file.Size > report.LargestFileSize {
			report.LargestFileSize = file.Size
		}
		if file.Path == "index.html" {
			report.HasIndex = true
		}
		inspectFile(file, read, addFeature, addWarning)
	}
	if !report.HasIndex {
		addWarning("missing_index", "", "bundle does not contain index.html")
	}
	if report.FileCount > 1000 {
		addWarning("many_files", "", fmt.Sprintf("bundle contains %d files; large file counts increase upload and deployment risk", report.FileCount))
	}
	report.Features = sortedKeys(features)
	report.Warnings = sortedWarnings(warnings)
	return report
}

func InspectDirectory(root string) (Report, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return Report{}, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return Report{}, err
	}
	if !info.IsDir() {
		return Report{}, fmt.Errorf("%s is not a directory", root)
	}
	var files []File
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
		clean, err := cleanPath(filepath.ToSlash(rel))
		if err != nil || clean == SiteConfigFile || shouldSkip(clean) {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		files = append(files, File{Path: clean, Size: info.Size()})
		return nil
	})
	if err != nil {
		return Report{}, err
	}
	if len(files) == 0 {
		return Report{}, fmt.Errorf("%s contains no deployable files", root)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return InspectFiles(files, func(filePath string, maxBytes int64) ([]byte, error) {
		return readLimitedFile(filepath.Join(root, filepath.FromSlash(filePath)), maxBytes)
	}), nil
}

func InspectZip(zipPath string) (Report, error) {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return Report{}, err
	}
	defer reader.Close()
	files := make([]File, 0, len(reader.File))
	byPath := map[string]*zip.File{}
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		clean, err := cleanPath(entry.Name)
		if err != nil {
			return Report{}, fmt.Errorf("invalid zip path %q: %w", entry.Name, err)
		}
		if clean == SiteConfigFile || shouldSkip(clean) {
			continue
		}
		if byPath[clean] != nil {
			return Report{}, fmt.Errorf("duplicate site file %q", clean)
		}
		byPath[clean] = entry
		files = append(files, File{Path: clean, Size: int64(entry.UncompressedSize64)})
	}
	if len(files) == 0 {
		return Report{}, fmt.Errorf("%s contains no deployable files", zipPath)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return InspectFiles(files, func(filePath string, maxBytes int64) ([]byte, error) {
		entry := byPath[filePath]
		if entry == nil {
			return nil, os.ErrNotExist
		}
		rc, err := entry.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(io.LimitReader(rc, maxBytes))
	}), nil
}

func inspectFile(file File, read ReadFunc, addFeature func(string), addWarning func(string, string, string)) {
	ext := strings.ToLower(path.Ext(file.Path))
	switch ext {
	case ".map":
		addFeature("source_maps")
		addWarning("source_map", file.Path, "source map files will be publicly reachable if deployed")
	case ".wasm":
		addFeature("wasm")
		addWarning("wasm_cross_origin", file.Path, "wasm can require strict MIME and CORS headers when redirected to storage")
	case ".woff", ".woff2", ".ttf", ".otf", ".eot":
		addFeature("fonts")
		addWarning("font_cross_origin", file.Path, "font files can require CORS headers when CSS is redirected to storage")
	}
	lowerPath := strings.ToLower(file.Path)
	if isServiceWorkerPath(lowerPath) {
		addFeature("service_worker")
		addWarning("service_worker", file.Path, "service workers usually need same-origin delivery; keep this file on origin if registration fails")
	}
	if file.Size > 50<<20 {
		addWarning("large_file", file.Path, fmt.Sprintf("file is %d bytes; direct storage delivery is preferred for large assets", file.Size))
	}
	if read == nil || !textLike(ext) {
		return
	}
	raw, err := read(file.Path, maxInspectBytes)
	if err != nil {
		addWarning("inspect_read_failed", file.Path, err.Error())
		return
	}
	inspectText(file.Path, ext, raw, addFeature, addWarning)
}

func inspectText(filePath, ext string, raw []byte, addFeature func(string), addWarning func(string, string, string)) {
	lower := bytes.ToLower(raw)
	switch ext {
	case ".html", ".htm":
		if bytes.Contains(lower, []byte("type=\"module\"")) || bytes.Contains(lower, []byte("type='module'")) {
			addFeature("module_scripts")
			addWarning("module_script", filePath, "module scripts redirected to storage require correct JavaScript MIME and CORS headers")
		}
		if bytes.Contains(lower, []byte("rel=\"modulepreload\"")) || bytes.Contains(lower, []byte("rel='modulepreload'")) {
			addFeature("modulepreload")
			addWarning("modulepreload", filePath, "modulepreload resources redirected to storage require correct CORS behavior")
		}
		if hasRootAbsoluteHTMLRef(lower) {
			addFeature("root_absolute_paths")
			addWarning("root_absolute_paths", filePath, "bundle contains root-absolute internal paths; local /s/{site}/ testing cannot infer the site for those paths")
		}
	case ".css":
		if hasCSSRelativeURL(lower) {
			addFeature("css_relative_urls")
			addWarning("css_relative_urls", filePath, "CSS relative url(...) references may resolve against the final storage URL after a 302")
		}
		if hasCSSRootAbsoluteURL(lower) {
			addFeature("root_absolute_paths")
		}
	case ".js", ".mjs":
		if bytes.Contains(lower, []byte("import(")) {
			addFeature("dynamic_import")
			addWarning("dynamic_import", filePath, "dynamic imports may resolve chunks against the final storage URL after a 302")
		}
		if bytes.Contains(lower, []byte("import.meta.url")) || bytes.Contains(lower, []byte("new url(")) {
			addFeature("import_meta_url")
			addWarning("import_meta_url", filePath, "import.meta.url/new URL asset references may resolve against the final storage URL after a 302")
		}
		if bytes.Contains(lower, []byte("new worker(")) || bytes.Contains(lower, []byte("serviceworker")) || bytes.Contains(lower, []byte("service-worker")) {
			addFeature("workers")
			addWarning("worker", filePath, "workers and service workers are sensitive to same-origin, MIME, and CORS behavior")
		}
	}
}

func textLike(ext string) bool {
	switch ext {
	case ".html", ".htm", ".js", ".mjs", ".css":
		return true
	default:
		return false
	}
}

func hasRootAbsoluteHTMLRef(raw []byte) bool {
	prefixes := [][]byte{
		[]byte("src=\"/"), []byte("src='/"),
		[]byte("href=\"/"), []byte("href='/"),
		[]byte("content=\"/"), []byte("content='/"),
	}
	for _, prefix := range prefixes {
		idx := bytes.Index(raw, prefix)
		if idx >= 0 {
			next := idx + len(prefix)
			if next >= len(raw) || raw[next] != '/' {
				return true
			}
		}
	}
	return false
}

func hasCSSRelativeURL(raw []byte) bool {
	for _, target := range cssURLTargets(raw) {
		if target == "" || strings.HasPrefix(target, "/") || strings.HasPrefix(target, "#") || strings.HasPrefix(target, "data:") || strings.HasPrefix(target, "http:") || strings.HasPrefix(target, "https:") {
			continue
		}
		return true
	}
	return false
}

func hasCSSRootAbsoluteURL(raw []byte) bool {
	for _, target := range cssURLTargets(raw) {
		if strings.HasPrefix(target, "/") && !strings.HasPrefix(target, "//") {
			return true
		}
	}
	return false
}

func cssURLTargets(raw []byte) []string {
	var targets []string
	rest := raw
	for {
		idx := bytes.Index(rest, []byte("url("))
		if idx < 0 {
			return targets
		}
		rest = rest[idx+4:]
		end := bytes.IndexByte(rest, ')')
		if end < 0 {
			return targets
		}
		target := strings.TrimSpace(string(rest[:end]))
		target = strings.Trim(target, `"'`)
		targets = append(targets, target)
		rest = rest[end+1:]
	}
}

func isServiceWorkerPath(value string) bool {
	base := path.Base(value)
	return base == "sw.js" || base == "service-worker.js" || base == "ngsw-worker.js" || strings.HasPrefix(base, "workbox-")
}

func cleanPath(value string) (string, error) {
	value = strings.ReplaceAll(value, "\\", "/")
	value = strings.TrimLeft(value, "/")
	clean := path.Clean(value)
	if clean == "." || clean == "" || strings.HasPrefix(clean, "../") || clean == ".." {
		return "", fmt.Errorf("unsafe path")
	}
	return clean, nil
}

func shouldSkip(clean string) bool {
	return strings.HasPrefix(clean, "__MACOSX/") || path.Base(clean) == ".DS_Store"
}

func readLimitedFile(filePath string, maxBytes int64) ([]byte, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxBytes))
}

func sortedKeys(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func sortedWarnings(values map[string]Warning) []Warning {
	out := make([]Warning, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Code != out[j].Code {
			return out[i].Code < out[j].Code
		}
		return out[i].Path < out[j].Path
	})
	return out
}
