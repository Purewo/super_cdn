package storage

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestAListInitDirsUsesListMkdirAndMarkerUpload(t *testing.T) {
	var mu sync.Mutex
	dirs := map[string]bool{"/repo": true}
	var markerPath string
	var markerBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "token" {
			t.Fatalf("missing authorization header")
		}
		switch r.URL.Path {
		case "/api/fs/list":
			var req struct {
				Path string `json:"path"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			exists := dirs[req.Path]
			mu.Unlock()
			if exists {
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "success", "data": map[string]any{}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 404, "message": "not found"})
		case "/api/fs/mkdir":
			var req struct {
				Path string `json:"path"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			dirs[req.Path] = true
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "success"})
		case "/api/fs/put":
			markerPath = r.Header.Get("File-Path")
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			markerBody = string(raw)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "success"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	store, err := NewAListStore(AListOptions{
		Name:    "alist",
		BaseURL: server.URL,
		Token:   "token",
		Root:    "/repo",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.InitDirs(context.Background(), InitOptions{
		Directories:   []string{"_supercdn", "assets/objects"},
		MarkerPath:    "_supercdn/init.json",
		MarkerPayload: []byte(`{"ok":true}`),
	})
	if err != nil {
		t.Fatalf("init dirs failed: %v", err)
	}
	if len(result.Directories) != 4 {
		t.Fatalf("directories = %+v", result.Directories)
	}
	if markerPath != "/repo/_supercdn/init.json" {
		t.Fatalf("marker path = %q", markerPath)
	}
	if markerBody != `{"ok":true}` {
		t.Fatalf("marker body = %q", markerBody)
	}
}

func TestAListHealthCheckPassiveOnlyListsRoot(t *testing.T) {
	var listCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/fs/list":
			listCalls++
			var req struct {
				Path string `json:"path"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			if req.Path != "/repo" {
				t.Fatalf("listed path = %q", req.Path)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "success", "data": map[string]any{}})
		case "/api/fs/put", "/api/fs/mkdir", "/api/fs/remove":
			t.Fatalf("passive health check unexpectedly called %s", r.URL.Path)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	store, err := NewAListStore(AListOptions{
		Name:    "alist",
		BaseURL: server.URL,
		Token:   "token",
		Root:    "/repo",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.HealthCheck(context.Background(), HealthCheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if listCalls != 1 {
		t.Fatalf("list calls = %d", listCalls)
	}
	if len(result.Items) != 1 || result.Items[0].Status != HealthStatusOK || result.Items[0].CheckMode != HealthModePassive {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestAListStatPrefersSignedProxyURLWhenConfigured(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/fs/get" {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Path != "/repo/dir/file.txt" {
			t.Fatalf("stat path = %q", req.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    200,
			"message": "success",
			"data": map[string]any{
				"name":    "file.txt",
				"size":    7,
				"raw_url": "http://raw.example/file.txt",
				"sign":    "sig=:0",
			},
		})
	}))
	defer server.Close()

	store, err := NewAListStore(AListOptions{
		Name:          "alist",
		BaseURL:       server.URL,
		Token:         "token",
		Root:          "/repo",
		UseProxyURL:   true,
		PublicBaseURL: "http://public.example/base",
	})
	if err != nil {
		t.Fatal(err)
	}
	stat, err := store.Stat(context.Background(), "dir/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	want := "http://public.example/base/d/repo/dir/file.txt?sign=sig%3D%3A0"
	if stat.Locator != want {
		t.Fatalf("locator = %q, want %q", stat.Locator, want)
	}
}

func TestAListRefreshesExpiredTokenAndRetriesStat(t *testing.T) {
	var getCalls int
	var loginCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/login":
			loginCalls++
			var req struct {
				Username string `json:"username"`
				Password string `json:"password"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			if req.Username != "admin" || req.Password != "password" {
				t.Fatalf("unexpected login payload")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    200,
				"message": "success",
				"data": map[string]any{
					"token": "fresh-token",
				},
			})
		case "/api/fs/get":
			getCalls++
			switch r.Header.Get("Authorization") {
			case "old-token":
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 401, "message": "token is expired"})
			case "fresh-token":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code":    200,
					"message": "success",
					"data": map[string]any{
						"name":    "file.txt",
						"size":    7,
						"raw_url": "http://raw.example/file.txt",
					},
				})
			default:
				t.Fatalf("unexpected authorization header")
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	store, err := NewAListStore(AListOptions{
		Name:     "alist",
		BaseURL:  server.URL,
		Token:    "old-token",
		Username: "admin",
		Password: "password",
		Root:     "/repo",
	})
	if err != nil {
		t.Fatal(err)
	}
	stat, err := store.Stat(context.Background(), "dir/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if stat.Size != 7 {
		t.Fatalf("stat size = %d", stat.Size)
	}
	if loginCalls != 1 || getCalls != 2 {
		t.Fatalf("login calls = %d, get calls = %d", loginCalls, getCalls)
	}
}

func TestAListRefreshesExpiredTokenAndRetriesPut(t *testing.T) {
	dirs := map[string]bool{"/repo": true, "/repo/dir": true}
	var putCalls int
	var loginCalls int
	var uploadedPath string
	var uploadedBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/login":
			loginCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    200,
				"message": "success",
				"data": map[string]any{
					"token": "fresh-token",
				},
			})
		case "/api/fs/list":
			var req struct {
				Path string `json:"path"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			if dirs[req.Path] {
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "success", "data": map[string]any{}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 404, "message": "not found"})
		case "/api/fs/put":
			putCalls++
			switch r.Header.Get("Authorization") {
			case "old-token":
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 401, "message": "token is expired"})
			case "fresh-token":
				uploadedPath = r.Header.Get("File-Path")
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatal(err)
				}
				uploadedBody = string(raw)
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "success"})
			default:
				t.Fatalf("unexpected authorization header")
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	file := filepath.Join(t.TempDir(), "probe.txt")
	if err := os.WriteFile(file, []byte("mobile probe"), 0644); err != nil {
		t.Fatal(err)
	}
	store, err := NewAListStore(AListOptions{
		Name:     "alist",
		BaseURL:  server.URL,
		Token:    "old-token",
		Username: "admin",
		Password: "password",
		Root:     "/repo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(context.Background(), PutOptions{
		Key:      "dir/probe.txt",
		FilePath: file,
		Size:     int64(len("mobile probe")),
	}); err != nil {
		t.Fatal(err)
	}
	if loginCalls != 1 || putCalls != 2 {
		t.Fatalf("login calls = %d, put calls = %d", loginCalls, putCalls)
	}
	if uploadedPath != "/repo/dir/probe.txt" || uploadedBody != "mobile probe" {
		t.Fatalf("uploaded path=%q body=%q", uploadedPath, uploadedBody)
	}
}

func TestAListPutCreatesParentDirectories(t *testing.T) {
	var mu sync.Mutex
	dirs := map[string]bool{"/repo": true}
	var made []string
	var uploadedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/fs/list":
			var req struct {
				Path string `json:"path"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			exists := dirs[req.Path]
			mu.Unlock()
			if exists {
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "success", "data": map[string]any{}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 404, "message": "not found"})
		case "/api/fs/mkdir":
			var req struct {
				Path string `json:"path"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			dirs[req.Path] = true
			made = append(made, req.Path)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "success"})
		case "/api/fs/put":
			uploadedPath = r.Header.Get("File-Path")
			_, _ = io.Copy(io.Discard, r.Body)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "success"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	file := filepath.Join(t.TempDir(), "app.js")
	if err := os.WriteFile(file, []byte("console.log('ok')"), 0644); err != nil {
		t.Fatal(err)
	}
	store, err := NewAListStore(AListOptions{
		Name:    "alist",
		BaseURL: server.URL,
		Token:   "token",
		Root:    "/repo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(context.Background(), PutOptions{
		Key:      "sites/demo/deployments/dpl/root/assets/app.js",
		FilePath: file,
		Size:     int64(len("console.log('ok')")),
	}); err != nil {
		t.Fatal(err)
	}
	wantMade := []string{
		"/repo/sites",
		"/repo/sites/demo",
		"/repo/sites/demo/deployments",
		"/repo/sites/demo/deployments/dpl",
		"/repo/sites/demo/deployments/dpl/root",
		"/repo/sites/demo/deployments/dpl/root/assets",
	}
	if strings.Join(made, "\n") != strings.Join(wantMade, "\n") {
		t.Fatalf("made dirs = %#v", made)
	}
	if uploadedPath != "/repo/sites/demo/deployments/dpl/root/assets/app.js" {
		t.Fatalf("uploaded path = %q", uploadedPath)
	}
}
