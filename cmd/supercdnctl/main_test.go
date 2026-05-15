package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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
		"/*\n  X-SuperCDN-Edge-Source: cloudflare_static",
		"/\n  Cache-Control: public, max-age=0, must-revalidate",
		"/index.html\n  Cache-Control: public, max-age=0, must-revalidate",
		"/path2agi-data.js\n  Cache-Control: public, max-age=31536000, immutable",
	} {
		if !strings.Contains(headers, want) {
			t.Fatalf("generated headers missing %q:\n%s", want, headers)
		}
	}
}

func TestCLIProfileConfigRoundTrip(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "cli.json")
	t.Setenv("SUPERCDN_CONFIG", cfgPath)

	if err := saveCLIProfile("team", "http://127.0.0.1:8080/", "sct_secret"); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadCLIConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CurrentProfile != "team" {
		t.Fatalf("current profile = %q", cfg.CurrentProfile)
	}
	profile, ok := cfg.Profiles["team"]
	if !ok {
		t.Fatalf("missing saved profile: %+v", cfg.Profiles)
	}
	if profile.Server != "http://127.0.0.1:8080" || profile.Token != "sct_secret" {
		t.Fatalf("profile = %+v", profile)
	}
}

func TestDoctorCallsAPI(t *testing.T) {
	var saw bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		saw = true
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/doctor" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-token" {
			t.Fatalf("Authorization = %q", auth)
		}
		if got := r.URL.Query().Get("resources"); got != "false" {
			t.Fatalf("resources = %q", got)
		}
		if got := r.URL.Query().Get("routing"); got != "false" {
			t.Fatalf("routing = %q", got)
		}
		_, _ = w.Write([]byte(`{"status":"ok","checks":[{"name":"auth","status":"ok"}]}`))
	}))
	defer srv.Close()

	err := doctor(client{baseURL: srv.URL, token: "test-token", http: srv.Client()}, []string{"-resources=false", "-routing=false"})
	if err != nil {
		t.Fatal(err)
	}
	if !saw {
		t.Fatal("doctor API was not called")
	}
}

func TestAuditLogCallsAPI(t *testing.T) {
	var saw bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		saw = true
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/audit-events" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		if got := r.URL.Query().Get("limit"); got != "25" {
			t.Fatalf("limit = %q", got)
		}
		if got := r.URL.Query().Get("action"); got != "site.create" {
			t.Fatalf("action = %q", got)
		}
		if got := r.URL.Query().Get("resource"); got != "site:demo" {
			t.Fatalf("resource = %q", got)
		}
		if got := r.URL.Query().Get("workspace_id"); got != "default" {
			t.Fatalf("workspace_id = %q", got)
		}
		_, _ = w.Write([]byte(`{"events":[{"id":1,"workspace_id":"default","action":"site.create","resource":"site:demo"}],"limit":25}`))
	}))
	defer srv.Close()

	err := auditLog(client{baseURL: srv.URL, token: "test-token", http: srv.Client()}, []string{"-limit", "25", "-workspace", "default", "-action", "site.create", "-resource", "site:demo"})
	if err != nil {
		t.Fatal(err)
	}
	if !saw {
		t.Fatal("audit-log API was not called")
	}
}

func TestReconcileHybridDeploymentRunsStrictProbe(t *testing.T) {
	siteSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
			w.Header().Set("X-SuperCDN-Edge-Source", "cloudflare_static")
			_, _ = w.Write([]byte(`<script src="/app.js"></script>`))
		case "/app.js":
			w.Header().Set("Content-Type", "text/javascript")
			w.Header().Set("X-SuperCDN-Edge-Source", "manifest")
			w.Header().Set("X-SuperCDN-Edge-Manifest", "route")
			w.Header().Set("X-SuperCDN-Edge-Action", "route")
			_, _ = w.Write([]byte(`console.log("ok")`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer siteSrv.Close()

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/sites/demo/deployments/dpl-1" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":                "dpl-1",
			"site_id":           "demo",
			"environment":       "production",
			"status":            "active",
			"route_profile":     "china_mobile",
			"deployment_target": "hybrid_edge",
			"active":            true,
			"production_url":    siteSrv.URL + "/",
			"site_domains":      []string{"demo.example.com"},
		})
	}))
	defer apiSrv.Close()

	var err error
	out := captureStdout(t, func() {
		err = reconcileDeployment(client{baseURL: apiSrv.URL, token: "test-token", http: apiSrv.Client()}, []string{"-site", "demo", "-deployment", "dpl-1"})
	})
	if err != nil {
		t.Fatal(err)
	}
	var report reconcileDeploymentReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	if report.Status != "ok" || !report.Settled || !report.Provider.RequireEdgeManifestAssets {
		t.Fatalf("unexpected report: %+v", report)
	}
	if report.Provider.AssetOK != 1 || report.Provider.HTMLSource != "cloudflare_static" {
		t.Fatalf("unexpected provider summary: %+v", report.Provider)
	}
	if !strings.Contains(strings.Join(report.NextCommands, "\n"), "refresh-edge-manifest") {
		t.Fatalf("missing refresh hint: %+v", report.NextCommands)
	}
}

func TestReconcileCloudflareStaticFailureReturnsReport(t *testing.T) {
	siteSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<script src="/app.js"></script>`))
	}))
	defer siteSrv.Close()

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/sites/demo/deployments/dpl-static" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":                "dpl-static",
			"site_id":           "demo",
			"environment":       "production",
			"status":            "active",
			"route_profile":     "overseas",
			"deployment_target": "cloudflare_static",
			"active":            true,
			"production_url":    siteSrv.URL + "/",
			"cloudflare_static": map[string]any{
				"worker_name":         "supercdn-demo-static",
				"headers_generated":   true,
				"verification_status": "verify_failed_after_provider_write",
			},
		})
	}))
	defer apiSrv.Close()

	var err error
	out := captureStdout(t, func() {
		err = reconcileDeployment(client{baseURL: apiSrv.URL, token: "test-token", http: apiSrv.Client()}, []string{"-site", "demo", "-deployment", "dpl-static"})
	})
	if err == nil {
		t.Fatal("expected reconcile failure")
	}
	var report reconcileDeploymentReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	if report.Status != "failed" || report.Settled {
		t.Fatalf("unexpected report: %+v", report)
	}
	if !report.Provider.RequireDirectAssets || !report.Provider.RequireHTMLRevalidate {
		t.Fatalf("unexpected probe requirements: %+v", report.Provider)
	}
	if !strings.Contains(strings.Join(report.Warnings, "\n"), "provider state is not verified") {
		t.Fatalf("missing warning: %+v", report.Warnings)
	}
}

func TestCDNDoctorCallsAPI(t *testing.T) {
	var saw bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		saw = true
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/asset-buckets/downloads/doctor" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-token" {
			t.Fatalf("Authorization = %q", auth)
		}
		if got := r.URL.Query().Get("path"); got != "release/app.zip" {
			t.Fatalf("path = %q", got)
		}
		if got := r.URL.Query().Get("country"); got != "CN" {
			t.Fatalf("country = %q", got)
		}
		_, _ = w.Write([]byte(`{"status":"ok","checks":[{"name":"bucket_status","status":"ok"}]}`))
	}))
	defer srv.Close()

	err := cdnDoctor(client{baseURL: srv.URL, token: "test-token", http: srv.Client()}, []string{"-bucket", "downloads", "-path", "release/app.zip", "-country", "CN"})
	if err != nil {
		t.Fatal(err)
	}
	if !saw {
		t.Fatal("cdn-doctor API was not called")
	}
}

func TestSiteDoctorCallsAPI(t *testing.T) {
	var saw bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		saw = true
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/sites/cyberstream/doctor" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-token" {
			t.Fatalf("Authorization = %q", auth)
		}
		if got := r.URL.Query().Get("path"); got != "/assets/app.js" {
			t.Fatalf("path = %q", got)
		}
		if got := r.URL.Query().Get("deployment"); got != "dpl-abc" {
			t.Fatalf("deployment = %q", got)
		}
		if got := r.URL.Query().Get("country"); got != "CN" {
			t.Fatalf("country = %q", got)
		}
		_, _ = w.Write([]byte(`{"status":"ok","checks":[{"name":"site_status","status":"ok"}]}`))
	}))
	defer srv.Close()

	err := siteDoctor(client{baseURL: srv.URL, token: "test-token", http: srv.Client()}, []string{"-site", "cyberstream", "-path", "/assets/app.js", "-deployment", "dpl-abc", "-country", "CN"})
	if err != nil {
		t.Fatal(err)
	}
	if !saw {
		t.Fatal("site-doctor API was not called")
	}
}

func TestSwitchPlanBuildsBucketPlanFromCDNDoctor(t *testing.T) {
	var saw bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		saw = true
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/asset-buckets/downloads/doctor" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		if got := r.URL.Query().Get("path"); got != "release/app.zip" {
			t.Fatalf("path = %q", got)
		}
		if got := r.URL.Query().Get("country"); got != "CN" {
			t.Fatalf("country = %q", got)
		}
		_, _ = w.Write([]byte(`{
			"status":"ok",
			"path":"release/app.zip",
			"checks":[{"name":"selection","status":"ok"}],
			"selection":{"target":"backup","reason":"region_fallback:china"},
			"candidates":[
				{"target":"edge","status":"ready"},
				{"target":"backup","status":"ready","selected":true}
			],
			"recommendations":[{"action":"manual_switch_available","level":"info","summary":"review candidates"}],
			"next_commands":["supercdnctl routing-policy-status -policy global_smart"]
		}`))
	}))
	defer srv.Close()

	var plan switchPlanOutput
	out := captureStdout(t, func() {
		if err := switchPlan(client{baseURL: srv.URL, token: "test-token", http: srv.Client()}, []string{"-bucket", "downloads", "-path", "release/app.zip", "-country", "CN"}); err != nil {
			t.Fatal(err)
		}
	})
	if err := json.Unmarshal([]byte(out), &plan); err != nil {
		t.Fatal(err)
	}
	if !saw {
		t.Fatal("switch-plan did not call cdn doctor")
	}
	if !plan.SafeToSwitch || plan.Mode != "bucket" || plan.ReadyCandidates != 2 || plan.SelectedTarget != "backup" {
		t.Fatalf("unexpected switch plan: %+v", plan)
	}
	if !plan.CandidateReady || !plan.ApplySupported || plan.ApplyReason != "" {
		t.Fatalf("unexpected apply support: %+v", plan)
	}
}

func TestSwitchPlanSeparatesPolicyReadinessFromApplySupport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/asset-buckets/downloads/doctor" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		_, _ = w.Write([]byte(`{
			"status":"ok",
			"path":"release/app.zip",
			"route_profile":{"name":"smart","routing_policy":"global_smart"},
			"checks":[{"name":"selection","status":"ok"}],
			"selection":{"target":"backup","reason":"region_balance:CN"},
			"candidates":[
				{"target":"edge","status":"ready"},
				{"target":"backup","status":"ready","selected":true}
			]
		}`))
	}))
	defer srv.Close()

	var plan switchPlanOutput
	out := captureStdout(t, func() {
		if err := switchPlan(client{baseURL: srv.URL, token: "test-token", http: srv.Client()}, []string{"-bucket", "downloads", "-path", "release/app.zip"}); err != nil {
			t.Fatal(err)
		}
	})
	if err := json.Unmarshal([]byte(out), &plan); err != nil {
		t.Fatal(err)
	}
	if !plan.CandidateReady || plan.ApplySupported || plan.SafeToSwitch {
		t.Fatalf("expected ready candidates but unsupported apply: %+v", plan)
	}
	if !strings.Contains(plan.ApplyReason, "routing_policy") || len(plan.Risks) == 0 {
		t.Fatalf("missing apply reason/risk: %+v", plan)
	}
	if len(plan.NextCommands) != 1 || !strings.Contains(plan.NextCommands[0], "routing-policy-status -policy global_smart") {
		t.Fatalf("next_commands = %#v", plan.NextCommands)
	}
}

func TestSwitchPlanRejectsResourceFailoverApplySupport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/sites/cyberstream/doctor" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		_, _ = w.Write([]byte(`{
			"status":"ok",
			"path":"/assets/app.js",
			"checks":[{"name":"route","status":"ok"}],
			"route":{
				"resource_failover":true,
				"selection":{"target":"edge","reason":"failover"},
				"candidates":[
					{"target":"edge","status":"ready"},
					{"target":"backup","status":"ready"}
				]
			}
		}`))
	}))
	defer srv.Close()

	var plan switchPlanOutput
	out := captureStdout(t, func() {
		if err := switchPlan(client{baseURL: srv.URL, token: "test-token", http: srv.Client()}, []string{"-site", "cyberstream", "-path", "/assets/app.js"}); err != nil {
			t.Fatal(err)
		}
	})
	if err := json.Unmarshal([]byte(out), &plan); err != nil {
		t.Fatal(err)
	}
	if !plan.CandidateReady || plan.ApplySupported || plan.SafeToSwitch {
		t.Fatalf("expected ready candidates but unsupported resource_failover apply: %+v", plan)
	}
	if !strings.Contains(plan.ApplyReason, "resource_failover") {
		t.Fatalf("apply_reason = %q", plan.ApplyReason)
	}
	if len(plan.NextCommands) != 1 || !strings.Contains(plan.NextCommands[0], "route-explain -site cyberstream -path /assets/app.js") {
		t.Fatalf("next_commands = %#v", plan.NextCommands)
	}
}

func TestSwitchPlanReportsSiteRisksFromSiteDoctor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/sites/cyberstream/doctor" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		if got := r.URL.Query().Get("deployment"); got != "dpl-abc" {
			t.Fatalf("deployment = %q", got)
		}
		_, _ = w.Write([]byte(`{
			"status":"warning",
			"path":"/assets/app.js",
			"checks":[{"name":"route","status":"warning"}],
			"route":{
				"selection":{"target":"edge","reason":"fallback_order"},
				"candidates":[
					{"target":"edge","status":"ready"},
					{"target":"backup","status":"skipped","reason":"skipped by health: failed"}
				]
			},
			"recommendations":[{"action":"manual_switch_not_ready","level":"warning","summary":"backup candidates are not ready"}]
		}`))
	}))
	defer srv.Close()

	var plan switchPlanOutput
	out := captureStdout(t, func() {
		if err := switchPlan(client{baseURL: srv.URL, token: "test-token", http: srv.Client()}, []string{"-site", "cyberstream", "-path", "/assets/app.js", "-deployment", "dpl-abc"}); err != nil {
			t.Fatal(err)
		}
	})
	if err := json.Unmarshal([]byte(out), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.SafeToSwitch || plan.ReadyCandidates != 1 || len(plan.Risks) == 0 {
		t.Fatalf("expected risky switch plan: %+v", plan)
	}
}

func TestSwitchApplyCallsBucketPrimaryTargetAPI(t *testing.T) {
	var saw bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		saw = true
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/asset-buckets/downloads/objects/primary-target" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		if body["path"] != "release/app.zip" || body["target"] != "backup" || body["expected_current_target"] != "edge" || body["confirm"] != "switch" {
			t.Fatalf("unexpected body: %+v", body)
		}
		if body["dry_run"] != false {
			t.Fatalf("dry_run = %#v", body["dry_run"])
		}
		_, _ = w.Write([]byte(`{"status":"switched","mode":"bucket","resource":"downloads","path":"release/app.zip","previous_target":"edge","target":"backup"}`))
	}))
	defer srv.Close()

	if err := switchApply(client{baseURL: srv.URL, token: "test-token", http: srv.Client()}, []string{"-bucket", "downloads", "-path", "release/app.zip", "-target", "backup", "-expected-current", "edge", "-dry-run=false", "-confirm", "switch"}); err != nil {
		t.Fatal(err)
	}
	if !saw {
		t.Fatal("switch-apply did not call bucket API")
	}
}

func TestSwitchApplyCallsSiteDeploymentPrimaryTargetAPI(t *testing.T) {
	var saw bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		saw = true
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/sites/cyberstream/deployments/dpl-abc/files/primary-target" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		if body["path"] != "/assets/app.js" || body["target"] != "backup" || body["dry_run"] != true {
			t.Fatalf("unexpected body: %+v", body)
		}
		_, _ = w.Write([]byte(`{"status":"planned","mode":"site","resource":"cyberstream","deployment_id":"dpl-abc","path":"/assets/app.js","target":"backup","dry_run":true}`))
	}))
	defer srv.Close()

	if err := switchApply(client{baseURL: srv.URL, token: "test-token", http: srv.Client()}, []string{"-site", "cyberstream", "-deployment", "dpl-abc", "-path", "/assets/app.js", "-target", "backup"}); err != nil {
		t.Fatal(err)
	}
	if !saw {
		t.Fatal("switch-apply did not call site API")
	}
}

func TestRollbackPlanOriginAssistedUsesMetadataPromote(t *testing.T) {
	var saw bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		saw = true
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/sites/blog/deployments/dpl-old" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		_, _ = w.Write([]byte(`{"id":"dpl-old","site_id":"blog","status":"ready","environment":"production","deployment_target":"origin_assisted","active":false}`))
	}))
	defer srv.Close()

	var plan rollbackPlanOutput
	out := captureStdout(t, func() {
		if err := rollbackPlan(client{baseURL: srv.URL, token: "test-token", http: srv.Client()}, []string{"-site", "blog", "-deployment", "dpl-old"}); err != nil {
			t.Fatal(err)
		}
	})
	if err := json.Unmarshal([]byte(out), &plan); err != nil {
		t.Fatal(err)
	}
	if !saw {
		t.Fatal("rollback-plan did not fetch deployment")
	}
	if plan.Action != "metadata_promote" || !plan.MetadataPromoteSupported || !plan.SafeToRun || plan.RedeployRequired {
		t.Fatalf("unexpected rollback plan: %+v", plan)
	}
	if !plan.RollbackWriteReady || len(plan.WriteBlockers) != 0 || len(plan.MissingEvidence) != 0 {
		t.Fatalf("unexpected rollback write readiness: %+v", plan)
	}
	if len(plan.NextCommands) != 1 || !strings.Contains(plan.NextCommands[0], "promote-deployment -site blog -deployment dpl-old") {
		t.Fatalf("next_commands = %#v", plan.NextCommands)
	}
}

func TestRollbackPlanCloudflareStaticRequiresRedeploy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/sites/blog/deployments/dpl-static" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		_, _ = w.Write([]byte(`{
			"id":"dpl-static",
			"site_id":"blog",
			"status":"ready",
			"environment":"production",
			"route_profile":"overseas",
			"deployment_target":"cloudflare_static",
			"artifact_sha256":"artifact-sha",
			"manifest_key":"sites/blog/manifests/dpl-static.json",
			"file_count":12,
			"total_size":3456,
			"production_urls":["https://blog.example.com/"],
			"cloudflare_static":{
				"worker_name":"supercdn-blog-static",
				"version_id":"ver-123",
				"domains":["blog.example.com"],
				"urls":["https://blog.example.com/"],
				"assets_sha256":"assets-sha",
				"verification_status":"ok",
				"verified_at":"2026-05-15T00:00:00Z",
				"published_at":"2026-05-15T00:01:00Z"
			},
			"active":false
		}`))
	}))
	defer srv.Close()

	var plan rollbackPlanOutput
	out := captureStdout(t, func() {
		if err := rollbackPlan(client{baseURL: srv.URL, token: "test-token", http: srv.Client()}, []string{"-site", "blog", "-deployment", "dpl-static", "-dir", "dist old"}); err != nil {
			t.Fatal(err)
		}
	})
	if err := json.Unmarshal([]byte(out), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Action != "redeploy_cloudflare_static" || !plan.RedeployRequired || plan.MetadataPromoteSupported || plan.SafeToRun {
		t.Fatalf("unexpected rollback plan: %+v", plan)
	}
	if plan.RollbackWriteReady || len(plan.WriteBlockers) != 3 || len(plan.MissingEvidence) != 0 {
		t.Fatalf("unexpected rollback write readiness: %+v", plan)
	}
	if len(plan.NextCommands) != 2 || !strings.Contains(plan.NextCommands[0], "deploy-site -site blog -dir 'dist old' -target cloudflare_static -profile overseas") {
		t.Fatalf("next_commands = %#v", plan.NextCommands)
	}
	if !strings.Contains(plan.NextCommands[1], "probe-site -site blog -production -require-edge-static-html -require-html-revalidate -require-immutable-assets") {
		t.Fatalf("next_commands = %#v", plan.NextCommands)
	}
	if len(plan.Warnings) == 0 || !strings.Contains(plan.Warnings[0], "promote-deployment") {
		t.Fatalf("warnings = %#v", plan.Warnings)
	}
	if plan.Evidence.ArtifactSHA256 != "artifact-sha" || plan.Evidence.FileCount != 12 || len(plan.Evidence.ProductionURLs) != 1 {
		t.Fatalf("evidence = %+v", plan.Evidence)
	}
	if plan.Evidence.CloudflareStatic == nil || plan.Evidence.CloudflareStatic.WorkerName != "supercdn-blog-static" || plan.Evidence.CloudflareStatic.VersionID != "ver-123" || plan.Evidence.CloudflareStatic.AssetsSHA256 != "assets-sha" {
		t.Fatalf("cloudflare evidence = %+v", plan.Evidence.CloudflareStatic)
	}
}

func TestRollbackPlanHybridReportsMissingWriteEvidence(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/sites/blog/deployments/dpl-hybrid" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		_, _ = w.Write([]byte(`{
			"id":"dpl-hybrid",
			"site_id":"blog",
			"status":"ready",
			"environment":"production",
			"deployment_target":"hybrid_edge",
			"active":false
		}`))
	}))
	defer srv.Close()

	var plan rollbackPlanOutput
	out := captureStdout(t, func() {
		if err := rollbackPlan(client{baseURL: srv.URL, token: "test-token", http: srv.Client()}, []string{"-site", "blog", "-deployment", "dpl-hybrid"}); err != nil {
			t.Fatal(err)
		}
	})
	if err := json.Unmarshal([]byte(out), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Action != "redeploy_hybrid_edge" || plan.RollbackWriteReady || len(plan.WriteBlockers) != 4 {
		t.Fatalf("unexpected rollback plan: %+v", plan)
	}
	if len(plan.NextCommands) != 2 || !strings.Contains(plan.NextCommands[1], "probe-site -site blog -production -require-edge-static-html -require-edge-manifest-assets") {
		t.Fatalf("next_commands = %#v", plan.NextCommands)
	}
	for _, want := range []string{"source_dist_dir", "route_profile", "artifact_sha256", "file_count", "cloudflare_static", "edge_manifest_key"} {
		if !stringSliceContains(plan.MissingEvidence, want) {
			t.Fatalf("missing_evidence %#v does not contain %q", plan.MissingEvidence, want)
		}
	}
}

func TestDeleteDeploymentDryRunPlansSafeOriginDelete(t *testing.T) {
	var saw bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		saw = true
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/sites/blog/deployments/dpl-old" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		_, _ = w.Write([]byte(`{
			"id":"dpl-old",
			"site_id":"blog",
			"status":"ready",
			"deployment_target":"origin_assisted",
			"active":false,
			"pinned":false,
			"file_count":2,
			"artifact_sha256":"artifact-sha",
			"manifest_key":"sites/blog/manifests/dpl-old.json"
		}`))
	}))
	defer srv.Close()

	var plan deleteDeploymentPlanOutput
	out := captureStdout(t, func() {
		if err := deleteDeployment(client{baseURL: srv.URL, token: "test-token", http: srv.Client()}, []string{"-site", "blog", "-deployment", "dpl-old", "-dry-run", "-delete-objects", "-delete-remote=false"}); err != nil {
			t.Fatal(err)
		}
	})
	if err := json.Unmarshal([]byte(out), &plan); err != nil {
		t.Fatal(err)
	}
	if !saw {
		t.Fatal("delete-deployment dry-run did not fetch deployment")
	}
	if !plan.SafeToRun || plan.DeleteObjects != true || plan.DeleteRemote != false || plan.Target != "origin_assisted" || plan.RemoteCleanupSupported {
		t.Fatalf("unexpected delete plan: %+v", plan)
	}
	if plan.Evidence.FileCount != 2 || plan.Evidence.ArtifactSHA256 != "artifact-sha" || plan.Evidence.ManifestKey != "sites/blog/manifests/dpl-old.json" {
		t.Fatalf("evidence = %+v", plan.Evidence)
	}
	if len(plan.Warnings) != 0 {
		t.Fatalf("warnings = %#v", plan.Warnings)
	}
	if len(plan.NextCommands) != 1 || !strings.Contains(plan.NextCommands[0], "delete-deployment -site blog -deployment dpl-old -delete-objects -delete-remote=false") {
		t.Fatalf("next_commands = %#v", plan.NextCommands)
	}
}

func TestDeleteDeploymentDryRunWarnsForCloudflareMetadataOnlyDelete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/sites/blog/deployments/dpl-static" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		_, _ = w.Write([]byte(`{
			"id":"dpl-static",
			"site_id":"blog",
			"status":"ready",
			"deployment_target":"cloudflare_static",
			"active":false,
			"pinned":false,
			"cloudflare_static":{
				"worker_name":"supercdn-blog-static",
				"version_id":"ver-123",
				"domains":["blog.example.com"],
				"assets_sha256":"assets-sha"
			}
		}`))
	}))
	defer srv.Close()

	var plan deleteDeploymentPlanOutput
	out := captureStdout(t, func() {
		if err := deleteDeployment(client{baseURL: srv.URL, token: "test-token", http: srv.Client()}, []string{"-site", "blog", "-deployment", "dpl-static", "-dry-run"}); err != nil {
			t.Fatal(err)
		}
	})
	if err := json.Unmarshal([]byte(out), &plan); err != nil {
		t.Fatal(err)
	}
	if !plan.SafeToRun || plan.Target != "cloudflare_static" || len(plan.NextCommands) != 1 || plan.RemoteCleanupSupported {
		t.Fatalf("unexpected delete plan: %+v", plan)
	}
	if len(plan.RemoteCleanupBlockers) != 2 || !strings.Contains(plan.RemoteCleanupBlockers[0], "Cloudflare Worker versions") {
		t.Fatalf("remote_cleanup_blockers = %#v", plan.RemoteCleanupBlockers)
	}
	if plan.Evidence.CloudflareStatic == nil || plan.Evidence.CloudflareStatic.WorkerName != "supercdn-blog-static" || plan.Evidence.CloudflareStatic.VersionID != "ver-123" {
		t.Fatalf("cloudflare evidence = %+v", plan.Evidence.CloudflareStatic)
	}
	if len(plan.Warnings) != 1 || !strings.Contains(plan.Warnings[0], "Cloudflare Worker versions") {
		t.Fatalf("warnings = %#v", plan.Warnings)
	}
}

func TestDeleteDeploymentDryRunBlocksActiveOrPinnedDeployment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/sites/blog/deployments/dpl-active" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		_, _ = w.Write([]byte(`{
			"id":"dpl-active",
			"site_id":"blog",
			"status":"ready",
			"deployment_target":"origin_assisted",
			"active":true,
			"pinned":true
		}`))
	}))
	defer srv.Close()

	var plan deleteDeploymentPlanOutput
	out := captureStdout(t, func() {
		if err := deleteDeployment(client{baseURL: srv.URL, token: "test-token", http: srv.Client()}, []string{"-site", "blog", "-deployment", "dpl-active", "-dry-run"}); err != nil {
			t.Fatal(err)
		}
	})
	if err := json.Unmarshal([]byte(out), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.SafeToRun || len(plan.NextCommands) != 0 {
		t.Fatalf("unsafe deployment should not produce apply command: %+v", plan)
	}
	if len(plan.Warnings) != 2 || !strings.Contains(plan.Warnings[0], "active production") || !strings.Contains(plan.Warnings[1], "pinned") {
		t.Fatalf("warnings = %#v", plan.Warnings)
	}
}

func TestPrepareCloudflareStaticAssetsDirAugmentsExistingHeaders(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "index.html"), "ok")
	writeTestFile(t, filepath.Join(dir, "_headers"), "/*\n  X-Test: yes\n")

	prepared, cleanup, meta, err := prepareCloudflareStaticAssetsDir(dir, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if cleanup == nil || prepared == dir {
		t.Fatalf("existing _headers without edge evidence should be copied to a temporary directory")
	}
	defer cleanup()
	if !meta.Generated || meta.Source != "existing_with_edge_header" {
		t.Fatalf("unexpected headers meta: prepared=%s meta=%+v", prepared, meta)
	}
	raw, err := os.ReadFile(filepath.Join(prepared, "_headers"))
	if err != nil {
		t.Fatal(err)
	}
	headers := string(raw)
	for _, want := range []string{
		"X-Test: yes",
		"/*\n  X-SuperCDN-Edge-Source: cloudflare_static",
	} {
		if !strings.Contains(headers, want) {
			t.Fatalf("augmented headers missing %q:\n%s", want, headers)
		}
	}

	summary, err := summarizeCloudflareStaticDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if summary.FileCount != 1 {
		t.Fatalf("cloudflare static summary should ignore _headers, got %d files", summary.FileCount)
	}
}

func TestPrepareCloudflareStaticAssetsDirKeepsExistingEdgeHeaders(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "index.html"), "ok")
	writeTestFile(t, filepath.Join(dir, "_headers"), "/*\n  X-SuperCDN-Edge-Source: cloudflare_static\n")

	prepared, cleanup, meta, err := prepareCloudflareStaticAssetsDir(dir, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if cleanup != nil {
		t.Fatalf("existing edge-aware _headers should not need a temporary directory")
	}
	if prepared != dir || meta.Generated || meta.Source != "existing" {
		t.Fatalf("unexpected headers meta: prepared=%s meta=%+v", prepared, meta)
	}
}

func TestCloudflareVerifyFailureNextCommands(t *testing.T) {
	staticCommands := cloudflareVerifyFailureNextCommands("blog", `dist old`, "cloudflare_static", "overseas", []string{"blog.example.com"}, false)
	if len(staticCommands) != 2 ||
		!strings.Contains(staticCommands[0], "deploy-site -site blog -dir 'dist old' -target cloudflare_static -profile overseas -domains blog.example.com") ||
		!strings.Contains(staticCommands[1], "probe-site -url https://blog.example.com/ -require-edge-static-html -require-html-revalidate -require-immutable-assets") {
		t.Fatalf("static commands = %#v", staticCommands)
	}
	hybridCommands := cloudflareVerifyFailureNextCommands("blog", `dist`, "hybrid_edge", "ipfs_archive", []string{"blog.example.com"}, true)
	if len(hybridCommands) != 2 ||
		!strings.Contains(hybridCommands[0], "deploy-site -site blog -dir dist -target hybrid_edge -profile ipfs_archive -domains blog.example.com") ||
		!strings.Contains(hybridCommands[1], "probe-site -url https://blog.example.com/ -require-edge-static-html -require-edge-manifest-assets") {
		t.Fatalf("hybrid commands = %#v", hybridCommands)
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
		`EDGE_ENTRY_ORIGIN_FALLBACK = "false"`,
		`EDGE_MANIFEST_MODE = "route"`,
		`EDGE_ORIGIN_FALLBACK = "false"`,
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

func TestEdgeManifestCandidateReadinessAcceptsSmartRoutes(t *testing.T) {
	report, err := edgeManifestCandidateReadiness([]byte(`{
		"site_id":"demo",
		"deployment_id":"dpl",
		"routes":{
			"/":{"type":"origin","delivery":"origin","file":"index.html","status":200},
			"/assets/app.js":{
				"type":"smart",
				"delivery":"redirect",
				"file":"assets/app.js",
				"status":302,
				"candidates":[
					{"target":"repo_china_mobile","url":"https://alist.example/app.js","status":"ready"},
					{"target":"ipfs_pinata","url":"https://gateway.example/ipfs/cid","status":"ready"}
				]
			}
		}
	}`), "routing_policy", 2)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "ok" || report.RequiredRoutes != 1 || report.ReadyRoutes != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestEdgeManifestCandidateReadinessWaitsForSingleSourceFallback(t *testing.T) {
	report, err := edgeManifestCandidateReadiness([]byte(`{
		"site_id":"demo",
		"deployment_id":"dpl",
		"routes":{
			"/assets/app.js":{
				"type":"ipfs",
				"delivery":"redirect",
				"file":"assets/app.js",
				"status":200,
				"candidates":[
					{"target":"ipfs_pinata","url":"https://gateway.example/ipfs/cid","status":"ready"}
				]
			}
		}
	}`), "routing_policy", 2)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "pending" || report.RequiredRoutes != 1 || report.ReadyRoutes != 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if len(report.Routes) != 1 || report.Routes[0].OK || !strings.Contains(report.Routes[0].Message, "smart route") {
		t.Fatalf("unexpected route status: %+v", report.Routes)
	}
}

func TestEdgeManifestCandidateReadinessAcceptsFailoverRoutes(t *testing.T) {
	report, err := edgeManifestCandidateReadiness([]byte(`{
		"site_id":"demo",
		"deployment_id":"dpl",
		"routes":{
			"/assets/app.js":{
				"type":"failover",
				"delivery":"failover",
				"file":"assets/app.js",
				"status":200,
				"candidates":[
					{"target":"primary","url":"https://primary.example/app.js","status":"ready"},
					{"target":"backup","url":"https://backup.example/app.js","status":"ready"}
				]
			}
		}
	}`), "resource_failover", 2)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "ok" || report.RequiredRoutes != 1 || report.ReadyRoutes != 1 {
		t.Fatalf("unexpected report: %+v", report)
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

func TestListSitesCallsControlPlane(t *testing.T) {
	saw := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		saw = true
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/sites" {
			t.Fatalf("request = %s %s", r.Method, r.URL.String())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization = %q", got)
		}
		_, _ = w.Write([]byte(`{"sites":[{"id":"demo","url":"https://demo.example.com/"}]}`))
	}))
	defer srv.Close()

	var listErr error
	out := captureStdout(t, func() {
		listErr = listSites(client{baseURL: srv.URL, token: "test-token", http: srv.Client()}, nil)
	})
	if listErr != nil {
		t.Fatal(listErr)
	}
	if !saw {
		t.Fatal("server was not called")
	}
	if !strings.Contains(out, `"id": "demo"`) {
		t.Fatalf("unexpected output:\n%s", out)
	}
}

func TestSiteLifecycleCommandsCallControlPlane(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.String())
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization = %q", got)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := client{baseURL: srv.URL, token: "test-token", http: srv.Client()}
	for _, run := range []struct {
		name string
		fn   func(client, []string) error
		args []string
	}{
		{name: "offline", fn: offlineSite, args: []string{"-site", "demo"}},
		{name: "online", fn: onlineSite, args: []string{"-site", "demo"}},
		{name: "delete", fn: deleteSite, args: []string{"-site", "demo", "-force", "-delete-remote=false"}},
	} {
		if err := run.fn(c, run.args); err != nil {
			t.Fatalf("%s: %v", run.name, err)
		}
	}
	want := []string{
		"POST /api/v1/sites/demo/offline",
		"POST /api/v1/sites/demo/online",
		"DELETE /api/v1/sites/demo?delete_remote=false&force=true",
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls = %#v", calls)
	}
	if err := deleteSite(c, []string{"-site", "demo"}); err == nil || !strings.Contains(err.Error(), "-force") {
		t.Fatalf("delete without force error = %v", err)
	}
}

func TestUpdateSiteReusesExistingDeploymentDefaults(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "index.html"), "<h1>updated</h1>")

	sawResolve := false
	sawUpload := false
	sawWait := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sites/demo/deployment-target":
			sawResolve = true
			if got := r.URL.Query().Get("route_profile"); got != "" {
				t.Fatalf("route_profile query = %q", got)
			}
			if got := r.URL.Query().Get("deployment_target"); got != "" {
				t.Fatalf("deployment_target query = %q", got)
			}
			_, _ = w.Write([]byte(`{"site_id":"demo","site_exists":true,"route_profile":"china_all","deployment_target":"origin_assisted","source":"site","domains":["demo.example.com"]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sites/demo/deployments":
			sawUpload = true
			if err := r.ParseMultipartForm(32 << 20); err != nil {
				t.Fatal(err)
			}
			for field, want := range map[string]string{
				"route_profile":     "china_all",
				"deployment_target": "origin_assisted",
				"environment":       "production",
				"promote":           "true",
			} {
				if got := r.FormValue(field); got != want {
					t.Fatalf("%s = %q, want %q", field, got, want)
				}
			}
			file, header, err := r.FormFile("artifact")
			if err != nil {
				t.Fatal(err)
			}
			_ = file.Close()
			if !strings.HasSuffix(strings.ToLower(header.Filename), ".zip") {
				t.Fatalf("artifact filename = %q", header.Filename)
			}
			_, _ = w.Write([]byte(`{"deployment_id":"dpl-update"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sites/demo/deployments/dpl-update":
			sawWait = true
			_, _ = w.Write([]byte(`{"id":"dpl-update","site_id":"demo","status":"active","route_profile":"china_all","deployment_target":"origin_assisted","active":true,"production_url":"https://demo.example.com/"}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()

	var updateErr error
	out := captureStdout(t, func() {
		updateErr = updateSite(client{baseURL: srv.URL, token: "test-token", http: srv.Client()}, []string{"-site", "demo", "-dir", dir})
	})
	if updateErr != nil {
		t.Fatal(updateErr)
	}
	if !sawResolve || !sawUpload || !sawWait {
		t.Fatalf("sawResolve=%v sawUpload=%v sawWait=%v", sawResolve, sawUpload, sawWait)
	}
	if !strings.Contains(out, `"production_url": "https://demo.example.com/"`) {
		t.Fatalf("unexpected output:\n%s", out)
	}
}

func TestUpdateSiteRequiresExistingSite(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "index.html"), "<h1>updated</h1>")

	sawUpload := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sites/missing/deployment-target":
			_, _ = w.Write([]byte(`{"site_id":"missing","site_exists":false,"route_profile":"overseas","deployment_target":"cloudflare_static","source":"route_profile","domains":["missing.example.com"]}`))
		case r.Method == http.MethodPost:
			sawUpload = true
			t.Fatalf("update-site should not upload for a missing site")
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()

	err := updateSite(client{baseURL: srv.URL, token: "test-token", http: srv.Client()}, []string{"-site", "missing", "-dir", dir})
	if err == nil || !strings.Contains(err.Error(), "requires an existing site") {
		t.Fatalf("unexpected error: %v", err)
	}
	if sawUpload {
		t.Fatal("upload should not have been attempted")
	}
}

func TestIPFSStatusUsesTargetQuery(t *testing.T) {
	saw := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		saw = true
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/ipfs/status" {
			t.Fatalf("request = %s %s", r.Method, r.URL.String())
		}
		if got := r.URL.Query().Get("target"); got != "ipfs_pinata" {
			t.Fatalf("target query = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization = %q", got)
		}
		_, _ = w.Write([]byte(`{"configured":true,"ok":true,"providers":[]}`))
	}))
	defer srv.Close()

	err := ipfsStatus(client{baseURL: srv.URL, token: "test-token", http: srv.Client()}, []string{"-target", "ipfs_pinata"})
	if err != nil {
		t.Fatal(err)
	}
	if !saw {
		t.Fatal("server was not called")
	}
}

func TestRefreshIPFSPinsPostsObjectIDAndTarget(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/ipfs/pins/refresh" {
			t.Fatalf("request = %s %s", r.Method, r.URL.String())
		}
		if gotAuth := r.Header.Get("Authorization"); gotAuth != "Bearer test-token" {
			t.Fatalf("Authorization = %q", gotAuth)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"status":"ok","object_id":123,"pins":[]}`))
	}))
	defer srv.Close()

	err := refreshIPFSPins(client{baseURL: srv.URL, token: "test-token", http: srv.Client()}, []string{"-object-id", "123", "-target", "ipfs_pinata"})
	if err != nil {
		t.Fatal(err)
	}
	if got["object_id"] != float64(123) || got["target"] != "ipfs_pinata" {
		t.Fatalf("request body = %#v", got)
	}
}

func TestIPFSSmokeUploadsProbesAndRefreshes(t *testing.T) {
	var (
		srv        *httptest.Server
		sawStatus  bool
		sawCreate  bool
		sawUpload  bool
		sawRefresh bool
		sawHead    bool
		sawRange   bool
		sawGet     bool
	)
	const cid = "bafybeigdyrztcli"
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if auth := r.Header.Get("Authorization"); auth != "Bearer test-token" {
				t.Fatalf("Authorization = %q", auth)
			}
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/ipfs/status":
			sawStatus = true
			if got := r.URL.Query().Get("target"); got != "ipfs_pinata" {
				t.Fatalf("target = %q", got)
			}
			_, _ = w.Write([]byte(`{"configured":true,"ok":true,"providers":[{"target":"ipfs_pinata","ok":true}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/asset-buckets":
			sawCreate = true
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			if req["slug"] != "ipfs-smoke" || req["route_profile"] != "ipfs_archive" {
				t.Fatalf("create request = %#v", req)
			}
			_, _ = w.Write([]byte(`{"slug":"ipfs-smoke"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/asset-buckets/ipfs-smoke/objects":
			sawUpload = true
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			if got := r.FormValue("path"); got != "smoke/file.txt" {
				t.Fatalf("upload path = %q", got)
			}
			gatewayURL := srv.URL + "/ipfs/" + cid
			_, _ = w.Write([]byte(`{
				"object":{"id":123,"ipfs":[{"object_id":123,"target":"ipfs_pinata","provider":"pinata","cid":"` + cid + `","gateway_url":"` + gatewayURL + `"}]},
				"public_url":"https://origin.example/a/ipfs-smoke/smoke/file.txt",
				"cdn_url":"` + gatewayURL + `",
				"ipfs":[{"object_id":123,"target":"ipfs_pinata","provider":"pinata","cid":"` + cid + `","gateway_url":"` + gatewayURL + `"}]
			}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/ipfs/pins/refresh":
			sawRefresh = true
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			if req["object_id"] != float64(123) || req["target"] != "ipfs_pinata" {
				t.Fatalf("refresh request = %#v", req)
			}
			_, _ = w.Write([]byte(`{"status":"ok","object_id":123,"pins":[{"target":"ipfs_pinata","cid":"` + cid + `","pin_status":"pinned"}]}`))
		case r.Method == http.MethodHead && r.URL.Path == "/ipfs/"+cid:
			sawHead = true
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("Content-Length", "10")
		case r.Method == http.MethodGet && r.URL.Path == "/ipfs/"+cid && r.Header.Get("Range") != "":
			sawRange = true
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Content-Range", "bytes 0-4/10")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte("hello"))
		case r.Method == http.MethodGet && r.URL.Path == "/ipfs/"+cid:
			sawGet = true
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("hello ipfs"))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()

	file := filepath.Join(t.TempDir(), "file.txt")
	writeTestFile(t, file, "hello ipfs")
	var smokeErr error
	out := captureStdout(t, func() {
		smokeErr = ipfsSmoke(client{baseURL: srv.URL, token: "test-token", http: srv.Client()}, []string{
			"-file", file,
			"-bucket", "ipfs-smoke",
			"-path", "smoke/file.txt",
			"-download-runs", "1",
		})
	})
	if smokeErr != nil {
		t.Fatal(smokeErr)
	}
	for name, saw := range map[string]bool{
		"status":  sawStatus,
		"create":  sawCreate,
		"upload":  sawUpload,
		"refresh": sawRefresh,
		"head":    sawHead,
		"range":   sawRange,
		"get":     sawGet,
	} {
		if !saw {
			t.Fatalf("missing %s request", name)
		}
	}
	var result ipfsSmokeResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" || result.CID != cid || result.GatewayURL != srv.URL+"/ipfs/"+cid || len(result.Probes) != 3 {
		t.Fatalf("unexpected smoke result: %+v", result)
	}
}

func TestIPFSWebSmokeDeploysExportsAndProbes(t *testing.T) {
	var (
		srv           *httptest.Server
		sawStatus     bool
		sawCreate     bool
		sawDeploy     bool
		sawWait       bool
		sawManifest   bool
		sawRoot       bool
		sawAssetHop   bool
		sawGateway    bool
		sawRange      bool
		sawGatewayGet bool
		sawCleanup    bool
	)
	const cid = "bafybeigdyrztweb"
	const site = "ipfs-web-smoke"
	const deployment = "dpl-ipfs"
	const assetPath = "assets/demo.txt"
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if auth := r.Header.Get("Authorization"); auth != "Bearer test-token" {
				t.Fatalf("Authorization = %q", auth)
			}
		}
		gatewayURL := srv.URL + "/ipfs/" + cid
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/ipfs/status":
			sawStatus = true
			if got := r.URL.Query().Get("target"); got != "ipfs_pinata" {
				t.Fatalf("target = %q", got)
			}
			_, _ = w.Write([]byte(`{"configured":true,"ok":true,"providers":[{"target":"ipfs_pinata","ok":true}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sites":
			sawCreate = true
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			if req["id"] != site || req["route_profile"] != "ipfs_archive" || req["deployment_target"] != "origin_assisted" {
				t.Fatalf("create site request = %#v", req)
			}
			_, _ = w.Write([]byte(`{"id":"` + site + `","route_profile":"ipfs_archive","deployment_target":"origin_assisted"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sites/"+site+"/deployments":
			sawDeploy = true
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			if r.FormValue("route_profile") != "ipfs_archive" || r.FormValue("deployment_target") != "origin_assisted" || r.FormValue("environment") != "preview" || r.FormValue("pinned") != "false" {
				t.Fatalf("deploy form profile=%q target=%q env=%q pinned=%q", r.FormValue("route_profile"), r.FormValue("deployment_target"), r.FormValue("environment"), r.FormValue("pinned"))
			}
			_, _ = w.Write([]byte(`{"deployment_id":"` + deployment + `"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sites/"+site+"/deployments/"+deployment:
			sawWait = true
			_, _ = w.Write([]byte(`{"id":"` + deployment + `","site_id":"` + site + `","status":"ready","route_profile":"ipfs_archive","deployment_target":"origin_assisted","preview_url":"/p/` + site + `/` + deployment + `/"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sites/"+site+"/deployments/"+deployment+"/edge-manifest":
			sawManifest = true
			_, _ = w.Write([]byte(`{
				"site_id":"` + site + `",
				"deployment_id":"` + deployment + `",
				"route_profile":"ipfs_archive",
				"routes":{
					"/` + assetPath + `":{
						"type":"ipfs",
						"location":"` + gatewayURL + `",
						"ipfs":[{"target":"ipfs_pinata","provider":"pinata","cid":"` + cid + `","gateway_url":"` + gatewayURL + `"}],
						"gateway_fallbacks":["` + gatewayURL + `"]
					}
				}
			}`))
		case r.Method == http.MethodGet && r.URL.Path == "/p/"+site+"/"+deployment+"/":
			sawRoot = true
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<a href="` + assetPath + `">asset</a>`))
		case r.Method == http.MethodHead && r.URL.Path == "/p/"+site+"/"+deployment+"/"+assetPath:
			sawAssetHop = true
			w.Header().Set("Location", gatewayURL)
			w.Header().Set("X-SuperCDN-Redirect", "storage")
			w.WriteHeader(http.StatusFound)
		case r.Method == http.MethodHead && r.URL.Path == "/ipfs/"+cid:
			sawGateway = true
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("Content-Length", "10")
		case r.Method == http.MethodGet && r.URL.Path == "/ipfs/"+cid && r.Header.Get("Range") != "":
			sawRange = true
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Content-Range", "bytes 0-4/10")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte("hello"))
		case r.Method == http.MethodGet && r.URL.Path == "/ipfs/"+cid:
			sawGatewayGet = true
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("hello ipfs"))
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/sites/"+site+"/deployments/"+deployment:
			sawCleanup = true
			if got := r.URL.Query().Get("delete_objects"); got != "true" {
				t.Fatalf("delete_objects = %q", got)
			}
			if got := r.URL.Query().Get("delete_remote"); got != "true" {
				t.Fatalf("delete_remote = %q", got)
			}
			_, _ = w.Write([]byte(`{"deleted_deployment":true,"delete_objects":true,"delete_remote":true}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()

	file := filepath.Join(t.TempDir(), "demo.txt")
	writeTestFile(t, file, "hello web ipfs")
	var smokeErr error
	out := captureStdout(t, func() {
		smokeErr = ipfsWebSmoke(client{baseURL: srv.URL, token: "test-token", http: srv.Client()}, []string{
			"-file", file,
			"-site", site,
			"-asset-path", assetPath,
			"-download-runs", "1",
			"-timeout", "2s",
			"-cleanup",
		})
	})
	if smokeErr != nil {
		t.Fatal(smokeErr)
	}
	for name, saw := range map[string]bool{
		"status":      sawStatus,
		"create":      sawCreate,
		"deploy":      sawDeploy,
		"wait":        sawWait,
		"manifest":    sawManifest,
		"root":        sawRoot,
		"asset_hop":   sawAssetHop,
		"gateway":     sawGateway,
		"range":       sawRange,
		"gateway_get": sawGatewayGet,
		"cleanup":     sawCleanup,
	} {
		if !saw {
			t.Fatalf("missing %s request", name)
		}
	}
	var result ipfsWebSmokeResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" || result.CID != cid || result.GatewayURL != srv.URL+"/ipfs/"+cid || result.ManifestRoute.Type != "ipfs" {
		t.Fatalf("unexpected web smoke result: %+v", result)
	}
	if !result.Cleanup || len(result.Deleted) == 0 {
		t.Fatalf("cleanup result missing: %+v", result)
	}
	if len(result.Probes) != 5 || result.Probes[1].HTTPStatus != http.StatusFound || result.Probes[1].Location != result.GatewayURL {
		t.Fatalf("unexpected probes: %+v", result.Probes)
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

func TestRouteExplainCallsControlPlane(t *testing.T) {
	saw := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-token" {
			t.Fatalf("Authorization = %q", auth)
		}
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/sites/demo/route-explain" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		saw = true
		if got := r.URL.Query().Get("path"); got != "/assets/app.js" {
			t.Fatalf("path query = %q", got)
		}
		if got := r.URL.Query().Get("country"); got != "CN" {
			t.Fatalf("country query = %q", got)
		}
		if got := r.URL.Query().Get("client_ip"); got != "203.0.113.10" {
			t.Fatalf("client_ip query = %q", got)
		}
		_, _ = w.Write([]byte(`{"site_id":"demo","deployment_id":"dpl","path":"/assets/app.js","route":{"type":"smart"}}`))
	}))
	defer srv.Close()

	err := routeExplain(client{baseURL: srv.URL, token: "test-token", http: srv.Client()}, []string{
		"-site", "demo",
		"-path", "/assets/app.js",
		"-country", "CN",
		"-client-ip", "203.0.113.10",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !saw {
		t.Fatal("route explain request was not sent")
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

func TestProbeSiteRedactsSignedURLsByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<script type="module" src="/assets/app.js"></script>`))
		case "/assets/app.js":
			http.Redirect(w, r, "/signed/app.js?X-Amz-Signature=secret&plain=keep", http.StatusFound)
		case "/signed/app.js":
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = w.Write([]byte("console.log('ok')"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	var probeErr error
	out := captureStdout(t, func() {
		probeErr = probeSite(client{}, []string{
			"-url", srv.URL + "/",
			"-max-assets", "1",
		})
	})
	if probeErr != nil {
		t.Fatal(probeErr)
	}
	if strings.Contains(out, "secret") || strings.Contains(out, "plain=keep") {
		t.Fatalf("probe output leaked signed query values:\n%s", out)
	}
	if !strings.Contains(out, "X-Amz-Signature=%3Credacted%3E") || !strings.Contains(out, "plain=%3Credacted%3E") {
		t.Fatalf("probe output did not contain redacted query values:\n%s", out)
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

func TestCreateIPFSBucketUsesArchiveDefaults(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/asset-buckets" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"slug":"ipfs"}`))
	}))
	defer srv.Close()

	err := createIPFSBucket(client{baseURL: srv.URL, token: "test-token", http: srv.Client()}, []string{
		"-slug", "ipfs",
		"-types", "image,archive",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got["route_profile"] != "ipfs_archive" {
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

func TestDeleteBucketObjectSelectorQueries(t *testing.T) {
	var calls []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v1/asset-buckets/docs/objects" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		calls = append(calls, r.URL.Query())
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := client{baseURL: srv.URL, token: "test-token", http: srv.Client()}
	for _, args := range [][]string{
		{"-bucket", "docs", "-paths", "a.txt,b.txt", "-delete-remote=false"},
		{"-bucket", "docs", "-prefix", "tmp/", "-force"},
		{"-bucket", "docs", "-all", "-force"},
	} {
		if err := deleteBucketObject(c, args); err != nil {
			t.Fatal(err)
		}
	}
	if len(calls) != 3 {
		t.Fatalf("calls = %+v", calls)
	}
	if got := calls[0]["path"]; len(got) != 2 || got[0] != "a.txt" || got[1] != "b.txt" || calls[0].Get("delete_remote") != "false" {
		t.Fatalf("paths query = %+v", calls[0])
	}
	if calls[1].Get("prefix") != "tmp/" || calls[1].Get("force") != "true" {
		t.Fatalf("prefix query = %+v", calls[1])
	}
	if calls[2].Get("all") != "true" || calls[2].Get("force") != "true" {
		t.Fatalf("all query = %+v", calls[2])
	}
	if err := deleteBucketObject(c, []string{"-bucket", "docs", "-prefix", "tmp/"}); err == nil || !strings.Contains(err.Error(), "-force") {
		t.Fatalf("prefix without force error = %v", err)
	}
}

func TestRepairReplicasPostsRepairRequest(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/objects/42/replicas/repair" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"status":"queued"}`))
	}))
	defer srv.Close()

	c := client{baseURL: srv.URL, token: "test-token", http: srv.Client()}
	if err := repairReplicas(c, []string{"-object-id", "42", "-target", "repo_backup", "-force"}); err != nil {
		t.Fatal(err)
	}
	if got["target"] != "repo_backup" || got["force"] != true {
		t.Fatalf("repair request = %#v", got)
	}
	if err := repairReplicas(c, []string{"-target", "repo_backup"}); err == nil || !strings.Contains(err.Error(), "-object-id") {
		t.Fatalf("missing object id error = %v", err)
	}
}

func TestRefreshReplicasPostsRefreshRequest(t *testing.T) {
	var got map[string]any
	var bucketGot map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/objects/42/replicas/refresh":
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
			}
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/api/v1/asset-buckets/docs/replicas/refresh":
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
			}
			if err := json.NewDecoder(r.Body).Decode(&bucketGot); err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()

	c := client{baseURL: srv.URL, token: "test-token", http: srv.Client()}
	if err := refreshReplicas(c, []string{"-object-id", "42", "-target", "repo_backup"}); err != nil {
		t.Fatal(err)
	}
	if got["target"] != "repo_backup" {
		t.Fatalf("refresh request = %#v", got)
	}
	if err := refreshReplicas(c, []string{"-bucket", "docs", "-prefix", "tmp/", "-target", "repo_backup", "-limit", "25"}); err != nil {
		t.Fatal(err)
	}
	if bucketGot["target"] != "repo_backup" || bucketGot["prefix"] != "tmp/" || bucketGot["limit"] != float64(25) || bucketGot["all"] != nil {
		t.Fatalf("bucket refresh request = %#v", bucketGot)
	}
	bucketGot = nil
	if err := refreshReplicas(c, []string{"-bucket", "docs"}); err != nil {
		t.Fatal(err)
	}
	if bucketGot["all"] != true {
		t.Fatalf("bucket default refresh request = %#v", bucketGot)
	}
	if err := refreshReplicas(c, []string{"-target", "repo_backup"}); err == nil || !strings.Contains(err.Error(), "one of -object-id or -bucket") {
		t.Fatalf("missing selector error = %v", err)
	}
	if err := refreshReplicas(c, []string{"-object-id", "1", "-bucket", "docs"}); err == nil || !strings.Contains(err.Error(), "choose only one") {
		t.Fatalf("conflicting selector error = %v", err)
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
	var uploadErr error
	out := captureStdout(t, func() {
		uploadErr = uploadBucket(client{baseURL: srv.URL, token: "test-token", http: srv.Client()}, []string{
			"-bucket", "posters",
			"-file", file,
			"-path", "images/one.png",
			"-asset-type", "image",
			"-warmup",
			"-warmup-method", "GET",
			"-warmup-base-url", "https://cdn.example.com",
		})
	})
	if uploadErr != nil {
		t.Fatal(uploadErr)
	}
	if !uploadSeen || !warmupSeen {
		t.Fatalf("uploadSeen=%v warmupSeen=%v", uploadSeen, warmupSeen)
	}
	if warmupReq["path"] != "images/one.png" || warmupReq["method"] != "GET" || warmupReq["base_url"] != "https://cdn.example.com" {
		t.Fatalf("warmup request = %#v", warmupReq)
	}
	var report struct {
		Summary      string            `json:"summary"`
		CopyURLs     map[string]string `json:"copy_urls"`
		NextCommands []string          `json:"next_commands"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	if report.Summary != "uploaded and warmed posters/images/one.png" {
		t.Fatalf("summary = %q", report.Summary)
	}
	if report.CopyURLs["public_url"] != "https://cdn.example.com/a/posters/images/one.png" {
		t.Fatalf("copy_urls = %#v", report.CopyURLs)
	}
	if len(report.NextCommands) != 1 || !strings.Contains(report.NextCommands[0], "cdn-doctor -bucket posters -path images/one.png") {
		t.Fatalf("next_commands = %#v", report.NextCommands)
	}
}

func TestUploadBucketFailureSuggestsCDNDoctor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bucket is not ready", http.StatusBadRequest)
	}))
	defer srv.Close()

	file := filepath.Join(t.TempDir(), "one.png")
	writeTestFile(t, file, "png")
	err := uploadBucket(client{baseURL: srv.URL, token: "test-token", http: srv.Client()}, []string{
		"-bucket", "posters",
		"-file", file,
		"-path", "images/one.png",
	})
	if err == nil {
		t.Fatal("expected upload error")
	}
	if !strings.Contains(err.Error(), "next diagnostic: supercdnctl cdn-doctor -bucket posters -path images/one.png") {
		t.Fatalf("error missing diagnostic command: %v", err)
	}
}

func TestCLIHintArgQuotesPowerShellArgs(t *testing.T) {
	got := makeCDNDoctorCommand("poster bucket", "images/O'Brien one.png")
	for _, want := range []string{
		"-bucket 'poster bucket'",
		"-path 'images/O''Brien one.png'",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("command %q missing %q", got, want)
		}
	}
}

func TestUploadBucketDirUploadsFilesWithConcurrencyLimit(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "one.txt"), "one")
	writeTestFile(t, filepath.Join(dir, "nested", "two.txt"), "two")
	writeTestFile(t, filepath.Join(dir, "nested", "three.txt"), "three")

	var (
		mu      sync.Mutex
		seen    = map[string]string{}
		current int
		maxSeen int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/asset-buckets/posters/objects" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("upload method = %s", r.Method)
		}
		mu.Lock()
		current++
		if current > maxSeen {
			maxSeen = current
		}
		mu.Unlock()
		defer func() {
			mu.Lock()
			current--
			mu.Unlock()
		}()
		time.Sleep(20 * time.Millisecond)
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		if got := r.FormValue("asset_type"); got != "document" {
			t.Fatalf("asset_type = %q", got)
		}
		if got := r.FormValue("cache_control"); got != "public, max-age=60" {
			t.Fatalf("cache_control = %q", got)
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Fatal(err)
		}
		_ = file.Close()
		logicalPath := r.FormValue("path")
		mu.Lock()
		seen[logicalPath] = header.Filename
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"bucket": "posters",
			"bucket_object": map[string]any{
				"logical_path": logicalPath,
			},
		})
	}))
	defer srv.Close()

	var uploadErr error
	out := captureStdout(t, func() {
		uploadErr = uploadBucketDir(client{baseURL: srv.URL, token: "test-token", http: srv.Client()}, []string{
			"-bucket", "posters",
			"-dir", dir,
			"-prefix", "uploads",
			"-asset-type", "document",
			"-cache-control", "public, max-age=60",
			"-concurrency", "2",
		})
	})
	if uploadErr != nil {
		t.Fatal(uploadErr)
	}
	var report bucketDirUploadReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	if report.Total != 3 || report.Succeeded != 3 || report.Failed != 0 || report.Concurrency != 2 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if report.Summary != "3 total, 3 succeeded, 0 skipped, 0 failed" {
		t.Fatalf("summary = %q", report.Summary)
	}
	if len(report.NextCommands) != 1 || !strings.Contains(report.NextCommands[0], "cdn-doctor -bucket posters -path uploads/") {
		t.Fatalf("next_commands = %#v", report.NextCommands)
	}
	for _, want := range []string{"uploads/nested/three.txt", "uploads/nested/two.txt", "uploads/one.txt"} {
		if seen[want] == "" {
			t.Fatalf("missing upload path %q in %#v", want, seen)
		}
	}
	if maxSeen < 2 || maxSeen > 2 {
		t.Fatalf("max concurrent uploads = %d, want 2", maxSeen)
	}
}

func TestUploadBucketDirReportsFailuresAfterCompletingBatch(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "ok.txt"), "ok")
	writeTestFile(t, filepath.Join(dir, "bad.txt"), "bad")
	reportFile := filepath.Join(t.TempDir(), "failed-report.json")
	var (
		mu    sync.Mutex
		calls int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		calls++
		mu.Unlock()
		if r.FormValue("path") == "bad.txt" {
			http.Error(w, "boom", http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"bucket":"posters"}`))
	}))
	defer srv.Close()

	var uploadErr error
	out := captureStdout(t, func() {
		uploadErr = uploadBucketDir(client{baseURL: srv.URL, token: "test-token", http: srv.Client()}, []string{
			"-bucket", "posters",
			"-dir", dir,
			"-concurrency", "2",
			"-report-file", reportFile,
		})
	})
	if uploadErr == nil || !strings.Contains(uploadErr.Error(), "1 of 2 files failed") {
		t.Fatalf("expected batch failure, got %v", uploadErr)
	}
	mu.Lock()
	if calls != 2 {
		mu.Unlock()
		t.Fatalf("calls = %d, want 2", calls)
	}
	mu.Unlock()
	var report bucketDirUploadReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	if report.Succeeded != 1 || report.Failed != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if report.Summary != "2 total, 1 succeeded, 0 skipped, 1 failed" || report.ReportSavedTo != reportFile {
		t.Fatalf("unexpected report summary/path: %+v", report)
	}
	if len(report.NextCommands) != 2 || !strings.Contains(strings.Join(report.NextCommands, "\n"), "upload-bucket-dir -bucket posters") {
		t.Fatalf("next_commands = %#v", report.NextCommands)
	}
	raw, err := os.ReadFile(reportFile)
	if err != nil {
		t.Fatal(err)
	}
	var written bucketDirUploadReport
	if err := json.Unmarshal(raw, &written); err != nil {
		t.Fatal(err)
	}
	if written.Succeeded != 1 || written.Failed != 1 {
		t.Fatalf("unexpected written failure report: %+v", written)
	}
}

func TestUploadBucketDirDryRunWritesPlanReport(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "one.txt"), "one")
	writeTestFile(t, filepath.Join(dir, "nested", "two.txt"), "two")
	reportFile := filepath.Join(t.TempDir(), "reports", "upload.json")

	var uploadErr error
	out := captureStdout(t, func() {
		uploadErr = uploadBucketDir(client{baseURL: "http://127.0.0.1:1", token: "test-token", http: http.DefaultClient}, []string{
			"-bucket", "posters",
			"-dir", dir,
			"-prefix", "uploads",
			"-dry-run",
			"-report-file", reportFile,
		})
	})
	if uploadErr != nil {
		t.Fatal(uploadErr)
	}
	var report bucketDirUploadReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	if !report.DryRun || report.Total != 2 || report.Planned != 2 || report.Succeeded != 0 || report.Failed != 0 {
		t.Fatalf("unexpected dry-run report: %+v", report)
	}
	if report.Summary != "2 total, 2 planned, 0 failed" || report.ReportSavedTo != reportFile {
		t.Fatalf("unexpected dry-run summary/path: %+v", report)
	}
	if len(report.NextCommands) != 1 || !strings.Contains(report.NextCommands[0], "upload-bucket-dir -bucket posters") {
		t.Fatalf("next_commands = %#v", report.NextCommands)
	}
	for _, result := range report.Results {
		if result.Status != "planned" || !strings.HasPrefix(result.LogicalPath, "uploads/") || result.Size == 0 {
			t.Fatalf("unexpected planned result: %+v", result)
		}
	}
	raw, err := os.ReadFile(reportFile)
	if err != nil {
		t.Fatal(err)
	}
	var written bucketDirUploadReport
	if err := json.Unmarshal(raw, &written); err != nil {
		t.Fatal(err)
	}
	if written.Total != report.Total || written.Planned != report.Planned || !written.DryRun {
		t.Fatalf("unexpected written report: %+v", written)
	}
}

func TestUploadBucketDirRetriesFailedFile(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "flaky.txt"), "flaky")
	var (
		mu    sync.Mutex
		calls int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		if r.FormValue("path") != "flaky.txt" {
			t.Fatalf("path = %q", r.FormValue("path"))
		}
		mu.Lock()
		calls++
		current := calls
		mu.Unlock()
		if current == 1 {
			http.Error(w, "temporary failure", http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"bucket":"posters"}`))
	}))
	defer srv.Close()

	var uploadErr error
	out := captureStdout(t, func() {
		uploadErr = uploadBucketDir(client{baseURL: srv.URL, token: "test-token", http: srv.Client()}, []string{
			"-bucket", "posters",
			"-dir", dir,
			"-retry", "1",
		})
	})
	if uploadErr != nil {
		t.Fatal(uploadErr)
	}
	mu.Lock()
	if calls != 2 {
		mu.Unlock()
		t.Fatalf("calls = %d, want 2", calls)
	}
	mu.Unlock()
	var report bucketDirUploadReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	if report.Retry != 1 || report.Succeeded != 1 || report.Failed != 0 || len(report.Results) != 1 || report.Results[0].Attempts != 2 {
		t.Fatalf("unexpected retry report: %+v", report)
	}
}

func TestUploadBucketDirSkipExistingOnlyUploadsMissingFiles(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "exists.txt"), "exists")
	writeTestFile(t, filepath.Join(dir, "new.txt"), "new")
	var (
		mu          sync.Mutex
		uploaded    []string
		listQueries []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/asset-buckets/posters/objects":
			prefix := r.URL.Query().Get("prefix")
			mu.Lock()
			listQueries = append(listQueries, prefix)
			mu.Unlock()
			if prefix == "exists.txt" {
				_, _ = w.Write([]byte(`{"objects":[{"logical_path":"exists.txt"}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"objects":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/asset-buckets/posters/objects":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			uploaded = append(uploaded, r.FormValue("path"))
			mu.Unlock()
			_, _ = w.Write([]byte(`{"bucket":"posters"}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()

	var uploadErr error
	out := captureStdout(t, func() {
		uploadErr = uploadBucketDir(client{baseURL: srv.URL, token: "test-token", http: srv.Client()}, []string{
			"-bucket", "posters",
			"-dir", dir,
			"-skip-existing",
		})
	})
	if uploadErr != nil {
		t.Fatal(uploadErr)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(listQueries) != 2 {
		t.Fatalf("list queries = %#v, want two exact probes", listQueries)
	}
	if len(uploaded) != 1 || uploaded[0] != "new.txt" {
		t.Fatalf("uploaded = %#v", uploaded)
	}
	var report bucketDirUploadReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	if !report.SkipExisting || report.Succeeded != 1 || report.Skipped != 1 || report.Failed != 0 {
		t.Fatalf("unexpected skip-existing report: %+v", report)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	fn()
	if err := w.Close(); err != nil {
		os.Stdout = old
		t.Fatal(err)
	}
	os.Stdout = old
	raw, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	return string(raw)
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

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
