package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
)

type PinataStore struct {
	name    string
	jwt     string
	gateway string
	client  *http.Client
}

type PinataOptions struct {
	Name           string
	JWT            string
	GatewayBaseURL string
	ProxyURL       string
}

func NewPinataStore(opts PinataOptions) (*PinataStore, error) {
	client, err := newHTTPClient(opts.ProxyURL)
	if err != nil {
		return nil, err
	}
	return &PinataStore{
		name:    opts.Name,
		jwt:     opts.JWT,
		gateway: strings.TrimRight(opts.GatewayBaseURL, "/"),
		client:  client,
	}, nil
}

func (s *PinataStore) Name() string { return s.name }
func (s *PinataStore) Type() string { return "pinata" }

func (s *PinataStore) Put(ctx context.Context, opts PutOptions) (string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	f, err := os.Open(opts.FilePath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	part, err := writer.CreateFormFile("file", firstNonEmpty(opts.FileName, path.Base(opts.Key)))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, f); err != nil {
		return "", err
	}
	meta := map[string]any{
		"name": opts.Key,
		"keyvalues": map[string]string{
			"sha256": opts.SHA256,
			"key":    opts.Key,
		},
	}
	metaRaw, _ := json.Marshal(meta)
	_ = writer.WriteField("pinataMetadata", string(metaRaw))
	if err := writer.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.pinata.cloud/pinning/pinFileToIPFS", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+s.jwt)
	req.Header.Set("Content-Type", writer.FormDataContentType())
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
		return "", fmt.Errorf("pinata upload failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out struct {
		IPFSHash string `json:"IpfsHash"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if out.IPFSHash == "" {
		return "", fmt.Errorf("pinata upload returned no IpfsHash")
	}
	return "ipfs://" + out.IPFSHash, nil
}

func (s *PinataStore) Get(ctx context.Context, key string, opts GetOptions) (*ObjectStream, error) {
	locator := firstNonEmpty(opts.Locator, s.PublicURL(key))
	if strings.HasPrefix(locator, "ipfs://") {
		if s.gateway == "" {
			return nil, fmt.Errorf("pinata gateway_base_url is required to read %s", locator)
		}
		locator = s.gateway + "/ipfs/" + strings.TrimPrefix(locator, "ipfs://")
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

func (s *PinataStore) Delete(_ context.Context, key string) error {
	return nil
}

func (s *PinataStore) PublicURL(key string) string {
	if s.gateway == "" {
		return ""
	}
	if strings.HasPrefix(key, "ipfs://") {
		return s.gateway + "/ipfs/" + strings.TrimPrefix(key, "ipfs://")
	}
	u, err := url.Parse(s.gateway)
	if err != nil {
		return ""
	}
	u.Path = path.Join(u.Path, strings.TrimLeft(key, "/"))
	return u.String()
}
