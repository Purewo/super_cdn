package main

import (
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

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
