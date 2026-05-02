package storage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPinataProviderStatusChecksAuthAndGateway(t *testing.T) {
	var authHeader string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/files/public" {
			t.Fatalf("unexpected API path %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("limit"); got != "1" {
			t.Fatalf("limit query = %q", got)
		}
		authHeader = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":{"files":[]}}`))
	}))
	defer api.Close()
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Fatalf("gateway method = %s", r.Method)
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer gateway.Close()

	store, err := NewPinataStore(PinataOptions{
		Name:           "ipfs_pinata",
		APIBaseURL:     api.URL,
		JWT:            "pinata-secret",
		GatewayBaseURL: gateway.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	status := store.ProviderStatus(context.Background())
	if !status.OK || !status.Token.OK || !status.Gateway.OK {
		t.Fatalf("status = %+v", status)
	}
	if status.Provider != "pinata" || status.Target != "ipfs_pinata" || status.TargetType != "pinata" {
		t.Fatalf("provider identity = %+v", status)
	}
	if authHeader != "Bearer pinata-secret" {
		t.Fatalf("Authorization = %q", authHeader)
	}
	if strings.Contains(status.Token.Message, "pinata-secret") {
		t.Fatalf("status leaked jwt: %+v", status.Token)
	}
}

func TestIPFSCIDFromLocator(t *testing.T) {
	for raw, want := range map[string]string{
		"ipfs://bafybeigdyrzt":                                          "bafybeigdyrzt",
		"ipfs://bafybeigdyrzt/assets/app.js":                            "bafybeigdyrzt",
		"ipfs://bafybeigdyrzt?pinata_file_id=file-1":                    "bafybeigdyrzt",
		"https://gateway.pinata.cloud/ipfs/bafybeigdyrzt":               "bafybeigdyrzt",
		"https://gateway.pinata.cloud/ipfs/bafybeigdyrzt/assets/app.js": "bafybeigdyrzt",
	} {
		got, ok := IPFSCIDFromLocator(raw)
		if !ok || got != want {
			t.Fatalf("IPFSCIDFromLocator(%q) = %q, %v; want %q", raw, got, ok, want)
		}
	}
	if got, ok := IPFSCIDFromLocator("https://example.com/nope"); ok || got != "" {
		t.Fatalf("unexpected cid = %q ok=%v", got, ok)
	}
}

func TestPreserveIPFSProviderQueryKeepsGroupID(t *testing.T) {
	got := PreserveIPFSProviderQuery(
		"ipfs://bafybeigdyrzt?pinata_file_id=file-2",
		"ipfs://bafybeigdyrzt?pinata_file_id=file-1&pinata_group_id=group-1",
	)
	if !strings.Contains(got, "pinata_file_id=file-2") || !strings.Contains(got, "pinata_group_id=group-1") {
		t.Fatalf("locator = %q", got)
	}
}

func TestPinataProviderStatusReportsMissingCredentials(t *testing.T) {
	store, err := NewPinataStore(PinataOptions{Name: "ipfs_pinata"})
	if err != nil {
		t.Fatal(err)
	}
	status := store.ProviderStatus(context.Background())
	if status.OK || status.Token.Configured || status.Gateway.Configured {
		t.Fatalf("status = %+v", status)
	}
	if len(status.Warnings) != 2 {
		t.Fatalf("warnings = %#v", status.Warnings)
	}
}

func TestPinataProviderStatusRedactsJWTFromErrorBody(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad token pinata-secret", http.StatusUnauthorized)
	}))
	defer api.Close()
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer gateway.Close()

	store, err := NewPinataStore(PinataOptions{
		Name:           "ipfs_pinata",
		APIBaseURL:     api.URL,
		JWT:            "pinata-secret",
		GatewayBaseURL: gateway.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	status := store.ProviderStatus(context.Background())
	if status.Token.OK {
		t.Fatalf("token unexpectedly ok: %+v", status.Token)
	}
	if strings.Contains(status.Token.Message, "pinata-secret") || !strings.Contains(status.Token.Message, "<redacted>") {
		t.Fatalf("token message was not redacted: %+v", status.Token)
	}
}

func TestPinataProviderStatusDoesNotFallbackToLegacy(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/files/public" {
			t.Fatalf("unexpected API path %s", r.URL.Path)
		}
		http.Error(w, `{"error":{"code":401,"message":"Not Authorized"}}`, http.StatusUnauthorized)
	}))
	defer api.Close()
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer gateway.Close()

	store, err := NewPinataStore(PinataOptions{
		Name:           "ipfs_pinata",
		APIBaseURL:     api.URL,
		JWT:            "pinata-secret",
		GatewayBaseURL: gateway.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	status := store.ProviderStatus(context.Background())
	if status.OK || status.Token.OK || !strings.Contains(status.Token.Message, "pinata v3 authentication failed") {
		t.Fatalf("status = %+v", status)
	}
}

func TestPinataRefreshIPFSPin(t *testing.T) {
	const cid = "bafybeigdyrztrefresh"
	var rawQuery string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/files/public" {
			t.Fatalf("unexpected API path %s", r.URL.Path)
		}
		rawQuery = r.URL.RawQuery
		if got := r.Header.Get("Authorization"); got != "Bearer pinata-secret" {
			t.Fatalf("Authorization = %q", got)
		}
		_, _ = w.Write([]byte(`{"data":{"files":[{"id":"file-1","cid":"` + cid + `"}]}}`))
	}))
	defer api.Close()
	store, err := NewPinataStore(PinataOptions{
		Name:           "ipfs_pinata",
		APIBaseURL:     api.URL,
		JWT:            "pinata-secret",
		GatewayBaseURL: "https://gateway.example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	status, err := store.RefreshIPFSPin(context.Background(), cid)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rawQuery, "cid="+cid) || !strings.Contains(rawQuery, "limit=10") {
		t.Fatalf("query = %q", rawQuery)
	}
	if status.PinStatus != "pinned" || status.ProviderPinID != "file-1" || status.GatewayURL != "https://gateway.example.com/ipfs/"+cid {
		t.Fatalf("status = %+v", status)
	}
	if status.Locator != "ipfs://"+cid+"?pinata_file_id=file-1" {
		t.Fatalf("locator = %q", status.Locator)
	}
}

func TestPinataDeleteLocatorUnpinsCID(t *testing.T) {
	const cid = "bafybeigdyrztdelete"
	var authHeader string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/v3/files/public/file-1" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		authHeader = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer api.Close()
	store, err := NewPinataStore(PinataOptions{
		Name:       "ipfs_pinata",
		APIBaseURL: api.URL,
		JWT:        "pinata-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteLocator(context.Background(), "assets/readme.txt", "ipfs://"+cid+"?pinata_file_id=file-1"); err != nil {
		t.Fatal(err)
	}
	if authHeader != "Bearer pinata-secret" {
		t.Fatalf("Authorization = %q", authHeader)
	}
}

func TestPinataPutUsesV3Upload(t *testing.T) {
	const cid = "bafybeigdyrztv3upload"
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v3/files" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer pinata-secret" {
			t.Fatalf("Authorization = %q", got)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		if got := r.FormValue("network"); got != "public" {
			t.Fatalf("network = %q", got)
		}
		if got := r.FormValue("name"); got != "probe.txt" {
			t.Fatalf("name = %q", got)
		}
		var keyvalues map[string]string
		if err := json.Unmarshal([]byte(r.FormValue("keyvalues")), &keyvalues); err != nil {
			t.Fatal(err)
		}
		if keyvalues["key"] != "probe.txt" || keyvalues["sha256"] != "sha256-test" {
			t.Fatalf("keyvalues = %#v", keyvalues)
		}
		_, _ = w.Write([]byte(`{"data":{"id":"file-v3","cid":"` + cid + `"}}`))
	}))
	defer api.Close()
	store, err := NewPinataStore(PinataOptions{
		Name:       "ipfs_pinata",
		APIBaseURL: api.URL,
		JWT:        "pinata-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "probe.txt")
	if err := os.WriteFile(file, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	locator, err := store.Put(context.Background(), PutOptions{
		Key:      "probe.txt",
		FilePath: file,
		FileName: "probe.txt",
		SHA256:   "sha256-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if locator != "ipfs://"+cid+"?pinata_file_id=file-v3" {
		t.Fatalf("locator=%q", locator)
	}
}

func TestPinataPutUploadsToV3Group(t *testing.T) {
	const cid = "bafybeigdyrztgroup"
	const token = "pinata-secret"
	var createdGroupName string
	var uploadGroupID string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/groups/public":
			switch r.Method {
			case http.MethodGet:
				if got := r.URL.Query().Get("name"); got != "supercdn-bucket-docs" {
					t.Fatalf("group name query = %q", got)
				}
				_, _ = w.Write([]byte(`{"data":{"groups":[]}}`))
			case http.MethodPost:
				if got := r.Header.Get("Authorization"); got != "Bearer "+token {
					t.Fatalf("Authorization = %q", got)
				}
				var req struct {
					Name string `json:"name"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Fatal(err)
				}
				createdGroupName = req.Name
				_, _ = w.Write([]byte(`{"data":{"id":"group-1","name":"` + req.Name + `"}}`))
			default:
				t.Fatalf("unexpected group method %s", r.Method)
			}
		case "/v3/files":
			if got := r.Header.Get("Authorization"); got != "Bearer "+token {
				t.Fatalf("Authorization = %q", got)
			}
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			uploadGroupID = r.FormValue("group_id")
			_, _ = w.Write([]byte(`{"data":{"id":"file-1","cid":"` + cid + `"}}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer api.Close()
	store, err := NewPinataStore(PinataOptions{
		Name:       "ipfs_pinata",
		APIBaseURL: api.URL,
		JWT:        token,
	})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "probe.txt")
	if err := os.WriteFile(file, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	locator, err := store.Put(context.Background(), PutOptions{
		Key:      "probe.txt",
		FilePath: file,
		FileName: "probe.txt",
		Group:    "bucket-docs",
	})
	if err != nil {
		t.Fatal(err)
	}
	if createdGroupName != "supercdn-bucket-docs" || uploadGroupID != "group-1" {
		t.Fatalf("createdGroupName=%q uploadGroupID=%q", createdGroupName, uploadGroupID)
	}
	if !strings.Contains(locator, "pinata_file_id=file-1") || !strings.Contains(locator, "pinata_group_id=group-1") {
		t.Fatalf("locator = %q", locator)
	}
}
