package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"supercdn/internal/config"
	"supercdn/internal/db"
	"supercdn/internal/model"
	"supercdn/internal/storage"
)

type Server struct {
	cfg         *config.Config
	db          *db.DB
	stores      *storage.Manager
	apiMux      *http.ServeMux
	logger      *slog.Logger
	staging     string
	transferSem chan struct{}
}

func New(ctx context.Context, cfg *config.Config, logger *slog.Logger) (*Server, error) {
	if logger == nil {
		logger = slog.Default()
	}
	storeManager, err := storage.BuildManager(ctx, cfg)
	if err != nil {
		return nil, err
	}
	state, err := db.Open(ctx, cfg.Database.Path)
	if err != nil {
		return nil, err
	}
	maxTransfers := cfg.Limits.MaxActiveTransfers
	if maxTransfers <= 0 {
		maxTransfers = 5
	}
	cfg.Limits.MaxActiveTransfers = maxTransfers
	if cfg.Limits.DefaultMaxSiteFiles == 0 {
		cfg.Limits.DefaultMaxSiteFiles = 5
	}
	if cfg.Limits.ResourceHealthMinIntervalSeconds == 0 {
		cfg.Limits.ResourceHealthMinIntervalSeconds = 300
	}
	s := &Server{
		cfg:         cfg,
		db:          state,
		stores:      storeManager,
		apiMux:      http.NewServeMux(),
		logger:      logger,
		staging:     filepath.Join(cfg.Server.DataDir, "staging"),
		transferSem: make(chan struct{}, maxTransfers),
	}
	if err := os.MkdirAll(s.staging, 0o755); err != nil {
		_ = state.Close()
		return nil, err
	}
	if s.overclockMode() {
		s.logger.Warn(overclockWarning)
	}
	s.routes()
	return s, nil
}

func (s *Server) Close() error {
	return s.db.Close()
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/v1/") {
		if s.publicAPI(r) {
			http.StripPrefix("/api/v1", s.apiMux).ServeHTTP(w, r)
			return
		}
		principal, ok := s.authenticate(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "missing or invalid bearer token")
			return
		}
		if !s.authorizeAPI(r, principal) {
			writeError(w, http.StatusForbidden, "insufficient permissions")
			return
		}
		ctx := context.WithValue(r.Context(), authContextKey{}, principal)
		http.StripPrefix("/api/v1", s.apiMux).ServeHTTP(w, r.WithContext(ctx))
		return
	}
	s.servePublic(w, r)
}

func (s *Server) StartJobs(ctx context.Context) {
	workers := s.cfg.Limits.MaxActiveTransfers
	if workers <= 0 {
		workers = 1
	}
	for i := 0; i < workers; i++ {
		go s.jobLoop(ctx, i+1)
	}
}

func (s *Server) routes() {
	s.apiMux.HandleFunc("POST /projects", s.handleCreateProject)
	s.apiMux.HandleFunc("POST /auth/invites", s.handleCreateInvite)
	s.apiMux.HandleFunc("POST /auth/accept-invite", s.handleAcceptInvite)
	s.apiMux.HandleFunc("GET /auth/me", s.handleAuthMe)
	s.apiMux.HandleFunc("GET /users", s.handleListUsers)
	s.apiMux.HandleFunc("POST /users/{id}/tokens", s.handleCreateUserToken)
	s.apiMux.HandleFunc("DELETE /tokens/{id}", s.handleRevokeToken)
	s.apiMux.HandleFunc("POST /preflight/upload", s.handlePreflightUpload)
	s.apiMux.HandleFunc("POST /preflight/site-deploy", s.handlePreflightSiteDeploy)
	s.apiMux.HandleFunc("GET /doctor", s.handleDoctor)
	s.apiMux.HandleFunc("POST /init/resource-libraries", s.handleInitResourceLibraries)
	s.apiMux.HandleFunc("GET /init/jobs/{id}", s.handleGetInitJob)
	s.apiMux.HandleFunc("GET /resource-libraries/status", s.handleResourceLibraryStatus)
	s.apiMux.HandleFunc("POST /resource-libraries/health-check", s.handleResourceLibraryHealthCheck)
	s.apiMux.HandleFunc("POST /resource-libraries/e2e-probe", s.handleResourceLibraryE2EProbe)
	s.apiMux.HandleFunc("GET /routing-policies/status", s.handleRoutingPolicyStatus)
	s.apiMux.HandleFunc("POST /asset-buckets", s.handleCreateAssetBucket)
	s.apiMux.HandleFunc("GET /asset-buckets", s.handleListAssetBuckets)
	s.apiMux.HandleFunc("GET /asset-buckets/{slug}", s.handleGetAssetBucket)
	s.apiMux.HandleFunc("GET /asset-buckets/{slug}/doctor", s.handleCDNDoctor)
	s.apiMux.HandleFunc("DELETE /asset-buckets/{slug}", s.handleDeleteAssetBucket)
	s.apiMux.HandleFunc("POST /asset-buckets/{slug}/init", s.handleInitAssetBucket)
	s.apiMux.HandleFunc("POST /asset-buckets/{slug}/purge", s.handlePurgeAssetBucketCache)
	s.apiMux.HandleFunc("POST /asset-buckets/{slug}/warmup", s.handleWarmupAssetBucket)
	s.apiMux.HandleFunc("POST /asset-buckets/{slug}/replicas/refresh", s.handleRefreshAssetBucketReplicas)
	s.apiMux.HandleFunc("POST /asset-buckets/{slug}/objects", s.handleUploadBucketObject)
	s.apiMux.HandleFunc("GET /asset-buckets/{slug}/objects", s.handleListBucketObjects)
	s.apiMux.HandleFunc("DELETE /asset-buckets/{slug}/objects", s.handleDeleteBucketObject)
	s.apiMux.HandleFunc("POST /assets", s.handleUploadAsset)
	s.apiMux.HandleFunc("GET /sites", s.handleListSites)
	s.apiMux.HandleFunc("POST /sites", s.handleCreateSite)
	s.apiMux.HandleFunc("DELETE /sites/{id}", s.handleDeleteSite)
	s.apiMux.HandleFunc("POST /sites/{id}/offline", s.handleOfflineSite)
	s.apiMux.HandleFunc("POST /sites/{id}/online", s.handleOnlineSite)
	s.apiMux.HandleFunc("POST /sites/{id}/domains", s.handleBindSiteDomains)
	s.apiMux.HandleFunc("POST /sites/{id}/dns", s.handleSyncSiteDNS)
	s.apiMux.HandleFunc("POST /sites/{id}/worker-routes", s.handleSyncSiteWorkerRoutes)
	s.apiMux.HandleFunc("POST /sites/{id}/purge", s.handlePurgeSiteCache)
	s.apiMux.HandleFunc("GET /sites/{id}/deployment-target", s.handleResolveSiteDeploymentTarget)
	s.apiMux.HandleFunc("GET /domains/{host}/status", s.handleDomainStatus)
	s.apiMux.HandleFunc("GET /cloudflare/status", s.handleCloudflareStatus)
	s.apiMux.HandleFunc("GET /ipfs/status", s.handleIPFSStatus)
	s.apiMux.HandleFunc("POST /ipfs/pins/refresh", s.handleRefreshIPFSPins)
	s.apiMux.HandleFunc("POST /cloudflare/r2/sync", s.handleSyncCloudflareR2)
	s.apiMux.HandleFunc("POST /cloudflare/r2/provision", s.handleProvisionCloudflareR2)
	s.apiMux.HandleFunc("POST /cloudflare/r2/credentials", s.handleCreateCloudflareR2Credentials)
	s.apiMux.HandleFunc("POST /sites/{id}/deployments", s.handleCreateSiteDeployment)
	s.apiMux.HandleFunc("POST /sites/{id}/cloudflare-static/deployments", s.handleRecordCloudflareStaticDeployment)
	s.apiMux.HandleFunc("GET /sites/{id}/deployments", s.handleListSiteDeployments)
	s.apiMux.HandleFunc("GET /sites/{id}/deployments/{deployment}", s.handleGetSiteDeployment)
	s.apiMux.HandleFunc("GET /sites/{id}/deployments/{deployment}/edge-manifest", s.handleExportSiteEdgeManifest)
	s.apiMux.HandleFunc("POST /sites/{id}/deployments/{deployment}/edge-manifest/publish", s.handlePublishSiteEdgeManifest)
	s.apiMux.HandleFunc("GET /sites/{id}/route-explain", s.handleExplainSiteRoute)
	s.apiMux.HandleFunc("GET /sites/{id}/doctor", s.handleSiteDoctor)
	s.apiMux.HandleFunc("POST /sites/{id}/deployments/{deployment}/promote", s.handlePromoteSiteDeployment)
	s.apiMux.HandleFunc("POST /sites/{id}/deployments/{deployment}/purge", s.handlePurgeSiteDeploymentCache)
	s.apiMux.HandleFunc("DELETE /sites/{id}/deployments/{deployment}", s.handleDeleteSiteDeployment)
	s.apiMux.HandleFunc("POST /sites/{id}/gc", s.handleSiteGC)
	s.apiMux.HandleFunc("POST /gc", s.handleManualGC)
	s.apiMux.HandleFunc("GET /jobs/{id}", s.handleGetJob)
	s.apiMux.HandleFunc("GET /objects/{id}/replicas", s.handleObjectReplicas)
	s.apiMux.HandleFunc("POST /objects/{id}/replicas/refresh", s.handleRefreshObjectReplicas)
	s.apiMux.HandleFunc("POST /objects/{id}/replicas/repair", s.handleRepairObjectReplicas)
	s.apiMux.HandleFunc("POST /cache/purge", s.handlePurgeCache)
}

func (s *Server) getAssetBucketForAPI(w http.ResponseWriter, r *http.Request, slug string) (*model.AssetBucket, bool) {
	bucket, err := s.db.GetAssetBucket(r.Context(), slug)
	if err != nil {
		if db.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "bucket not found")
			return nil, false
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return nil, false
	}
	if !principalCanAccessWorkspace(currentPrincipal(r.Context()), bucket.WorkspaceID) {
		writeError(w, http.StatusNotFound, "bucket not found")
		return nil, false
	}
	return bucket, true
}

func (s *Server) getSiteForAPI(w http.ResponseWriter, r *http.Request, siteID string) (*model.Site, bool) {
	site, err := s.db.GetSite(r.Context(), siteID)
	if err != nil {
		writeError(w, http.StatusNotFound, "site not found")
		return nil, false
	}
	if !principalCanAccessWorkspace(currentPrincipal(r.Context()), site.WorkspaceID) {
		writeError(w, http.StatusNotFound, "site not found")
		return nil, false
	}
	return site, true
}

func (s *Server) ensureSiteAccessIfExists(w http.ResponseWriter, r *http.Request, siteID string) bool {
	site, err := s.db.GetSite(r.Context(), siteID)
	if errors.Is(err, sql.ErrNoRows) {
		return true
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return false
	}
	if !principalCanAccessWorkspace(currentPrincipal(r.Context()), site.WorkspaceID) {
		writeError(w, http.StatusNotFound, "site not found")
		return false
	}
	return true
}

const (
	defaultPreviewSiteFiles    = 300
	defaultProductionSiteFiles = 1000
	siteConfigFile             = "supercdn.site.json"
	overclockWarning           = "overclock mode is enabled: configured size, capacity, file-count, daily-upload, health, and transfer-slot limits are ignored; this can cause unpredictable or catastrophic results"
)

type siteRules struct {
	Mode      string             `json:"mode,omitempty"`
	Headers   []siteHeaderRule   `json:"headers,omitempty"`
	Delivery  []siteDeliveryRule `json:"delivery,omitempty"`
	Redirects []siteRedirectRule `json:"redirects,omitempty"`
	Rewrites  []siteRewriteRule  `json:"rewrites,omitempty"`
	NotFound  string             `json:"not_found,omitempty"`
}

type siteHeaderRule struct {
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
}

type siteRedirectRule struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Status int    `json:"status"`
}

type siteRewriteRule struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type siteDeliveryRule struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
}

func (s *Server) withTransferSlot(ctx context.Context, fn func() error) error {
	if s.overclockMode() {
		return fn()
	}
	select {
	case s.transferSem <- struct{}{}:
		defer func() { <-s.transferSem }()
	case <-ctx.Done():
		return ctx.Err()
	}
	return fn()
}

func newDeploymentID() string {
	return "dpl-" + strconv.FormatInt(time.Now().UTC().UnixNano(), 36)
}

func cleanDeploymentID(v string) string {
	v = strings.TrimSpace(strings.ToLower(v))
	var b strings.Builder
	for _, r := range v {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), "-_")
}

func cleanID(v string) string {
	v = strings.TrimSpace(strings.ToLower(v))
	v = strings.ReplaceAll(v, " ", "-")
	var b strings.Builder
	for _, r := range v {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == ':' {
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), "-_")
}

func cleanBucketSlug(v string) string {
	v = strings.TrimSpace(strings.ToLower(v))
	v = strings.ReplaceAll(v, " ", "-")
	var b strings.Builder
	for _, r := range v {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), "-_")
}

func cleanDomainLabel(v string) string {
	v = strings.TrimSpace(strings.ToLower(v))
	var b strings.Builder
	lastHyphen := false
	for _, r := range v {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastHyphen = false
			continue
		}
		if !lastHyphen {
			b.WriteRune('-')
			lastHyphen = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 63 {
		out = strings.Trim(out[:63], "-")
	}
	return out
}

func cleanHost(v string) string {
	v = strings.TrimSpace(strings.ToLower(v))
	if host, _, err := net.SplitHostPort(v); err == nil {
		v = host
	}
	return strings.TrimSuffix(v, ".")
}

func publicURLHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		parsed, err = url.Parse("https://" + raw)
		if err != nil {
			return ""
		}
	}
	return cleanHost(parsed.Hostname())
}

func escapeURLPath(v string) string {
	v = strings.Trim(strings.ReplaceAll(v, "\\", "/"), "/")
	if v == "" {
		return ""
	}
	parts := strings.Split(v, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}

func elapsedMS(start time.Time) int64 {
	return time.Since(start).Milliseconds()
}

func validDomainName(host string) bool {
	if host == "" || len(host) > 253 || strings.Contains(host, "..") || strings.Contains(host, "*") {
		return false
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, r := range label {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func domainParent(host string) string {
	parts := strings.Split(cleanHost(host), ".")
	if len(parts) <= 1 {
		return ""
	}
	return strings.Join(parts[1:], ".")
}

func randomDomainPart() (string, error) {
	var raw [5]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func normalizeSiteEnvironment(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case model.SiteEnvironmentProduction, "prod":
		return model.SiteEnvironmentProduction
	case model.SiteEnvironmentPreview, "":
		return model.SiteEnvironmentPreview
	default:
		return ""
	}
}

func normalizeSiteMode(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case "standard", "spa":
		return v
	default:
		return ""
	}
}

func normalizeDeploymentTarget(v string) (string, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return "", nil
	}
	switch v {
	case "origin", "go_origin", model.SiteDeploymentTargetOriginAssisted:
		return model.SiteDeploymentTargetOriginAssisted, nil
	case "cloudflare", "cloudflare_static", "workers_static", "workers_assets", "pages":
		return model.SiteDeploymentTargetCloudflareStatic, nil
	case "hybrid", "hybrid_edge", "edge":
		return model.SiteDeploymentTargetHybridEdge, nil
	default:
		return "", fmt.Errorf("deployment_target must be origin_assisted, cloudflare_static or hybrid_edge")
	}
}

func defaultDeploymentTarget(profile config.RouteProfile) string {
	if profile.DeploymentTarget != "" {
		return profile.DeploymentTarget
	}
	return model.SiteDeploymentTargetOriginAssisted
}

func validateResourceFailoverProfile(profileName string, profile config.RouteProfile) error {
	if len(routeProfileFailoverTargets(profile)) < 2 {
		return fmt.Errorf("resource_failover for route_profile %q requires primary plus at least one backup resource library", profileName)
	}
	return nil
}

func routeProfileFailoverTargets(profile config.RouteProfile) []string {
	seen := map[string]bool{}
	var out []string
	for _, target := range append([]string{profile.Primary}, profile.Backups...) {
		target = strings.TrimSpace(target)
		if target == "" || seen[target] {
			continue
		}
		seen[target] = true
		out = append(out, target)
	}
	return out
}

func parseFormBool(r *http.Request, key string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(r.FormValue(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean", key)
	}
	return value, nil
}

func mimeByName(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".json":
		return "application/json"
	case ".svg":
		return "image/svg+xml"
	case ".wasm":
		return "application/wasm"
	default:
		return ""
	}
}

func inSet(value string, values ...string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func queryBool(r *http.Request, key string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean", key)
	}
	return value, nil
}

func isHTTPURL(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://")
}

func directLocatorURL(value string) string {
	value = strings.TrimSpace(value)
	if isHTTPURL(value) {
		return value
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "resource-library" {
		return ""
	}
	inner := u.Query().Get("locator")
	if isHTTPURL(inner) {
		return inner
	}
	return ""
}

func (s *Server) overclockMode() bool {
	return s != nil && s.cfg != nil && s.cfg.Limits.OverclockMode
}

func (s *Server) withOverclockWarning(view map[string]any) map[string]any {
	if !s.overclockMode() {
		return view
	}
	view["overclock_mode"] = true
	warnings, _ := view["warnings"].([]string)
	view["warnings"] = append(warnings, overclockWarning)
	return view
}

func jobView(job *model.Job) map[string]any {
	view := map[string]any{
		"id":         job.ID,
		"type":       job.Type,
		"status":     job.Status,
		"attempts":   job.Attempts,
		"last_error": job.LastError,
		"created_at": job.CreatedAt,
		"updated_at": job.UpdatedAt,
	}
	if job.Payload != "" {
		var payload any
		if json.Unmarshal([]byte(job.Payload), &payload) == nil {
			view["payload"] = payload
		} else {
			view["payload"] = job.Payload
		}
	}
	if job.Result != "" {
		var result any
		if json.Unmarshal([]byte(job.Result), &result) == nil {
			view["result"] = result
		} else {
			view["result"] = job.Result
		}
	}
	return view
}

func sitePurgePathsForFile(filePath string) []string {
	clean := strings.TrimPrefix(path.Clean("/"+strings.TrimSpace(filePath)), "/")
	if clean == "." || clean == "" {
		return nil
	}
	urlPath := "/" + clean
	out := []string{urlPath}
	if clean == "index.html" {
		out = append([]string{"/"}, out...)
	} else if strings.HasSuffix(urlPath, "/index.html") {
		out = append(out, strings.TrimSuffix(urlPath, "index.html"))
	}
	return out
}

func cleanDNSTarget(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if parsed, err := url.Parse(value); err == nil && parsed.Hostname() != "" {
		value = parsed.Hostname()
	}
	value = strings.TrimSuffix(strings.TrimSpace(value), ".")
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	if ip := net.ParseIP(value); ip != nil {
		return ip.String()
	}
	return cleanHost(value)
}

func inferDNSRecordType(target string) string {
	ip := net.ParseIP(target)
	if ip == nil {
		return "CNAME"
	}
	if ip.To4() != nil {
		return "A"
	}
	return "AAAA"
}

func validateDNSRecordTarget(recordType, target string) error {
	switch strings.ToUpper(strings.TrimSpace(recordType)) {
	case "A":
		ip := net.ParseIP(target)
		if ip == nil || ip.To4() == nil {
			return fmt.Errorf("A record target must be an IPv4 address")
		}
	case "AAAA":
		ip := net.ParseIP(target)
		if ip == nil || ip.To4() != nil {
			return fmt.Errorf("AAAA record target must be an IPv6 address")
		}
	case "CNAME":
		if net.ParseIP(target) != nil || !validDomainName(target) {
			return fmt.Errorf("CNAME target must be a domain name")
		}
	default:
		return fmt.Errorf("dns record type must be A, AAAA or CNAME")
	}
	return nil
}

func uniqueStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func boolPtrValue(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func boolPtr(value bool) *bool {
	return &value
}

func mergeDomains(groups ...[]string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, group := range groups {
		for _, domain := range group {
			domain = cleanHost(domain)
			if domain == "" || seen[domain] {
				continue
			}
			seen[domain] = true
			out = append(out, domain)
		}
	}
	return out
}

func closeErr(c io.Closer, previous error) error {
	err := c.Close()
	if previous != nil {
		return previous
	}
	return err
}

func elapsedSince(start time.Time) int64 {
	return time.Since(start).Milliseconds()
}
