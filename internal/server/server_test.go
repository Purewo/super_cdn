package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"supercdn/internal/config"
	"supercdn/internal/model"
	"supercdn/internal/storage"
)

func TestAssetUploadAndServe(t *testing.T) {
	app := newTestServer(t)
	reqBody, ctype := multipartBody(t, map[string]string{
		"project_id":    "assets",
		"path":          "/docs/readme.txt",
		"route_profile": "overseas",
	}, "file", "readme.txt", []byte("hello cdn"))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/assets", reqBody)
	req.Header.Set("Content-Type", ctype)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload status = %d body=%s", rec.Code, rec.Body.String())
	}

	get := httptest.NewRequest(http.MethodGet, "/o/assets/docs/readme.txt", nil)
	out := httptest.NewRecorder()
	app.ServeHTTP(out, get)
	if out.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s", out.Code, out.Body.String())
	}
	if out.Body.String() != "hello cdn" {
		t.Fatalf("body = %q", out.Body.String())
	}
	if out.Header().Get("ETag") == "" {
		t.Fatal("missing ETag")
	}
}

func TestDeploySiteAndServeIndexAnd404(t *testing.T) {
	app := newTestServer(t)
	create := map[string]any{
		"id":            "demo",
		"name":          "Demo Site",
		"route_profile": "overseas",
		"mode":          "standard",
		"domains":       []string{"demo.local"},
	}
	raw, _ := json.Marshal(create)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sites", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create site status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"name":"Demo Site"`) {
		t.Fatalf("create site response missing name: %s", rec.Body.String())
	}

	createDeployment(t, app, "demo", map[string]string{
		"index.html": "home",
		"404.html":   "missing",
	}, map[string]string{"route_profile": "overseas", "environment": "production", "promote": "true"})

	get := httptest.NewRequest(http.MethodGet, "/", nil)
	get.Host = "demo.local"
	out := httptest.NewRecorder()
	app.ServeHTTP(out, get)
	if out.Code != http.StatusOK || out.Body.String() != "home" {
		t.Fatalf("index status=%d body=%q", out.Code, out.Body.String())
	}

	missing := httptest.NewRequest(http.MethodGet, "/nope", nil)
	missing.Host = "demo.local"
	missingOut := httptest.NewRecorder()
	app.ServeHTTP(missingOut, missing)
	if missingOut.Code != http.StatusNotFound || missingOut.Body.String() != "missing" {
		t.Fatalf("404 status=%d body=%q", missingOut.Code, missingOut.Body.String())
	}
}

func TestSiteDeploymentTargetComesFromRouteProfileAndManifest(t *testing.T) {
	app := newTestServer(t)
	app.cfg.RouteProfiles[0].DeploymentTarget = model.SiteDeploymentTargetCloudflareStatic
	raw, _ := json.Marshal(map[string]any{
		"id":            "demo",
		"route_profile": "overseas",
		"mode":          "spa",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sites", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create site status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"deployment_target":"cloudflare_static"`) {
		t.Fatalf("create site response missing deployment target: %s", rec.Body.String())
	}

	deploymentID := createDeployment(t, app, "demo", map[string]string{"index.html": "home"}, map[string]string{"environment": "production", "promote": "true"})
	depReq := httptest.NewRequest(http.MethodGet, "/api/v1/sites/demo/deployments/"+deploymentID, nil)
	depReq.Header.Set("Authorization", "Bearer test-token")
	depRec := httptest.NewRecorder()
	app.ServeHTTP(depRec, depReq)
	if depRec.Code != http.StatusOK {
		t.Fatalf("deployment status = %d body=%s", depRec.Code, depRec.Body.String())
	}
	if !strings.Contains(depRec.Body.String(), `"deployment_target":"cloudflare_static"`) {
		t.Fatalf("deployment response missing deployment target: %s", depRec.Body.String())
	}

	manifestReq := httptest.NewRequest(http.MethodGet, "/api/v1/sites/demo/deployments/"+deploymentID+"/edge-manifest", nil)
	manifestReq.Header.Set("Authorization", "Bearer test-token")
	manifestRec := httptest.NewRecorder()
	app.ServeHTTP(manifestRec, manifestReq)
	if manifestRec.Code != http.StatusOK {
		t.Fatalf("edge manifest status = %d body=%s", manifestRec.Code, manifestRec.Body.String())
	}
	if !strings.Contains(manifestRec.Body.String(), `"deployment_target":"cloudflare_static"`) {
		t.Fatalf("edge manifest missing deployment target: %s", manifestRec.Body.String())
	}
}

func TestResolveSiteDeploymentTargetUsesRouteProfileAndSuggestsDomain(t *testing.T) {
	app := newTestServer(t)
	app.cfg.RouteProfiles[0].DeploymentTarget = model.SiteDeploymentTargetCloudflareStatic
	app.cfg.Cloudflare.RootDomain = "example.com"
	app.cfg.Cloudflare.SiteDomainSuffix = "sites.example.com"

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sites/demo/deployment-target?route_profile=overseas", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve target status = %d body=%s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{
		`"site_exists":false`,
		`"route_profile":"overseas"`,
		`"deployment_target":"cloudflare_static"`,
		`"source":"route_profile"`,
		`"default_domain":"demo.example.com"`,
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("resolve response missing %s: %s", want, rec.Body.String())
		}
	}
}

func TestResolveSiteDeploymentTargetUsesExistingSiteDomains(t *testing.T) {
	app := newTestServer(t)
	app.cfg.RouteProfiles[0].DeploymentTarget = model.SiteDeploymentTargetCloudflareStatic
	_, err := app.db.CreateSite(context.Background(), "demo", "Demo", "spa", "overseas", model.SiteDeploymentTargetCloudflareStatic, []string{"demo.example.com"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sites/demo/deployment-target", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve target status = %d body=%s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{
		`"site_exists":true`,
		`"deployment_target":"cloudflare_static"`,
		`"source":"site"`,
		`"domains":["demo.example.com"]`,
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("resolve response missing %s: %s", want, rec.Body.String())
		}
	}
}

func TestTeamInviteLoginAndRolePermissions(t *testing.T) {
	app := newTestServer(t)
	inviteToken := createInviteForTest(t, app, "alice", model.RoleMaintainer)
	apiToken := acceptInviteForTest(t, app, inviteToken)

	me := apiJSON(t, app, http.MethodGet, "/api/v1/auth/me", apiToken, nil)
	if me.Code != http.StatusOK {
		t.Fatalf("whoami status = %d body=%s", me.Code, me.Body.String())
	}
	if !strings.Contains(me.Body.String(), `"user_name":"alice"`) || !strings.Contains(me.Body.String(), `"role":"maintainer"`) {
		t.Fatalf("unexpected whoami response: %s", me.Body.String())
	}

	create := apiJSON(t, app, http.MethodPost, "/api/v1/sites", apiToken, map[string]any{
		"id":            "team-site",
		"route_profile": "overseas",
		"mode":          "standard",
	})
	if create.Code != http.StatusOK {
		t.Fatalf("maintainer create site status = %d body=%s", create.Code, create.Body.String())
	}

	invite := apiJSON(t, app, http.MethodPost, "/api/v1/auth/invites", apiToken, map[string]any{"name": "bob", "role": "viewer"})
	if invite.Code != http.StatusForbidden {
		t.Fatalf("maintainer invite status = %d body=%s", invite.Code, invite.Body.String())
	}

	cf := apiJSON(t, app, http.MethodGet, "/api/v1/cloudflare/status", apiToken, nil)
	if cf.Code != http.StatusForbidden {
		t.Fatalf("user cloudflare status = %d body=%s", cf.Code, cf.Body.String())
	}

	if _, err := app.db.SQL().ExecContext(context.Background(), `INSERT INTO workspaces(id, name, created_at) VALUES('other', 'Other', '2026-04-30T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := app.db.CreateSiteInWorkspace(context.Background(), "other", "private-site", "", "standard", "overseas", "", nil); err != nil {
		t.Fatal(err)
	}
	hidden := apiJSON(t, app, http.MethodGet, "/api/v1/sites/private-site/deployment-target", apiToken, nil)
	if hidden.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace site status = %d body=%s", hidden.Code, hidden.Body.String())
	}
}

func TestViewerCannotMutateTeamResources(t *testing.T) {
	app := newTestServer(t)
	inviteToken := createInviteForTest(t, app, "viewer", model.RoleViewer)
	apiToken := acceptInviteForTest(t, app, inviteToken)

	read := apiJSON(t, app, http.MethodGet, "/api/v1/auth/me", apiToken, nil)
	if read.Code != http.StatusOK {
		t.Fatalf("viewer whoami status = %d body=%s", read.Code, read.Body.String())
	}
	create := apiJSON(t, app, http.MethodPost, "/api/v1/sites", apiToken, map[string]any{
		"id":            "viewer-site",
		"route_profile": "overseas",
		"mode":          "standard",
	})
	if create.Code != http.StatusForbidden {
		t.Fatalf("viewer create site status = %d body=%s", create.Code, create.Body.String())
	}
}

func TestRecordCloudflareStaticDeployment(t *testing.T) {
	app := newTestServer(t)
	raw, _ := json.Marshal(map[string]any{
		"environment":         "production",
		"route_profile":       "overseas",
		"deployment_target":   "cloudflare_static",
		"mode":                "standard",
		"worker_name":         "supercdn-demo-static",
		"version_id":          "ver-123",
		"domains":             []string{"demo-static.example.com"},
		"compatibility_date":  "2026-04-29",
		"cache_policy":        "auto",
		"headers_generated":   true,
		"not_found_handling":  "single-page-application",
		"verification_status": "ok",
		"verified_at_utc":     "2026-04-29T00:00:01Z",
		"file_count":          2,
		"total_size":          1200,
		"published_at_utc":    "2026-04-29T00:00:00Z",
		"promote":             true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sites/demo/cloudflare-static/deployments", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("record static deployment status = %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`"deployment_target":"cloudflare_static"`,
		`"status":"active"`,
		`"production_url":"https://demo-static.example.com/"`,
		`"worker_name":"supercdn-demo-static"`,
		`"version_id":"ver-123"`,
		`"cache_policy":"auto"`,
		`"headers_generated":true`,
		`"not_found_handling":"single-page-application"`,
		`"verification_status":"ok"`,
		`"delivery_summary":{"cloudflare_static":2}`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("response missing %s: %s", want, body)
		}
	}
	site, err := app.db.GetSite(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if !contains(site.Domains, "demo-static.example.com") {
		t.Fatalf("site domains = %+v", site.Domains)
	}
}

func TestPromoteCloudflareStaticDeploymentRejectsMetadataOnlyRollback(t *testing.T) {
	app := newTestServer(t)
	activeID := recordCloudflareStaticDeploymentForTest(t, app, "demo", true)
	readyID := recordCloudflareStaticDeploymentForTest(t, app, "demo", false)
	if activeID == readyID {
		t.Fatal("expected distinct deployment ids")
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/sites/demo/deployments/"+readyID+"/promote", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("promote status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "metadata alone") {
		t.Fatalf("promote response missing safety message: %s", rec.Body.String())
	}

	dep, err := app.db.GetSiteDeployment(context.Background(), activeID)
	if err != nil {
		t.Fatal(err)
	}
	if !dep.Active {
		t.Fatalf("active deployment was changed: %+v", dep)
	}
}

func TestDeleteCloudflareStaticDeploymentWarnsMetadataOnly(t *testing.T) {
	app := newTestServer(t)
	deploymentID := recordCloudflareStaticDeploymentForTest(t, app, "demo", false)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/sites/demo/deployments/"+deploymentID, nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"deleted":true`) || !strings.Contains(rec.Body.String(), "metadata only") {
		t.Fatalf("delete response missing warning: %s", rec.Body.String())
	}
}

func TestCreateSiteAllocatesDefaultDomainAndPreventsSteal(t *testing.T) {
	app := newTestServer(t)
	app.cfg.Server.PublicBaseURL = "https://origin.example.com"
	app.cfg.Cloudflare.RootDomain = "example.com"
	app.cfg.Cloudflare.SiteDomainSuffix = "sites.example.com"

	create := map[string]any{
		"id":            "demo_site",
		"name":          "Demo Site",
		"route_profile": "overseas",
		"mode":          "standard",
	}
	raw, _ := json.Marshal(create)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sites", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create site status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"demo-site.sites.example.com"`) {
		t.Fatalf("create site response missing allocated domain: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"url":"https://demo-site.sites.example.com/"`) {
		t.Fatalf("create site response missing public URL: %s", rec.Body.String())
	}
	if _, err := app.db.SiteByHost(context.Background(), "demo-site.sites.example.com"); err != nil {
		t.Fatalf("allocated domain lookup failed: %v", err)
	}

	deploymentID := createDeployment(t, app, "demo_site", map[string]string{"index.html": "home"}, map[string]string{"environment": "production", "promote": "true"})
	get := httptest.NewRequest(http.MethodGet, "/", nil)
	get.Host = "demo-site.sites.example.com"
	out := httptest.NewRecorder()
	app.ServeHTTP(out, get)
	if out.Code != http.StatusOK || out.Body.String() != "home" {
		t.Fatalf("allocated domain status=%d body=%q", out.Code, out.Body.String())
	}
	depReq := httptest.NewRequest(http.MethodGet, "/api/v1/sites/demo_site/deployments/"+deploymentID, nil)
	depReq.Header.Set("Authorization", "Bearer test-token")
	depRec := httptest.NewRecorder()
	app.ServeHTTP(depRec, depReq)
	if depRec.Code != http.StatusOK {
		t.Fatalf("deployment status code=%d body=%s", depRec.Code, depRec.Body.String())
	}
	if !strings.Contains(depRec.Body.String(), `"production_url":"https://demo-site.sites.example.com/"`) {
		t.Fatalf("deployment response missing production URL: %s", depRec.Body.String())
	}
	if !strings.Contains(depRec.Body.String(), `"preview_url":"https://origin.example.com/p/demo_site/`+deploymentID+`/"`) {
		t.Fatalf("deployment response missing absolute preview URL: %s", depRec.Body.String())
	}

	conflict := map[string]any{
		"id":            "other",
		"route_profile": "overseas",
		"mode":          "standard",
		"domains":       []string{"demo-site.sites.example.com"},
	}
	raw, _ = json.Marshal(conflict)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/sites", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestBindDomainAndDomainStatus(t *testing.T) {
	app := newTestServer(t)
	app.cfg.Cloudflare.RootDomain = "example.com"
	app.cfg.Cloudflare.SiteDomainSuffix = "sites.example.com"

	create := map[string]any{
		"id":                  "demo",
		"route_profile":       "overseas",
		"mode":                "standard",
		"skip_default_domain": true,
	}
	raw, _ := json.Marshal(create)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sites", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create site status = %d body=%s", rec.Code, rec.Body.String())
	}

	bind := map[string]any{
		"domains":             []string{"custom.example.com"},
		"skip_default_domain": true,
		"append":              true,
	}
	raw, _ = json.Marshal(bind)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/sites/demo/domains", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bind domain status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"custom.example.com"`) {
		t.Fatalf("bind response missing custom domain: %s", rec.Body.String())
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/api/v1/domains/custom.example.com/status", nil)
	statusReq.Header.Set("Authorization", "Bearer test-token")
	statusRec := httptest.NewRecorder()
	app.ServeHTTP(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("domain status code = %d body=%s", statusRec.Code, statusRec.Body.String())
	}
	var status domainStatusResponse
	if err := json.Unmarshal(statusRec.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.Bound || status.SiteID != "demo" || status.CloudflareConfigured {
		t.Fatalf("unexpected domain status: %+v", status)
	}
}

func TestSyncWorkerRoutesDryRunPlansBoundDomains(t *testing.T) {
	app := newTestServer(t)
	app.cfg.Cloudflare.RootDomain = "example.com"
	app.cfg.Cloudflare.SiteDomainSuffix = "sites.example.com"
	app.cfg.Cloudflare.WorkerScript = "supercdn-edge"

	create := map[string]any{
		"id":                  "demo",
		"route_profile":       "overseas",
		"mode":                "standard",
		"domains":             []string{"demo.sites.example.com"},
		"skip_default_domain": true,
	}
	raw, _ := json.Marshal(create)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sites", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create site status = %d body=%s", rec.Code, rec.Body.String())
	}

	raw, _ = json.Marshal(map[string]any{"dry_run": true})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/sites/demo/worker-routes", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("sync worker routes status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp syncWorkerRoutesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != "planned" || resp.Script != "supercdn-edge" || len(resp.Routes) != 1 || resp.Routes[0].Pattern != "demo.sites.example.com/*" {
		t.Fatalf("unexpected route plan: %+v", resp)
	}
}

func TestSyncSiteDNSDryRunPlansProxiedCNAME(t *testing.T) {
	app := newTestServer(t)
	app.cfg.Cloudflare.RootDomain = "example.com"
	app.cfg.Cloudflare.SiteDomainSuffix = "sites.example.com"
	app.cfg.Cloudflare.SiteDNSTarget = "origin.example.com"

	create := map[string]any{
		"id":                  "demo",
		"route_profile":       "overseas",
		"mode":                "standard",
		"domains":             []string{"demo.sites.example.com"},
		"skip_default_domain": true,
	}
	raw, _ := json.Marshal(create)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sites", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create site status = %d body=%s", rec.Code, rec.Body.String())
	}

	raw, _ = json.Marshal(map[string]any{"dry_run": true})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/sites/demo/dns", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("sync site dns status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp syncSiteDNSResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != "planned" || resp.Type != "CNAME" || resp.Target != "origin.example.com" || !resp.Proxied || len(resp.Records) != 1 {
		t.Fatalf("unexpected dns plan: %+v", resp)
	}
	record := resp.Records[0]
	if record.Name != "demo.sites.example.com" || record.Type != "CNAME" || record.Content != "origin.example.com" || record.Action != "create" || !record.Proxied {
		t.Fatalf("unexpected dns record plan: %+v", record)
	}
}

func TestSyncSiteDNSSelectsCloudflareAccountByDomain(t *testing.T) {
	app := newTestServer(t)
	app.cfg.CloudflareAccounts = []config.CloudflareAccountConfig{
		{
			Name:             "cf_primary",
			Default:          true,
			RootDomain:       "primary.example.com",
			SiteDomainSuffix: "sites.primary.example.com",
			SiteDNSTarget:    "origin-primary.example.com",
		},
		{
			Name:             "cf_backup",
			RootDomain:       "backup.example.com",
			SiteDomainSuffix: "sites.backup.example.com",
			SiteDNSTarget:    "origin-backup.example.com",
		},
	}
	app.cfg.CloudflareLibraries = []config.CloudflareLibraryConfig{{
		Name: "overseas_accel",
		Bindings: []config.CloudflareLibraryBinding{
			{Name: "primary", Account: "cf_primary"},
			{Name: "backup", Account: "cf_backup"},
		},
	}}
	create := map[string]any{
		"id":                  "demo",
		"route_profile":       "overseas",
		"mode":                "standard",
		"domains":             []string{"demo.sites.backup.example.com"},
		"skip_default_domain": true,
	}
	raw, _ := json.Marshal(create)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sites", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create site status = %d body=%s", rec.Code, rec.Body.String())
	}

	raw, _ = json.Marshal(map[string]any{"dry_run": true, "cloudflare_library": "overseas_accel"})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/sites/demo/dns", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("sync site dns status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp syncSiteDNSResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.CloudflareAccount != "cf_backup" || resp.CloudflareLibrary != "overseas_accel" || resp.Target != "origin-backup.example.com" {
		t.Fatalf("unexpected cloudflare account selection: %+v", resp)
	}
}

func TestCloudflareR2ProvisionDefaultsUseLibraryAndRootDomain(t *testing.T) {
	app := newTestServer(t)
	target := cloudflareR2SyncTarget{
		Account: config.CloudflareAccountConfig{
			Name:       "cf_business_main",
			RootDomain: "qwk.ccwu.cc",
		},
		Library: "overseas_accel",
	}
	bucket := app.cloudflareR2ProvisionBucket(provisionCloudflareR2Request{}, target, false)
	publicBaseURL, warnings := app.cloudflareR2ProvisionPublicBaseURL(provisionCloudflareR2Request{}, target, false)
	if bucket != "supercdn-overseas-accel" {
		t.Fatalf("bucket = %q", bucket)
	}
	if publicBaseURL != "https://overseas-accel.r2.qwk.ccwu.cc" || len(warnings) != 0 {
		t.Fatalf("publicBaseURL=%q warnings=%v", publicBaseURL, warnings)
	}
}

func TestPurgeSiteCacheDryRunBuildsDeploymentURLs(t *testing.T) {
	app := newTestServer(t)
	app.cfg.Server.PublicBaseURL = "https://origin.example.com"
	app.cfg.Cloudflare.RootDomain = "example.com"
	app.cfg.Cloudflare.SiteDomainSuffix = "sites.example.com"

	create := map[string]any{
		"id":                  "demo",
		"route_profile":       "overseas",
		"mode":                "standard",
		"domains":             []string{"demo.sites.example.com"},
		"skip_default_domain": true,
	}
	raw, _ := json.Marshal(create)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sites", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create site status = %d body=%s", rec.Code, rec.Body.String())
	}
	createDeployment(t, app, "demo", map[string]string{
		"index.html":      "home",
		"assets/app.js":   "console.log('ok')",
		"docs/index.html": "docs",
	}, map[string]string{"environment": "production", "promote": "true"})

	raw, _ = json.Marshal(map[string]any{"dry_run": true})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/sites/demo/purge", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("purge site status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp purgeSiteCacheResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"https://demo.sites.example.com/",
		"https://demo.sites.example.com/index.html",
		"https://demo.sites.example.com/assets/app.js",
		"https://demo.sites.example.com/docs/",
		"https://demo.sites.example.com/docs/index.html",
	} {
		if !contains(resp.URLs, want) {
			t.Fatalf("purge urls missing %q in %+v", want, resp.URLs)
		}
	}
	if resp.Status != "planned" || resp.URLCount != len(resp.URLs) {
		t.Fatalf("unexpected purge response: %+v", resp)
	}
}

func TestSiteDeploymentsPreviewPromoteRollbackAndVerbatimLayout(t *testing.T) {
	app := newTestServer(t)
	create := map[string]any{
		"id":            "demo",
		"route_profile": "overseas",
		"mode":          "standard",
		"domains":       []string{"demo.local"},
	}
	raw, _ := json.Marshal(create)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sites", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create site status = %d body=%s", rec.Code, rec.Body.String())
	}

	previewID := createDeployment(t, app, "demo", map[string]string{
		"index.html":         "preview home",
		"assets/app.js":      "console.log('shared')",
		"supercdn.site.json": `{"headers":[{"path":"/assets/*","headers":{"X-Test-Asset":"yes"}}]}`,
	}, map[string]string{"environment": "preview"})
	preview := httptest.NewRequest(http.MethodGet, "/p/demo/"+previewID+"/", nil)
	previewRec := httptest.NewRecorder()
	app.ServeHTTP(previewRec, preview)
	if previewRec.Code != http.StatusOK || previewRec.Body.String() != "preview home" {
		t.Fatalf("preview status=%d body=%q", previewRec.Code, previewRec.Body.String())
	}
	if previewRec.Header().Get("X-Robots-Tag") != "noindex" {
		t.Fatalf("preview missing noindex header")
	}
	asset := httptest.NewRequest(http.MethodGet, "/p/demo/"+previewID+"/assets/app.js", nil)
	assetRec := httptest.NewRecorder()
	app.ServeHTTP(assetRec, asset)
	if assetRec.Header().Get("X-Test-Asset") != "yes" {
		t.Fatalf("asset header = %q", assetRec.Header().Get("X-Test-Asset"))
	}

	prod1 := createDeployment(t, app, "demo", map[string]string{
		"index.html":    "production one",
		"assets/app.js": "console.log('shared')",
	}, map[string]string{"environment": "production", "promote": "true"})
	get := httptest.NewRequest(http.MethodGet, "/", nil)
	get.Host = "demo.local"
	out := httptest.NewRecorder()
	app.ServeHTTP(out, get)
	if out.Code != http.StatusOK || out.Body.String() != "production one" {
		t.Fatalf("production one status=%d body=%q", out.Code, out.Body.String())
	}

	prod2 := createDeployment(t, app, "demo", map[string]string{
		"index.html":    "production two",
		"assets/app.js": "console.log('shared')",
	}, map[string]string{"environment": "production", "promote": "true"})
	out = httptest.NewRecorder()
	app.ServeHTTP(out, get)
	if out.Code != http.StatusOK || out.Body.String() != "production two" {
		t.Fatalf("production two status=%d body=%q", out.Code, out.Body.String())
	}

	promote := httptest.NewRequest(http.MethodPost, "/api/v1/sites/demo/deployments/"+prod1+"/promote", nil)
	promote.Header.Set("Authorization", "Bearer test-token")
	promoteRec := httptest.NewRecorder()
	app.ServeHTTP(promoteRec, promote)
	if promoteRec.Code != http.StatusOK {
		t.Fatalf("promote status = %d body=%s", promoteRec.Code, promoteRec.Body.String())
	}
	out = httptest.NewRecorder()
	app.ServeHTTP(out, get)
	if out.Code != http.StatusOK || out.Body.String() != "production one" {
		t.Fatalf("rollback status=%d body=%q", out.Code, out.Body.String())
	}
	if prod2 == "" {
		t.Fatal("empty second deployment id")
	}

	var appKey string
	if err := app.db.SQL().QueryRow(`
		SELECT o.key
		FROM site_deployment_files f
		JOIN objects o ON o.id = f.object_id
		WHERE f.deployment_id = ? AND f.path = ?`, prod1, "assets/app.js").Scan(&appKey); err != nil {
		t.Fatal(err)
	}
	wantKey := "sites/demo/deployments/" + prod1 + "/root/assets/app.js"
	if appKey != wantKey {
		t.Fatalf("app key = %q, want %q", appKey, wantKey)
	}
}

func TestSiteHTMLIsServedVerbatimAndRelativeAssetsRoute(t *testing.T) {
	app := newTestServer(t)
	create := map[string]any{"id": "demo", "route_profile": "overseas", "mode": "standard"}
	raw, _ := json.Marshal(create)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sites", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create site status = %d body=%s", rec.Code, rec.Body.String())
	}
	html := `<html><head><script src="app.js"></script><link href='style.css' rel='stylesheet'></head></html>`
	createDeployment(t, app, "demo", map[string]string{
		"index.html": html,
		"app.js":     "console.log('ok')",
		"style.css":  "body{}",
	}, map[string]string{"environment": "production", "promote": "true"})
	get := httptest.NewRequest(http.MethodGet, "/s/demo/", nil)
	out := httptest.NewRecorder()
	app.ServeHTTP(out, get)
	if out.Code != http.StatusOK {
		t.Fatalf("site status = %d body=%s", out.Code, out.Body.String())
	}
	if out.Body.String() != html {
		t.Fatalf("html was rewritten: %s", out.Body.String())
	}
	asset := httptest.NewRequest(http.MethodGet, "/s/demo/app.js", nil)
	assetOut := httptest.NewRecorder()
	app.ServeHTTP(assetOut, asset)
	if assetOut.Code != http.StatusOK || assetOut.Body.String() != "console.log('ok')" {
		t.Fatalf("asset status=%d body=%q", assetOut.Code, assetOut.Body.String())
	}
}

func TestSiteNonIndexFilesRedirectToStorage(t *testing.T) {
	app := newTestServer(t)
	create := map[string]any{
		"id":            "demo",
		"route_profile": "overseas",
		"mode":          "standard",
		"domains":       []string{"demo.local"},
	}
	raw, _ := json.Marshal(create)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sites", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create site status = %d body=%s", rec.Code, rec.Body.String())
	}

	deploymentID := createDeployment(t, app, "demo", map[string]string{
		"index.html":    "home",
		"404.html":      "missing",
		"assets/app.js": "console.log('ok')",
	}, map[string]string{"environment": "production", "promote": "true"})
	ctx := context.Background()
	assetObj, err := app.db.SiteDeploymentFileObject(ctx, deploymentID, "assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	assetLocator := "http://storage.example/assets/app.js?sign=fresh"
	if _, err := app.db.UpsertReplica(ctx, assetObj.ID, assetObj.PrimaryTarget, model.ReplicaReady, assetLocator, ""); err != nil {
		t.Fatal(err)
	}
	notFoundObj, err := app.db.SiteDeploymentFileObject(ctx, deploymentID, "404.html")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.db.UpsertReplica(ctx, notFoundObj.ID, notFoundObj.PrimaryTarget, model.ReplicaReady, "http://storage.example/404.html?sign=fresh", ""); err != nil {
		t.Fatal(err)
	}

	index := httptest.NewRequest(http.MethodGet, "/", nil)
	index.Host = "demo.local"
	indexOut := httptest.NewRecorder()
	app.ServeHTTP(indexOut, index)
	if indexOut.Code != http.StatusOK || indexOut.Body.String() != "home" {
		t.Fatalf("index status=%d body=%q", indexOut.Code, indexOut.Body.String())
	}
	if indexOut.Header().Get("Location") != "" {
		t.Fatalf("index redirected to %q", indexOut.Header().Get("Location"))
	}

	asset := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	asset.Host = "demo.local"
	assetOut := httptest.NewRecorder()
	app.ServeHTTP(assetOut, asset)
	if assetOut.Code != http.StatusFound {
		t.Fatalf("asset status=%d body=%s", assetOut.Code, assetOut.Body.String())
	}
	if assetOut.Header().Get("Location") != assetLocator {
		t.Fatalf("asset redirect location = %q", assetOut.Header().Get("Location"))
	}
	if assetOut.Header().Get("X-SuperCDN-Redirect") != "storage" {
		t.Fatalf("redirect marker = %q", assetOut.Header().Get("X-SuperCDN-Redirect"))
	}
	if assetOut.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("redirect cache-control = %q", assetOut.Header().Get("Cache-Control"))
	}

	ranged := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	ranged.Host = "demo.local"
	ranged.Header.Set("Range", "bytes=0-6")
	rangeOut := httptest.NewRecorder()
	app.ServeHTTP(rangeOut, ranged)
	if rangeOut.Code != http.StatusPartialContent || rangeOut.Header().Get("Location") != "" || rangeOut.Body.String() != "console" {
		t.Fatalf("range status=%d location=%q body=%q", rangeOut.Code, rangeOut.Header().Get("Location"), rangeOut.Body.String())
	}

	missing := httptest.NewRequest(http.MethodGet, "/missing", nil)
	missing.Host = "demo.local"
	missingOut := httptest.NewRecorder()
	app.ServeHTTP(missingOut, missing)
	if missingOut.Code != http.StatusNotFound || missingOut.Header().Get("Location") != "" || missingOut.Body.String() != "missing" {
		t.Fatalf("missing status=%d location=%q body=%q", missingOut.Code, missingOut.Header().Get("Location"), missingOut.Body.String())
	}
}

func TestExportEdgeManifestBuildsRoutesAndStorageRedirects(t *testing.T) {
	app := newTestServer(t)
	create := map[string]any{
		"id":            "demo",
		"route_profile": "overseas",
		"mode":          "spa",
		"domains":       []string{"demo.local"},
	}
	raw, _ := json.Marshal(create)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sites", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create site status = %d body=%s", rec.Code, rec.Body.String())
	}

	deploymentID := createDeployment(t, app, "demo", map[string]string{
		"index.html":         "home",
		"404.html":           "missing",
		"assets/app.js":      "console.log('ok')",
		"docs/index.html":    "docs",
		"supercdn.site.json": `{"headers":[{"path":"/assets/*","headers":{"X-Test-Asset":"yes"}}]}`,
	}, map[string]string{"environment": "production", "promote": "true"})
	ctx := context.Background()
	for filePath, locator := range map[string]string{
		"404.html":        "http://storage.example/404.html?sign=fresh",
		"assets/app.js":   "http://storage.example/assets/app.js?sign=fresh",
		"docs/index.html": "http://storage.example/docs/index.html?sign=fresh",
	} {
		obj, err := app.db.SiteDeploymentFileObject(ctx, deploymentID, filePath)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := app.db.UpsertReplica(ctx, obj.ID, obj.PrimaryTarget, model.ReplicaReady, locator, ""); err != nil {
			t.Fatal(err)
		}
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/sites/demo/deployments/"+deploymentID+"/edge-manifest", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("edge manifest status = %d body=%s", rec.Code, rec.Body.String())
	}
	var manifest edgeManifest
	if err := json.Unmarshal(rec.Body.Bytes(), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != 1 || manifest.Kind != "supercdn-edge-manifest" || manifest.Mode != "spa" || manifest.DeploymentID != deploymentID {
		t.Fatalf("unexpected manifest metadata: %+v", manifest)
	}
	root := manifest.Routes["/"]
	if root.Type != "origin" || root.File != "index.html" || root.Status != http.StatusOK || root.Location != "" {
		t.Fatalf("unexpected root route: %+v", root)
	}
	if got := manifest.Routes["/index.html"]; got.File != "index.html" || got.Type != "origin" {
		t.Fatalf("unexpected index route: %+v", got)
	}
	asset := manifest.Routes["/assets/app.js"]
	if asset.Type != "redirect" || asset.Delivery != "redirect" || asset.Status != http.StatusFound || asset.Location != "http://storage.example/assets/app.js?sign=fresh" {
		t.Fatalf("unexpected asset route: %+v", asset)
	}
	if asset.CacheControl != "no-store" || asset.Headers["X-Test-Asset"] != "yes" {
		t.Fatalf("unexpected asset response metadata: %+v", asset)
	}
	for _, routePath := range []string{"/docs", "/docs/", "/docs/index.html"} {
		route := manifest.Routes[routePath]
		if route.File != "docs/index.html" || route.Type != "redirect" || route.Location != "http://storage.example/docs/index.html?sign=fresh" {
			t.Fatalf("unexpected docs route %s: %+v", routePath, route)
		}
	}
	if manifest.Fallback == nil || manifest.Fallback.File != "index.html" || manifest.Fallback.Type != "origin" || manifest.Fallback.Status != http.StatusOK {
		t.Fatalf("unexpected fallback: %+v", manifest.Fallback)
	}
	if manifest.NotFound == nil || manifest.NotFound.File != "404.html" || manifest.NotFound.Type != "origin" || manifest.NotFound.Status != http.StatusNotFound {
		t.Fatalf("unexpected not_found: %+v", manifest.NotFound)
	}
	if len(manifest.Warnings) != 0 {
		t.Fatalf("unexpected manifest warnings: %+v", manifest.Warnings)
	}
}

func TestExportEdgeManifestHonorsOriginDeliveryRules(t *testing.T) {
	app := newTestServer(t)
	create := map[string]any{
		"id":            "demo",
		"route_profile": "overseas",
		"mode":          "standard",
	}
	raw, _ := json.Marshal(create)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sites", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create site status = %d body=%s", rec.Code, rec.Body.String())
	}

	deploymentID := createDeployment(t, app, "demo", map[string]string{
		"index.html":         "home",
		"assets/app.js":      "console.log('ok')",
		"supercdn.site.json": `{"delivery":[{"path":"/assets/*","mode":"origin"}]}`,
	}, map[string]string{"environment": "production", "promote": "true"})
	assetObj, err := app.db.SiteDeploymentFileObject(context.Background(), deploymentID, "assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.db.UpsertReplica(context.Background(), assetObj.ID, assetObj.PrimaryTarget, model.ReplicaReady, "http://storage.example/assets/app.js?sign=fresh", ""); err != nil {
		t.Fatal(err)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/sites/demo/deployments/"+deploymentID+"/edge-manifest", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("edge manifest status = %d body=%s", rec.Code, rec.Body.String())
	}
	var manifest edgeManifest
	if err := json.Unmarshal(rec.Body.Bytes(), &manifest); err != nil {
		t.Fatal(err)
	}
	asset := manifest.Routes["/assets/app.js"]
	if asset.Type != "origin" || asset.Delivery != "origin" || asset.Status != http.StatusOK || asset.Location != "" {
		t.Fatalf("unexpected asset route: %+v", asset)
	}
	if len(manifest.Warnings) != 0 {
		t.Fatalf("unexpected manifest warnings: %+v", manifest.Warnings)
	}
}

func TestPublishEdgeManifestDryRunPlansKVKeys(t *testing.T) {
	app := newTestServer(t)
	app.cfg.Cloudflare.RootDomain = "example.com"
	app.cfg.Cloudflare.SiteDomainSuffix = "sites.example.com"

	create := map[string]any{
		"id":                  "demo",
		"route_profile":       "overseas",
		"mode":                "standard",
		"domains":             []string{"demo.sites.example.com"},
		"skip_default_domain": true,
	}
	raw, _ := json.Marshal(create)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sites", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create site status = %d body=%s", rec.Code, rec.Body.String())
	}

	deploymentID := createDeployment(t, app, "demo", map[string]string{
		"index.html":    "home",
		"assets/app.js": "console.log('ok')",
	}, map[string]string{"environment": "production", "promote": "true"})

	raw, _ = json.Marshal(map[string]any{"dry_run": true})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/sites/demo/deployments/"+deploymentID+"/edge-manifest/publish", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("publish manifest status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp publishEdgeManifestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != "planned" || !resp.DryRun || resp.CloudflareAccount != "default" || resp.ManifestSize == 0 || resp.ManifestSHA256 == "" {
		t.Fatalf("unexpected publish response: %+v", resp)
	}
	for _, want := range []string{
		"sites/demo.sites.example.com/deployments/" + deploymentID + "/edge-manifest",
		"sites/demo.sites.example.com/active/edge-manifest",
	} {
		found := false
		for _, write := range resp.Writes {
			if write.Key == want && write.Action == "planned" && write.DryRun {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing planned write %q in %+v", want, resp.Writes)
		}
	}
	if len(resp.Warnings) == 0 {
		t.Fatalf("expected namespace warning: %+v", resp)
	}
}

func TestSiteDeliveryRuleCanKeepAssetsOnOrigin(t *testing.T) {
	app := newTestServer(t)
	create := map[string]any{
		"id":            "demo",
		"route_profile": "overseas",
		"mode":          "standard",
		"domains":       []string{"demo.local"},
	}
	raw, _ := json.Marshal(create)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sites", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create site status = %d body=%s", rec.Code, rec.Body.String())
	}

	deploymentID := createDeployment(t, app, "demo", map[string]string{
		"index.html":         "home",
		"assets/app.js":      "console.log('ok')",
		"supercdn.site.json": `{"delivery":[{"path":"/assets/*","mode":"origin"}]}`,
	}, map[string]string{"environment": "production", "promote": "true"})
	assetObj, err := app.db.SiteDeploymentFileObject(context.Background(), deploymentID, "assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.db.UpsertReplica(context.Background(), assetObj.ID, assetObj.PrimaryTarget, model.ReplicaReady, "http://storage.example/assets/app.js?sign=fresh", ""); err != nil {
		t.Fatal(err)
	}

	asset := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	asset.Host = "demo.local"
	assetOut := httptest.NewRecorder()
	app.ServeHTTP(assetOut, asset)
	if assetOut.Code != http.StatusOK || assetOut.Header().Get("Location") != "" || assetOut.Body.String() != "console.log('ok')" {
		t.Fatalf("asset status=%d location=%q body=%q", assetOut.Code, assetOut.Header().Get("Location"), assetOut.Body.String())
	}

	depReq := httptest.NewRequest(http.MethodGet, "/api/v1/sites/demo/deployments/"+deploymentID, nil)
	depReq.Header.Set("Authorization", "Bearer test-token")
	depRec := httptest.NewRecorder()
	app.ServeHTTP(depRec, depReq)
	if depRec.Code != http.StatusOK {
		t.Fatalf("deployment status = %d body=%s", depRec.Code, depRec.Body.String())
	}
	if !strings.Contains(depRec.Body.String(), `"delivery_summary":{"origin":2}`) {
		t.Fatalf("deployment response missing delivery summary: %s", depRec.Body.String())
	}
}

func TestEdgeBypassSecretKeepsRedirectFileOnOrigin(t *testing.T) {
	app := newTestServer(t)
	app.cfg.Cloudflare.EdgeBypassSecret = "edge-secret"
	create := map[string]any{
		"id":            "demo",
		"route_profile": "overseas",
		"mode":          "standard",
		"domains":       []string{"demo.local"},
	}
	raw, _ := json.Marshal(create)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sites", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create site status = %d body=%s", rec.Code, rec.Body.String())
	}

	deploymentID := createDeployment(t, app, "demo", map[string]string{
		"index.html":    "home",
		"assets/app.js": "console.log('ok')",
	}, map[string]string{"environment": "production", "promote": "true"})
	assetObj, err := app.db.SiteDeploymentFileObject(context.Background(), deploymentID, "assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.db.UpsertReplica(context.Background(), assetObj.ID, assetObj.PrimaryTarget, model.ReplicaReady, "http://storage.example/assets/app.js?sign=fresh", ""); err != nil {
		t.Fatal(err)
	}

	asset := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	asset.Host = "demo.local"
	asset.Header.Set("X-SuperCDN-Origin-Delivery", "origin")
	asset.Header.Set("X-SuperCDN-Edge-Secret", "edge-secret")
	assetOut := httptest.NewRecorder()
	app.ServeHTTP(assetOut, asset)
	if assetOut.Code != http.StatusOK || assetOut.Header().Get("Location") != "" || assetOut.Body.String() != "console.log('ok')" {
		t.Fatalf("asset status=%d location=%q body=%q", assetOut.Code, assetOut.Header().Get("Location"), assetOut.Body.String())
	}

	wrongSecret := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	wrongSecret.Host = "demo.local"
	wrongSecret.Header.Set("X-SuperCDN-Origin-Delivery", "origin")
	wrongSecret.Header.Set("X-SuperCDN-Edge-Secret", "wrong")
	wrongSecretOut := httptest.NewRecorder()
	app.ServeHTTP(wrongSecretOut, wrongSecret)
	if wrongSecretOut.Code != http.StatusFound {
		t.Fatalf("wrong secret status=%d body=%s", wrongSecretOut.Code, wrongSecretOut.Body.String())
	}
}

func TestSiteDeploymentIncludesInspectWarnings(t *testing.T) {
	app := newTestServer(t)
	create := map[string]any{"id": "demo", "route_profile": "overseas", "mode": "standard"}
	raw, _ := json.Marshal(create)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sites", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create site status = %d body=%s", rec.Code, rec.Body.String())
	}
	deploymentID := createDeployment(t, app, "demo", map[string]string{
		"index.html":    `<script type="module" src="/assets/app.js"></script>`,
		"assets/app.js": `import("./chunk.js");`,
	}, map[string]string{"environment": "production", "promote": "true"})

	depReq := httptest.NewRequest(http.MethodGet, "/api/v1/sites/demo/deployments/"+deploymentID, nil)
	depReq.Header.Set("Authorization", "Bearer test-token")
	depRec := httptest.NewRecorder()
	app.ServeHTTP(depRec, depReq)
	if depRec.Code != http.StatusOK {
		t.Fatalf("deployment status = %d body=%s", depRec.Code, depRec.Body.String())
	}
	body := depRec.Body.String()
	for _, want := range []string{`"inspect"`, `"module_script"`, `"dynamic_import"`, `"root_absolute_paths"`, `"delivery_summary":{"origin":1,"redirect":1}`} {
		if !strings.Contains(body, want) {
			t.Fatalf("deployment response missing %s: %s", want, body)
		}
	}
}

func TestObjectRedirectURLRefreshesSignedLocator(t *testing.T) {
	app := newTestServer(t)
	signed := "http://storage.example/app.js?sign=fresh:0"
	app.stores = storage.NewManager([]storage.Store{&signedLocatorStore{
		name:          "remote",
		statLocator:   signed,
		publicLocator: "http://storage.example/app.js",
	}})
	obj, err := app.db.SaveObject(context.Background(), model.Object{
		ProjectID:     "sites/demo",
		Path:          "app.js",
		Key:           "sites/demo/deployments/dpl-test/root/app.js",
		RouteProfile:  "overseas",
		Size:          1,
		SHA256:        "sha",
		ContentType:   "application/javascript",
		PrimaryTarget: "remote",
	})
	if err != nil {
		t.Fatal(err)
	}
	unsigned := "http://storage.example/app.js"
	stored := "resource-library://remote?locator=" + url.QueryEscape(unsigned)
	if _, err := app.db.UpsertReplica(context.Background(), obj.ID, "remote", model.ReplicaReady, stored, ""); err != nil {
		t.Fatal(err)
	}
	got, err := app.objectRedirectURL(context.Background(), obj)
	if err != nil {
		t.Fatal(err)
	}
	if got != signed {
		t.Fatalf("redirect URL = %q, want refreshed signed locator %q", got, signed)
	}
}

func TestPreflightUploadRejectsResourceLibraryLimit(t *testing.T) {
	app := newResourceLibraryTestServer(t, 4, 2)
	reqBody, _ := json.Marshal(map[string]any{
		"route_profile":     "limited",
		"total_size":        5,
		"largest_file_size": 5,
		"batch_file_count":  1,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/preflight/upload", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("preflight status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestPreflightSiteDeployRejectsBatchLimit(t *testing.T) {
	app := newResourceLibraryTestServer(t, 100, 2)
	reqBody, _ := json.Marshal(map[string]any{
		"site_id":           "demo",
		"route_profile":     "limited",
		"total_size":        3,
		"largest_file_size": 1,
		"batch_file_count":  3,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/preflight/site-deploy", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("preflight status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestPreflightSiteDeployRejectsDefaultFileLimit(t *testing.T) {
	app := newTestServer(t)
	reqBody, _ := json.Marshal(map[string]any{
		"site_id":           "demo",
		"route_profile":     "overseas",
		"total_size":        6,
		"largest_file_size": 1,
		"batch_file_count":  6,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/preflight/site-deploy", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("preflight status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestOverclockModeBypassesUploadAndPreflightLimits(t *testing.T) {
	app := newResourceLibraryTestServer(t, 4, 2)
	app.cfg.Limits.OverclockMode = true
	app.cfg.Limits.MaxUploadBytes = 4
	app.cfg.Limits.DefaultMaxSiteFiles = 2

	uploadBody, _ := json.Marshal(map[string]any{
		"route_profile":     "limited",
		"total_size":        5,
		"largest_file_size": 5,
		"batch_file_count":  3,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/preflight/upload", bytes.NewReader(uploadBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("overclock upload preflight status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"overclock_mode":true`) || !strings.Contains(rec.Body.String(), `"limits_ignored"`) {
		t.Fatalf("overclock response missing warning fields: %s", rec.Body.String())
	}

	siteBody, _ := json.Marshal(map[string]any{
		"site_id":           "demo",
		"route_profile":     "limited",
		"total_size":        5,
		"largest_file_size": 1,
		"batch_file_count":  3,
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/preflight/site-deploy", bytes.NewReader(siteBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("overclock site preflight status = %d body=%s", rec.Code, rec.Body.String())
	}
	if err := app.checkDeploymentFileCount(model.SiteEnvironmentPreview, defaultPreviewSiteFiles+1); err != nil {
		t.Fatalf("overclock deployment file count should pass: %v", err)
	}
}

func TestOverclockModeBypassesBucketLimits(t *testing.T) {
	app := newResourceLibraryTestServer(t, 4, 2)
	app.cfg.Limits.OverclockMode = true
	create := map[string]any{
		"slug":                "images",
		"name":                "Images",
		"route_profile":       "limited",
		"allowed_types":       []string{"image"},
		"max_capacity_bytes":  4,
		"max_file_size_bytes": 4,
	}
	raw, _ := json.Marshal(create)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/asset-buckets", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create bucket status = %d body=%s", rec.Code, rec.Body.String())
	}

	body, ctype := multipartBody(t, map[string]string{"path": "docs/readme.txt"}, "file", "readme.txt", []byte("hello"))
	upload := httptest.NewRequest(http.MethodPost, "/api/v1/asset-buckets/images/objects", body)
	upload.Header.Set("Content-Type", ctype)
	upload.Header.Set("Authorization", "Bearer test-token")
	uploadRec := httptest.NewRecorder()
	app.ServeHTTP(uploadRec, upload)
	if uploadRec.Code != http.StatusCreated {
		t.Fatalf("overclock bucket upload status = %d body=%s", uploadRec.Code, uploadRec.Body.String())
	}
	if !strings.Contains(uploadRec.Body.String(), `"overclock_mode":true`) {
		t.Fatalf("overclock upload response missing warning: %s", uploadRec.Body.String())
	}
}

func TestInitResourceLibrariesDryRun(t *testing.T) {
	app := newResourceLibraryTestServer(t, 100, 10)
	app.cfg.ResourceLibraries = []config.ResourceLibraryConfig{{Name: "limited_repo"}}
	reqBody, _ := json.Marshal(map[string]any{
		"libraries": []string{"limited_repo"},
		"dry_run":   true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/init/resource-libraries", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("init dry-run status = %d body=%s", rec.Code, rec.Body.String())
	}
	var result initResourceLibrariesResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.DryRun || len(result.Libraries) != 1 || result.Libraries[0].Target != "limited_repo" {
		t.Fatalf("unexpected init result: %+v", result)
	}
	if len(result.Libraries[0].Bindings) != 1 || len(result.Libraries[0].Bindings[0].Directories) == 0 {
		t.Fatalf("missing binding directory plan: %+v", result.Libraries[0])
	}
}

func TestResourceLibraryHealthCheckStoresLocalStatus(t *testing.T) {
	app := newResourceLibraryTestServer(t, 100, 10)
	app.cfg.ResourceLibraries = []config.ResourceLibraryConfig{{
		Name: "limited_repo",
		Bindings: []config.ResourceLibraryBinding{{
			Name:       "limited_binding",
			MountPoint: "test_mount",
			Path:       "/limited",
		}},
	}}
	reqBody, _ := json.Marshal(map[string]any{
		"libraries": []string{"limited_repo"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/resource-libraries/health-check", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("health-check status = %d body=%s", rec.Code, rec.Body.String())
	}
	statusReq := httptest.NewRequest(http.MethodGet, "/api/v1/resource-libraries/status?library=limited_repo", nil)
	statusReq.Header.Set("Authorization", "Bearer test-token")
	statusRec := httptest.NewRecorder()
	app.ServeHTTP(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("status code = %d body=%s", statusRec.Code, statusRec.Body.String())
	}
	var status resourceLibraryStatusResponse
	if err := json.Unmarshal(statusRec.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if len(status.Libraries) != 1 || len(status.Libraries[0].Bindings) != 1 {
		t.Fatalf("unexpected status response: %+v", status)
	}
	binding := status.Libraries[0].Bindings[0]
	if binding.Status != storage.HealthStatusOK || binding.Health == nil {
		t.Fatalf("unexpected binding status: %+v", binding)
	}
	if binding.Health.CheckMode != storage.HealthModePassive {
		t.Fatalf("check mode = %q", binding.Health.CheckMode)
	}
}

func TestPreflightRejectsRecentResourceLibraryHealthFailure(t *testing.T) {
	app := newResourceLibraryTestServer(t, 100, 10)
	app.cfg.ResourceLibraries = []config.ResourceLibraryConfig{{
		Name: "limited_repo",
		Bindings: []config.ResourceLibraryBinding{{
			Name:       "limited_binding",
			MountPoint: "test_mount",
			Path:       "/limited",
		}},
	}}
	if _, err := app.db.UpsertResourceLibraryHealth(context.Background(), model.ResourceLibraryHealth{
		Library:       "limited_repo",
		Binding:       "limited_binding",
		BindingPath:   "/limited",
		Target:        "local",
		TargetType:    "local",
		Status:        storage.HealthStatusFailed,
		CheckMode:     storage.HealthModePassive,
		LastError:     "probe failed",
		LastCheckedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	reqBody, _ := json.Marshal(map[string]any{
		"route_profile":     "limited",
		"total_size":        1,
		"largest_file_size": 1,
		"batch_file_count":  1,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/preflight/upload", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("preflight status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestResourceLibraryE2EProbeCleansUp(t *testing.T) {
	app := newTestServer(t)
	reqBody, _ := json.Marshal(map[string]any{
		"route_profile": "overseas",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/resource-libraries/e2e-probe", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("e2e probe status = %d body=%s", rec.Code, rec.Body.String())
	}
	var result resourceLibraryE2EProbeResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.HTTPStatus != http.StatusOK || result.CleanupRemote != "deleted" || result.CleanupDB != "deleted" {
		t.Fatalf("unexpected e2e result: %+v", result)
	}
	store, ok := app.stores.Get("local")
	if !ok {
		t.Fatal("missing local store")
	}
	if _, err := store.Stat(context.Background(), result.Key); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("probe file still exists or unexpected error: %v", err)
	}
	if _, err := app.db.GetObject(context.Background(), result.ObjectID); err == nil {
		t.Fatal("probe object record still exists")
	}
}

func TestAssetBucketUploadServeAndList(t *testing.T) {
	app := newTestServer(t)
	app.cfg.Server.PublicBaseURL = "https://cdn.example.com"
	create := map[string]any{
		"slug":          "markdown",
		"name":          "Markdown",
		"route_profile": "overseas",
		"allowed_types": []string{"document"},
	}
	raw, _ := json.Marshal(create)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/asset-buckets", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create bucket status = %d body=%s", rec.Code, rec.Body.String())
	}

	body, ctype := multipartBody(t, map[string]string{"path": "docs/readme.md"}, "file", "readme.md", []byte("# hello bucket"))
	upload := httptest.NewRequest(http.MethodPost, "/api/v1/asset-buckets/markdown/objects", body)
	upload.Header.Set("Content-Type", ctype)
	upload.Header.Set("Authorization", "Bearer test-token")
	uploadRec := httptest.NewRecorder()
	app.ServeHTTP(uploadRec, upload)
	if uploadRec.Code != http.StatusCreated {
		t.Fatalf("upload bucket object status = %d body=%s", uploadRec.Code, uploadRec.Body.String())
	}
	var uploadResult struct {
		BucketObject model.AssetBucketObject `json:"bucket_object"`
		URL          string                  `json:"url"`
		PublicURL    string                  `json:"public_url"`
		URLs         []string                `json:"urls"`
	}
	if err := json.Unmarshal(uploadRec.Body.Bytes(), &uploadResult); err != nil {
		t.Fatal(err)
	}
	if uploadResult.URL != "/a/markdown/docs/readme.md" {
		t.Fatalf("url = %q", uploadResult.URL)
	}
	if want := "https://cdn.example.com/a/markdown/docs/readme.md"; uploadResult.PublicURL != want {
		t.Fatalf("public_url = %q want %q", uploadResult.PublicURL, want)
	}
	if len(uploadResult.URLs) != 1 || uploadResult.URLs[0] != uploadResult.PublicURL {
		t.Fatalf("urls = %#v public_url=%q", uploadResult.URLs, uploadResult.PublicURL)
	}
	if uploadResult.BucketObject.AssetType != model.AssetTypeDocument {
		t.Fatalf("asset type = %q", uploadResult.BucketObject.AssetType)
	}
	if !strings.HasPrefix(uploadResult.BucketObject.PhysicalKey, "assets/buckets/markdown/documents/") {
		t.Fatalf("physical key = %q", uploadResult.BucketObject.PhysicalKey)
	}

	get := httptest.NewRequest(http.MethodGet, "/a/markdown/docs/readme.md", nil)
	out := httptest.NewRecorder()
	app.ServeHTTP(out, get)
	if out.Code != http.StatusOK || out.Body.String() != "# hello bucket" {
		t.Fatalf("bucket read status=%d body=%q", out.Code, out.Body.String())
	}

	list := httptest.NewRequest(http.MethodGet, "/api/v1/asset-buckets/markdown/objects", nil)
	list.Header.Set("Authorization", "Bearer test-token")
	listRec := httptest.NewRecorder()
	app.ServeHTTP(listRec, list)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", listRec.Code, listRec.Body.String())
	}
	if !strings.Contains(listRec.Body.String(), `"logical_path":"docs/readme.md"`) {
		t.Fatalf("list did not include uploaded object: %s", listRec.Body.String())
	}
}

func TestAssetBucketUploadReturnsCDNURL(t *testing.T) {
	app := newTestServer(t)
	app.cfg.Server.PublicBaseURL = "https://origin.example.com"
	app.cfg.RouteProfiles = []config.RouteProfile{{
		Name:                "overseas_r2",
		Primary:             "remote",
		DefaultCacheControl: "public, max-age=31536000, immutable",
		AllowRedirect:       true,
	}}
	app.stores = storage.NewManager([]storage.Store{&signedLocatorStore{
		name:          "remote",
		statLocator:   "https://r2.example.com/assets/poster.jpg?sig=fresh",
		publicLocator: "https://r2.example.com/assets/poster.jpg",
	}})
	raw, _ := json.Marshal(map[string]any{
		"slug":          "posters",
		"name":          "Posters",
		"route_profile": "overseas_r2",
		"allowed_types": []string{"image"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/asset-buckets", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create bucket status = %d body=%s", rec.Code, rec.Body.String())
	}

	body, ctype := multipartBody(t, map[string]string{"path": "images/poster.jpg"}, "file", "poster.jpg", []byte("jpg"))
	upload := httptest.NewRequest(http.MethodPost, "/api/v1/asset-buckets/posters/objects", body)
	upload.Header.Set("Content-Type", ctype)
	upload.Header.Set("Authorization", "Bearer test-token")
	uploadRec := httptest.NewRecorder()
	app.ServeHTTP(uploadRec, upload)
	if uploadRec.Code != http.StatusCreated {
		t.Fatalf("upload bucket object status = %d body=%s", uploadRec.Code, uploadRec.Body.String())
	}
	var result struct {
		PublicURL  string   `json:"public_url"`
		CDNURL     string   `json:"cdn_url"`
		StorageURL string   `json:"storage_url"`
		URLs       []string `json:"urls"`
	}
	if err := json.Unmarshal(uploadRec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.PublicURL != "https://origin.example.com/a/posters/images/poster.jpg" {
		t.Fatalf("public_url = %q", result.PublicURL)
	}
	if want := "https://r2.example.com/assets/poster.jpg?sig=fresh"; result.CDNURL != want || result.StorageURL != want {
		t.Fatalf("cdn_url=%q storage_url=%q want %q", result.CDNURL, result.StorageURL, want)
	}
	if len(result.URLs) != 2 || result.URLs[0] != result.PublicURL || result.URLs[1] != result.CDNURL {
		t.Fatalf("urls = %#v", result.URLs)
	}

	get := httptest.NewRequest(http.MethodGet, "/a/posters/images/poster.jpg", nil)
	out := httptest.NewRecorder()
	app.ServeHTTP(out, get)
	if out.Code != http.StatusFound {
		t.Fatalf("bucket read status = %d body=%s", out.Code, out.Body.String())
	}
	if got, want := out.Header().Get("Location"), "https://r2.example.com/assets/poster.jpg"; got != want {
		t.Fatalf("redirect location = %q want %q", got, want)
	}
}

func TestListAssetBucketsReturnsUsage(t *testing.T) {
	app := newTestServer(t)
	createAssetBucketForTest(t, app, "docs")
	uploadBucketObjectForTest(t, app, "docs", "readme.md", []byte("hello bucket list"))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/asset-buckets", nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list buckets status = %d body=%s", rec.Code, rec.Body.String())
	}
	var result struct {
		Buckets []model.AssetBucket `json:"buckets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Buckets) != 1 {
		t.Fatalf("bucket count = %d", len(result.Buckets))
	}
	if result.Buckets[0].ObjectCount != 1 || result.Buckets[0].UsedBytes != int64(len("hello bucket list")) {
		t.Fatalf("usage = objects:%d bytes:%d", result.Buckets[0].ObjectCount, result.Buckets[0].UsedBytes)
	}
}

func TestAssetBucketDeleteObjectAndBucket(t *testing.T) {
	app := newTestServer(t)
	create := map[string]any{
		"slug":          "videos",
		"name":          "Videos",
		"route_profile": "overseas",
		"allowed_types": []string{"video"},
	}
	raw, _ := json.Marshal(create)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/asset-buckets", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create bucket status = %d body=%s", rec.Code, rec.Body.String())
	}

	for _, logicalPath := range []string{"dynamic/one.mp4", "dynamic/two.mp4"} {
		body, ctype := multipartBody(t, map[string]string{"path": logicalPath, "asset_type": "video"}, "file", filepath.Base(logicalPath), []byte("fake mp4 "+logicalPath))
		upload := httptest.NewRequest(http.MethodPost, "/api/v1/asset-buckets/videos/objects", body)
		upload.Header.Set("Content-Type", ctype)
		upload.Header.Set("Authorization", "Bearer test-token")
		uploadRec := httptest.NewRecorder()
		app.ServeHTTP(uploadRec, upload)
		if uploadRec.Code != http.StatusCreated {
			t.Fatalf("upload %s status = %d body=%s", logicalPath, uploadRec.Code, uploadRec.Body.String())
		}
	}

	blocked := httptest.NewRequest(http.MethodDelete, "/api/v1/asset-buckets/videos", nil)
	blocked.Header.Set("Authorization", "Bearer test-token")
	blockedRec := httptest.NewRecorder()
	app.ServeHTTP(blockedRec, blocked)
	if blockedRec.Code != http.StatusConflict {
		t.Fatalf("non-empty delete status = %d body=%s", blockedRec.Code, blockedRec.Body.String())
	}

	deleteOne := httptest.NewRequest(http.MethodDelete, "/api/v1/asset-buckets/videos/objects?path="+url.QueryEscape("dynamic/one.mp4"), nil)
	deleteOne.Header.Set("Authorization", "Bearer test-token")
	deleteOneRec := httptest.NewRecorder()
	app.ServeHTTP(deleteOneRec, deleteOne)
	if deleteOneRec.Code != http.StatusOK {
		t.Fatalf("delete object status = %d body=%s", deleteOneRec.Code, deleteOneRec.Body.String())
	}
	var deletedOne deleteBucketObjectResult
	if err := json.Unmarshal(deleteOneRec.Body.Bytes(), &deletedOne); err != nil {
		t.Fatal(err)
	}
	if !deletedOne.DeletedLocal || len(deletedOne.Remote) != 1 || deletedOne.Remote[0].Status != "deleted" {
		t.Fatalf("unexpected delete object result: %+v", deletedOne)
	}
	getDeleted := httptest.NewRequest(http.MethodGet, "/a/videos/dynamic/one.mp4", nil)
	getDeletedRec := httptest.NewRecorder()
	app.ServeHTTP(getDeletedRec, getDeleted)
	if getDeletedRec.Code != http.StatusNotFound {
		t.Fatalf("deleted object read status = %d body=%s", getDeletedRec.Code, getDeletedRec.Body.String())
	}

	deleteBucket := httptest.NewRequest(http.MethodDelete, "/api/v1/asset-buckets/videos?force=true", nil)
	deleteBucket.Header.Set("Authorization", "Bearer test-token")
	deleteBucketRec := httptest.NewRecorder()
	app.ServeHTTP(deleteBucketRec, deleteBucket)
	if deleteBucketRec.Code != http.StatusOK {
		t.Fatalf("delete bucket status = %d body=%s", deleteBucketRec.Code, deleteBucketRec.Body.String())
	}
	var deletedBucket deleteAssetBucketResult
	if err := json.Unmarshal(deleteBucketRec.Body.Bytes(), &deletedBucket); err != nil {
		t.Fatal(err)
	}
	if !deletedBucket.DeletedBucket || deletedBucket.ObjectCount != 1 || len(deletedBucket.Objects) != 1 || !deletedBucket.Objects[0].DeletedLocal {
		t.Fatalf("unexpected delete bucket result: %+v", deletedBucket)
	}
	getBucket := httptest.NewRequest(http.MethodGet, "/api/v1/asset-buckets/videos", nil)
	getBucket.Header.Set("Authorization", "Bearer test-token")
	getBucketRec := httptest.NewRecorder()
	app.ServeHTTP(getBucketRec, getBucket)
	if getBucketRec.Code != http.StatusNotFound {
		t.Fatalf("get deleted bucket status = %d body=%s", getBucketRec.Code, getBucketRec.Body.String())
	}
	getRemaining := httptest.NewRequest(http.MethodGet, "/a/videos/dynamic/two.mp4", nil)
	getRemainingRec := httptest.NewRecorder()
	app.ServeHTTP(getRemainingRec, getRemaining)
	if getRemainingRec.Code != http.StatusNotFound {
		t.Fatalf("remaining object read status = %d body=%s", getRemainingRec.Code, getRemainingRec.Body.String())
	}
}

func TestAssetBucketRejectsDisallowedType(t *testing.T) {
	app := newTestServer(t)
	create := map[string]any{
		"slug":          "images",
		"name":          "Images",
		"route_profile": "overseas",
		"allowed_types": []string{"image"},
	}
	raw, _ := json.Marshal(create)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/asset-buckets", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create bucket status = %d body=%s", rec.Code, rec.Body.String())
	}

	body, ctype := multipartBody(t, map[string]string{"path": "notes/readme.txt"}, "file", "readme.txt", []byte("not an image"))
	upload := httptest.NewRequest(http.MethodPost, "/api/v1/asset-buckets/images/objects", body)
	upload.Header.Set("Content-Type", ctype)
	upload.Header.Set("Authorization", "Bearer test-token")
	uploadRec := httptest.NewRecorder()
	app.ServeHTTP(uploadRec, upload)
	if uploadRec.Code != http.StatusBadRequest {
		t.Fatalf("upload status = %d body=%s", uploadRec.Code, uploadRec.Body.String())
	}
}

func TestAssetBucketInitDryRun(t *testing.T) {
	app := newTestServer(t)
	create := map[string]any{
		"slug":          "posters",
		"name":          "Posters",
		"route_profile": "overseas",
		"allowed_types": []string{"image"},
	}
	raw, _ := json.Marshal(create)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/asset-buckets", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create bucket status = %d body=%s", rec.Code, rec.Body.String())
	}
	initBody, _ := json.Marshal(map[string]any{"dry_run": true})
	initReq := httptest.NewRequest(http.MethodPost, "/api/v1/asset-buckets/posters/init", bytes.NewReader(initBody))
	initReq.Header.Set("Content-Type", "application/json")
	initReq.Header.Set("Authorization", "Bearer test-token")
	initRec := httptest.NewRecorder()
	app.ServeHTTP(initRec, initReq)
	if initRec.Code != http.StatusOK {
		t.Fatalf("init status = %d body=%s", initRec.Code, initRec.Body.String())
	}
	var result initAssetBucketResult
	if err := json.Unmarshal(initRec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.DryRun || result.Status != "ok" || len(result.Directories) == 0 {
		t.Fatalf("unexpected init result: %+v", result)
	}
}

func TestAssetBucketPurgeDryRunBuildsObjectURLs(t *testing.T) {
	app := newTestServer(t)
	createAssetBucketForTest(t, app, "posters")
	uploadBucketObjectForTest(t, app, "posters", "posters/one.jpg", []byte("one"))
	uploadBucketObjectForTest(t, app, "posters", "posters/two.jpg", []byte("two"))
	uploadBucketObjectForTest(t, app, "posters", "icons/skip.jpg", []byte("skip"))

	raw, _ := json.Marshal(map[string]any{
		"prefix":   "posters/",
		"base_url": "https://cdn.example.com",
		"dry_run":  true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/asset-buckets/posters/purge", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("purge bucket status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp purgeAssetBucketCacheResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != "planned" || resp.URLCount != 2 {
		t.Fatalf("unexpected purge response: %+v", resp)
	}
	for _, want := range []string{
		"https://cdn.example.com/a/posters/posters/one.jpg",
		"https://cdn.example.com/a/posters/posters/two.jpg",
	} {
		if !contains(resp.URLs, want) {
			t.Fatalf("purge urls missing %q in %+v", want, resp.URLs)
		}
	}
	if contains(resp.URLs, "https://cdn.example.com/a/posters/icons/skip.jpg") {
		t.Fatalf("purge urls should not include skipped prefix: %+v", resp.URLs)
	}
}

func TestAssetBucketWarmupDryRunBuildsEscapedObjectURL(t *testing.T) {
	app := newTestServer(t)
	createAssetBucketForTest(t, app, "docs")
	uploadBucketObjectForTest(t, app, "docs", "manuals/hello world.txt", []byte("hello"))

	raw, _ := json.Marshal(map[string]any{
		"path":     "manuals/hello world.txt",
		"base_url": "https://cdn.example.com",
		"dry_run":  true,
		"method":   "GET",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/asset-buckets/docs/warmup", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("warmup bucket status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp warmupAssetBucketResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != "planned" || resp.Method != "GET" || resp.URLCount != 1 {
		t.Fatalf("unexpected warmup response: %+v", resp)
	}
	if got, want := resp.URLs[0], "https://cdn.example.com/a/docs/manuals/hello%20world.txt"; got != want {
		t.Fatalf("warmup url = %q want %q", got, want)
	}
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		Server: config.ServerConfig{
			Addr:       "127.0.0.1:0",
			DataDir:    dir,
			AdminToken: "test-token",
		},
		Database: config.DatabaseConfig{Path: filepath.Join(dir, "test.db")},
		Storage: []config.StorageConfig{{
			Name: "local",
			Type: "local",
			Local: config.LocalConfig{
				Root: filepath.Join(dir, "objects"),
			},
		}},
		RouteProfiles: []config.RouteProfile{{
			Name:                "overseas",
			Primary:             "local",
			DefaultCacheControl: "public, max-age=60",
		}},
	}
	if err := cfg.ApplyDefaults(dir); err != nil {
		t.Fatal(err)
	}
	app, err := New(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	return app
}

func newResourceLibraryTestServer(t *testing.T, maxFileSize int64, maxBatchFiles int) *Server {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		Server: config.ServerConfig{
			Addr:       "127.0.0.1:0",
			DataDir:    dir,
			AdminToken: "test-token",
		},
		Database: config.DatabaseConfig{Path: filepath.Join(dir, "test.db")},
		Storage: []config.StorageConfig{{
			Name: "local",
			Type: "local",
			Local: config.LocalConfig{
				Root: filepath.Join(dir, "objects"),
			},
		}},
		RouteProfiles: []config.RouteProfile{{
			Name:    "limited",
			Primary: "limited_repo",
		}},
	}
	app, err := New(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	local, err := storage.NewLocalStore("local", filepath.Join(dir, "limited-objects"))
	if err != nil {
		t.Fatal(err)
	}
	library, err := storage.NewResourceLibraryStore("limited_repo", []storage.ResourceLibraryBindingStore{{
		Name:  "limited_binding",
		Path:  "/limited",
		Store: local,
		Constraints: storage.BindingConstraints{
			MaxFileSizeBytes: &maxFileSize,
			MaxBatchFiles:    &maxBatchFiles,
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	app.stores = storage.NewManager([]storage.Store{library})
	t.Cleanup(func() { _ = app.Close() })
	return app
}

func multipartBody(t *testing.T, fields map[string]string, fileField, fileName string, content []byte) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for k, v := range fields {
		if err := writer.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	part, err := writer.CreateFormFile(fileField, fileName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return &body, writer.FormDataContentType()
}

func siteZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	tmp, err := os.CreateTemp(t.TempDir(), "site-*.zip")
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(tmp)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := tmp.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(tmp.Name())
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func createAssetBucketForTest(t *testing.T, app *Server, slug string) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{
		"slug":          slug,
		"route_profile": "overseas",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/asset-buckets", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create bucket status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func uploadBucketObjectForTest(t *testing.T, app *Server, bucket, logicalPath string, payload []byte) {
	t.Helper()
	body, ctype := multipartBody(t, map[string]string{"path": logicalPath}, "file", path.Base(logicalPath), payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/asset-buckets/"+bucket+"/objects", body)
	req.Header.Set("Content-Type", ctype)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload bucket object status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func apiJSON(t *testing.T, app *Server, method, apiPath, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, apiPath, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	return rec
}

func createInviteForTest(t *testing.T, app *Server, name, role string) string {
	t.Helper()
	rec := apiJSON(t, app, http.MethodPost, "/api/v1/auth/invites", "test-token", map[string]any{
		"name": name,
		"role": role,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create invite status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		InviteToken string `json:"invite_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.InviteToken == "" {
		t.Fatalf("missing invite token: %s", rec.Body.String())
	}
	return resp.InviteToken
}

func acceptInviteForTest(t *testing.T, app *Server, inviteToken string) string {
	t.Helper()
	rec := apiJSON(t, app, http.MethodPost, "/api/v1/auth/accept-invite", "", map[string]any{
		"invite_token": inviteToken,
		"token_name":   "test",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("accept invite status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		APIToken string `json:"api_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.APIToken == "" {
		t.Fatalf("missing api token: %s", rec.Body.String())
	}
	return resp.APIToken
}

func createDeployment(t *testing.T, app *Server, siteID string, files map[string]string, fields map[string]string) string {
	t.Helper()
	zipBytes := siteZip(t, files)
	body, ctype := multipartBody(t, fields, "artifact", "dist.zip", zipBytes)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sites/"+siteID+"/deployments", body)
	req.Header.Set("Content-Type", ctype)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create deployment status = %d body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		DeploymentID string `json:"deployment_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	job, err := app.db.NextQueuedJob(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	result, err := app.runJob(context.Background(), job)
	if err != nil {
		t.Fatalf("run deployment job result=%s err=%v", result, err)
	}
	if err := app.db.FinishJobWithResult(context.Background(), job.ID, result); err != nil {
		t.Fatal(err)
	}
	return created.DeploymentID
}

func recordCloudflareStaticDeploymentForTest(t *testing.T, app *Server, siteID string, promote bool) string {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{
		"environment":         "production",
		"route_profile":       "overseas",
		"deployment_target":   "cloudflare_static",
		"worker_name":         "supercdn-" + siteID + "-static",
		"version_id":          newDeploymentID(),
		"domains":             []string{siteID + ".example.com"},
		"compatibility_date":  "2026-04-29",
		"cache_policy":        "auto",
		"headers_generated":   true,
		"not_found_handling":  "single-page-application",
		"verification_status": "ok",
		"verified_at_utc":     "2026-04-29T00:00:01Z",
		"file_count":          2,
		"total_size":          1200,
		"published_at_utc":    "2026-04-29T00:00:00Z",
		"promote":             promote,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sites/"+siteID+"/cloudflare-static/deployments", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("record static deployment status = %d body=%s", rec.Code, rec.Body.String())
	}
	var dep model.SiteDeployment
	if err := json.Unmarshal(rec.Body.Bytes(), &dep); err != nil {
		t.Fatal(err)
	}
	if dep.ID == "" {
		t.Fatalf("record response missing deployment id: %s", rec.Body.String())
	}
	return dep.ID
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

type signedLocatorStore struct {
	name          string
	statLocator   string
	publicLocator string
	statErr       error
}

func (s *signedLocatorStore) Name() string { return s.name }
func (s *signedLocatorStore) Type() string { return "signed-test" }

func (s *signedLocatorStore) Put(context.Context, storage.PutOptions) (string, error) {
	if s.publicLocator != "" {
		return "resource-library://" + s.name + "?locator=" + url.QueryEscape(s.publicLocator), nil
	}
	if s.statLocator != "" {
		return s.statLocator, nil
	}
	return "", errors.New("not implemented")
}

func (s *signedLocatorStore) Get(context.Context, string, storage.GetOptions) (*storage.ObjectStream, error) {
	return nil, errors.New("not implemented")
}

func (s *signedLocatorStore) Stat(context.Context, string) (*storage.Stat, error) {
	if s.statErr != nil {
		return nil, s.statErr
	}
	return &storage.Stat{Locator: s.statLocator}, nil
}

func (s *signedLocatorStore) Delete(context.Context, string) error {
	return errors.New("not implemented")
}

func (s *signedLocatorStore) PublicURL(string) string {
	return s.publicLocator
}
