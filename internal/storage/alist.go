package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"
)

type AListStore struct {
	name          string
	baseURL       string
	tokenMu       sync.Mutex
	token         string
	username      string
	password      string
	root          string
	useProxyURL   bool
	publicBaseURL string
	client        *http.Client
}

type AListOptions struct {
	Name          string
	BaseURL       string
	Token         string
	Username      string
	Password      string
	Root          string
	UseProxyURL   bool
	PublicBaseURL string
	ProxyURL      string
}

func NewAListStore(opts AListOptions) (*AListStore, error) {
	base := strings.TrimRight(opts.BaseURL, "/")
	public := strings.TrimRight(firstNonEmpty(opts.PublicBaseURL, base), "/")
	client, err := newHTTPClient(opts.ProxyURL)
	if err != nil {
		return nil, err
	}
	return &AListStore{
		name:          opts.Name,
		baseURL:       base,
		token:         opts.Token,
		username:      opts.Username,
		password:      opts.Password,
		root:          "/" + strings.Trim(strings.ReplaceAll(opts.Root, "\\", "/"), "/"),
		useProxyURL:   opts.UseProxyURL,
		publicBaseURL: public,
		client:        client,
	}, nil
}

func (s *AListStore) Name() string { return s.name }
func (s *AListStore) Type() string { return "alist" }

func (s *AListStore) Put(ctx context.Context, opts PutOptions) (string, error) {
	f, err := os.Open(opts.FilePath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if err := s.putReader(ctx, opts.Key, f, opts.Size, firstNonEmpty(opts.ContentType, "application/octet-stream")); err != nil {
		return "", err
	}
	return s.PublicURL(opts.Key), nil
}

func (s *AListStore) Get(ctx context.Context, key string, opts GetOptions) (*ObjectStream, error) {
	info, err := s.getInfo(ctx, key)
	if err != nil {
		return nil, err
	}
	locator := s.locatorURL(key, info)
	if locator == "" {
		return nil, fmt.Errorf("alist returned no download URL for %s", key)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, locator, nil)
	if err != nil {
		return nil, err
	}
	if opts.Range != "" {
		req.Header.Set("Range", opts.Range)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		_ = resp.Body.Close()
		return nil, ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		_ = resp.Body.Close()
		return nil, fmt.Errorf("alist download failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return &ObjectStream{
		Body:         resp.Body,
		StatusCode:   resp.StatusCode,
		Size:         resp.ContentLength,
		ContentType:  firstNonEmpty(resp.Header.Get("Content-Type"), detectByName(key)),
		CacheControl: resp.Header.Get("Cache-Control"),
		ETag:         resp.Header.Get("ETag"),
		LastModified: parseHTTPTime(resp.Header.Get("Last-Modified")),
		ContentRange: resp.Header.Get("Content-Range"),
		Locator:      locator,
	}, nil
}

func (s *AListStore) Stat(ctx context.Context, key string) (*Stat, error) {
	info, err := s.getInfo(ctx, key)
	if err != nil {
		return nil, err
	}
	return &Stat{
		Size:         info.Size,
		ContentType:  detectByName(key),
		LastModified: info.ModifiedTime(),
		Locator:      s.locatorURL(key, info),
	}, nil
}

func (s *AListStore) Delete(ctx context.Context, key string) error {
	payload := map[string]any{
		"dir":   path.Dir(s.remotePath(key)),
		"names": []string{path.Base(key)},
	}
	raw, _ := json.Marshal(payload)
	var resp aListEnvelope[any]
	if err := s.doAuthorizedJSON(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/api/fs/remove", bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	}, &resp); err != nil {
		return err
	}
	if resp.Code != 200 {
		return fmt.Errorf("alist delete failed: code=%d message=%s", resp.Code, resp.Message)
	}
	return nil
}

func (s *AListStore) PublicURL(key string) string {
	return s.proxyURL(key, "")
}

func (s *AListStore) HealthCheck(ctx context.Context, opts HealthCheckOptions) (*HealthCheckResult, error) {
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
	exists, err := s.dirExists(ctx, s.root)
	item.ListLatencyMS = elapsedMS(start)
	if err != nil {
		item.Status = HealthStatusFailed
		item.LastError = err.Error()
		return &HealthCheckResult{Target: s.name, TargetType: s.Type(), Items: []HealthCheckItem{item}}, err
	}
	if !exists {
		err = fmt.Errorf("alist root path %q does not exist", s.root)
		item.Status = HealthStatusFailed
		item.LastError = err.Error()
		return &HealthCheckResult{Target: s.name, TargetType: s.Type(), Items: []HealthCheckItem{item}}, err
	}
	if opts.WriteProbe {
		if err := s.writeProbe(ctx, opts, &item); err != nil {
			item.Status = HealthStatusFailed
			item.LastError = err.Error()
			return &HealthCheckResult{Target: s.name, TargetType: s.Type(), Items: []HealthCheckItem{item}}, err
		}
	}
	return &HealthCheckResult{Target: s.name, TargetType: s.Type(), Items: []HealthCheckItem{item}}, nil
}

func (s *AListStore) InitDirs(ctx context.Context, opts InitOptions) (*InitResult, error) {
	result := &InitResult{Target: s.name, TargetType: s.Type()}
	dirs, err := expandInitDirs(opts.Directories)
	if err != nil {
		return nil, err
	}
	var firstErr error
	for _, dir := range dirs {
		remote, err := s.remoteDirPath(dir)
		if err != nil {
			result.Directories = append(result.Directories, InitPathResult{Path: dir, Status: "error", Error: err.Error()})
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		item := InitPathResult{Path: dir, RemotePath: remote}
		if opts.DryRun {
			item.Status = "planned"
			result.Directories = append(result.Directories, item)
			continue
		}
		exists, err := s.dirExists(ctx, remote)
		if err != nil {
			item.Status = "error"
			item.Error = err.Error()
			result.Directories = append(result.Directories, item)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if exists {
			item.Status = "exists"
			result.Directories = append(result.Directories, item)
			continue
		}
		status, err := s.mkdir(ctx, remote)
		item.Status = status
		if err != nil {
			item.Error = err.Error()
			if firstErr == nil {
				firstErr = err
			}
		}
		result.Directories = append(result.Directories, item)
	}
	if opts.MarkerPath != "" {
		fileResult := s.initMarker(ctx, opts)
		result.Files = append(result.Files, fileResult)
		if fileResult.Error != "" && firstErr == nil {
			firstErr = fmt.Errorf("%s", fileResult.Error)
		}
	}
	return result, firstErr
}

func (s *AListStore) getInfo(ctx context.Context, key string) (aListFileInfo, error) {
	payload := map[string]any{"path": s.remotePath(key), "password": ""}
	raw, _ := json.Marshal(payload)
	var resp aListEnvelope[aListFileInfo]
	if err := s.doAuthorizedJSON(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/api/fs/get", bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	}, &resp); err != nil {
		return aListFileInfo{}, err
	}
	if resp.Code == 404 {
		return aListFileInfo{}, ErrNotFound
	}
	if resp.Code != 200 {
		return aListFileInfo{}, fmt.Errorf("alist get failed: code=%d message=%s", resp.Code, resp.Message)
	}
	return resp.Data, nil
}

func (s *AListStore) putReader(ctx context.Context, key string, body io.Reader, size int64, contentType string) error {
	if _, err := CleanObjectPath(key); err != nil {
		return err
	}
	if err := s.ensureParentDir(ctx, key); err != nil {
		return err
	}
	var buildCount int
	var resp aListEnvelope[any]
	if err := s.doAuthorizedJSON(ctx, func() (*http.Request, error) {
		if buildCount > 0 {
			seeker, ok := body.(io.Seeker)
			if !ok {
				return nil, fmt.Errorf("alist upload cannot be retried because request body is not seekable")
			}
			if _, err := seeker.Seek(0, io.SeekStart); err != nil {
				return nil, err
			}
		}
		buildCount++
		requestBody := io.Reader(io.NopCloser(body))
		if size == 0 {
			requestBody = http.NoBody
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.baseURL+"/api/fs/put", requestBody)
		if err != nil {
			return nil, err
		}
		req.Header.Set("File-Path", s.remotePath(key))
		req.Header.Set("As-Task", "false")
		req.Header.Set("Content-Type", firstNonEmpty(contentType, "application/octet-stream"))
		if size >= 0 {
			req.ContentLength = size
		}
		return req, nil
	}, &resp); err != nil {
		return err
	}
	if resp.Code != 200 {
		return fmt.Errorf("alist upload failed: code=%d message=%s", resp.Code, resp.Message)
	}
	return nil
}

func (s *AListStore) ensureParentDir(ctx context.Context, key string) error {
	remote := path.Clean(s.remotePath(key))
	parent := path.Dir(remote)
	root := path.Clean(s.root)
	if root == "." || root == "" {
		root = "/"
	}
	if parent == "." || parent == "" {
		parent = "/"
	}
	if root != "/" {
		exists, err := s.dirExists(ctx, root)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("alist root path %q does not exist", root)
		}
	}
	if parent == root || parent == "/" {
		return nil
	}
	rel := strings.TrimPrefix(parent, root)
	if root == "/" {
		rel = strings.TrimPrefix(parent, "/")
	}
	rel = strings.Trim(rel, "/")
	if rel == "" {
		return nil
	}
	current := root
	for _, part := range strings.Split(rel, "/") {
		if part == "" {
			continue
		}
		if current == "/" {
			current = "/" + part
		} else {
			current = path.Join(current, part)
		}
		exists, err := s.dirExists(ctx, current)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := s.mkdir(ctx, current); err != nil {
			return fmt.Errorf("ensure alist dir %q: %w", current, err)
		}
	}
	return nil
}

func (s *AListStore) dirExists(ctx context.Context, remote string) (bool, error) {
	payload := map[string]any{
		"path":     remote,
		"password": "",
		"page":     1,
		"per_page": 1,
		"refresh":  false,
	}
	raw, _ := json.Marshal(payload)
	var resp aListEnvelope[json.RawMessage]
	if err := s.doAuthorizedJSON(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/api/fs/list", bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	}, &resp); err != nil {
		return false, err
	}
	if resp.Code == 200 {
		return true, nil
	}
	if resp.Code == 404 || isMissingMessage(resp.Message) {
		return false, nil
	}
	return false, fmt.Errorf("alist list failed: code=%d message=%s", resp.Code, resp.Message)
}

func (s *AListStore) mkdir(ctx context.Context, remote string) (string, error) {
	payload := map[string]any{"path": remote}
	raw, _ := json.Marshal(payload)
	var resp aListEnvelope[any]
	if err := s.doAuthorizedJSON(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/api/fs/mkdir", bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	}, &resp); err != nil {
		return "error", err
	}
	if resp.Code == 200 {
		return "created", nil
	}
	if isExistsMessage(resp.Message) {
		return "exists", nil
	}
	return "error", fmt.Errorf("alist mkdir failed: code=%d message=%s", resp.Code, resp.Message)
}

func (s *AListStore) initMarker(ctx context.Context, opts InitOptions) InitPathResult {
	remote, err := s.remoteFilePath(opts.MarkerPath)
	item := InitPathResult{Path: opts.MarkerPath, RemotePath: remote}
	if err != nil {
		item.Status = "error"
		item.Error = err.Error()
		return item
	}
	if opts.DryRun {
		item.Status = "planned"
		return item
	}
	if err := s.putReader(ctx, opts.MarkerPath, bytes.NewReader(opts.MarkerPayload), int64(len(opts.MarkerPayload)), "application/json"); err != nil {
		item.Status = "error"
		item.Error = err.Error()
		return item
	}
	item.Status = "written"
	return item
}

func (s *AListStore) writeProbe(ctx context.Context, opts HealthCheckOptions, item *HealthCheckItem) error {
	key := firstNonEmpty(opts.ProbeKey, "_supercdn/healthcheck.tmp")
	payload := opts.ProbePayload
	if len(payload) == 0 {
		payload = []byte("supercdn health probe\n")
	}
	start := time.Now()
	if err := s.putReader(ctx, key, bytes.NewReader(payload), int64(len(payload)), "text/plain; charset=utf-8"); err != nil {
		item.WriteLatencyMS = elapsedMS(start)
		return err
	}
	item.WriteLatencyMS = elapsedMS(start)
	defer func() {
		start := time.Now()
		_ = s.Delete(context.WithoutCancel(ctx), key)
		item.DeleteLatencyMS = elapsedMS(start)
	}()
	start = time.Now()
	stream, err := s.Get(ctx, key, GetOptions{})
	if err != nil {
		item.ReadLatencyMS = elapsedMS(start)
		return err
	}
	_, readErr := io.Copy(io.Discard, io.LimitReader(stream.Body, int64(len(payload))))
	closeErr := stream.Body.Close()
	item.ReadLatencyMS = elapsedMS(start)
	if readErr != nil {
		return readErr
	}
	return closeErr
}

func (s *AListStore) doJSON(req *http.Request, out any) error {
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("alist http error: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	return json.Unmarshal(body, out)
}

func (s *AListStore) doAuthorizedJSON(ctx context.Context, build func() (*http.Request, error), out any) error {
	token := s.currentToken()
	if err := s.doAuthorizedJSONOnce(build, token, out); err != nil {
		if !s.canRefreshToken() || !isAListAuthExpiredError(err) {
			return err
		}
		if refreshErr := s.refreshToken(ctx, token); refreshErr != nil {
			return fmt.Errorf("%w; alist token refresh failed: %v", err, refreshErr)
		}
		return s.doAuthorizedJSONOnce(build, s.currentToken(), out)
	}
	if !aListResponseAuthExpired(out) {
		return nil
	}
	if !s.canRefreshToken() {
		return nil
	}
	if err := s.refreshToken(ctx, token); err != nil {
		return fmt.Errorf("alist token expired and refresh failed: %w", err)
	}
	return s.doAuthorizedJSONOnce(build, s.currentToken(), out)
}

func (s *AListStore) doAuthorizedJSONOnce(build func() (*http.Request, error), token string, out any) error {
	req, err := build()
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	return s.doJSON(req, out)
}

func (s *AListStore) currentToken() string {
	s.tokenMu.Lock()
	defer s.tokenMu.Unlock()
	return s.token
}

func (s *AListStore) canRefreshToken() bool {
	return s.username != "" && s.password != ""
}

func (s *AListStore) refreshToken(ctx context.Context, expiredToken string) error {
	s.tokenMu.Lock()
	defer s.tokenMu.Unlock()
	if s.token != "" && s.token != expiredToken {
		return nil
	}
	payload := map[string]string{
		"username": s.username,
		"password": s.password,
	}
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/api/auth/login", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	var resp aListEnvelope[struct {
		Token string `json:"token"`
	}]
	if err := s.doJSON(req, &resp); err != nil {
		return err
	}
	if resp.Code != 200 {
		return fmt.Errorf("login failed: code=%d message=%s", resp.Code, resp.Message)
	}
	if resp.Data.Token == "" {
		return fmt.Errorf("login response did not include token")
	}
	s.token = resp.Data.Token
	return nil
}

func aListResponseAuthExpired(out any) bool {
	resp, ok := out.(interface {
		aListStatus() (int, string)
	})
	if !ok {
		return false
	}
	code, message := resp.aListStatus()
	return isAListAuthExpired(code, message)
}

func (e *aListEnvelope[T]) aListStatus() (int, string) {
	return e.Code, e.Message
}

func isAListAuthExpired(code int, message string) bool {
	message = strings.ToLower(message)
	if code == http.StatusUnauthorized {
		return true
	}
	return strings.Contains(message, "token is expired") ||
		strings.Contains(message, "token expired") ||
		strings.Contains(message, "invalid token") ||
		strings.Contains(message, "unauthorized")
}

func isAListAuthExpiredError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "status=401") ||
		strings.Contains(message, "token is expired") ||
		strings.Contains(message, "token expired") ||
		strings.Contains(message, "invalid token") ||
		strings.Contains(message, "unauthorized")
}

func (s *AListStore) remotePath(key string) string {
	clean, _ := CleanObjectPath(key)
	if s.root == "/" {
		return "/" + clean
	}
	return path.Join(s.root, clean)
}

func (s *AListStore) remoteDirPath(dir string) (string, error) {
	clean, err := CleanDirectoryPath(dir)
	if err != nil {
		return "", err
	}
	if clean == "" {
		return s.root, nil
	}
	if s.root == "/" {
		return "/" + clean, nil
	}
	return path.Join(s.root, clean), nil
}

func (s *AListStore) remoteFilePath(file string) (string, error) {
	if _, err := CleanObjectPath(file); err != nil {
		return "", err
	}
	return s.remotePath(file), nil
}

func (s *AListStore) proxyURL(key, sign string) string {
	if s.publicBaseURL == "" {
		return ""
	}
	u, err := url.Parse(s.publicBaseURL)
	if err != nil {
		return ""
	}
	u.Path = path.Join(u.Path, "/d", s.remotePath(key))
	if sign != "" {
		q := u.Query()
		q.Set("sign", sign)
		u.RawQuery = q.Encode()
	}
	return u.String()
}

func (s *AListStore) locatorURL(key string, info aListFileInfo) string {
	proxy := s.proxyURL(key, info.Sign)
	if s.useProxyURL {
		return firstNonEmpty(proxy, info.RawURL)
	}
	return firstNonEmpty(info.RawURL, proxy)
}

type aListEnvelope[T any] struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

type aListFileInfo struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	RawURL   string `json:"raw_url"`
	Sign     string `json:"sign"`
	Modified string `json:"modified"`
}

func (i aListFileInfo) ModifiedTime() time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05Z07:00"} {
		if t, err := time.Parse(layout, i.Modified); err == nil {
			return t
		}
	}
	return time.Time{}
}

func parseHTTPTime(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	t, _ := http.ParseTime(v)
	return t
}

func expandInitDirs(dirs []string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{""}
	seen[""] = true
	for _, dir := range dirs {
		clean, err := CleanDirectoryPath(dir)
		if err != nil {
			return nil, err
		}
		if clean == "" {
			continue
		}
		parts := strings.Split(clean, "/")
		for i := range parts {
			prefix := path.Join(parts[:i+1]...)
			if !seen[prefix] {
				seen[prefix] = true
				out = append(out, prefix)
			}
		}
	}
	return out, nil
}

func isMissingMessage(message string) bool {
	message = strings.ToLower(message)
	return strings.Contains(message, "not found") ||
		strings.Contains(message, "no such") ||
		strings.Contains(message, "does not exist") ||
		strings.Contains(message, "not exist")
}

func isExistsMessage(message string) bool {
	message = strings.ToLower(message)
	if strings.Contains(message, "not exist") || strings.Contains(message, "not found") {
		return false
	}
	return strings.Contains(message, "exist") ||
		strings.Contains(message, "already")
}

func elapsedMS(start time.Time) int64 {
	return time.Since(start).Milliseconds()
}
