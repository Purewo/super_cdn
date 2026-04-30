package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareCloudflareStaticAssetsDirGeneratesHeaders(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "index.html"), `<script src="path2agi-data.js?v=20260429"></script>`)
	writeTestFile(t, filepath.Join(dir, "path2agi-data.js"), `window.data = [];`)

	prepared, cleanup, meta, err := prepareCloudflareStaticAssetsDir(dir, "auto")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if prepared == dir {
		t.Fatalf("expected generated headers to use a temporary assets directory")
	}
	if !meta.Generated || meta.Source != "generated" || meta.Policy != "auto" {
		t.Fatalf("unexpected headers meta: %+v", meta)
	}
	if _, err := os.Stat(filepath.Join(dir, "_headers")); !os.IsNotExist(err) {
		t.Fatalf("source directory should not be mutated, stat err=%v", err)
	}
	raw, err := os.ReadFile(filepath.Join(prepared, "_headers"))
	if err != nil {
		t.Fatal(err)
	}
	headers := string(raw)
	for _, want := range []string{
		"/\n  Cache-Control: public, max-age=0, must-revalidate",
		"/index.html\n  Cache-Control: public, max-age=0, must-revalidate",
		"/path2agi-data.js\n  Cache-Control: public, max-age=31536000, immutable",
	} {
		if !strings.Contains(headers, want) {
			t.Fatalf("generated headers missing %q:\n%s", want, headers)
		}
	}
}

func TestPrepareCloudflareStaticAssetsDirRespectsExistingHeaders(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "index.html"), "ok")
	writeTestFile(t, filepath.Join(dir, "_headers"), "/*\n  X-Test: yes\n")

	prepared, cleanup, meta, err := prepareCloudflareStaticAssetsDir(dir, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if cleanup != nil {
		t.Fatalf("existing _headers should not need a temporary directory")
	}
	if prepared != dir || meta.Generated || meta.Source != "existing" {
		t.Fatalf("unexpected headers meta: prepared=%s meta=%+v", prepared, meta)
	}

	summary, err := summarizeCloudflareStaticDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if summary.FileCount != 1 {
		t.Fatalf("cloudflare static summary should ignore _headers, got %d files", summary.FileCount)
	}
}

func TestWranglerDeployArgsUsesConfigForSPA(t *testing.T) {
	args := wranglerDeployArgs(
		"npx",
		"worker",
		"supercdn-demo-static",
		"ignored-assets",
		[]string{"demo.example.com"},
		"2026-04-29",
		"deploy",
		true,
		"C:/tmp/wrangler.toml",
	)
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--prefix worker wrangler deploy",
		"--config C:/tmp/wrangler.toml",
		"--domain demo.example.com",
		"--message deploy",
		"--dry-run",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args missing %q: %s", want, joined)
		}
	}
	if strings.Contains(joined, "--assets ignored-assets") {
		t.Fatalf("config deploy should not also pass --assets: %s", joined)
	}
}

func TestWriteCloudflareStaticWranglerConfig(t *testing.T) {
	path, cleanup, err := writeCloudflareStaticWranglerConfig("supercdn-demo-static", `C:\tmp\assets`, "2026-04-29", cloudflareStaticNotFoundSPA)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(raw)
	for _, want := range []string{
		`name = "supercdn-demo-static"`,
		`compatibility_date = "2026-04-29"`,
		`[assets]`,
		`directory = "C:/tmp/assets"`,
		`not_found_handling = "single-page-application"`,
	} {
		if !strings.Contains(cfg, want) {
			t.Fatalf("config missing %q:\n%s", want, cfg)
		}
	}
}

func TestWriteHybridEdgeWranglerConfig(t *testing.T) {
	path, cleanup, err := writeHybridEdgeWranglerConfig(hybridEdgeWranglerConfigOptions{
		WorkerName:          "supercdn-demo-edge",
		WorkerMain:          `C:\repo\worker\src\index.ts`,
		AssetsDir:           `C:\repo\dist`,
		CompatibilityDate:   "2026-04-30",
		NotFoundHandling:    cloudflareStaticNotFoundSPA,
		KVNamespaceID:       "kv-123",
		ManifestMode:        "route",
		DefaultCacheControl: "public, max-age=300",
		OriginBaseURL:       "https://origin.example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(raw)
	for _, want := range []string{
		`name = "supercdn-demo-edge"`,
		`main = "C:/repo/worker/src/index.ts"`,
		`compatibility_date = "2026-04-30"`,
		`ORIGIN_BASE_URL = "https://origin.example.com"`,
		`EDGE_MANIFEST_MODE = "route"`,
		`EDGE_STATIC_ASSETS = "true"`,
		`[assets]`,
		`directory = "C:/repo/dist"`,
		`binding = "ASSETS"`,
		`run_worker_first = true`,
		`not_found_handling = "single-page-application"`,
		`[[kv_namespaces]]`,
		`binding = "EDGE_MANIFEST"`,
		`id = "kv-123"`,
	} {
		if !strings.Contains(cfg, want) {
			t.Fatalf("hybrid config missing %q:\n%s", want, cfg)
		}
	}
}

func TestResolveSiteDeploymentTargetCallsControlPlane(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/sites/demo/deployment-target" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("route_profile"); got != "overseas" {
			t.Fatalf("route_profile query = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization = %q", got)
		}
		_, _ = w.Write([]byte(`{"site_id":"demo","route_profile":"overseas","deployment_target":"cloudflare_static","source":"route_profile","domains":["demo.sites.example.com"],"default_domain":"demo.sites.example.com"}`))
	}))
	defer srv.Close()

	defaults, err := (client{baseURL: srv.URL, token: "test-token", http: srv.Client()}).resolveSiteDeploymentTarget("demo", "overseas", "")
	if err != nil {
		t.Fatal(err)
	}
	if defaults.DeploymentTarget != "cloudflare_static" || len(defaults.Domains) != 1 || defaults.Domains[0] != "demo.sites.example.com" {
		t.Fatalf("unexpected defaults: %+v", defaults)
	}
}

func TestRefreshEdgeManifestPublishesActiveManifest(t *testing.T) {
	var publishReq map[string]any
	var sawList, sawPublish bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-token" {
			t.Fatalf("Authorization = %q", auth)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sites/demo/deployments":
			sawList = true
			if got := r.URL.Query().Get("limit"); got != "100" {
				t.Fatalf("limit query = %q", got)
			}
			_, _ = w.Write([]byte(`{"deployments":[{"id":"dpl-preview","environment":"preview","active":false},{"id":"dpl-active","environment":"production","active":true,"production_url":"https://demo.example.com/"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sites/demo/deployments/dpl-active/edge-manifest/publish":
			sawPublish = true
			if err := json.NewDecoder(r.Body).Decode(&publishReq); err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write([]byte(`{"status":"ok","site_id":"demo","deployment_id":"dpl-active","active":true,"kv_namespace_id":"kv-1","kv_namespace":"supercdn-edge-manifest","key_prefix":"sites/","domains":["demo.example.com"],"manifest_size":12,"manifest_sha256":"abc"}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()

	err := refreshEdgeManifest(client{baseURL: srv.URL, token: "test-token", http: srv.Client()}, []string{
		"-site", "demo",
		"-domains", "https://demo.example.com/",
		"-kv-namespace", "supercdn-edge-manifest",
		"-probe=false",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sawList || !sawPublish {
		t.Fatalf("sawList=%v sawPublish=%v", sawList, sawPublish)
	}
	if publishReq["kv_namespace"] != "supercdn-edge-manifest" {
		t.Fatalf("kv_namespace = %#v", publishReq["kv_namespace"])
	}
	if publishReq["dry_run"] != false || publishReq["active_key"] != true || publishReq["deployment_key"] != true {
		t.Fatalf("publish flags = %#v", publishReq)
	}
	domains, ok := publishReq["domains"].([]any)
	if !ok || len(domains) != 1 || domains[0] != "demo.example.com" {
		t.Fatalf("domains = %#v", publishReq["domains"])
	}
}

func TestRedactSignedURL(t *testing.T) {
	got := redactSignedURL("https://storage.example.com/app.js?X-Amz-Date=20260430T000000Z&X-Amz-Signature=secret&plain=keep")
	if strings.Contains(got, "secret") || strings.Contains(got, "20260430T000000Z") || strings.Contains(got, "plain=keep") {
		t.Fatalf("signed URL was not redacted: %s", got)
	}
	if !strings.Contains(got, "plain=%3Credacted%3E") || !strings.Contains(got, "X-Amz-Signature=%3Credacted%3E") {
		t.Fatalf("unexpected redacted URL: %s", got)
	}
}

func TestNormalizeCloudflareStaticVerifyMode(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"", "wait"},
		{"WAIT", "wait"},
		{"warn", "warn"},
		{"none", "none"},
	} {
		got, err := normalizeCloudflareStaticVerifyMode(tc.in)
		if err != nil {
			t.Fatalf("normalize %q: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("normalize %q = %q, want %q", tc.in, got, tc.want)
		}
	}
	if _, err := normalizeCloudflareStaticVerifyMode("off"); err == nil {
		t.Fatal("expected invalid verify mode error")
	}
}

func TestCreateCDNBucketUsesOverseasDefaults(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/asset-buckets" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-token" {
			t.Fatalf("Authorization = %q", auth)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"slug":"posters"}`))
	}))
	defer srv.Close()

	err := createCDNBucket(client{baseURL: srv.URL, token: "test-token", http: srv.Client()}, []string{
		"-slug", "posters",
		"-name", "Posters",
		"-types", "image,archive",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got["route_profile"] != "overseas_r2" {
		t.Fatalf("route_profile = %#v", got["route_profile"])
	}
	if got["default_cache_control"] != "public, max-age=31536000, immutable" {
		t.Fatalf("default_cache_control = %#v", got["default_cache_control"])
	}
	types, ok := got["allowed_types"].([]any)
	if !ok || len(types) != 2 || types[0] != "image" || types[1] != "archive" {
		t.Fatalf("allowed_types = %#v", got["allowed_types"])
	}
}

func TestCreateDomesticCDNBucketDefaultsToMobile(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/asset-buckets" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"slug":"mobile"}`))
	}))
	defer srv.Close()

	err := createDomesticCDNBucket(client{baseURL: srv.URL, token: "test-token", http: srv.Client()}, []string{
		"-slug", "mobile",
		"-types", "image,document",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got["route_profile"] != "china_mobile" {
		t.Fatalf("route_profile = %#v", got["route_profile"])
	}
	if got["default_cache_control"] != "public, max-age=86400" {
		t.Fatalf("default_cache_control = %#v", got["default_cache_control"])
	}
}

func TestCreateDomesticCDNBucketLineMapping(t *testing.T) {
	for _, tc := range []struct {
		line string
		want string
	}{
		{"mobile", "china_mobile"},
		{"telecom", "china_telecom"},
		{"unicom", "china_unicom"},
		{"all", "china_all"},
	} {
		got, err := domesticLineProfile(tc.line)
		if err != nil {
			t.Fatalf("line %q: %v", tc.line, err)
		}
		if got != tc.want {
			t.Fatalf("line %q = %q want %q", tc.line, got, tc.want)
		}
	}
	if _, err := domesticLineProfile("satellite"); err == nil {
		t.Fatal("expected invalid line error")
	}
}

func TestCreateMobileCDNBucketUsesMobileDefaults(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"slug":"mobile"}`))
	}))
	defer srv.Close()

	err := createMobileCDNBucket(client{baseURL: srv.URL, token: "test-token", http: srv.Client()}, []string{
		"-slug", "mobile",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got["route_profile"] != "china_mobile" {
		t.Fatalf("route_profile = %#v", got["route_profile"])
	}
	if got["default_cache_control"] != "public, max-age=86400" {
		t.Fatalf("default_cache_control = %#v", got["default_cache_control"])
	}
}

func TestUploadBucketWarmupCallsWarmupEndpoint(t *testing.T) {
	var uploadSeen, warmupSeen bool
	var warmupReq map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/asset-buckets/posters/objects":
			uploadSeen = true
			if r.Method != http.MethodPost {
				t.Fatalf("upload method = %s", r.Method)
			}
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			if got := r.FormValue("path"); got != "images/one.png" {
				t.Fatalf("upload path = %q", got)
			}
			if got := r.FormValue("asset_type"); got != "image" {
				t.Fatalf("asset_type = %q", got)
			}
			_, _ = w.Write([]byte(`{"bucket":"posters","public_url":"https://cdn.example.com/a/posters/images/one.png"}`))
		case "/api/v1/asset-buckets/posters/warmup":
			warmupSeen = true
			if r.Method != http.MethodPost {
				t.Fatalf("warmup method = %s", r.Method)
			}
			if err := json.NewDecoder(r.Body).Decode(&warmupReq); err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write([]byte(`{"bucket":"posters","status":"ok","urls":["https://cdn.example.com/a/posters/images/one.png"]}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	file := filepath.Join(t.TempDir(), "one.png")
	writeTestFile(t, file, "png")
	err := uploadBucket(client{baseURL: srv.URL, token: "test-token", http: srv.Client()}, []string{
		"-bucket", "posters",
		"-file", file,
		"-path", "images/one.png",
		"-asset-type", "image",
		"-warmup",
		"-warmup-method", "GET",
		"-warmup-base-url", "https://cdn.example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !uploadSeen || !warmupSeen {
		t.Fatalf("uploadSeen=%v warmupSeen=%v", uploadSeen, warmupSeen)
	}
	if warmupReq["path"] != "images/one.png" || warmupReq["method"] != "GET" || warmupReq["base_url"] != "https://cdn.example.com" {
		t.Fatalf("warmup request = %#v", warmupReq)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
