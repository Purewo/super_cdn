package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type LocalStore struct {
	name string
	root string
}

type localMetadata struct {
	ContentType  string    `json:"content_type"`
	CacheControl string    `json:"cache_control"`
	SHA256       string    `json:"sha256"`
	Size         int64     `json:"size"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func NewLocalStore(name, root string) (*LocalStore, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &LocalStore{name: name, root: root}, nil
}

func (s *LocalStore) Name() string { return s.name }
func (s *LocalStore) Type() string { return "local" }

func (s *LocalStore) Put(_ context.Context, opts PutOptions) (string, error) {
	dst, err := s.fullPath(opts.Key)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	in, err := os.Open(opts.FilePath)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, in); closeErr(out, err) != nil {
		return "", closeErr(out, err)
	}
	meta := localMetadata{
		ContentType:  firstNonEmpty(opts.ContentType, detectByName(opts.Key)),
		CacheControl: opts.CacheControl,
		SHA256:       opts.SHA256,
		Size:         opts.Size,
		UpdatedAt:    time.Now().UTC(),
	}
	if err := writeJSON(metaPath(dst), meta); err != nil {
		return "", err
	}
	return dst, nil
}

func (s *LocalStore) Get(_ context.Context, key string, opts GetOptions) (*ObjectStream, error) {
	p, err := s.fullPath(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	stat, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	meta := readLocalMetadata(p)
	meta.Size = stat.Size()
	if meta.ContentType == "" {
		meta.ContentType = detectByName(key)
	}
	status := http.StatusOK
	body := io.ReadCloser(f)
	size := stat.Size()
	contentRange := ""
	if opts.Range != "" {
		start, length, cr, ok := parseSingleRange(opts.Range, stat.Size())
		if !ok {
			_ = f.Close()
			return &ObjectStream{
				Body:         io.NopCloser(strings.NewReader("")),
				StatusCode:   http.StatusRequestedRangeNotSatisfiable,
				Size:         0,
				ContentRange: fmt.Sprintf("bytes */%d", stat.Size()),
			}, nil
		}
		status = http.StatusPartialContent
		body = &sectionReadCloser{SectionReader: io.NewSectionReader(f, start, length), closer: f}
		size = length
		contentRange = cr
	}
	return &ObjectStream{
		Body:         body,
		StatusCode:   status,
		Size:         size,
		ContentType:  meta.ContentType,
		CacheControl: meta.CacheControl,
		ETag:         quoteETag(meta.SHA256),
		LastModified: stat.ModTime().UTC(),
		ContentRange: contentRange,
		Locator:      p,
	}, nil
}

func (s *LocalStore) Stat(_ context.Context, key string) (*Stat, error) {
	p, err := s.fullPath(key)
	if err != nil {
		return nil, err
	}
	fs, err := os.Stat(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	meta := readLocalMetadata(p)
	if meta.ContentType == "" {
		meta.ContentType = detectByName(key)
	}
	return &Stat{
		Size:         fs.Size(),
		ContentType:  meta.ContentType,
		CacheControl: meta.CacheControl,
		ETag:         quoteETag(meta.SHA256),
		LastModified: fs.ModTime().UTC(),
		Locator:      p,
	}, nil
}

func (s *LocalStore) Delete(_ context.Context, key string) error {
	p, err := s.fullPath(key)
	if err != nil {
		return err
	}
	_ = os.Remove(metaPath(p))
	err = os.Remove(p)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (s *LocalStore) PublicURL(key string) string {
	p, err := s.fullPath(key)
	if err != nil {
		return ""
	}
	return p
}

func (s *LocalStore) HealthCheck(ctx context.Context, opts HealthCheckOptions) (*HealthCheckResult, error) {
	checkedAt := time.Now().UTC()
	mode := HealthModePassive
	if opts.WriteProbe {
		mode = HealthModeWriteProbe
	}
	item := HealthCheckItem{
		Target:     s.name,
		TargetType: s.Type(),
		Status:     HealthStatusOK,
		CheckMode:  mode,
		CheckedAt:  checkedAt,
	}
	start := time.Now()
	info, err := os.Stat(s.root)
	item.ListLatencyMS = elapsedMS(start)
	if err != nil {
		item.Status = HealthStatusFailed
		item.LastError = err.Error()
		return &HealthCheckResult{Target: s.name, TargetType: s.Type(), Items: []HealthCheckItem{item}}, err
	}
	if !info.IsDir() {
		err = fmt.Errorf("local root %q is not a directory", s.root)
		item.Status = HealthStatusFailed
		item.LastError = err.Error()
		return &HealthCheckResult{Target: s.name, TargetType: s.Type(), Items: []HealthCheckItem{item}}, err
	}
	if opts.WriteProbe {
		if err := s.localWriteProbe(ctx, opts, &item); err != nil {
			item.Status = HealthStatusFailed
			item.LastError = err.Error()
			return &HealthCheckResult{Target: s.name, TargetType: s.Type(), Items: []HealthCheckItem{item}}, err
		}
	}
	return &HealthCheckResult{Target: s.name, TargetType: s.Type(), Items: []HealthCheckItem{item}}, nil
}

func (s *LocalStore) InitDirs(_ context.Context, opts InitOptions) (*InitResult, error) {
	result := &InitResult{Target: s.name, TargetType: s.Type()}
	dirs, err := expandInitDirs(opts.Directories)
	if err != nil {
		return nil, err
	}
	var firstErr error
	for _, dir := range dirs {
		dst, err := s.fullDirPath(dir)
		item := InitPathResult{Path: dir, RemotePath: dst}
		if err != nil {
			item.Status = "error"
			item.Error = err.Error()
			if firstErr == nil {
				firstErr = err
			}
			result.Directories = append(result.Directories, item)
			continue
		}
		if opts.DryRun {
			item.Status = "planned"
			result.Directories = append(result.Directories, item)
			continue
		}
		if _, err := os.Stat(dst); err == nil {
			item.Status = "exists"
		} else if os.IsNotExist(err) {
			if err := os.MkdirAll(dst, 0o755); err != nil {
				item.Status = "error"
				item.Error = err.Error()
				if firstErr == nil {
					firstErr = err
				}
			} else {
				item.Status = "created"
			}
		} else {
			item.Status = "error"
			item.Error = err.Error()
			if firstErr == nil {
				firstErr = err
			}
		}
		result.Directories = append(result.Directories, item)
	}
	if opts.MarkerPath != "" {
		dst, err := s.fullPath(opts.MarkerPath)
		item := InitPathResult{Path: opts.MarkerPath, RemotePath: dst}
		if err != nil {
			item.Status = "error"
			item.Error = err.Error()
			if firstErr == nil {
				firstErr = err
			}
		} else if opts.DryRun {
			item.Status = "planned"
		} else if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			item.Status = "error"
			item.Error = err.Error()
			if firstErr == nil {
				firstErr = err
			}
		} else if err := os.WriteFile(dst, opts.MarkerPayload, 0o644); err != nil {
			item.Status = "error"
			item.Error = err.Error()
			if firstErr == nil {
				firstErr = err
			}
		} else {
			item.Status = "written"
		}
		result.Files = append(result.Files, item)
	}
	return result, firstErr
}

func (s *LocalStore) localWriteProbe(ctx context.Context, opts HealthCheckOptions, item *HealthCheckItem) error {
	key := firstNonEmpty(opts.ProbeKey, "_supercdn/healthcheck.tmp")
	payload := opts.ProbePayload
	if len(payload) == 0 {
		payload = []byte("supercdn health probe\n")
	}
	dst, err := s.fullPath(key)
	if err != nil {
		return err
	}
	start := time.Now()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		item.WriteLatencyMS = elapsedMS(start)
		return err
	}
	if err := os.WriteFile(dst, payload, 0o644); err != nil {
		item.WriteLatencyMS = elapsedMS(start)
		return err
	}
	item.WriteLatencyMS = elapsedMS(start)
	defer func() {
		start := time.Now()
		_ = os.Remove(dst)
		item.DeleteLatencyMS = elapsedMS(start)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	start = time.Now()
	raw, err := os.ReadFile(dst)
	item.ReadLatencyMS = elapsedMS(start)
	if err != nil {
		return err
	}
	if !bytes.Equal(raw, payload) {
		return fmt.Errorf("local health probe readback mismatch")
	}
	return nil
}

func (s *LocalStore) fullPath(key string) (string, error) {
	clean, err := CleanObjectPath(key)
	if err != nil {
		return "", err
	}
	root, err := filepath.Abs(s.root)
	if err != nil {
		return "", err
	}
	p := filepath.Join(root, filepath.FromSlash(clean))
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if abs != root && !strings.HasPrefix(abs, root+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes local store root")
	}
	return abs, nil
}

func (s *LocalStore) fullDirPath(dir string) (string, error) {
	clean, err := CleanDirectoryPath(dir)
	if err != nil {
		return "", err
	}
	root, err := filepath.Abs(s.root)
	if err != nil {
		return "", err
	}
	p := root
	if clean != "" {
		p = filepath.Join(root, filepath.FromSlash(clean))
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if abs != root && !strings.HasPrefix(abs, root+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes local store root")
	}
	return abs, nil
}

type sectionReadCloser struct {
	*io.SectionReader
	closer io.Closer
}

func (r *sectionReadCloser) Close() error { return r.closer.Close() }

func parseSingleRange(header string, size int64) (start, length int64, contentRange string, ok bool) {
	if !strings.HasPrefix(header, "bytes=") || strings.Contains(header, ",") || size < 0 {
		return 0, 0, "", false
	}
	spec := strings.TrimPrefix(header, "bytes=")
	parts := strings.SplitN(spec, "-", 2)
	if len(parts) != 2 {
		return 0, 0, "", false
	}
	if parts[0] == "" {
		suffix, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || suffix <= 0 {
			return 0, 0, "", false
		}
		if suffix > size {
			suffix = size
		}
		start = size - suffix
		length = suffix
	} else {
		var err error
		start, err = strconv.ParseInt(parts[0], 10, 64)
		if err != nil || start < 0 || start >= size {
			return 0, 0, "", false
		}
		end := size - 1
		if parts[1] != "" {
			end, err = strconv.ParseInt(parts[1], 10, 64)
			if err != nil || end < start {
				return 0, 0, "", false
			}
			if end >= size {
				end = size - 1
			}
		}
		length = end - start + 1
	}
	if length < 0 {
		return 0, 0, "", false
	}
	return start, length, fmt.Sprintf("bytes %d-%d/%d", start, start+length-1, size), true
}

func readLocalMetadata(path string) localMetadata {
	var meta localMetadata
	raw, err := os.ReadFile(metaPath(path))
	if err == nil {
		_ = json.Unmarshal(raw, &meta)
	}
	return meta
}

func writeJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

func metaPath(path string) string { return path + ".meta.json" }

func detectByName(name string) string {
	if ctype := mime.TypeByExtension(filepath.Ext(name)); ctype != "" {
		return ctype
	}
	return "application/octet-stream"
}

func quoteETag(v string) string {
	if v == "" {
		return ""
	}
	if strings.HasPrefix(v, "\"") {
		return v
	}
	return `"` + v + `"`
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func closeErr(c io.Closer, previous error) error {
	err := c.Close()
	if previous != nil {
		return previous
	}
	return err
}
