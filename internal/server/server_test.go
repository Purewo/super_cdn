package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
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
	if _, err := app.db.CreateSiteInWorkspace(context.Background(), "other", "private-site", "", "standard", "overseas", "", "", nil); err != nil {
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

func TestIPFSStatusChecksPinataProvider(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/files/public" {
			t.Fatalf("unexpected API path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-pinata-jwt" {
			t.Fatalf("Authorization = %q", got)
		}
		_, _ = w.Write([]byte(`{"data":{"files":[]}}`))
	}))
	defer api.Close()
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer gateway.Close()

	app := newPinataStatusTestServer(t, api.URL, gateway.URL)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ipfs/status?target=ipfs_pinata", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Configured bool `json:"configured"`
		OK         bool `json:"ok"`
		Providers  []struct {
			Target         string `json:"target"`
			Provider       string `json:"provider"`
			OK             bool   `json:"ok"`
			APIBaseURL     string `json:"api_base_url"`
			UploadBaseURL  string `json:"upload_base_url"`
			GatewayBaseURL string `json:"gateway_base_url"`
			Token          struct {
				OK bool `json:"ok"`
			} `json:"token"`
			Gateway struct {
				OK bool `json:"ok"`
			} `json:"gateway"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Configured || !out.OK || len(out.Providers) != 1 {
		t.Fatalf("response = %+v", out)
	}
	provider := out.Providers[0]
	if provider.Target != "ipfs_pinata" || provider.Provider != "pinata" || !provider.OK || !provider.Token.OK || !provider.Gateway.OK {
		t.Fatalf("provider = %+v", provider)
	}
	if provider.APIBaseURL != api.URL || provider.UploadBaseURL != api.URL || provider.GatewayBaseURL != gateway.URL {
		t.Fatalf("provider urls = %+v", provider)
	}
	if strings.Contains(rec.Body.String(), "test-pinata-jwt") {
		t.Fatalf("status leaked jwt: %s", rec.Body.String())
	}
}

func TestIPFSUploadPersistsCIDMetadata(t *testing.T) {
	const cid = "bafybeigdyrztipfsupload"
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/files" {
			t.Fatalf("unexpected API path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-pinata-jwt" {
			t.Fatalf("Authorization = %q", got)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		if r.FormValue("network") != "public" {
			t.Fatalf("network = %q", r.FormValue("network"))
		}
		_, _ = w.Write([]byte(`{"data":{"id":"file-upload","cid":"` + cid + `"}}`))
	}))
	defer api.Close()
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer gateway.Close()

	app := newPinataStatusTestServer(t, api.URL, gateway.URL)
	body, ctype := multipartBody(t, map[string]string{
		"project_id":    "assets",
		"path":          "/docs/readme.txt",
		"route_profile": "ipfs_archive",
	}, "file", "readme.txt", []byte("hello ipfs"))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/assets", body)
	req.Header.Set("Content-Type", ctype)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload status = %d body=%s", rec.Code, rec.Body.String())
	}
	var upload struct {
		Object model.Object `json:"object"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &upload); err != nil {
		t.Fatal(err)
	}
	if len(upload.Object.IPFS) != 1 || upload.Object.IPFS[0].CID != cid {
		t.Fatalf("object ipfs metadata = %+v", upload.Object.IPFS)
	}
	if want := gateway.URL + "/ipfs/" + cid; upload.Object.IPFS[0].GatewayURL != want {
		t.Fatalf("gateway url = %q want %q", upload.Object.IPFS[0].GatewayURL, want)
	}
	pins, err := app.db.IPFSPins(context.Background(), upload.Object.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pins) != 1 || pins[0].CID != cid || pins[0].Provider != "pinata" {
		t.Fatalf("stored pins = %+v", pins)
	}
	if pins[0].ProviderPinID != "file-upload" {
		t.Fatalf("provider pin id = %q", pins[0].ProviderPinID)
	}

	replicasReq := httptest.NewRequest(http.MethodGet, "/api/v1/objects/"+strconv.FormatInt(upload.Object.ID, 10)+"/replicas", nil)
	replicasReq.Header.Set("Authorization", "Bearer test-token")
	replicasRec := httptest.NewRecorder()
	app.ServeHTTP(replicasRec, replicasReq)
	if replicasRec.Code != http.StatusOK {
		t.Fatalf("replicas status = %d body=%s", replicasRec.Code, replicasRec.Body.String())
	}
	var replicas []model.Replica
	if err := json.Unmarshal(replicasRec.Body.Bytes(), &replicas); err != nil {
		t.Fatal(err)
	}
	if len(replicas) != 1 || replicas[0].IPFS == nil || replicas[0].IPFS.CID != cid {
		t.Fatalf("replicas = %+v", replicas)
	}
}

func TestIPFSSiteDeploymentRedirectsAssetToGateway(t *testing.T) {
	var uploadCount int
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/files" {
			t.Fatalf("unexpected API path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-pinata-jwt" {
			t.Fatalf("Authorization = %q", got)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		uploadCount++
		_, _ = w.Write([]byte(fmt.Sprintf(`{"data":{"id":"file-web-%d","cid":"bafyweb%d"}}`, uploadCount, uploadCount)))
	}))
	defer api.Close()
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer gateway.Close()

	app := newPinataStatusTestServer(t, api.URL, gateway.URL)
	deploymentID := createDeployment(t, app, "ipfs-site", map[string]string{
		"index.html":       `<a href="assets/wall.txt">wall</a>`,
		"assets/wall.txt":  "hello ipfs web",
		"assets/other.txt": "other",
	}, map[string]string{"environment": "preview", "route_profile": "ipfs_archive"})
	if uploadCount == 0 {
		t.Fatal("expected Pinata uploads")
	}
	assetObj, err := app.db.SiteDeploymentFileObject(context.Background(), deploymentID, "assets/wall.txt")
	if err != nil {
		t.Fatal(err)
	}
	pins, err := app.db.IPFSPins(context.Background(), assetObj.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pins) != 1 || pins[0].CID == "" {
		t.Fatalf("asset pins = %+v", pins)
	}

	root := httptest.NewRequest(http.MethodGet, "/p/ipfs-site/"+deploymentID+"/", nil)
	rootRec := httptest.NewRecorder()
	app.ServeHTTP(rootRec, root)
	if rootRec.Code >= http.StatusBadRequest || rootRec.Header().Get("Location") != "" {
		t.Fatalf("root status=%d location=%q body=%q", rootRec.Code, rootRec.Header().Get("Location"), rootRec.Body.String())
	}

	asset := httptest.NewRequest(http.MethodGet, "/p/ipfs-site/"+deploymentID+"/assets/wall.txt", nil)
	assetRec := httptest.NewRecorder()
	app.ServeHTTP(assetRec, asset)
	if assetRec.Code != http.StatusFound {
		t.Fatalf("asset status=%d body=%s", assetRec.Code, assetRec.Body.String())
	}
	if got, want := assetRec.Header().Get("Location"), gateway.URL+"/ipfs/"+pins[0].CID; got != want {
		t.Fatalf("asset redirect = %q want %q", got, want)
	}
	if assetRec.Header().Get("X-SuperCDN-Redirect") != "storage" {
		t.Fatalf("redirect marker = %q", assetRec.Header().Get("X-SuperCDN-Redirect"))
	}
}

func TestIPFSSiteDeploymentDeleteCleansRemoteObjects(t *testing.T) {
	var uploadCount int
	var deleteCount int
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v3/files":
			if got := r.Header.Get("Authorization"); got != "Bearer test-pinata-jwt" {
				t.Fatalf("Authorization = %q", got)
			}
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			uploadCount++
			_, _ = w.Write([]byte(fmt.Sprintf(`{"data":{"id":"file-web-%d","cid":"bafywebcleanup%d"}}`, uploadCount, uploadCount)))
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v3/files/public/file-web-"):
			if got := r.Header.Get("Authorization"); got != "Bearer test-pinata-jwt" {
				t.Fatalf("Authorization = %q", got)
			}
			deleteCount++
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected API request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer api.Close()
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer gateway.Close()

	app := newPinataStatusTestServer(t, api.URL, gateway.URL)
	deploymentID := createDeployment(t, app, "ipfs-cleanup-site", map[string]string{
		"index.html":      `<a href="assets/wall.txt">wall</a>`,
		"assets/wall.txt": "hello ipfs web cleanup",
	}, map[string]string{"environment": "preview", "route_profile": "ipfs_archive"})
	if uploadCount == 0 {
		t.Fatal("expected Pinata uploads")
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/sites/ipfs-cleanup-site/deployments/"+deploymentID+"?delete_objects=true&delete_remote=true", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", rec.Code, rec.Body.String())
	}
	var result deleteSiteDeploymentResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.DeletedDeployment || !result.DeleteObjects || !result.DeleteRemote || len(result.Errors) > 0 {
		t.Fatalf("delete result = %+v", result)
	}
	if result.ObjectCount != uploadCount || deleteCount != uploadCount {
		t.Fatalf("object/delete count result=%d upload=%d delete=%d", result.ObjectCount, uploadCount, deleteCount)
	}
	for _, obj := range result.Objects {
		if !obj.DeletedLocal || len(obj.Remote) != 1 || obj.Remote[0].Status != "deleted" {
			t.Fatalf("object delete result = %+v", obj)
		}
	}
	if _, err := app.db.GetSiteDeployment(context.Background(), deploymentID); err == nil {
		t.Fatal("deployment still exists")
	}
}

func TestIPFSBucketUploadReturnsGatewayAndListMetadata(t *testing.T) {
	const cid = "bafybeigdyrztbucket"
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v3/groups/public":
			if got := r.URL.Query().Get("name"); got != "supercdn-bucket-ipfs-bucket" {
				t.Fatalf("group name = %q", got)
			}
			_, _ = w.Write([]byte(`{"data":{"groups":[{"id":"group-bucket","name":"supercdn-bucket-ipfs-bucket"}]}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v3/files":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			if got := r.FormValue("group_id"); got != "group-bucket" {
				t.Fatalf("group_id = %q", got)
			}
			_, _ = w.Write([]byte(`{"data":{"id":"file-bucket","cid":"` + cid + `"}}`))
		default:
			t.Fatalf("unexpected API path %s", r.URL.Path)
		}
	}))
	defer api.Close()
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer gateway.Close()

	app := newPinataStatusTestServer(t, api.URL, gateway.URL)
	create := apiJSON(t, app, http.MethodPost, "/api/v1/asset-buckets", "test-token", map[string]any{
		"slug":          "ipfs-bucket",
		"route_profile": "ipfs_archive",
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create bucket status = %d body=%s", create.Code, create.Body.String())
	}
	body, ctype := multipartBody(t, map[string]string{"path": "docs/readme.txt"}, "file", "readme.txt", []byte("hello bucket"))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/asset-buckets/ipfs-bucket/objects", body)
	req.Header.Set("Content-Type", ctype)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload bucket status = %d body=%s", rec.Code, rec.Body.String())
	}
	var upload struct {
		CDNURL       string                  `json:"cdn_url"`
		IPFS         []model.IPFSPin         `json:"ipfs"`
		BucketObject model.AssetBucketObject `json:"bucket_object"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &upload); err != nil {
		t.Fatal(err)
	}
	if want := gateway.URL + "/ipfs/" + cid; upload.CDNURL != want {
		t.Fatalf("cdn_url = %q want %q", upload.CDNURL, want)
	}
	if len(upload.IPFS) != 1 || upload.IPFS[0].CID != cid || len(upload.BucketObject.IPFS) != 1 {
		t.Fatalf("upload ipfs metadata = top=%+v bucket=%+v", upload.IPFS, upload.BucketObject.IPFS)
	}
	if !strings.Contains(upload.IPFS[0].Locator, "pinata_group_id=group-bucket") {
		t.Fatalf("locator = %q", upload.IPFS[0].Locator)
	}

	list := apiJSON(t, app, http.MethodGet, "/api/v1/asset-buckets/ipfs-bucket/objects", "test-token", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list bucket status = %d body=%s", list.Code, list.Body.String())
	}
	var listed struct {
		Objects []model.AssetBucketObject `json:"objects"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Objects) != 1 || len(listed.Objects[0].IPFS) != 1 || listed.Objects[0].IPFS[0].CID != cid {
		t.Fatalf("listed objects = %+v", listed.Objects)
	}
}

func TestIPFSBucketDeleteUnpinsLastCIDReference(t *testing.T) {
	const cid = "bafybeigdyrztshared"
	var deleteCount int
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v3/groups/public":
			_, _ = w.Write([]byte(`{"data":{"groups":[{"id":"group-shared","name":"supercdn-bucket-ipfs-bucket"}]}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v3/files":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			if got := r.FormValue("group_id"); got != "group-shared" {
				t.Fatalf("group_id = %q", got)
			}
			_, _ = w.Write([]byte(`{"data":{"id":"file-shared","cid":"` + cid + `"}}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/v3/files/public/file-shared":
			if got := r.Header.Get("Authorization"); got != "Bearer test-pinata-jwt" {
				t.Fatalf("Authorization = %q", got)
			}
			deleteCount++
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected API request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer api.Close()
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer gateway.Close()

	app := newPinataStatusTestServer(t, api.URL, gateway.URL)
	create := apiJSON(t, app, http.MethodPost, "/api/v1/asset-buckets", "test-token", map[string]any{
		"slug":          "ipfs-bucket",
		"route_profile": "ipfs_archive",
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create bucket status = %d body=%s", create.Code, create.Body.String())
	}
	for _, logicalPath := range []string{"images/a.png", "images/b.png"} {
		body, ctype := multipartBody(t, map[string]string{"path": logicalPath}, "file", "wall.png", []byte("same image"))
		req := httptest.NewRequest(http.MethodPost, "/api/v1/asset-buckets/ipfs-bucket/objects", body)
		req.Header.Set("Content-Type", ctype)
		req.Header.Set("Authorization", "Bearer test-token")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("upload %s status = %d body=%s", logicalPath, rec.Code, rec.Body.String())
		}
	}

	firstDelete := apiJSON(t, app, http.MethodDelete, "/api/v1/asset-buckets/ipfs-bucket/objects?path=images/a.png&delete_remote=true", "test-token", nil)
	if firstDelete.Code != http.StatusOK {
		t.Fatalf("first delete status = %d body=%s", firstDelete.Code, firstDelete.Body.String())
	}
	var first deleteBucketObjectResult
	if err := json.Unmarshal(firstDelete.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Remote) != 1 || first.Remote[0].Status != "kept_shared" || deleteCount != 0 {
		t.Fatalf("first delete result = %+v deleteCount=%d", first, deleteCount)
	}

	secondDelete := apiJSON(t, app, http.MethodDelete, "/api/v1/asset-buckets/ipfs-bucket/objects?path=images/b.png&delete_remote=true", "test-token", nil)
	if secondDelete.Code != http.StatusOK {
		t.Fatalf("second delete status = %d body=%s", secondDelete.Code, secondDelete.Body.String())
	}
	var second deleteBucketObjectResult
	if err := json.Unmarshal(secondDelete.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if len(second.Remote) != 1 || second.Remote[0].Status != "deleted" || deleteCount != 1 {
		t.Fatalf("second delete result = %+v deleteCount=%d", second, deleteCount)
	}
}

func TestIPFSBucketDeleteDistinctPinataFileIDsForSameCID(t *testing.T) {
	const cid = "bafybeigdyrztdistinct"
	var uploadCount int
	var deleted []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v3/groups/public":
			_, _ = w.Write([]byte(`{"data":{"groups":[{"id":"group-distinct","name":"supercdn-bucket-ipfs-bucket"}]}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v3/files":
			uploadCount++
			_, _ = w.Write([]byte(fmt.Sprintf(`{"data":{"id":"file-distinct-%d","cid":"`+cid+`"}}`, uploadCount)))
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v3/files/public/file-distinct-"):
			deleted = append(deleted, path.Base(r.URL.Path))
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected API request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer api.Close()
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer gateway.Close()

	app := newPinataStatusTestServer(t, api.URL, gateway.URL)
	create := apiJSON(t, app, http.MethodPost, "/api/v1/asset-buckets", "test-token", map[string]any{
		"slug":          "ipfs-bucket",
		"route_profile": "ipfs_archive",
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create bucket status = %d body=%s", create.Code, create.Body.String())
	}
	for _, logicalPath := range []string{"images/a.png", "images/b.png"} {
		body, ctype := multipartBody(t, map[string]string{"path": logicalPath}, "file", "wall.png", []byte("same image"))
		req := httptest.NewRequest(http.MethodPost, "/api/v1/asset-buckets/ipfs-bucket/objects", body)
		req.Header.Set("Content-Type", ctype)
		req.Header.Set("Authorization", "Bearer test-token")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("upload %s status = %d body=%s", logicalPath, rec.Code, rec.Body.String())
		}
	}

	firstDelete := apiJSON(t, app, http.MethodDelete, "/api/v1/asset-buckets/ipfs-bucket/objects?path=images/a.png&delete_remote=true", "test-token", nil)
	if firstDelete.Code != http.StatusOK {
		t.Fatalf("first delete status = %d body=%s", firstDelete.Code, firstDelete.Body.String())
	}
	var first deleteBucketObjectResult
	if err := json.Unmarshal(firstDelete.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Remote) != 1 || first.Remote[0].Status != "deleted" || len(deleted) != 1 || deleted[0] != "file-distinct-1" {
		t.Fatalf("first delete result = %+v deleted=%+v", first, deleted)
	}
	secondDelete := apiJSON(t, app, http.MethodDelete, "/api/v1/asset-buckets/ipfs-bucket/objects?path=images/b.png&delete_remote=true", "test-token", nil)
	if secondDelete.Code != http.StatusOK {
		t.Fatalf("second delete status = %d body=%s", secondDelete.Code, secondDelete.Body.String())
	}
	if len(deleted) != 2 || deleted[1] != "file-distinct-2" {
		t.Fatalf("deleted = %+v", deleted)
	}
}

func TestRefreshIPFSPinsUpdatesPinStatus(t *testing.T) {
	const cid = "bafybeigdyrztrefresh"
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/files":
			_, _ = w.Write([]byte(`{"data":{"id":"file-refresh","cid":"` + cid + `"}}`))
		case "/v3/files/public":
			if got := r.URL.Query().Get("cid"); got != cid {
				t.Fatalf("cid = %q", got)
			}
			_, _ = w.Write([]byte(`{"data":{"files":[{"id":"file-refresh","cid":"` + cid + `"}]}}`))
		default:
			t.Fatalf("unexpected API path %s", r.URL.Path)
		}
	}))
	defer api.Close()
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer gateway.Close()

	app := newPinataStatusTestServer(t, api.URL, gateway.URL)
	body, ctype := multipartBody(t, map[string]string{
		"project_id":    "assets",
		"path":          "/docs/readme.txt",
		"route_profile": "ipfs_archive",
	}, "file", "readme.txt", []byte("hello ipfs"))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/assets", body)
	req.Header.Set("Content-Type", ctype)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload status = %d body=%s", rec.Code, rec.Body.String())
	}
	var upload struct {
		Object model.Object `json:"object"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &upload); err != nil {
		t.Fatal(err)
	}

	refresh := apiJSON(t, app, http.MethodPost, "/api/v1/ipfs/pins/refresh", "test-token", map[string]any{
		"object_id": upload.Object.ID,
	})
	if refresh.Code != http.StatusOK {
		t.Fatalf("refresh status = %d body=%s", refresh.Code, refresh.Body.String())
	}
	var out refreshIPFSPinsResponse
	if err := json.Unmarshal(refresh.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Status != "ok" || len(out.Pins) != 1 || out.Pins[0].PinStatus != "pinned" || out.Pins[0].ProviderPinID != "file-refresh" {
		t.Fatalf("refresh response = %+v", out)
	}
	pin, err := app.db.GetIPFSPin(context.Background(), upload.Object.ID, "ipfs_pinata")
	if err != nil {
		t.Fatal(err)
	}
	if pin.PinStatus != "pinned" || pin.ProviderPinID != "file-refresh" {
		t.Fatalf("stored pin = %+v", pin)
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

func TestExportEdgeManifestDoesNotUseBackupReplicaWithoutRoutingPolicy(t *testing.T) {
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
		"index.html":    "home",
		"assets/app.js": "console.log('ok')",
	}, map[string]string{"environment": "production", "promote": "true"})
	ctx := context.Background()
	obj, err := app.db.SiteDeploymentFileObject(ctx, deploymentID, "assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.db.UpsertReplica(ctx, obj.ID, "backup", model.ReplicaReady, "http://backup.example/assets/app.js?sign=fresh", ""); err != nil {
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
	if asset.Type != "origin" || asset.Location != "" {
		t.Fatalf("asset should not use backup replica without explicit routing policy: %+v", asset)
	}
	warnings := strings.Join(manifest.Warnings, "\n")
	if len(manifest.Warnings) == 0 || !strings.Contains(warnings, "primary redirect URL unavailable") {
		t.Fatalf("expected primary redirect warning, got %+v", manifest.Warnings)
	}
	if strings.Contains(warnings, "backup.example") {
		t.Fatalf("backup URL leaked into manifest warnings: %+v", manifest.Warnings)
	}
}

func TestExportEdgeManifestBuildsIPFSGatewayRoute(t *testing.T) {
	const cid = "bafybeigdyrztmanifest"
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/files" {
			t.Fatalf("unexpected API path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":{"id":"file-manifest","cid":"` + cid + `"}}`))
	}))
	defer api.Close()
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer gateway.Close()

	app := newPinataStatusTestServer(t, api.URL, gateway.URL)
	create := map[string]any{
		"id":            "demo",
		"route_profile": "ipfs_archive",
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
		"index.html":      "home",
		"assets/wall.png": "png",
	}, map[string]string{"environment": "production", "promote": "true", "route_profile": "ipfs_archive"})

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
	asset := manifest.Routes["/assets/wall.png"]
	if asset.Type != "ipfs" || asset.Status != http.StatusOK || asset.Location != gateway.URL+"/ipfs/"+cid {
		t.Fatalf("unexpected ipfs asset route: %+v", asset)
	}
	if len(asset.IPFS) != 1 || asset.IPFS[0].CID != cid || len(asset.GatewayFallbacks) != 1 || asset.GatewayFallbacks[0] != gateway.URL+"/ipfs/"+cid {
		t.Fatalf("unexpected ipfs route metadata: %+v", asset)
	}
}

func TestExportEdgeManifestBuildsSmartRoutingCandidates(t *testing.T) {
	app := newSmartRoutingTestServer(t)
	create := map[string]any{
		"id":             "demo",
		"route_profile":  "smart",
		"routing_policy": "global_smart",
		"mode":           "standard",
		"domains":        []string{"demo.local"},
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
	}, map[string]string{"environment": "production", "promote": "true", "route_profile": "smart"})
	ctx := context.Background()
	obj, err := app.db.SiteDeploymentFileObject(ctx, deploymentID, "assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.db.UpsertReplica(ctx, obj.ID, "china", model.ReplicaReady, "https://china.example/assets/app.js?sig=cn", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := app.db.UpsertReplica(ctx, obj.ID, "overseas", model.ReplicaReady, "https://overseas.example/assets/app.js?sig=global", ""); err != nil {
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
	route := manifest.Routes["/assets/app.js"]
	if manifest.RoutingPolicy != "global_smart" || route.Type != "smart" || route.RoutingPolicy == nil || route.RoutingPolicy.Name != "global_smart" {
		t.Fatalf("unexpected smart route metadata: manifest=%+v route=%+v", manifest, route)
	}
	if route.Status != http.StatusFound || route.Location == "" || route.CacheControl != "no-store" {
		t.Fatalf("unexpected smart route response fields: %+v", route)
	}
	if len(route.Candidates) != 2 {
		t.Fatalf("candidate count = %d route=%+v", len(route.Candidates), route)
	}
	if route.Candidates[0].Target != "china" || route.Candidates[0].RegionGroup != "china" || route.Candidates[0].URL == "" {
		t.Fatalf("unexpected first candidate: %+v", route.Candidates[0])
	}
	if route.Candidates[1].Target != "overseas" || route.Candidates[1].RegionGroup != "overseas" || route.Candidates[1].URL == "" {
		t.Fatalf("unexpected second candidate: %+v", route.Candidates[1])
	}
}

func TestExportEdgeManifestSkipsUnhealthySmartRoutingCandidate(t *testing.T) {
	app := newSmartRoutingTestServer(t)
	configureSmartRoutingHealthLibraries(app)
	create := map[string]any{
		"id":             "demo",
		"route_profile":  "smart",
		"routing_policy": "global_smart",
		"mode":           "standard",
		"domains":        []string{"demo.local"},
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
	}, map[string]string{"environment": "production", "promote": "true", "route_profile": "smart"})
	ctx := context.Background()
	obj, err := app.db.SiteDeploymentFileObject(ctx, deploymentID, "assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.db.UpsertReplica(ctx, obj.ID, "china", model.ReplicaReady, "https://china.example/assets/app.js?sig=cn", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := app.db.UpsertReplica(ctx, obj.ID, "overseas", model.ReplicaReady, "https://overseas.example/assets/app.js?sig=global", ""); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := app.db.UpsertResourceLibraryHealth(ctx, model.ResourceLibraryHealth{
		Library:       "china",
		Binding:       "china_primary",
		BindingPath:   "/china",
		Target:        "china:china_primary",
		TargetType:    "resource_library",
		Status:        storage.HealthStatusFailed,
		CheckMode:     storage.HealthModePassive,
		LastError:     "dial tcp4 timeout",
		LastCheckedAt: now,
		LastFailureAt: now,
	}); err != nil {
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
	route := manifest.Routes["/assets/app.js"]
	if route.Type != "redirect" || route.Location != "https://overseas.example/object?sig=global" {
		t.Fatalf("expected degraded overseas route, got %+v", route)
	}
	if len(route.Candidates) != 1 || route.Candidates[0].Target != "overseas" {
		t.Fatalf("unexpected candidates: %+v", route.Candidates)
	}
	if !strings.Contains(strings.Join(manifest.Warnings, "\n"), "skipped by health") {
		t.Fatalf("expected health warning: %+v", manifest.Warnings)
	}
}

func TestExportEdgeManifestBuildsExplicitResourceFailoverRoute(t *testing.T) {
	app := newSmartRoutingTestServer(t)
	create := map[string]any{
		"id":            "demo",
		"route_profile": "smart",
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
	}, map[string]string{"environment": "production", "promote": "true", "route_profile": "smart", "resource_failover": "true"})
	ctx := context.Background()
	obj, err := app.db.SiteDeploymentFileObject(ctx, deploymentID, "assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.db.UpsertReplica(ctx, obj.ID, "china", model.ReplicaReady, "https://china.example/assets/app.js?sig=cn", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := app.db.UpsertReplica(ctx, obj.ID, "overseas", model.ReplicaReady, "https://overseas.example/assets/app.js?sig=global", ""); err != nil {
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
	route := manifest.Routes["/assets/app.js"]
	if !manifest.ResourceFailover || route.Type != "failover" || route.Delivery != "failover" || route.Status != http.StatusOK {
		t.Fatalf("unexpected failover route metadata: manifest=%+v route=%+v", manifest, route)
	}
	if len(route.Candidates) != 2 {
		t.Fatalf("candidate count = %d route=%+v", len(route.Candidates), route)
	}
	if route.Candidates[0].Target != "china" || route.Candidates[1].Target != "overseas" {
		t.Fatalf("unexpected failover candidates: %+v", route.Candidates)
	}
}

func TestExportEdgeManifestResourceFailoverWithoutCandidatesDoesNotUseOrigin(t *testing.T) {
	app := newSmartRoutingTestServer(t)
	create := map[string]any{
		"id":            "demo",
		"route_profile": "smart",
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
	}, map[string]string{"environment": "production", "promote": "true", "route_profile": "smart", "resource_failover": "true"})
	ctx := context.Background()
	obj, err := app.db.SiteDeploymentFileObject(ctx, deploymentID, "assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.db.UpsertReplica(ctx, obj.ID, "china", model.ReplicaPending, "", "not ready"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.db.UpsertReplica(ctx, obj.ID, "overseas", model.ReplicaFailed, "", "not ready"); err != nil {
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
	route := manifest.Routes["/assets/app.js"]
	if route.Type != "failover" || route.Delivery != "failover" || route.Status != http.StatusBadGateway {
		t.Fatalf("unexpected failover error route: %+v", route)
	}
	if route.Location != "" || len(route.Candidates) != 0 {
		t.Fatalf("failover error route should not include origin or candidates: %+v", route)
	}
}

func TestReplicateObjectRetriesTransientSourceGet(t *testing.T) {
	app := newTestServer(t)
	oldAttempts := replicateSourceGetAttempts
	oldDelay := replicateSourceGetDelay
	replicateSourceGetAttempts = 3
	replicateSourceGetDelay = time.Millisecond
	t.Cleanup(func() {
		replicateSourceGetAttempts = oldAttempts
		replicateSourceGetDelay = oldDelay
	})

	source := &flakyGetStore{name: "source", failures: 2, content: []byte("hello replica")}
	target := &capturingPutStore{name: "target"}
	app.stores = storage.NewManager([]storage.Store{source, target})

	ctx := context.Background()
	if _, err := app.db.CreateProject(ctx, "replicate-test"); err != nil {
		t.Fatal(err)
	}
	obj, err := app.db.SaveObject(ctx, model.Object{
		ProjectID:     "replicate-test",
		Path:          "files/demo.txt",
		Key:           "objects/demo.txt",
		RouteProfile:  "test",
		Size:          int64(len(source.content)),
		ContentType:   "text/plain",
		CacheControl:  "public, max-age=60",
		PrimaryTarget: "source",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.db.UpsertReplica(ctx, obj.ID, "source", model.ReplicaReady, "source-locator", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := app.db.UpsertReplica(ctx, obj.ID, "target", model.ReplicaPending, "", ""); err != nil {
		t.Fatal(err)
	}

	if err := app.replicateObject(ctx, replicatePayload{ObjectID: obj.ID, Target: "target"}); err != nil {
		t.Fatal(err)
	}
	if source.calls != 3 {
		t.Fatalf("source get calls = %d", source.calls)
	}
	if string(target.content) != string(source.content) {
		t.Fatalf("target content = %q", string(target.content))
	}
	replicas, err := app.db.Replicas(ctx, obj.ID)
	if err != nil {
		t.Fatal(err)
	}
	var targetReplica *model.Replica
	for i := range replicas {
		if replicas[i].Target == "target" {
			targetReplica = &replicas[i]
		}
	}
	if targetReplica == nil || targetReplica.Status != model.ReplicaReady || targetReplica.Locator != "target://objects/demo.txt" {
		t.Fatalf("target replica = %+v", targetReplica)
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

func TestAssetBucketSmartRoutingRedirectsByRegion(t *testing.T) {
	app := newSmartRoutingTestServer(t)
	raw, _ := json.Marshal(map[string]any{
		"slug":           "posters",
		"name":           "Posters",
		"route_profile":  "smart",
		"routing_policy": "global_smart",
		"allowed_types":  []string{"image"},
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
	item, err := app.db.GetAssetBucketObject(context.Background(), "posters", "images/poster.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.db.UpsertReplica(context.Background(), item.ObjectID, "china", model.ReplicaReady, "https://china.example/images/poster.jpg?sig=cn", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := app.db.UpsertReplica(context.Background(), item.ObjectID, "overseas", model.ReplicaReady, "https://overseas.example/images/poster.jpg?sig=global", ""); err != nil {
		t.Fatal(err)
	}

	getCN := httptest.NewRequest(http.MethodGet, "/a/posters/images/poster.jpg", nil)
	getCN.Header.Set("CF-IPCountry", "CN")
	outCN := httptest.NewRecorder()
	app.ServeHTTP(outCN, getCN)
	if outCN.Code != http.StatusFound {
		t.Fatalf("cn bucket read status = %d body=%s", outCN.Code, outCN.Body.String())
	}
	if got := outCN.Header().Get("Location"); !strings.Contains(got, "china.example") {
		t.Fatalf("cn redirect location = %q", got)
	}
	if outCN.Header().Get("X-SuperCDN-Route-Policy") != "global_smart" || outCN.Header().Get("X-SuperCDN-Route-Target") != "china" {
		t.Fatalf("cn route headers: policy=%q target=%q reason=%q", outCN.Header().Get("X-SuperCDN-Route-Policy"), outCN.Header().Get("X-SuperCDN-Route-Target"), outCN.Header().Get("X-SuperCDN-Route-Reason"))
	}

	getUS := httptest.NewRequest(http.MethodGet, "/a/posters/images/poster.jpg", nil)
	getUS.Header.Set("CF-IPCountry", "US")
	outUS := httptest.NewRecorder()
	app.ServeHTTP(outUS, getUS)
	if outUS.Code != http.StatusFound {
		t.Fatalf("us bucket read status = %d body=%s", outUS.Code, outUS.Body.String())
	}
	if got := outUS.Header().Get("Location"); !strings.Contains(got, "overseas.example") {
		t.Fatalf("us redirect location = %q", got)
	}
	if outUS.Header().Get("X-SuperCDN-Route-Target") != "overseas" {
		t.Fatalf("us route target = %q", outUS.Header().Get("X-SuperCDN-Route-Target"))
	}
}

func TestAssetBucketRejectsRoutingPolicyOutsideRouteProfile(t *testing.T) {
	app := newTestServer(t)
	app.cfg.RoutingPolicies = []config.RoutingPolicy{{
		Name:               "bad_smart",
		Mode:               "global_load_balance",
		DefaultRegionGroup: "overseas",
		Sources: []config.RoutingPolicySource{
			{Target: "local", RegionGroup: "china", Weight: 1},
			{Target: "backup", RegionGroup: "overseas", Weight: 1},
		},
	}}
	raw, _ := json.Marshal(map[string]any{
		"slug":           "posters",
		"route_profile":  "overseas",
		"routing_policy": "bad_smart",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/asset-buckets", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("create bucket status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "not included in route_profile") {
		t.Fatalf("unexpected error body: %s", rec.Body.String())
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

func newSmartRoutingTestServer(t *testing.T) *Server {
	t.Helper()
	app := newTestServer(t)
	app.cfg.RouteProfiles = []config.RouteProfile{{
		Name:                "smart",
		Primary:             "china",
		Backups:             []string{"overseas"},
		DefaultCacheControl: "public, max-age=60",
		AllowRedirect:       true,
	}}
	app.cfg.RoutingPolicies = []config.RoutingPolicy{{
		Name:               "global_smart",
		Mode:               "global_load_balance",
		DefaultRegionGroup: "overseas",
		Sources: []config.RoutingPolicySource{
			{Target: "china", RegionGroup: "china", Weight: 1},
			{Target: "overseas", RegionGroup: "overseas", Weight: 1},
		},
	}}
	app.stores = storage.NewManager([]storage.Store{
		&signedLocatorStore{
			name:          "china",
			statLocator:   "https://china.example/object?sig=cn",
			publicLocator: "https://china.example/object",
		},
		&signedLocatorStore{
			name:          "overseas",
			statLocator:   "https://overseas.example/object?sig=global",
			publicLocator: "https://overseas.example/object",
		},
	})
	return app
}

func configureSmartRoutingHealthLibraries(app *Server) {
	app.cfg.ResourceLibraries = []config.ResourceLibraryConfig{
		{
			Name: "china",
			Bindings: []config.ResourceLibraryBinding{{
				Name:       "china_primary",
				MountPoint: "alist",
				Path:       "/china",
			}},
		},
		{
			Name: "overseas",
			Bindings: []config.ResourceLibraryBinding{{
				Name:       "overseas_primary",
				MountPoint: "alist",
				Path:       "/overseas",
			}},
		},
	}
}

func newPinataStatusTestServer(t *testing.T, apiBaseURL, gatewayBaseURL string) *Server {
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
			Name: "ipfs_pinata",
			Type: "pinata",
			Pinata: config.PinataConfig{
				APIBaseURL:     apiBaseURL,
				JWT:            "test-pinata-jwt",
				GatewayBaseURL: gatewayBaseURL,
			},
		}},
		RouteProfiles: []config.RouteProfile{{
			Name:    "ipfs_archive",
			Primary: "ipfs_pinata",
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

type flakyGetStore struct {
	name     string
	failures int
	calls    int
	content  []byte
}

func (s *flakyGetStore) Name() string { return s.name }
func (s *flakyGetStore) Type() string { return "flaky-test" }

func (s *flakyGetStore) Put(context.Context, storage.PutOptions) (string, error) {
	return "", errors.New("not implemented")
}

func (s *flakyGetStore) Get(context.Context, string, storage.GetOptions) (*storage.ObjectStream, error) {
	s.calls++
	if s.calls <= s.failures {
		return nil, fmt.Errorf("source not visible yet: %w", storage.ErrNotFound)
	}
	return &storage.ObjectStream{
		Body:        io.NopCloser(bytes.NewReader(s.content)),
		Size:        int64(len(s.content)),
		ContentType: "text/plain",
	}, nil
}

func (s *flakyGetStore) Stat(context.Context, string) (*storage.Stat, error) {
	return nil, errors.New("not implemented")
}

func (s *flakyGetStore) Delete(context.Context, string) error {
	return errors.New("not implemented")
}

func (s *flakyGetStore) PublicURL(string) string { return "" }

type capturingPutStore struct {
	name    string
	content []byte
}

func (s *capturingPutStore) Name() string { return s.name }
func (s *capturingPutStore) Type() string { return "capture-test" }

func (s *capturingPutStore) Put(_ context.Context, opts storage.PutOptions) (string, error) {
	content, err := os.ReadFile(opts.FilePath)
	if err != nil {
		return "", err
	}
	s.content = content
	return "target://" + opts.Key, nil
}

func (s *capturingPutStore) Get(context.Context, string, storage.GetOptions) (*storage.ObjectStream, error) {
	return nil, errors.New("not implemented")
}

func (s *capturingPutStore) Stat(context.Context, string) (*storage.Stat, error) {
	return nil, errors.New("not implemented")
}

func (s *capturingPutStore) Delete(context.Context, string) error {
	return errors.New("not implemented")
}

func (s *capturingPutStore) PublicURL(string) string { return "" }
