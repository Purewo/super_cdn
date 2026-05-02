package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"
)

const (
	defaultPinataAPIBaseURL    = "https://api.pinata.cloud"
	defaultPinataUploadBaseURL = "https://uploads.pinata.cloud"
	defaultPinataGroupPrefix   = "supercdn-"
)

type PinataStore struct {
	name          string
	apiBaseURL    string
	uploadBaseURL string
	jwt           string
	gateway       string
	groupPrefix   string
	groupMu       sync.Mutex
	groupIDs      map[string]string
	client        *http.Client
}

type PinataOptions struct {
	APIBaseURL     string
	UploadBaseURL  string
	Name           string
	JWT            string
	GatewayBaseURL string
	GroupPrefix    string
	ProxyURL       string
}

func NewPinataStore(opts PinataOptions) (*PinataStore, error) {
	client, err := newHTTPClient(opts.ProxyURL)
	if err != nil {
		return nil, err
	}
	apiBaseURL := firstNonEmpty(strings.TrimRight(strings.TrimSpace(opts.APIBaseURL), "/"), defaultPinataAPIBaseURL)
	uploadBaseURL := firstNonEmpty(strings.TrimRight(strings.TrimSpace(opts.UploadBaseURL), "/"), defaultPinataUploadBaseURL)
	if strings.TrimSpace(opts.UploadBaseURL) == "" && apiBaseURL != defaultPinataAPIBaseURL {
		uploadBaseURL = apiBaseURL
	}
	jwt := strings.TrimSpace(opts.JWT)
	return &PinataStore{
		name:          opts.Name,
		apiBaseURL:    apiBaseURL,
		uploadBaseURL: uploadBaseURL,
		jwt:           jwt,
		gateway:       strings.TrimRight(strings.TrimSpace(opts.GatewayBaseURL), "/"),
		groupPrefix:   firstNonEmpty(strings.TrimSpace(opts.GroupPrefix), defaultPinataGroupPrefix),
		groupIDs:      map[string]string{},
		client:        client,
	}, nil
}

func (s *PinataStore) Name() string { return s.name }
func (s *PinataStore) Type() string { return "pinata" }

func (s *PinataStore) Put(ctx context.Context, opts PutOptions) (string, error) {
	if s.jwt == "" {
		return "", fmt.Errorf("pinata jwt is not configured")
	}
	groupID, err := s.ensureGroupID(ctx, opts.Group)
	if err != nil {
		return "", err
	}
	return s.putV3(ctx, opts, groupID)
}

func (s *PinataStore) putV3(ctx context.Context, opts PutOptions, groupID string) (string, error) {
	body, contentType, err := pinataUploadBody(opts, groupID)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.uploadBaseURL+"/v3/files", body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+s.jwt)
	req.Header.Set("Content-Type", contentType)
	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("status=%d body=%s", resp.StatusCode, s.redact(strings.TrimSpace(string(raw))))
	}
	var out struct {
		ID   string `json:"id"`
		CID  string `json:"cid"`
		Data struct {
			ID   string `json:"id"`
			CID  string `json:"cid"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	cid := firstNonEmpty(out.Data.CID, out.CID)
	if cid == "" {
		return "", fmt.Errorf("pinata v3 upload returned no cid")
	}
	return pinataLocator(cid, firstNonEmpty(out.Data.ID, out.ID), groupID), nil
}

func pinataUploadBody(opts PutOptions, groupID string) (*bytes.Buffer, string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	f, err := os.Open(opts.FilePath)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	part, err := writer.CreateFormFile("file", firstNonEmpty(opts.FileName, path.Base(opts.Key)))
	if err != nil {
		return nil, "", err
	}
	if _, err := io.Copy(part, f); err != nil {
		return nil, "", err
	}
	keyvalues := map[string]string{
		"sha256": opts.SHA256,
		"key":    opts.Key,
	}
	keyvaluesRaw, _ := json.Marshal(keyvalues)
	_ = writer.WriteField("network", "public")
	_ = writer.WriteField("name", opts.Key)
	_ = writer.WriteField("keyvalues", string(keyvaluesRaw))
	if strings.TrimSpace(groupID) != "" {
		_ = writer.WriteField("group_id", strings.TrimSpace(groupID))
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return &body, writer.FormDataContentType(), nil
}

func (s *PinataStore) Get(ctx context.Context, key string, opts GetOptions) (*ObjectStream, error) {
	locator := firstNonEmpty(opts.Locator, s.PublicURL(key))
	if strings.HasPrefix(locator, "ipfs://") {
		if s.gateway == "" {
			return nil, fmt.Errorf("pinata gateway_base_url is required to read %s", locator)
		}
		cid, ok := IPFSCIDFromLocator(locator)
		if !ok {
			return nil, fmt.Errorf("invalid ipfs locator %s", locator)
		}
		locator = IPFSGatewayURL(s.gateway, cid)
	}
	if locator == "" {
		return nil, ErrNotFound
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
		return nil, fmt.Errorf("pinata gateway failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
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

func (s *PinataStore) Stat(_ context.Context, key string) (*Stat, error) {
	return nil, ErrNotFound
}

func (s *PinataStore) Delete(ctx context.Context, key string) error {
	return s.DeleteLocator(ctx, key, "")
}

func (s *PinataStore) DeleteLocator(ctx context.Context, key, locator string) error {
	cid, ok := IPFSCIDFromLocator(firstNonEmpty(locator, key))
	if !ok {
		return ErrNotFound
	}
	if s.jwt == "" {
		return fmt.Errorf("pinata jwt is not configured")
	}
	if fileID := pinataFileIDFromLocator(locator); fileID != "" {
		if err := s.deleteV3File(ctx, fileID); err == nil {
			return nil
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	if status, err := s.RefreshIPFSPin(ctx, cid); err == nil && status.ProviderPinID != "" {
		return s.deleteV3File(ctx, status.ProviderPinID)
	} else if err != nil {
		return err
	}
	return ErrNotFound
}

func (s *PinataStore) deleteV3File(ctx context.Context, fileID string) error {
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return ErrNotFound
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, s.apiBaseURL+"/v3/files/public/"+url.PathEscape(fileID), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.jwt)
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("pinata v3 delete failed: status=%d body=%s", resp.StatusCode, s.redact(strings.TrimSpace(string(raw))))
	}
	return nil
}

func (s *PinataStore) PublicURL(key string) string {
	if s.gateway == "" {
		return ""
	}
	if cid, ok := IPFSCIDFromLocator(key); ok {
		return IPFSGatewayURL(s.gateway, cid)
	}
	u, err := url.Parse(s.gateway)
	if err != nil {
		return ""
	}
	u.Path = path.Join(u.Path, strings.TrimLeft(key, "/"))
	return u.String()
}

func (s *PinataStore) ProviderStatus(ctx context.Context) ProviderStatus {
	status := ProviderStatus{
		Target:         s.name,
		TargetType:     s.Type(),
		Provider:       "pinata",
		APIBaseURL:     s.apiBaseURL,
		UploadBaseURL:  s.uploadBaseURL,
		GatewayBaseURL: s.gateway,
		CheckedAt:      time.Now().UTC(),
	}
	status.Token = s.pinataTokenStatus(ctx)
	status.Gateway = s.pinataGatewayStatus(ctx)
	status.OK = status.Token.OK && status.Gateway.OK
	if !status.Token.Configured {
		status.Warnings = append(status.Warnings, "pinata jwt is not configured")
	}
	if !status.Gateway.Configured {
		status.Warnings = append(status.Warnings, "pinata gateway_base_url is not configured")
	}
	return status
}

func (s *PinataStore) RefreshIPFSPin(ctx context.Context, cid string) (IPFSPinStatus, error) {
	cid = strings.TrimSpace(cid)
	if cid == "" {
		return IPFSPinStatus{}, fmt.Errorf("cid is required")
	}
	if s.jwt == "" {
		return IPFSPinStatus{}, fmt.Errorf("pinata jwt is not configured")
	}
	return s.refreshV3Pin(ctx, cid)
}

func (s *PinataStore) refreshV3Pin(ctx context.Context, cid string) (IPFSPinStatus, error) {
	q := url.Values{}
	q.Set("cid", cid)
	q.Set("limit", "10")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.apiBaseURL+"/v3/files/public?"+q.Encode(), nil)
	if err != nil {
		return IPFSPinStatus{}, err
	}
	req.Header.Set("Authorization", "Bearer "+s.jwt)
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return IPFSPinStatus{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return IPFSPinStatus{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return IPFSPinStatus{}, fmt.Errorf("status=%d body=%s", resp.StatusCode, s.redact(strings.TrimSpace(string(raw))))
	}
	files, err := parsePinataV3Files(raw)
	if err != nil {
		return IPFSPinStatus{}, err
	}
	status := baseIPFSPinStatus(s.gateway, cid)
	for _, file := range files {
		if file.CID != cid {
			continue
		}
		status.PinStatus = "pinned"
		status.ProviderPinID = file.ID
		status.Locator = pinataLocator(cid, file.ID, "")
		return status, nil
	}
	return status, nil
}

type pinataV3File struct {
	ID  string `json:"id"`
	CID string `json:"cid"`
}

type pinataV3Group struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func parsePinataV3Files(raw []byte) ([]pinataV3File, error) {
	var out struct {
		Data  json.RawMessage `json:"data"`
		Files []pinataV3File  `json:"files"`
		Rows  []pinataV3File  `json:"rows"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	files := append([]pinataV3File{}, out.Files...)
	files = append(files, out.Rows...)
	if len(out.Data) == 0 || string(out.Data) == "null" {
		return files, nil
	}
	var dataList []pinataV3File
	if err := json.Unmarshal(out.Data, &dataList); err == nil {
		return append(files, dataList...), nil
	}
	var dataObj struct {
		Files []pinataV3File `json:"files"`
		Rows  []pinataV3File `json:"rows"`
	}
	if err := json.Unmarshal(out.Data, &dataObj); err != nil {
		return nil, err
	}
	files = append(files, dataObj.Files...)
	files = append(files, dataObj.Rows...)
	return files, nil
}

func parsePinataV3Groups(raw []byte) ([]pinataV3Group, error) {
	var out struct {
		Data   json.RawMessage `json:"data"`
		Groups []pinataV3Group `json:"groups"`
		Rows   []pinataV3Group `json:"rows"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	groups := append([]pinataV3Group{}, out.Groups...)
	groups = append(groups, out.Rows...)
	if len(out.Data) == 0 || string(out.Data) == "null" {
		return groups, nil
	}
	var dataList []pinataV3Group
	if err := json.Unmarshal(out.Data, &dataList); err == nil {
		return append(groups, dataList...), nil
	}
	var dataObj struct {
		ID     string          `json:"id"`
		Name   string          `json:"name"`
		Groups []pinataV3Group `json:"groups"`
		Rows   []pinataV3Group `json:"rows"`
	}
	if err := json.Unmarshal(out.Data, &dataObj); err != nil {
		return nil, err
	}
	if dataObj.ID != "" || dataObj.Name != "" {
		groups = append(groups, pinataV3Group{ID: dataObj.ID, Name: dataObj.Name})
	}
	groups = append(groups, dataObj.Groups...)
	groups = append(groups, dataObj.Rows...)
	return groups, nil
}

func (s *PinataStore) ensureGroupID(ctx context.Context, group string) (string, error) {
	name := pinataGroupName(s.groupPrefix, group)
	if name == "" {
		return "", nil
	}
	s.groupMu.Lock()
	if id := s.groupIDs[name]; id != "" {
		s.groupMu.Unlock()
		return id, nil
	}
	s.groupMu.Unlock()
	id, err := s.findGroupID(ctx, name)
	if err != nil {
		return "", fmt.Errorf("pinata group %q lookup failed: %w", name, err)
	}
	if id == "" {
		id, err = s.createGroup(ctx, name)
		if err != nil {
			return "", fmt.Errorf("pinata group %q create failed: %w", name, err)
		}
	}
	s.groupMu.Lock()
	s.groupIDs[name] = id
	s.groupMu.Unlock()
	return id, nil
}

func (s *PinataStore) findGroupID(ctx context.Context, name string) (string, error) {
	q := url.Values{}
	q.Set("limit", "100")
	q.Set("name", name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.apiBaseURL+"/v3/groups/public?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+s.jwt)
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("status=%d body=%s", resp.StatusCode, s.redact(strings.TrimSpace(string(raw))))
	}
	groups, err := parsePinataV3Groups(raw)
	if err != nil {
		return "", err
	}
	for _, group := range groups {
		if group.ID != "" && group.Name == name {
			return group.ID, nil
		}
	}
	for _, group := range groups {
		if group.ID != "" {
			return group.ID, nil
		}
	}
	return "", nil
}

func (s *PinataStore) createGroup(ctx context.Context, name string) (string, error) {
	rawBody, _ := json.Marshal(map[string]string{"name": name})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.apiBaseURL+"/v3/groups/public", bytes.NewReader(rawBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+s.jwt)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("status=%d body=%s", resp.StatusCode, s.redact(strings.TrimSpace(string(raw))))
	}
	groups, err := parsePinataV3Groups(raw)
	if err != nil {
		return "", err
	}
	for _, group := range groups {
		if group.ID != "" {
			return group.ID, nil
		}
	}
	return "", fmt.Errorf("pinata create group returned no id")
}

func (s *PinataStore) addFileToGroup(ctx context.Context, groupID, fileID string) error {
	groupID = strings.TrimSpace(groupID)
	fileID = strings.TrimSpace(fileID)
	if groupID == "" || fileID == "" {
		return fmt.Errorf("pinata group_id and file_id are required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.apiBaseURL+"/v3/groups/public/"+url.PathEscape(groupID)+"/ids/"+url.PathEscape(fileID), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.jwt)
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("status=%d body=%s", resp.StatusCode, s.redact(strings.TrimSpace(string(raw))))
	}
	return nil
}

func (s *PinataStore) pinataFileIDForCID(ctx context.Context, cid string) (string, error) {
	var lastErr error
	for i := 0; i < 6; i++ {
		status, err := s.refreshV3Pin(ctx, cid)
		if err == nil && status.ProviderPinID != "" {
			return status.ProviderPinID, nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("pinata v3 file id for cid %s is not visible yet", cid)
		}
		timer := time.NewTimer(time.Duration(i+1) * 300 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", ctx.Err()
		case <-timer.C:
		}
	}
	return "", lastErr
}

func baseIPFSPinStatus(gateway, cid string) IPFSPinStatus {
	status := IPFSPinStatus{
		Provider:  "pinata",
		CID:       cid,
		PinStatus: "missing",
		Locator:   "ipfs://" + cid,
	}
	if gatewayURL := IPFSGatewayURL(gateway, cid); gatewayURL != "" {
		status.GatewayURL = gatewayURL
	}
	return status
}

func (s *PinataStore) pinataTokenStatus(ctx context.Context) ProviderCheckStatus {
	if s.jwt == "" {
		return ProviderCheckStatus{Configured: false, OK: false, Message: "pinata jwt is not configured"}
	}
	return s.pinataV3TokenStatus(ctx)
}

func (s *PinataStore) pinataV3TokenStatus(ctx context.Context) ProviderCheckStatus {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.apiBaseURL+"/v3/files/public?limit=1", nil)
	if err != nil {
		return ProviderCheckStatus{Configured: true, OK: false, Message: err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+s.jwt)
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return ProviderCheckStatus{Configured: true, OK: false, Message: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
		return ProviderCheckStatus{Configured: true, OK: true, Message: "pinata v3 authentication ok"}
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	message := fmt.Sprintf("pinata v3 authentication failed: status=%d body=%s", resp.StatusCode, s.redact(strings.TrimSpace(string(raw))))
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		return ProviderCheckStatus{Configured: false, OK: false, Message: message}
	}
	return ProviderCheckStatus{Configured: true, OK: false, Message: message}
}

func (s *PinataStore) pinataGatewayStatus(ctx context.Context) ProviderCheckStatus {
	if s.gateway == "" {
		return ProviderCheckStatus{Configured: false, OK: false, Message: "pinata gateway_base_url is not configured"}
	}
	status, err := s.probeGateway(ctx, http.MethodHead)
	if err != nil {
		return ProviderCheckStatus{Configured: true, OK: false, Message: err.Error()}
	}
	if status == http.StatusMethodNotAllowed {
		status, err = s.probeGateway(ctx, http.MethodGet)
		if err != nil {
			return ProviderCheckStatus{Configured: true, OK: false, Message: err.Error()}
		}
	}
	if status >= 500 {
		return ProviderCheckStatus{Configured: true, OK: false, Message: fmt.Sprintf("pinata gateway returned status=%d", status)}
	}
	return ProviderCheckStatus{Configured: true, OK: true, Message: fmt.Sprintf("pinata gateway reachable: status=%d", status)}
}

func (s *PinataStore) probeGateway(ctx context.Context, method string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, method, s.gateway, nil)
	if err != nil {
		return 0, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	return resp.StatusCode, nil
}

func redactSecret(value, secret string) string {
	if secret == "" {
		return value
	}
	return strings.ReplaceAll(value, secret, "<redacted>")
}

func (s *PinataStore) redact(value string) string {
	for _, secret := range []string{s.jwt} {
		value = redactSecret(value, secret)
	}
	return value
}

func pinataLocator(cid, fileID, groupID string) string {
	cid = strings.TrimSpace(cid)
	fileID = strings.TrimSpace(fileID)
	groupID = strings.TrimSpace(groupID)
	if cid == "" {
		return ""
	}
	if fileID == "" && groupID == "" {
		return "ipfs://" + cid
	}
	u := url.URL{Scheme: "ipfs", Host: cid}
	q := u.Query()
	if fileID != "" {
		q.Set("pinata_file_id", fileID)
	}
	if groupID != "" {
		q.Set("pinata_group_id", groupID)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func pinataFileIDFromLocator(raw string) string {
	return IPFSProviderPinIDFromLocator(raw)
}

func pinataGroupName(prefix, group string) string {
	group = strings.Trim(strings.TrimSpace(group), "-")
	if group == "" {
		return ""
	}
	replacer := strings.NewReplacer("\\", "-", "/", "-", ":", "-", " ", "-")
	group = strings.Trim(replacer.Replace(group), "-")
	if group == "" {
		return ""
	}
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return group
	}
	if strings.HasPrefix(group, prefix) {
		return group
	}
	return prefix + group
}
