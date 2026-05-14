package server

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"supercdn/internal/cloudflare"
	"supercdn/internal/config"
	"supercdn/internal/db"
	"supercdn/internal/model"
	"supercdn/internal/siteinspect"
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

type authContextKey struct{}

type authPrincipal struct {
	Root        bool   `json:"root"`
	UserID      int64  `json:"user_id,omitempty"`
	UserName    string `json:"user_name,omitempty"`
	WorkspaceID string `json:"workspace_id"`
	Role        string `json:"role"`
	TokenID     string `json:"token_id,omitempty"`
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

func (s *Server) publicAPI(r *http.Request) bool {
	return r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/accept-invite"
}

func (s *Server) authenticate(r *http.Request) (authPrincipal, bool) {
	raw := strings.TrimSpace(r.Header.Get("Authorization"))
	token, ok := strings.CutPrefix(raw, "Bearer ")
	if !ok || token == "" {
		return authPrincipal{}, false
	}
	if s.cfg.Server.AdminToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.Server.AdminToken)) == 1 {
		return authPrincipal{Root: true, WorkspaceID: model.DefaultWorkspaceID, Role: model.RoleOwner}, true
	}
	principal, err := s.db.TokenPrincipalByHash(r.Context(), hashSecret(token))
	if err != nil {
		return authPrincipal{}, false
	}
	_ = s.db.TouchAPIToken(r.Context(), principal.Token.ID)
	return authPrincipal{
		UserID:      principal.User.ID,
		UserName:    principal.User.Name,
		WorkspaceID: principal.Token.WorkspaceID,
		Role:        principal.Role,
		TokenID:     principal.Token.ID,
	}, true
}

func (s *Server) authorizeAPI(r *http.Request, principal authPrincipal) bool {
	if principal.Root {
		return true
	}
	apiPath := strings.TrimPrefix(r.URL.Path, "/api/v1")
	if apiPath == "/auth/me" {
		return r.Method == http.MethodGet
	}
	if apiPath == "/auth/invites" || apiPath == "/users" || strings.HasPrefix(apiPath, "/users/") {
		return principal.Role == model.RoleOwner
	}
	if strings.HasPrefix(apiPath, "/tokens/") {
		return r.Method == http.MethodDelete
	}
	if s.rootOnlyAPI(apiPath) {
		return false
	}
	if strings.Contains(apiPath, "/edge-manifest") && principal.Role == model.RoleViewer {
		return false
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	return principal.Role == model.RoleOwner || principal.Role == model.RoleMaintainer
}

func (s *Server) rootOnlyAPI(apiPath string) bool {
	return strings.HasPrefix(apiPath, "/init/") ||
		strings.HasPrefix(apiPath, "/resource-libraries/") ||
		strings.HasPrefix(apiPath, "/cloudflare/") ||
		strings.HasPrefix(apiPath, "/ipfs/") ||
		strings.HasPrefix(apiPath, "/jobs/") ||
		strings.HasPrefix(apiPath, "/objects/") ||
		apiPath == "/gc" ||
		apiPath == "/cache/purge" ||
		strings.Contains(apiPath, "/dns") ||
		strings.Contains(apiPath, "/worker-routes")
}

func currentPrincipal(ctx context.Context) authPrincipal {
	if principal, ok := ctx.Value(authContextKey{}).(authPrincipal); ok {
		return principal
	}
	return authPrincipal{Root: true, WorkspaceID: model.DefaultWorkspaceID, Role: model.RoleOwner}
}

func principalCanAccessWorkspace(principal authPrincipal, workspaceID string) bool {
	if principal.Root {
		return true
	}
	return workspaceID != "" && workspaceID == principal.WorkspaceID
}

func hashSecret(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func newSecret(prefix string) (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(raw[:]), nil
}

func newTokenID(prefix string) (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(raw[:]), nil
}

func validRole(role string) bool {
	switch role {
	case model.RoleOwner, model.RoleMaintainer, model.RoleViewer:
		return true
	default:
		return false
	}
}

func workspaceForContext(ctx context.Context) string {
	workspaceID := currentPrincipal(ctx).WorkspaceID
	if workspaceID == "" {
		return model.DefaultWorkspaceID
	}
	return workspaceID
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

func (s *Server) routingPolicyStatusView(ctx context.Context, policy config.RoutingPolicy) routingPolicyStatusView {
	view := routingPolicyStatusView{
		Name:               policy.Name,
		Mode:               policy.Mode,
		DefaultRegionGroup: policy.DefaultRegionGroup,
		SourceCount:        len(policy.Sources),
	}
	if len(policy.Sources) < 2 {
		view.Errors = append(view.Errors, "routing policy requires at least two sources")
	}
	for _, source := range policy.Sources {
		item := routingPolicySourceStatusView{
			Target:       source.Target,
			RegionGroup:  source.RegionGroup,
			Weight:       source.Weight,
			Priority:     source.Priority,
			FallbackOnly: source.FallbackOnly,
			Status:       "configured",
		}
		if store, ok := s.stores.Get(source.Target); ok {
			item.TargetType = store.Type()
		} else {
			item.Status = "missing"
			item.Error = "storage target is not configured"
			view.Errors = append(view.Errors, source.Target+": "+item.Error)
		}
		if health, ok := s.routingPolicySourceHealth(ctx, source.Target); ok {
			item.Health = &health
			if health.Status != storage.HealthStatusOK {
				item.Status = health.Status
				item.Error = firstNonEmpty(health.LastError, health.Status)
			}
		}
		view.Sources = append(view.Sources, item)
	}
	return view
}

func (s *Server) routingPolicySourceHealth(ctx context.Context, target string) (model.ResourceLibraryHealth, bool) {
	config, ok := s.resourceLibraryConfig(target)
	if !ok || len(config.Bindings) == 0 {
		return model.ResourceLibraryHealth{}, false
	}
	health, err := s.db.GetResourceLibraryHealth(ctx, target, bindingConfigName(config.Bindings[0], 0))
	if err != nil || health == nil {
		return model.ResourceLibraryHealth{}, false
	}
	return *health, true
}

func (s *Server) routingPolicyForProfile(name, profileName string, profile config.RouteProfile) (config.RoutingPolicy, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return config.RoutingPolicy{}, fmt.Errorf("routing_policy is required")
	}
	policy, ok := s.cfg.RoutingPolicy(name)
	if !ok {
		return config.RoutingPolicy{}, fmt.Errorf("unknown routing_policy %q", name)
	}
	if len(policy.Sources) < 2 {
		return config.RoutingPolicy{}, fmt.Errorf("routing_policy %q requires at least two sources", name)
	}
	allowed := map[string]bool{}
	if profile.Primary != "" {
		allowed[profile.Primary] = true
	}
	for _, target := range profile.Backups {
		if target != "" {
			allowed[target] = true
		}
	}
	for _, source := range policy.Sources {
		if !allowed[source.Target] {
			return config.RoutingPolicy{}, fmt.Errorf("routing_policy %q source %q is not included in route_profile %q", name, source.Target, profileName)
		}
	}
	return policy, nil
}

func (s *Server) preflightProfile(ctx context.Context, profileName string, profile config.RouteProfile, req preflightRequest) (map[string]any, error) {
	if req.BatchFileCount <= 0 {
		req.BatchFileCount = 1
	}
	if req.LargestFileSize <= 0 {
		req.LargestFileSize = req.TotalSize
	}
	if req.TotalSize <= 0 {
		req.TotalSize = req.LargestFileSize
	}
	if req.TotalSize < 0 || req.LargestFileSize < 0 {
		return nil, fmt.Errorf("upload sizes must be non-negative")
	}
	if !s.overclockMode() && s.cfg.Limits.MaxUploadBytes > 0 && req.TotalSize > s.cfg.Limits.MaxUploadBytes {
		return nil, fmt.Errorf("server max_upload_bytes is %d bytes, upload total got %d bytes", s.cfg.Limits.MaxUploadBytes, req.TotalSize)
	}
	primary, ok := s.stores.Get(profile.Primary)
	if !ok {
		return nil, fmt.Errorf("primary storage %q is not configured", profile.Primary)
	}
	if !s.overclockMode() {
		if err := s.checkRecentResourceLibraryHealth(ctx, profile.Primary); err != nil {
			return nil, err
		}
	}
	result := s.withOverclockWarning(map[string]any{
		"ok":                true,
		"route_profile":     profileName,
		"primary_target":    profile.Primary,
		"total_size":        req.TotalSize,
		"largest_file_size": req.LargestFileSize,
		"batch_file_count":  req.BatchFileCount,
	})
	if s.overclockMode() {
		result["limits_ignored"] = []string{
			"max_upload_bytes",
			"default_max_site_files",
			"deployment_file_count",
			"resource_health",
			"resource_library_capacity",
			"resource_library_file_size",
			"resource_library_batch_files",
			"resource_library_daily_upload",
			"asset_bucket_capacity",
			"asset_bucket_file_size",
			"asset_bucket_allowed_types",
			"transfer_slots",
		}
	}
	if preflight, ok := primary.(storage.PreflightStore); ok {
		preflightResult, err := preflight.PreflightPut(ctx, storage.PreflightOptions{
			TotalSize:       req.TotalSize,
			LargestFileSize: req.LargestFileSize,
			BatchFileCount:  req.BatchFileCount,
			IgnoreLimits:    s.overclockMode(),
		})
		if err != nil {
			return nil, err
		}
		result["primary"] = preflightResult
	} else {
		result["primary"] = storage.PreflightResult{
			Target:        primary.Name(),
			TargetType:    primary.Type(),
			OverclockMode: s.overclockMode(),
		}
	}
	return result, nil
}

func (s *Server) checkSiteFileCount(count int) error {
	if count <= 0 || s.cfg.Limits.DefaultMaxSiteFiles <= 0 || s.overclockMode() {
		return nil
	}
	if count > s.cfg.Limits.DefaultMaxSiteFiles {
		return fmt.Errorf("site deploy allows at most %d files in the first version, got %d; package larger sites before uploading", s.cfg.Limits.DefaultMaxSiteFiles, count)
	}
	return nil
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

func (s *Server) runResourceLibraryHealthCheck(ctx context.Context, library string, writeProbe bool) error {
	store, ok := s.stores.Get(library)
	if !ok {
		return fmt.Errorf("resource library %q is not configured", library)
	}
	checker, ok := store.(storage.HealthCheckStore)
	if !ok {
		return fmt.Errorf("resource library %q does not support health checks", library)
	}
	var result *storage.HealthCheckResult
	err := s.withTransferSlot(ctx, func() error {
		var checkErr error
		result, checkErr = checker.HealthCheck(ctx, storage.HealthCheckOptions{WriteProbe: writeProbe})
		return checkErr
	})
	if result != nil {
		for _, item := range result.Items {
			binding := item.BindingName
			if binding == "" {
				binding = item.Target
			}
			if _, saveErr := s.db.UpsertResourceLibraryHealth(ctx, model.ResourceLibraryHealth{
				Library:         library,
				Binding:         binding,
				BindingPath:     item.BindingPath,
				Target:          item.Target,
				TargetType:      item.TargetType,
				Status:          item.Status,
				CheckMode:       item.CheckMode,
				ListLatencyMS:   item.ListLatencyMS,
				WriteLatencyMS:  item.WriteLatencyMS,
				ReadLatencyMS:   item.ReadLatencyMS,
				DeleteLatencyMS: item.DeleteLatencyMS,
				LastError:       item.LastError,
				LastCheckedAt:   item.CheckedAt,
			}); saveErr != nil {
				return saveErr
			}
		}
	}
	return err
}

func (s *Server) resourceLibraryHealthFresh(ctx context.Context, library string, minIntervalSeconds int) bool {
	if minIntervalSeconds <= 0 {
		return false
	}
	config, ok := s.resourceLibraryConfig(library)
	if !ok || len(config.Bindings) == 0 {
		return false
	}
	cutoff := time.Now().UTC().Add(-time.Duration(minIntervalSeconds) * time.Second)
	for i, binding := range config.Bindings {
		name := bindingConfigName(binding, i)
		health, err := s.db.GetResourceLibraryHealth(ctx, library, name)
		if err != nil || health.LastCheckedAt.Before(cutoff) {
			return false
		}
	}
	return true
}

func (s *Server) resourceLibraryStatusViews(ctx context.Context, libraries []string, skipped map[string]string) ([]resourceLibraryStatusView, error) {
	healthRows, err := s.db.ResourceLibraryHealth(ctx, "")
	if err != nil {
		return nil, err
	}
	healthByKey := map[string]model.ResourceLibraryHealth{}
	for _, health := range healthRows {
		healthByKey[health.Library+"/"+health.Binding] = health
	}
	views := make([]resourceLibraryStatusView, 0, len(libraries))
	for _, name := range libraries {
		config, ok := s.resourceLibraryConfig(name)
		if !ok {
			if direct, ok := s.directStorageStatusView(ctx, name, skipped); ok {
				views = append(views, direct)
			}
			continue
		}
		view := resourceLibraryStatusView{Name: name, TargetType: "resource_library"}
		if store, ok := s.stores.Get(name); ok {
			view.TargetType = store.Type()
			view.Capabilities = storage.StoreCapabilities(store)
		} else {
			view.Capabilities = storage.StoreCapabilities(nil)
		}
		for i, binding := range config.Bindings {
			bindingName := bindingConfigName(binding, i)
			bindingView := resourceLibraryBindingView{
				Name:         bindingName,
				Path:         binding.Path,
				MountPoint:   binding.MountPoint,
				Status:       "unknown",
				Capabilities: view.Capabilities,
			}
			if store, ok := s.stores.Get(name); ok {
				if bindingCapable, ok := store.(storage.BindingCapabilityStore); ok {
					if capabilities, ok := bindingCapable.BindingCapabilities(bindingName); ok {
						bindingView.Capabilities = capabilities
					}
				}
			}
			if reason := skipped[name]; reason != "" {
				bindingView.Skipped = true
				bindingView.SkipReason = reason
			}
			if health, ok := healthByKey[name+"/"+bindingName]; ok {
				healthCopy := health
				bindingView.Status = health.Status
				bindingView.TargetType = health.TargetType
				bindingView.Health = &healthCopy
			}
			view.Bindings = append(view.Bindings, bindingView)
		}
		views = append(views, view)
	}
	return views, nil
}

func (s *Server) directStorageStatusView(_ context.Context, name string, skipped map[string]string) (resourceLibraryStatusView, bool) {
	store, ok := s.stores.Get(name)
	if !ok || !directResourceStatusStoreType(store.Type()) {
		return resourceLibraryStatusView{}, false
	}
	capabilities := storage.StoreCapabilities(store)
	binding := resourceLibraryBindingView{
		Name:         name,
		Path:         "/",
		TargetType:   store.Type(),
		Status:       "configured",
		Capabilities: capabilities,
	}
	if reason := skipped[name]; reason != "" {
		binding.Skipped = true
		binding.SkipReason = reason
	}
	return resourceLibraryStatusView{
		Name:         name,
		TargetType:   store.Type(),
		Capabilities: capabilities,
		Bindings:     []resourceLibraryBindingView{binding},
	}, true
}

func (s *Server) checkRecentResourceLibraryHealth(ctx context.Context, target string) error {
	config, ok := s.resourceLibraryConfig(target)
	if !ok || len(config.Bindings) == 0 || s.cfg.Limits.ResourceHealthMinIntervalSeconds <= 0 {
		return nil
	}
	binding := bindingConfigName(config.Bindings[0], 0)
	health, err := s.db.GetResourceLibraryHealth(ctx, target, binding)
	if err != nil {
		if db.IsNotFound(err) {
			return nil
		}
		return err
	}
	cutoff := time.Now().UTC().Add(-time.Duration(s.cfg.Limits.ResourceHealthMinIntervalSeconds) * time.Second)
	if health.Status != storage.HealthStatusOK && health.LastCheckedAt.After(cutoff) {
		return fmt.Errorf("resource library %q binding %q recent health check failed: %s", target, binding, firstNonEmpty(health.LastError, health.Status))
	}
	return nil
}

func (s *Server) recentResourceLibraryHealthFailure(ctx context.Context, target string) (string, bool) {
	config, ok := s.resourceLibraryConfig(target)
	if !ok || len(config.Bindings) == 0 || s.cfg.Limits.ResourceHealthMinIntervalSeconds <= 0 {
		return "", false
	}
	binding := bindingConfigName(config.Bindings[0], 0)
	health, err := s.db.GetResourceLibraryHealth(ctx, target, binding)
	if err != nil || health == nil || health.Status == storage.HealthStatusOK {
		return "", false
	}
	cutoff := time.Now().UTC().Add(-time.Duration(s.cfg.Limits.ResourceHealthMinIntervalSeconds) * time.Second)
	if health.LastCheckedAt.Before(cutoff) {
		return "", false
	}
	return fmt.Sprintf("binding %q is %s: %s", binding, health.Status, firstNonEmpty(health.LastError, health.Status)), true
}

func (s *Server) runResourceLibraryE2EProbe(ctx context.Context, req resourceLibraryE2EProbeRequest) (*resourceLibraryE2EProbeResult, error) {
	profileName := firstNonEmpty(req.RouteProfile, "china_all")
	profile, ok := s.cfg.Profile(profileName)
	if !ok {
		return &resourceLibraryE2EProbeResult{RouteProfile: profileName, Errors: []string{"unknown route_profile"}}, fmt.Errorf("unknown route_profile")
	}
	primary, ok := s.stores.Get(profile.Primary)
	if !ok {
		err := fmt.Errorf("primary storage %q is not configured", profile.Primary)
		return &resourceLibraryE2EProbeResult{RouteProfile: profileName, PrimaryTarget: profile.Primary, Errors: []string{err.Error()}}, err
	}
	projectID := cleanID(req.ProjectID)
	if projectID == "" {
		projectID = fmt.Sprintf("probe-%d", time.Now().UTC().UnixNano())
	}
	objectPath := req.Path
	if objectPath == "" {
		objectPath = fmt.Sprintf("assets/tmp/e2e-probe-%s.txt", time.Now().UTC().Format("20060102T150405.000000000Z"))
	}
	cleanPath, err := storage.CleanObjectPath(objectPath)
	result := &resourceLibraryE2EProbeResult{
		RouteProfile:  profileName,
		PrimaryTarget: profile.Primary,
		ProjectID:     projectID,
		ObjectPath:    cleanPath,
		Key:           cleanPath,
	}
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
		return result, err
	}
	payload := []byte("supercdn e2e probe " + time.Now().UTC().Format(time.RFC3339Nano) + "\n")
	sum := sha256.Sum256(payload)
	result.Size = int64(len(payload))
	result.SHA256 = hex.EncodeToString(sum[:])
	if _, err := s.preflightProfile(ctx, profileName, profile, preflightRequest{
		TotalSize:       result.Size,
		LargestFileSize: result.Size,
		BatchFileCount:  1,
	}); err != nil {
		result.Errors = append(result.Errors, err.Error())
		return result, err
	}
	tmp, err := os.CreateTemp(s.staging, "e2e-probe-*")
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
		return result, err
	}
	tmpPath := tmp.Name()
	_, err = tmp.Write(payload)
	if err := closeErr(tmp, err); err != nil {
		_ = os.Remove(tmpPath)
		result.Errors = append(result.Errors, err.Error())
		return result, err
	}
	defer os.Remove(tmpPath)
	if _, err := s.db.CreateProject(ctx, projectID); err != nil {
		result.Errors = append(result.Errors, err.Error())
		return result, err
	}
	probeProfile := profile
	probeProfile.Backups = nil
	start := time.Now()
	obj, _, err := s.putObjectFromFile(ctx, putObjectInput{
		ProjectID:      projectID,
		ObjectPath:     cleanPath,
		Key:            cleanPath,
		Profile:        probeProfile,
		ProfileName:    profileName,
		CacheControl:   "no-store",
		ContentType:    "text/plain; charset=utf-8",
		FilePath:       tmpPath,
		FileName:       path.Base(cleanPath),
		Size:           result.Size,
		SHA256:         result.SHA256,
		BatchFileCount: 1,
	})
	result.UploadLatencyMS = elapsedSince(start)
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
		_ = s.db.DeleteProject(ctx, projectID)
		return result, err
	}
	result.ObjectID = obj.ID
	defer func() {
		if req.Keep {
			return
		}
		if err := primary.Delete(context.WithoutCancel(ctx), cleanPath); err != nil {
			result.CleanupRemote = "failed: " + err.Error()
			result.Errors = append(result.Errors, result.CleanupRemote)
		} else {
			result.CleanupRemote = "deleted"
		}
		if err := s.db.DeleteObject(context.WithoutCancel(ctx), obj.ID); err != nil {
			result.CleanupDB = "failed object: " + err.Error()
			result.Errors = append(result.Errors, result.CleanupDB)
			return
		}
		if err := s.db.DeleteProject(context.WithoutCancel(ctx), projectID); err != nil {
			result.CleanupDB = "failed project: " + err.Error()
			result.Errors = append(result.Errors, result.CleanupDB)
			return
		}
		result.CleanupDB = "deleted"
	}()
	start = time.Now()
	status, headers, body, err := s.readProbeObject(ctx, projectID, cleanPath)
	result.ReadLatencyMS = elapsedSince(start)
	result.HTTPStatus = status
	result.ETag = headers.Get("ETag")
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
		return result, err
	}
	if status != http.StatusOK {
		err := fmt.Errorf("public read returned status %d", status)
		result.Errors = append(result.Errors, err.Error())
		return result, err
	}
	if !bytes.Equal(body, payload) {
		err := fmt.Errorf("public read payload mismatch")
		result.Errors = append(result.Errors, err.Error())
		return result, err
	}
	result.OK = true
	return result, nil
}

func (s *Server) readProbeObject(ctx context.Context, projectID, objectPath string) (int, http.Header, []byte, error) {
	req := httptest.NewRequest(http.MethodGet, "/o/"+projectID+"/"+objectPath, nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	resp := rec.Result()
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, resp.Header, nil, err
	}
	return resp.StatusCode, resp.Header, raw, nil
}

func (s *Server) resourceLibraryConfig(name string) (config.ResourceLibraryConfig, bool) {
	for _, library := range s.cfg.ResourceLibraries {
		if library.Name == name {
			return library, true
		}
	}
	if library, ok := s.cfg.CloudflareLibraryByName(name); ok {
		return cloudflareLibraryStatusConfig(library), true
	}
	return config.ResourceLibraryConfig{}, false
}

func cloudflareLibraryStatusConfig(library config.CloudflareLibraryConfig) config.ResourceLibraryConfig {
	bindings := make([]config.ResourceLibraryBinding, 0, len(library.Bindings))
	for _, binding := range library.Bindings {
		bindings = append(bindings, config.ResourceLibraryBinding{
			Name:        binding.Name,
			MountPoint:  binding.Account,
			Path:        binding.Path,
			Constraints: binding.Constraints,
		})
	}
	return config.ResourceLibraryConfig{Name: library.Name, Policy: library.Policy, Bindings: bindings}
}

func bindingConfigName(binding config.ResourceLibraryBinding, index int) string {
	if binding.Name != "" {
		return binding.Name
	}
	return fmt.Sprintf("%s_%d", binding.MountPoint, index+1)
}

func optionalLibrary(name string) []string {
	if name == "" {
		return nil
	}
	return []string{name}
}

func directResourceStatusStoreType(targetType string) bool {
	switch strings.ToLower(strings.TrimSpace(targetType)) {
	case "alist", "pinata", "r2":
		return true
	default:
		return false
	}
}

func normalizeInitDirectories(dirs []string) ([]string, error) {
	if len(dirs) == 0 {
		dirs = defaultResourceLibraryInitDirs
	}
	out := make([]string, 0, len(dirs))
	seen := map[string]bool{}
	for _, dir := range dirs {
		clean, err := storage.CleanDirectoryPath(dir)
		if err != nil {
			return nil, fmt.Errorf("invalid init directory %q: %w", dir, err)
		}
		if clean == "" || seen[clean] {
			continue
		}
		seen[clean] = true
		out = append(out, clean)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("at least one init directory is required")
	}
	return out, nil
}

func (s *Server) resolveResourceLibraries(requested []string) ([]string, error) {
	configured := map[string]bool{}
	for _, library := range s.cfg.ResourceLibraries {
		configured[library.Name] = true
	}
	for _, library := range s.cfg.CloudflareLibrariesEffective() {
		if s.cfg.CloudflareLibraryHasStorage(library) {
			configured[library.Name] = true
		}
	}
	if len(configured) == 0 {
		return nil, fmt.Errorf("no resource libraries are configured")
	}
	if len(requested) == 0 {
		names := make([]string, 0, len(configured))
		for _, library := range s.cfg.ResourceLibraries {
			names = append(names, library.Name)
		}
		for _, library := range s.cfg.CloudflareLibrariesEffective() {
			if s.cfg.CloudflareLibraryHasStorage(library) {
				names = append(names, library.Name)
			}
		}
		return names, nil
	}
	names := make([]string, 0, len(requested))
	seen := map[string]bool{}
	for _, name := range requested {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		if !configured[name] {
			return nil, fmt.Errorf("unknown resource library %q", name)
		}
		seen[name] = true
		names = append(names, name)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("at least one resource library is required")
	}
	return names, nil
}

func (s *Server) resolveResourceStatusTargets(requested []string) ([]string, error) {
	configured := map[string]bool{}
	for _, library := range s.cfg.ResourceLibraries {
		configured[library.Name] = true
	}
	for _, library := range s.cfg.CloudflareLibrariesEffective() {
		if s.cfg.CloudflareLibraryHasStorage(library) {
			configured[library.Name] = true
		}
	}
	for _, name := range s.stores.Names() {
		store, ok := s.stores.Get(name)
		if ok && directResourceStatusStoreType(store.Type()) {
			configured[name] = true
		}
	}
	if len(configured) == 0 {
		return nil, fmt.Errorf("no resource libraries or resource-capable storage targets are configured")
	}
	if len(requested) == 0 {
		names := make([]string, 0, len(configured))
		seen := map[string]bool{}
		add := func(name string) {
			name = strings.TrimSpace(name)
			if name == "" || seen[name] || !configured[name] {
				return
			}
			seen[name] = true
			names = append(names, name)
		}
		for _, library := range s.cfg.ResourceLibraries {
			add(library.Name)
		}
		for _, library := range s.cfg.CloudflareLibrariesEffective() {
			if s.cfg.CloudflareLibraryHasStorage(library) {
				add(library.Name)
			}
		}
		directNames := s.stores.Names()
		sort.Strings(directNames)
		for _, name := range directNames {
			if store, ok := s.stores.Get(name); ok && directResourceStatusStoreType(store.Type()) {
				add(name)
			}
		}
		return names, nil
	}
	names := make([]string, 0, len(requested))
	seen := map[string]bool{}
	for _, name := range requested {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		if !configured[name] {
			return nil, fmt.Errorf("unknown resource library or resource-capable storage target %q", name)
		}
		seen[name] = true
		names = append(names, name)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("at least one resource library or resource-capable storage target is required")
	}
	return names, nil
}

func (s *Server) initResourceLibraries(ctx context.Context, payload initResourceLibrariesPayload, dryRun bool) (*initResourceLibrariesResult, error) {
	result := &initResourceLibrariesResult{
		DryRun:      dryRun,
		Directories: payload.Directories,
	}
	markerPayload, err := json.MarshalIndent(map[string]any{
		"service":            "supercdn",
		"version":            1,
		"requested_at_utc":   payload.RequestedAtUTC,
		"initialized_at_utc": time.Now().UTC().Format(time.RFC3339Nano),
		"directories":        payload.Directories,
		"libraries":          payload.Libraries,
	}, "", "  ")
	if err != nil {
		return result, err
	}
	var firstErr error
	for _, name := range payload.Libraries {
		store, ok := s.stores.Get(name)
		if !ok {
			err := fmt.Errorf("resource library %q is not configured", name)
			if firstErr == nil {
				firstErr = err
			}
			result.Libraries = append(result.Libraries, storage.InitResult{
				Target:     name,
				TargetType: "resource_library",
				Bindings: []storage.InitBindingResult{{
					Target:     name,
					TargetType: "resource_library",
					Directories: []storage.InitPathResult{{
						Status: "error",
						Error:  err.Error(),
					}},
				}},
			})
			continue
		}
		initializer, ok := store.(storage.InitializableStore)
		if !ok {
			err := fmt.Errorf("resource library %q does not support initialization", name)
			if firstErr == nil {
				firstErr = err
			}
			result.Libraries = append(result.Libraries, storage.InitResult{
				Target:      store.Name(),
				TargetType:  store.Type(),
				Directories: []storage.InitPathResult{{Status: "error", Error: err.Error()}},
			})
			continue
		}
		var initResult *storage.InitResult
		run := func() error {
			var initErr error
			initResult, initErr = initializer.InitDirs(ctx, storage.InitOptions{
				Directories:   payload.Directories,
				MarkerPath:    payload.MarkerPath,
				MarkerPayload: markerPayload,
				DryRun:        dryRun,
			})
			return initErr
		}
		if dryRun {
			err = run()
		} else {
			err = s.withTransferSlot(ctx, run)
		}
		if initResult != nil {
			result.Libraries = append(result.Libraries, *initResult)
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return result, firstErr
}

type stagedFile struct {
	Path        string
	Size        int64
	SHA256      string
	ContentType string
}

func (s *Server) stageUpload(src io.Reader, name string) (*stagedFile, error) {
	tmp, err := os.CreateTemp(s.staging, "upload-*")
	if err != nil {
		return nil, err
	}
	defer tmp.Close()
	hash := sha256.New()
	var first bytes.Buffer
	writer := io.MultiWriter(tmp, hash)
	tee := io.TeeReader(io.LimitReader(src, 512), &first)
	n1, err := io.Copy(writer, tee)
	if err != nil {
		_ = os.Remove(tmp.Name())
		return nil, err
	}
	n2, err := io.Copy(writer, src)
	if err != nil {
		_ = os.Remove(tmp.Name())
		return nil, err
	}
	ctype := http.DetectContentType(first.Bytes())
	if strings.HasPrefix(ctype, "text/plain") {
		if byExt := mimeByName(name); byExt != "" {
			ctype = byExt
		}
	}
	return &stagedFile{
		Path:        tmp.Name(),
		Size:        n1 + n2,
		SHA256:      hex.EncodeToString(hash.Sum(nil)),
		ContentType: ctype,
	}, nil
}

type putObjectInput struct {
	ProjectID      string
	ObjectPath     string
	Key            string
	Profile        config.RouteProfile
	ProfileName    string
	CacheControl   string
	ContentType    string
	Group          string
	FilePath       string
	FileName       string
	Size           int64
	SHA256         string
	BatchFileCount int
}

func (s *Server) putObjectFromFile(ctx context.Context, in putObjectInput) (*model.Object, []model.Job, error) {
	primary, ok := s.stores.Get(in.Profile.Primary)
	if !ok {
		return nil, nil, fmt.Errorf("primary storage %q is not configured", in.Profile.Primary)
	}
	var locator string
	err := s.withTransferSlot(ctx, func() error {
		var putErr error
		locator, putErr = primary.Put(ctx, storage.PutOptions{
			Key:            in.Key,
			FilePath:       in.FilePath,
			ContentType:    in.ContentType,
			CacheControl:   in.CacheControl,
			Group:          in.Group,
			SHA256:         in.SHA256,
			Size:           in.Size,
			FileName:       in.FileName,
			BatchFileCount: in.BatchFileCount,
			IgnoreLimits:   s.overclockMode(),
		})
		return putErr
	})
	if err != nil {
		return nil, nil, fmt.Errorf("put primary %s: %w", primary.Name(), err)
	}
	obj, err := s.db.SaveObject(ctx, model.Object{
		ProjectID:     in.ProjectID,
		Path:          in.ObjectPath,
		Key:           in.Key,
		RouteProfile:  in.ProfileName,
		Size:          in.Size,
		SHA256:        in.SHA256,
		ContentType:   in.ContentType,
		CacheControl:  in.CacheControl,
		PrimaryTarget: in.Profile.Primary,
	})
	if err != nil {
		return nil, nil, err
	}
	if _, err := s.db.UpsertReplica(ctx, obj.ID, in.Profile.Primary, model.ReplicaReady, locator, ""); err != nil {
		return nil, nil, err
	}
	if err := s.recordIPFSReplica(ctx, obj.ID, in.Profile.Primary, primary, locator); err != nil {
		return nil, nil, err
	}
	var jobs []model.Job
	policy := replicationPolicyForProfile(in.Profile)
	for _, target := range routeProfileBackupTargets(in.Profile) {
		if _, ok := s.stores.Get(target); !ok {
			return nil, nil, fmt.Errorf("backup storage %q is not configured", target)
		}
		switch policy {
		case config.ReplicationPolicyPrimaryOnly:
			if _, err := s.db.UpsertReplica(ctx, obj.ID, target, model.ReplicaDeleted, "", "replication_policy primary_only"); err != nil {
				return nil, nil, err
			}
			if err := s.db.DeleteIPFSPin(ctx, obj.ID, target); err != nil {
				return nil, nil, err
			}
		case config.ReplicationPolicyRequireBackups:
			if _, err := s.db.UpsertReplica(ctx, obj.ID, target, model.ReplicaPending, "", ""); err != nil {
				return nil, nil, err
			}
			if err := s.db.DeleteIPFSPin(ctx, obj.ID, target); err != nil {
				return nil, nil, err
			}
			if err := s.replicateObject(ctx, replicatePayload{ObjectID: obj.ID, Target: target}); err != nil {
				return nil, nil, fmt.Errorf("replicate required backup %q: %w", target, err)
			}
		default:
			if _, err := s.db.UpsertReplica(ctx, obj.ID, target, model.ReplicaPending, "", ""); err != nil {
				return nil, nil, err
			}
			if err := s.db.DeleteIPFSPin(ctx, obj.ID, target); err != nil {
				return nil, nil, err
			}
			payload, _ := json.Marshal(replicatePayload{ObjectID: obj.ID, Target: target})
			job, err := s.db.CreateJob(ctx, model.JobReplicateObject, string(payload))
			if err != nil {
				return nil, nil, err
			}
			jobs = append(jobs, *job)
		}
	}
	obj, err = s.hydrateObjectIPFS(ctx, obj)
	if err != nil {
		return nil, nil, err
	}
	return obj, jobs, nil
}

func (s *Server) jobLoop(ctx context.Context, workerID int) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for {
				job, err := s.db.NextQueuedJob(ctx)
				if errors.Is(err, sql.ErrNoRows) {
					break
				}
				if err != nil {
					s.logger.Warn("load job failed", "error", err)
					break
				}
				result, err := s.runJob(ctx, job)
				if err != nil {
					retry := job.Attempts < 5
					if job.Type == model.JobDeploySite {
						retry = false
					}
					if failErr := s.db.FailJobWithResult(ctx, job.ID, err.Error(), retry, result); failErr != nil {
						s.logger.Warn("mark job failed", "worker", workerID, "job", job.ID, "error", failErr)
					}
					s.logger.Warn("job failed", "worker", workerID, "job", job.ID, "retry", retry, "error", err)
					continue
				}
				if err := s.db.FinishJobWithResult(ctx, job.ID, result); err != nil {
					s.logger.Warn("mark job done failed", "worker", workerID, "job", job.ID, "error", err)
				}
			}
		}
	}
}

func (s *Server) runJob(ctx context.Context, job *model.Job) (string, error) {
	switch job.Type {
	case model.JobReplicateObject:
		var payload replicatePayload
		if err := json.Unmarshal([]byte(job.Payload), &payload); err != nil {
			return "", err
		}
		return "", s.replicateObject(ctx, payload)
	case model.JobInitResourceLibraries:
		var payload initResourceLibrariesPayload
		if err := json.Unmarshal([]byte(job.Payload), &payload); err != nil {
			return "", err
		}
		result, err := s.initResourceLibraries(ctx, payload, false)
		raw, _ := json.Marshal(result)
		return string(raw), err
	case model.JobDeploySite:
		var payload deploySitePayload
		if err := json.Unmarshal([]byte(job.Payload), &payload); err != nil {
			return "", err
		}
		result, err := s.processSiteDeployment(ctx, payload)
		raw, _ := json.Marshal(result)
		return string(raw), err
	default:
		return "", fmt.Errorf("unknown job type %q", job.Type)
	}
}

func (s *Server) siteDomainsFromRequest(siteID string, requested []string, defaultID string, randomDefault, skipDefault bool, allocateDefault *bool) ([]string, error) {
	domains := make([]string, 0, len(requested)+1)
	seen := map[string]bool{}
	add := func(host string) error {
		host = cleanHost(host)
		if host == "" {
			return nil
		}
		if !validDomainName(host) {
			return fmt.Errorf("invalid domain %q", host)
		}
		if !seen[host] {
			seen[host] = true
			domains = append(domains, host)
		}
		return nil
	}
	shouldAllocate := s.cfg.Cloudflare.SiteDomainSuffix != "" && !skipDefault
	if allocateDefault != nil {
		shouldAllocate = *allocateDefault
	}
	if shouldAllocate {
		host, err := s.defaultSiteDomain(siteID, defaultID, randomDefault)
		if err != nil {
			return nil, err
		}
		if err := add(host); err != nil {
			return nil, err
		}
	}
	for _, host := range requested {
		if err := add(host); err != nil {
			return nil, err
		}
	}
	return domains, nil
}

func (s *Server) defaultSiteDomain(siteID, requestedID string, randomDefault bool) (string, error) {
	suffix := cleanHost(s.cfg.Cloudflare.SiteDomainSuffix)
	if suffix == "" {
		return "", fmt.Errorf("cloudflare.site_domain_suffix is not configured")
	}
	label := cleanDomainLabel(requestedID)
	if label == "" {
		label = cleanDomainLabel(siteID)
	}
	if randomDefault {
		randomPart, err := randomDomainPart()
		if err != nil {
			return "", err
		}
		if label == "" {
			label = randomPart
		} else {
			maxPrefix := 63 - len(randomPart) - 1
			if len(label) > maxPrefix {
				label = strings.Trim(label[:maxPrefix], "-")
			}
			if label == "" {
				label = randomPart
			} else {
				label = label + "-" + randomPart
			}
		}
	}
	if label == "" {
		return "", fmt.Errorf("default domain id is required")
	}
	return label + "." + suffix, nil
}

func (s *Server) defaultCloudflareStaticDomain(siteID string) (string, error) {
	root := cleanHost(s.cfg.Cloudflare.RootDomain)
	if root == "" {
		return s.defaultSiteDomain(siteID, "", false)
	}
	label := cleanDomainLabel(siteID)
	if label == "" {
		return "", fmt.Errorf("default domain id is required")
	}
	return label + "." + root, nil
}

func (s *Server) domainStatus(ctx context.Context, host string) domainStatusResponse {
	account, library, accountOK := s.cloudflareAccountForHost(host, "", "")
	cf := s.cloudflareClientForAccount(account)
	resp := domainStatusResponse{
		Host:                 host,
		CloudflareAccount:    account.Name,
		CloudflareLibrary:    library.Name,
		CloudflareConfigured: cf.Configured(),
		RootDomain:           account.RootDomain,
		SiteDomainSuffix:     account.SiteDomainSuffix,
		InManagedZone:        accountOK && accountInManagedCloudflareZone(account, host),
	}
	if site, err := s.db.SiteByHost(ctx, host); err == nil {
		resp.Bound = true
		resp.SiteID = site.ID
	} else if err != nil && !db.IsNotFound(err) {
		resp.Errors = append(resp.Errors, err.Error())
	}
	if !accountOK {
		resp.Errors = append(resp.Errors, "no matching cloudflare account for host")
		return resp
	}
	if !resp.CloudflareConfigured {
		resp.Errors = append(resp.Errors, "cloudflare zone_id/api_token not configured")
		return resp
	}
	exact, err := cf.ListDNSRecords(ctx, host)
	if err != nil {
		resp.Errors = append(resp.Errors, err.Error())
	} else {
		resp.ExactRecords = exact
	}
	for _, wildcard := range managedWildcardCandidates(account, host) {
		records, err := cf.ListDNSRecords(ctx, wildcard)
		if err != nil {
			resp.Errors = append(resp.Errors, err.Error())
			continue
		}
		resp.WildcardRecords = append(resp.WildcardRecords, records...)
	}
	return resp
}

func (s *Server) syncSiteWorkerRoutes(ctx context.Context, site *model.Site, req syncWorkerRoutesRequest) (syncWorkerRoutesResponse, error) {
	domains, err := s.siteBoundDomains(site, req.Domains)
	if err != nil {
		return syncWorkerRoutesResponse{}, err
	}
	account, library, err := s.cloudflareAccountForDomains(domains, req.CloudflareAccount, req.CloudflareLibrary)
	if err != nil {
		return syncWorkerRoutesResponse{}, err
	}
	script := firstNonEmpty(strings.TrimSpace(req.Script), account.WorkerScript, "supercdn-edge")
	patterns := make([]string, 0, len(domains))
	for _, domain := range domains {
		patterns = append(patterns, domain+"/*")
	}
	resp := syncWorkerRoutesResponse{
		SiteID:            site.ID,
		CloudflareAccount: account.Name,
		CloudflareLibrary: library.Name,
		Script:            script,
		Domains:           domains,
		Patterns:          patterns,
		DryRun:            req.DryRun,
		Force:             req.Force,
		Status:            "planned",
		Warnings:          []string{"Cloudflare Worker routes only run for proxied DNS records; DNS-only records bypass the Worker."},
	}
	cf := s.cloudflareClientForAccount(account)
	if !cf.Configured() {
		if req.DryRun {
			for _, pattern := range patterns {
				resp.Routes = append(resp.Routes, cloudflare.WorkerRouteSyncResult{
					Pattern: pattern,
					Script:  script,
					Action:  "create",
					DryRun:  true,
				})
			}
			resp.Warnings = append(resp.Warnings, "cloudflare zone_id/api_token not configured; returning local route plan only")
			return resp, nil
		}
		resp.Status = "skipped"
		resp.Errors = append(resp.Errors, "cloudflare zone_id/api_token not configured")
		return resp, nil
	}
	results, err := cf.SyncWorkerRoutes(ctx, patterns, script, cloudflare.SyncWorkerRouteOptions{DryRun: req.DryRun, Force: req.Force})
	if err != nil {
		return syncWorkerRoutesResponse{}, err
	}
	resp.Routes = results
	resp.Status = "ok"
	for _, result := range results {
		if result.Error != "" {
			resp.Status = "partial"
			resp.Errors = append(resp.Errors, result.Pattern+": "+result.Error)
		}
	}
	if req.DryRun && resp.Status == "ok" {
		resp.Status = "planned"
	}
	return resp, nil
}

func (s *Server) syncSiteDNS(ctx context.Context, site *model.Site, req syncSiteDNSRequest) (syncSiteDNSResponse, error) {
	domains, err := s.siteBoundDomains(site, req.Domains)
	if err != nil {
		return syncSiteDNSResponse{}, err
	}
	account, library, err := s.cloudflareAccountForDomains(domains, req.CloudflareAccount, req.CloudflareLibrary)
	if err != nil {
		return syncSiteDNSResponse{}, err
	}
	target := cleanDNSTarget(firstNonEmpty(req.Target, s.defaultSiteDNSTarget(account)))
	if target == "" {
		return syncSiteDNSResponse{}, fmt.Errorf("dns target is required; set -target or cloudflare.site_dns_target")
	}
	recordType := strings.ToUpper(strings.TrimSpace(req.Type))
	if recordType == "" {
		recordType = inferDNSRecordType(target)
	}
	if err := validateDNSRecordTarget(recordType, target); err != nil {
		return syncSiteDNSResponse{}, err
	}
	proxied := true
	if req.Proxied != nil {
		proxied = *req.Proxied
	}
	ttl := req.TTL
	if ttl <= 0 {
		ttl = 1
	}
	records := make([]cloudflare.DNSRecord, 0, len(domains))
	for _, domain := range domains {
		if recordType == "CNAME" && strings.EqualFold(domain, target) {
			return syncSiteDNSResponse{}, fmt.Errorf("cannot create CNAME %q pointing to itself", domain)
		}
		records = append(records, cloudflare.DNSRecord{
			Type:    recordType,
			Name:    domain,
			Content: target,
			Proxied: proxied,
			TTL:     ttl,
		})
	}
	resp := syncSiteDNSResponse{
		SiteID:            site.ID,
		CloudflareAccount: account.Name,
		CloudflareLibrary: library.Name,
		Domains:           domains,
		Type:              recordType,
		Target:            target,
		Proxied:           proxied,
		TTL:               ttl,
		DryRun:            req.DryRun,
		Force:             req.Force,
		Status:            "planned",
		Warnings:          []string{"Cloudflare Worker routes only run for proxied DNS records; DNS-only records bypass the Worker."},
	}
	cf := s.cloudflareClientForAccount(account)
	if !cf.Configured() {
		if req.DryRun {
			for _, record := range records {
				resp.Records = append(resp.Records, cloudflare.DNSRecordSyncResult{
					Name:    record.Name,
					Type:    record.Type,
					Content: record.Content,
					Proxied: record.Proxied,
					TTL:     record.TTL,
					Action:  "create",
					DryRun:  true,
				})
			}
			resp.Warnings = append(resp.Warnings, "cloudflare zone_id/api_token not configured; returning local DNS plan only")
			return resp, nil
		}
		resp.Status = "skipped"
		resp.Errors = append(resp.Errors, "cloudflare zone_id/api_token not configured")
		return resp, nil
	}
	results, err := cf.SyncDNSRecords(ctx, records, cloudflare.SyncDNSRecordOptions{DryRun: req.DryRun, Force: req.Force})
	if err != nil {
		return syncSiteDNSResponse{}, err
	}
	resp.Records = results
	resp.Status = "ok"
	for _, result := range results {
		if result.Error != "" {
			resp.Status = "partial"
			resp.Errors = append(resp.Errors, result.Name+": "+result.Error)
		}
	}
	if req.DryRun && resp.Status == "ok" {
		resp.Status = "planned"
	}
	return resp, nil
}

func (s *Server) siteBoundDomains(site *model.Site, requested []string) ([]string, error) {
	if site == nil {
		return nil, fmt.Errorf("site is required")
	}
	allowed := map[string]bool{}
	for _, domain := range site.Domains {
		domain = cleanHost(domain)
		if domain != "" {
			allowed[domain] = true
		}
	}
	source := site.Domains
	if len(requested) > 0 {
		source = requested
	}
	domains := make([]string, 0, len(source))
	seen := map[string]bool{}
	for _, domain := range source {
		domain = cleanHost(domain)
		if domain == "" {
			continue
		}
		if !allowed[domain] {
			return nil, fmt.Errorf("domain %q is not bound to site %q", domain, site.ID)
		}
		if !seen[domain] {
			seen[domain] = true
			domains = append(domains, domain)
		}
	}
	if len(domains) == 0 {
		return nil, fmt.Errorf("site has no bound domains")
	}
	return domains, nil
}

func (s *Server) defaultSiteDNSTarget(account config.CloudflareAccountConfig) string {
	if target := cleanDNSTarget(account.SiteDNSTarget); target != "" {
		return target
	}
	if s.cfg.Server.PublicBaseURL != "" {
		if parsed, err := url.Parse(s.cfg.Server.PublicBaseURL); err == nil && parsed.Hostname() != "" {
			return cleanDNSTarget(parsed.Hostname())
		}
	}
	return cleanDNSTarget(account.RootDomain)
}

func (s *Server) syncCloudflareR2(ctx context.Context, req syncCloudflareR2Request) syncCloudflareR2Response {
	dryRun := true
	if req.DryRun != nil {
		dryRun = *req.DryRun
	}
	syncCORS := true
	if req.SyncCORS != nil {
		syncCORS = *req.SyncCORS
	}
	syncDomain := true
	if req.SyncDomain != nil {
		syncDomain = *req.SyncDomain
	}
	resp := syncCloudflareR2Response{DryRun: dryRun, Force: req.Force, Status: "ok"}
	targets, warnings, err := s.cloudflareR2SyncTargets(req)
	resp.Warnings = append(resp.Warnings, warnings...)
	if err != nil {
		resp.Status = "failed"
		resp.Errors = append(resp.Errors, err.Error())
		return resp
	}
	for _, target := range targets {
		account := target.Account
		result := s.cloudflareR2ClientForAccount(account).SyncR2Bucket(ctx, cloudflare.SyncR2Options{
			Bucket:             account.R2.Bucket,
			PublicBaseURL:      account.R2.PublicBaseURL,
			ZoneID:             account.ZoneID,
			DryRun:             dryRun,
			Force:              req.Force,
			SyncCORS:           syncCORS,
			SyncDomain:         syncDomain,
			CORSAllowedOrigins: req.CORSOrigins,
			CORSAllowedMethods: req.CORSMethods,
			CORSAllowedHeaders: req.CORSHeaders,
			CORSExposeHeaders:  req.CORSExposeHeaders,
			CORSMaxAgeSeconds:  req.CORSMaxAgeSeconds,
		})
		resp.Accounts = append(resp.Accounts, syncCloudflareR2AccountResult{
			Account:       account.Name,
			Default:       account.Default,
			Library:       target.Library,
			Bucket:        account.R2.Bucket,
			PublicBaseURL: account.R2.PublicBaseURL,
			Result:        result,
		})
		if result.Status == "planned" && resp.Status == "ok" {
			resp.Status = "planned"
		}
		if result.Status == "partial" || result.Status == "failed" {
			if resp.Status != "failed" {
				resp.Status = "partial"
			}
			resp.Errors = append(resp.Errors, fmt.Sprintf("%s: %s", account.Name, strings.Join(result.Errors, "; ")))
		}
	}
	if len(resp.Accounts) == 0 {
		resp.Status = "failed"
		resp.Errors = append(resp.Errors, "no cloudflare accounts with r2 config selected")
	}
	return resp
}

func (s *Server) provisionCloudflareR2(ctx context.Context, req provisionCloudflareR2Request) provisionCloudflareR2Response {
	dryRun := true
	if req.DryRun != nil {
		dryRun = *req.DryRun
	}
	syncCORS := true
	if req.SyncCORS != nil {
		syncCORS = *req.SyncCORS
	}
	syncDomain := true
	if req.SyncDomain != nil {
		syncDomain = *req.SyncDomain
	}
	resp := provisionCloudflareR2Response{DryRun: dryRun, Force: req.Force, Status: "ok"}
	targets, warnings, err := s.cloudflareR2ProvisionTargets(req)
	resp.Warnings = append(resp.Warnings, warnings...)
	if err != nil {
		resp.Status = "failed"
		resp.Errors = append(resp.Errors, err.Error())
		return resp
	}
	for _, target := range targets {
		account := target.Account
		bucket := s.cloudflareR2ProvisionBucket(req, target, len(targets) > 1)
		publicBaseURL, publicWarnings := s.cloudflareR2ProvisionPublicBaseURL(req, target, len(targets) > 1)
		resp.Warnings = append(resp.Warnings, publicWarnings...)
		result := s.cloudflareR2ClientForAccount(account).ProvisionR2Bucket(ctx, cloudflare.R2ProvisionOptions{
			Bucket:             bucket,
			PublicBaseURL:      publicBaseURL,
			ZoneID:             account.ZoneID,
			LocationHint:       req.LocationHint,
			Jurisdiction:       req.Jurisdiction,
			StorageClass:       req.StorageClass,
			DryRun:             dryRun,
			Force:              req.Force,
			SyncCORS:           syncCORS,
			SyncDomain:         syncDomain,
			CORSAllowedOrigins: req.CORSOrigins,
			CORSAllowedMethods: req.CORSMethods,
			CORSAllowedHeaders: req.CORSHeaders,
			CORSExposeHeaders:  req.CORSExposeHeaders,
			CORSMaxAgeSeconds:  req.CORSMaxAgeSeconds,
		})
		resp.Accounts = append(resp.Accounts, provisionCloudflareR2AccountResult{
			Account:       account.Name,
			Default:       account.Default,
			Library:       target.Library,
			Bucket:        bucket,
			PublicBaseURL: publicBaseURL,
			Result:        result,
		})
		if result.Status == "planned" && resp.Status == "ok" {
			resp.Status = "planned"
		}
		if result.Status == "partial" || result.Status == "failed" {
			if resp.Status != "failed" {
				resp.Status = "partial"
			}
			resp.Errors = append(resp.Errors, fmt.Sprintf("%s: %s", account.Name, strings.Join(result.Errors, "; ")))
		}
	}
	if len(resp.Accounts) == 0 {
		resp.Status = "failed"
		resp.Errors = append(resp.Errors, "no cloudflare accounts selected")
	}
	return resp
}

func (s *Server) createCloudflareR2Credentials(ctx context.Context, req createCloudflareR2CredentialsRequest) createCloudflareR2CredentialsResponse {
	dryRun := true
	if req.DryRun != nil {
		dryRun = *req.DryRun
	}
	resp := createCloudflareR2CredentialsResponse{DryRun: dryRun, Force: req.Force, Status: "ok"}
	targets, warnings, err := s.cloudflareR2CredentialTargets(req)
	resp.Warnings = append(resp.Warnings, warnings...)
	if err != nil {
		resp.Status = "failed"
		resp.Errors = append(resp.Errors, err.Error())
		return resp
	}
	for _, target := range targets {
		account := target.Account
		bucket := s.cloudflareR2CredentialBucket(req, target, len(targets) > 1)
		publicBaseURL, publicWarnings := s.cloudflareR2CredentialPublicBaseURL(target)
		resp.Warnings = append(resp.Warnings, publicWarnings...)
		if !req.Force && account.R2.AccessKeyID != "" && account.R2.SecretAccessKey != "" {
			result := cloudflare.R2CredentialsResult{
				Bucket:              bucket,
				Jurisdiction:        normalizeR2CredentialJurisdiction(req.Jurisdiction),
				TokenName:           expandCloudflareProvisionTemplate(req.TokenName, target),
				PermissionGroupName: req.PermissionGroupName,
				DryRun:              dryRun,
				Action:              "skipped",
				Status:              "skipped",
				Error:               "r2 credentials already exist; pass force to create a replacement",
			}
			resp.Accounts = append(resp.Accounts, createCloudflareR2CredentialsAccountResult{
				Account:       account.Name,
				Default:       account.Default,
				Library:       target.Library,
				Bucket:        bucket,
				PublicBaseURL: publicBaseURL,
				Result:        result,
			})
			if resp.Status == "ok" {
				resp.Status = "partial"
			}
			resp.Errors = append(resp.Errors, fmt.Sprintf("%s: %s", account.Name, result.Error))
			continue
		}
		tokenName := expandCloudflareProvisionTemplate(req.TokenName, target)
		if strings.TrimSpace(tokenName) == "" {
			tokenName = defaultR2CredentialTokenName(account.Name, bucket)
		}
		result := s.cloudflareClientForAccount(account).CreateR2Credentials(ctx, cloudflare.R2CredentialsOptions{
			Bucket:              bucket,
			Jurisdiction:        req.Jurisdiction,
			TokenName:           tokenName,
			PermissionGroupName: req.PermissionGroupName,
			DryRun:              dryRun,
		})
		resp.Accounts = append(resp.Accounts, createCloudflareR2CredentialsAccountResult{
			Account:       account.Name,
			Default:       account.Default,
			Library:       target.Library,
			Bucket:        bucket,
			PublicBaseURL: publicBaseURL,
			Result:        result,
		})
		if result.Status == "planned" && resp.Status == "ok" {
			resp.Status = "planned"
		}
		if result.Status == "failed" || result.Status == "skipped" {
			if resp.Status != "failed" {
				resp.Status = "partial"
			}
			resp.Errors = append(resp.Errors, fmt.Sprintf("%s: %s", account.Name, result.Error))
		}
	}
	if len(resp.Accounts) == 0 {
		resp.Status = "failed"
		resp.Errors = append(resp.Errors, "no cloudflare accounts selected")
	}
	return resp
}

func (s *Server) cloudflareR2CredentialTargets(req createCloudflareR2CredentialsRequest) ([]cloudflareR2SyncTarget, []string, error) {
	return s.cloudflareR2ProvisionTargets(provisionCloudflareR2Request{
		CloudflareAccount: req.CloudflareAccount,
		CloudflareLibrary: req.CloudflareLibrary,
		All:               req.All,
		Bucket:            req.Bucket,
		DryRun:            req.DryRun,
	})
}

func (s *Server) cloudflareR2CredentialBucket(req createCloudflareR2CredentialsRequest, target cloudflareR2SyncTarget, multi bool) string {
	return s.cloudflareR2ProvisionBucket(provisionCloudflareR2Request{
		Bucket: req.Bucket,
	}, target, multi)
}

func (s *Server) cloudflareR2CredentialPublicBaseURL(target cloudflareR2SyncTarget) (string, []string) {
	if strings.TrimSpace(target.Account.R2.PublicBaseURL) != "" {
		return normalizeProvisionPublicBaseURL(target.Account.R2.PublicBaseURL), nil
	}
	return s.cloudflareR2ProvisionPublicBaseURL(provisionCloudflareR2Request{}, target, false)
}

func normalizeR2CredentialJurisdiction(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "default"
	}
	return v
}

func defaultR2CredentialTokenName(accountName, bucket string) string {
	account := cleanDomainLabel(accountName)
	if account == "" {
		account = "account"
	}
	name := cleanDomainLabel(bucket)
	if name == "" {
		name = "bucket"
	}
	return "supercdn-r2-" + account + "-" + name + "-" + time.Now().UTC().Format("20060102T150405Z")
}

func (s *Server) cloudflareR2ProvisionTargets(req provisionCloudflareR2Request) ([]cloudflareR2SyncTarget, []string, error) {
	var warnings []string
	seen := map[string]bool{}
	add := func(account config.CloudflareAccountConfig, library string, out *[]cloudflareR2SyncTarget) {
		if seen[account.Name] {
			return
		}
		seen[account.Name] = true
		*out = append(*out, cloudflareR2SyncTarget{Account: account, Library: library})
	}
	var targets []cloudflareR2SyncTarget
	if strings.TrimSpace(req.CloudflareAccount) != "" {
		account, ok := s.cfg.CloudflareAccountByName(req.CloudflareAccount)
		if !ok {
			return nil, warnings, fmt.Errorf("cloudflare account not found")
		}
		library, _ := s.cloudflareLibraryForAccount(account.Name, req.CloudflareLibrary)
		add(account, library.Name, &targets)
		return targets, warnings, nil
	}
	if strings.TrimSpace(req.CloudflareLibrary) != "" {
		library, ok := s.cfg.CloudflareLibraryByName(req.CloudflareLibrary)
		if !ok {
			return nil, warnings, fmt.Errorf("cloudflare library not found")
		}
		for _, binding := range library.Bindings {
			if account, ok := s.cfg.CloudflareAccountByName(binding.Account); ok {
				add(account, library.Name, &targets)
			} else {
				warnings = append(warnings, fmt.Sprintf("cloudflare library %q references missing account %q; skipped", library.Name, binding.Account))
			}
		}
		return targets, warnings, nil
	}
	if req.All {
		for _, account := range s.cfg.CloudflareAccountsEffective() {
			library, _ := s.cloudflareLibraryForAccount(account.Name, "")
			add(account, library.Name, &targets)
		}
		return targets, warnings, nil
	}
	account, ok := s.cfg.DefaultCloudflareAccount()
	if !ok {
		return nil, warnings, fmt.Errorf("cloudflare account is not configured")
	}
	library, _ := s.cloudflareLibraryForAccount(account.Name, "")
	add(account, library.Name, &targets)
	return targets, warnings, nil
}

func (s *Server) cloudflareR2ProvisionBucket(req provisionCloudflareR2Request, target cloudflareR2SyncTarget, multi bool) string {
	account := target.Account
	if strings.TrimSpace(req.Bucket) != "" {
		return cleanR2BucketName(expandCloudflareProvisionTemplate(req.Bucket, target))
	}
	if strings.TrimSpace(account.R2.Bucket) != "" {
		return cleanR2BucketName(account.R2.Bucket)
	}
	name := cloudflareProvisionResourceName(target, multi)
	return cleanR2BucketName("supercdn-" + name)
}

func (s *Server) cloudflareR2ProvisionPublicBaseURL(req provisionCloudflareR2Request, target cloudflareR2SyncTarget, multi bool) (string, []string) {
	account := target.Account
	var warnings []string
	raw := strings.TrimSpace(req.PublicBaseURL)
	if raw != "" {
		return normalizeProvisionPublicBaseURL(expandCloudflareProvisionTemplate(raw, target)), nil
	}
	if strings.TrimSpace(account.R2.PublicBaseURL) != "" {
		return normalizeProvisionPublicBaseURL(account.R2.PublicBaseURL), nil
	}
	root := cleanHost(account.RootDomain)
	if root == "" {
		warnings = append(warnings, fmt.Sprintf("cloudflare account %q has no root_domain; r2 public domain was not planned", account.Name))
		return "", warnings
	}
	label := cloudflareProvisionResourceName(target, multi)
	return "https://" + label + ".r2." + root, warnings
}

func cloudflareProvisionResourceName(target cloudflareR2SyncTarget, multi bool) string {
	base := target.Library
	if base == "" {
		base = target.Account.Name
	}
	base = cleanDomainLabel(base)
	account := cleanDomainLabel(target.Account.Name)
	if base == "" {
		base = "resource"
	}
	if multi && account != "" && !strings.Contains(base, account) {
		base = base + "-" + account
	}
	if len(base) > 63 {
		base = strings.Trim(base[:63], "-")
	}
	return base
}

func expandCloudflareProvisionTemplate(v string, target cloudflareR2SyncTarget) string {
	v = strings.ReplaceAll(v, "{account}", cleanDomainLabel(target.Account.Name))
	v = strings.ReplaceAll(v, "{library}", cleanDomainLabel(target.Library))
	v = strings.ReplaceAll(v, "{root}", cleanHost(target.Account.RootDomain))
	return v
}

func cleanR2BucketName(v string) string {
	v = cleanDomainLabel(v)
	if len(v) > 63 {
		v = strings.Trim(v[:63], "-")
	}
	return v
}

func normalizeProvisionPublicBaseURL(v string) string {
	v = strings.TrimRight(strings.TrimSpace(v), "/")
	if v == "" {
		return ""
	}
	parsed, err := url.Parse(v)
	if err == nil && parsed.Scheme != "" && parsed.Hostname() != "" {
		return v
	}
	return "https://" + strings.TrimPrefix(v, "//")
}

func (s *Server) cloudflareR2SyncTargets(req syncCloudflareR2Request) ([]cloudflareR2SyncTarget, []string, error) {
	var warnings []string
	seen := map[string]bool{}
	add := func(account config.CloudflareAccountConfig, library string, out *[]cloudflareR2SyncTarget) {
		if seen[account.Name] {
			return
		}
		seen[account.Name] = true
		if strings.TrimSpace(account.R2.Bucket) == "" || strings.TrimSpace(account.R2.AccessKeyID) == "" || strings.TrimSpace(account.R2.SecretAccessKey) == "" {
			warnings = append(warnings, fmt.Sprintf("cloudflare account %q has no complete r2 config; skipped", account.Name))
			return
		}
		*out = append(*out, cloudflareR2SyncTarget{Account: account, Library: library})
	}
	var targets []cloudflareR2SyncTarget
	if strings.TrimSpace(req.CloudflareAccount) != "" {
		account, ok := s.cfg.CloudflareAccountByName(req.CloudflareAccount)
		if !ok {
			return nil, warnings, fmt.Errorf("cloudflare account not found")
		}
		library, _ := s.cloudflareLibraryForAccount(account.Name, req.CloudflareLibrary)
		add(account, library.Name, &targets)
		return targets, warnings, nil
	}
	if strings.TrimSpace(req.CloudflareLibrary) != "" {
		library, ok := s.cfg.CloudflareLibraryByName(req.CloudflareLibrary)
		if !ok {
			return nil, warnings, fmt.Errorf("cloudflare library not found")
		}
		for _, binding := range library.Bindings {
			if account, ok := s.cfg.CloudflareAccountByName(binding.Account); ok {
				add(account, library.Name, &targets)
			}
		}
		return targets, warnings, nil
	}
	if req.All {
		for _, account := range s.cfg.CloudflareAccountsEffective() {
			library, _ := s.cloudflareLibraryForAccount(account.Name, "")
			add(account, library.Name, &targets)
		}
		return targets, warnings, nil
	}
	account, ok := s.cfg.DefaultCloudflareAccount()
	if !ok {
		return nil, warnings, fmt.Errorf("cloudflare account is not configured")
	}
	library, _ := s.cloudflareLibraryForAccount(account.Name, "")
	add(account, library.Name, &targets)
	return targets, warnings, nil
}

func (s *Server) purgeSiteDeploymentCache(ctx context.Context, site *model.Site, dep *model.SiteDeployment, req purgeSiteCacheRequest) purgeSiteCacheResponse {
	account, library, accountErr := s.purgeCloudflareAccount(site, req)
	resp := purgeSiteCacheResponse{
		SiteID:            site.ID,
		DeploymentID:      dep.ID,
		Active:            dep.Active,
		CloudflareAccount: account.Name,
		CloudflareLibrary: library.Name,
		DryRun:            req.DryRun,
		Status:            "planned",
	}
	if accountErr != nil {
		resp.Status = "failed"
		resp.Errors = append(resp.Errors, accountErr.Error())
		return resp
	}
	urls, warnings, err := s.siteDeploymentPurgeURLs(ctx, site, dep)
	resp.URLs = urls
	resp.URLCount = len(urls)
	resp.Warnings = append(resp.Warnings, warnings...)
	if err != nil {
		resp.Status = "failed"
		resp.Errors = append(resp.Errors, err.Error())
		return resp
	}
	if req.DryRun {
		return resp
	}
	cf := s.cloudflareClientForAccount(account)
	if !cf.Configured() {
		resp.Status = "skipped"
		resp.Errors = append(resp.Errors, "cloudflare zone_id/api_token not configured")
		return resp
	}
	batches, err := cf.PurgeCacheBatches(ctx, urls)
	if err != nil {
		resp.Status = "failed"
		resp.Errors = append(resp.Errors, err.Error())
		return resp
	}
	resp.Batches = batches
	resp.Status = "ok"
	for _, batch := range batches {
		if batch.Error != "" {
			resp.Status = "partial"
			resp.Errors = append(resp.Errors, fmt.Sprintf("batch %d: %s", batch.Batch, batch.Error))
		}
	}
	return resp
}

func (s *Server) cloudflareAccountForCacheBase(baseURL, requestedAccount, requestedLibrary string) (config.CloudflareAccountConfig, config.CloudflareLibraryConfig, error) {
	if strings.TrimSpace(requestedAccount) != "" {
		account, ok := s.cfg.CloudflareAccountByName(requestedAccount)
		if !ok {
			return config.CloudflareAccountConfig{}, config.CloudflareLibraryConfig{}, fmt.Errorf("cloudflare account not found")
		}
		library, _ := s.cloudflareLibraryForAccount(account.Name, requestedLibrary)
		return account, library, nil
	}
	host := publicURLHost(baseURL)
	if host != "" {
		if account, library, ok := s.cloudflareAccountForHost(host, requestedAccount, requestedLibrary); ok {
			return account, library, nil
		}
	}
	if strings.TrimSpace(requestedLibrary) != "" {
		library, ok := s.cfg.CloudflareLibraryByName(requestedLibrary)
		if !ok {
			return config.CloudflareAccountConfig{}, config.CloudflareLibraryConfig{}, fmt.Errorf("cloudflare library not found")
		}
		for _, binding := range library.Bindings {
			if account, ok := s.cfg.CloudflareAccountByName(binding.Account); ok {
				return account, library, nil
			}
		}
		return config.CloudflareAccountConfig{}, config.CloudflareLibraryConfig{}, fmt.Errorf("cloudflare library has no account bindings")
	}
	account, ok := s.cfg.DefaultCloudflareAccount()
	if !ok {
		return config.CloudflareAccountConfig{}, config.CloudflareLibraryConfig{}, fmt.Errorf("cloudflare account is not configured")
	}
	library, _ := s.cloudflareLibraryForAccount(account.Name, "")
	return account, library, nil
}

func (s *Server) siteDeploymentPurgeURLs(ctx context.Context, site *model.Site, dep *model.SiteDeployment) ([]string, []string, error) {
	var warnings []string
	if site == nil || dep == nil {
		return nil, nil, fmt.Errorf("site and deployment are required")
	}
	if len(site.Domains) == 0 {
		return nil, nil, fmt.Errorf("site has no bound domains")
	}
	if !dep.Active {
		warnings = append(warnings, "deployment is not the active production deployment; site-domain URLs currently serve the active deployment")
	}
	filePaths, err := s.siteDeploymentFilePaths(ctx, dep)
	if err != nil {
		return nil, warnings, err
	}
	if len(filePaths) == 0 {
		return nil, warnings, fmt.Errorf("deployment has no files")
	}
	var urls []string
	for _, domain := range site.Domains {
		domain = cleanHost(domain)
		if domain == "" {
			continue
		}
		base := s.publicScheme() + "://" + domain
		for _, filePath := range filePaths {
			for _, purgePath := range sitePurgePathsForFile(filePath) {
				urls = append(urls, base+purgePath)
			}
		}
	}
	urls = uniqueStrings(urls)
	if len(urls) == 0 {
		return nil, warnings, fmt.Errorf("no purge URLs generated")
	}
	return urls, warnings, nil
}

func (s *Server) purgeCloudflareAccount(site *model.Site, req purgeSiteCacheRequest) (config.CloudflareAccountConfig, config.CloudflareLibraryConfig, error) {
	domains, err := s.siteBoundDomains(site, nil)
	if err != nil {
		return config.CloudflareAccountConfig{}, config.CloudflareLibraryConfig{}, err
	}
	return s.cloudflareAccountForDomains(domains, req.CloudflareAccount, req.CloudflareLibrary)
}

func (s *Server) cloudflareClient() *cloudflare.Client {
	account, _ := s.cfg.DefaultCloudflareAccount()
	return s.cloudflareClientForAccount(account)
}

func (s *Server) cloudflareClientForAccount(account config.CloudflareAccountConfig) *cloudflare.Client {
	return cloudflare.New(account.ToCloudflareConfig(), http.DefaultClient)
}

func (s *Server) cloudflareR2ClientForAccount(account config.CloudflareAccountConfig) *cloudflare.Client {
	return cloudflare.New(account.ToCloudflareR2Config(), http.DefaultClient)
}

func (s *Server) cloudflareStatusForAccount(ctx context.Context, account config.CloudflareAccountConfig) cloudflare.Status {
	status := s.cloudflareClientForAccount(account).Status(ctx)
	status.R2 = s.cloudflareR2ClientForAccount(account).StatusWithR2Checks(ctx, cloudflare.R2CheckOptions{
		Bucket:        account.R2.Bucket,
		PublicBaseURL: account.R2.PublicBaseURL,
	}).R2
	return status
}

func (s *Server) cloudflareAccountForDomains(domains []string, requestedAccount, requestedLibrary string) (config.CloudflareAccountConfig, config.CloudflareLibraryConfig, error) {
	var selected *config.CloudflareAccountConfig
	var selectedLibrary config.CloudflareLibraryConfig
	for _, domain := range domains {
		account, library, ok := s.cloudflareAccountForHost(domain, requestedAccount, requestedLibrary)
		if !ok {
			return config.CloudflareAccountConfig{}, config.CloudflareLibraryConfig{}, fmt.Errorf("no matching cloudflare account for domain %q", domain)
		}
		if selected == nil {
			accountCopy := account
			selected = &accountCopy
			selectedLibrary = library
			continue
		}
		if selected.Name != account.Name {
			return config.CloudflareAccountConfig{}, config.CloudflareLibraryConfig{}, fmt.Errorf("domains span multiple cloudflare accounts; run the sync per account or pass -domains for one account")
		}
	}
	if selected == nil {
		return config.CloudflareAccountConfig{}, config.CloudflareLibraryConfig{}, fmt.Errorf("no domains to match cloudflare account")
	}
	return *selected, selectedLibrary, nil
}

func (s *Server) cloudflareAccountForHost(host, requestedAccount, requestedLibrary string) (config.CloudflareAccountConfig, config.CloudflareLibraryConfig, bool) {
	host = cleanHost(host)
	if strings.TrimSpace(requestedAccount) != "" {
		account, ok := s.cfg.CloudflareAccountByName(requestedAccount)
		if !ok {
			return config.CloudflareAccountConfig{}, config.CloudflareLibraryConfig{}, false
		}
		library, _ := s.cloudflareLibraryForAccount(account.Name, requestedLibrary)
		return account, library, accountInManagedCloudflareZone(account, host)
	}
	accounts := s.cfg.CloudflareAccountsEffective()
	var library config.CloudflareLibraryConfig
	if requestedLibrary != "" {
		lib, ok := s.cfg.CloudflareLibraryByName(requestedLibrary)
		if !ok {
			return config.CloudflareAccountConfig{}, config.CloudflareLibraryConfig{}, false
		}
		library = lib
		accounts = make([]config.CloudflareAccountConfig, 0, len(lib.Bindings))
		for _, binding := range lib.Bindings {
			if account, ok := s.cfg.CloudflareAccountByName(binding.Account); ok {
				accounts = append(accounts, account)
			}
		}
	}
	account, ok := bestCloudflareAccountForHost(accounts, host)
	if !ok {
		if fallback, fallbackOK := s.cfg.DefaultCloudflareAccount(); fallbackOK && len(accounts) == 1 {
			account = fallback
			ok = accountInManagedCloudflareZone(account, host)
		}
	}
	if !ok {
		return config.CloudflareAccountConfig{}, library, false
	}
	if library.Name == "" {
		library, _ = s.cloudflareLibraryForAccount(account.Name, requestedLibrary)
	}
	return account, library, true
}

func (s *Server) cloudflareLibraryForAccount(accountName, requestedLibrary string) (config.CloudflareLibraryConfig, bool) {
	if requestedLibrary != "" {
		library, ok := s.cfg.CloudflareLibraryByName(requestedLibrary)
		return library, ok
	}
	for _, library := range s.cfg.CloudflareLibrariesEffective() {
		for _, binding := range library.Bindings {
			if binding.Account == accountName {
				return library, true
			}
		}
	}
	return config.CloudflareLibraryConfig{}, false
}

func (s *Server) inManagedCloudflareZone(host string) bool {
	account, ok := s.cfg.DefaultCloudflareAccount()
	return ok && accountInManagedCloudflareZone(account, host)
}

func (s *Server) managedWildcardCandidates(host string) []string {
	account, _ := s.cfg.DefaultCloudflareAccount()
	return managedWildcardCandidates(account, host)
}

func bestCloudflareAccountForHost(accounts []config.CloudflareAccountConfig, host string) (config.CloudflareAccountConfig, bool) {
	var best config.CloudflareAccountConfig
	bestLen := -1
	for _, account := range accounts {
		for _, suffix := range []string{cleanHost(account.SiteDomainSuffix), cleanHost(account.RootDomain)} {
			if suffix == "" {
				continue
			}
			if (host == suffix || strings.HasSuffix(host, "."+suffix)) && len(suffix) > bestLen {
				best = account
				bestLen = len(suffix)
			}
		}
	}
	return best, bestLen >= 0
}

func accountInManagedCloudflareZone(account config.CloudflareAccountConfig, host string) bool {
	root := cleanHost(account.RootDomain)
	if root == "" {
		return false
	}
	return host == root || strings.HasSuffix(host, "."+root)
}

func managedWildcardCandidates(account config.CloudflareAccountConfig, host string) []string {
	parent := domainParent(host)
	var out []string
	for _, suffix := range []string{cleanHost(account.SiteDomainSuffix), cleanHost(account.RootDomain)} {
		if suffix != "" && parent == suffix {
			out = append(out, "*."+suffix)
		}
	}
	return out
}

func (s *Server) siteView(site *model.Site) model.Site {
	if site == nil {
		return model.Site{}
	}
	view := *site
	if view.DeploymentTarget == "" {
		view.DeploymentTarget = model.SiteDeploymentTargetOriginAssisted
	}
	if view.Status == "" {
		view.Status = model.SiteStatusActive
	}
	view.URLs = s.siteDomainURLs(site.Domains)
	if len(view.URLs) > 0 {
		view.URL = view.URLs[0]
	}
	return view
}

func (s *Server) siteDeploymentView(ctx context.Context, dep *model.SiteDeployment) model.SiteDeployment {
	if dep == nil {
		return model.SiteDeployment{}
	}
	view := *dep
	if view.DeploymentTarget == "" {
		view.DeploymentTarget = model.SiteDeploymentTargetOriginAssisted
	}
	view.PreviewURL = s.absolutePublicURL("/p/" + dep.SiteID + "/" + dep.ID + "/")
	if dep.ManifestJSON != "" {
		var manifest siteDeployManifest
		if json.Unmarshal([]byte(dep.ManifestJSON), &manifest) == nil {
			view.Inspect = manifest.Inspect
			view.DeliverySummary = manifest.DeliverySummary
			view.CloudflareStatic = manifest.CloudflareStatic
		}
	}
	if site, err := s.db.GetSite(ctx, dep.SiteID); err == nil {
		view.SiteDomains = site.Domains
		if dep.Environment == model.SiteEnvironmentProduction && dep.Active {
			if view.CloudflareStatic != nil && len(view.CloudflareStatic.URLs) > 0 {
				view.ProductionURLs = view.CloudflareStatic.URLs
			} else {
				view.ProductionURLs = s.siteDomainURLs(site.Domains)
			}
			if len(view.ProductionURLs) > 0 {
				view.ProductionURL = view.ProductionURLs[0]
			}
		}
	}
	return view
}

func (s *Server) siteDomainURLs(domains []string) []string {
	urls := make([]string, 0, len(domains))
	for _, domain := range domains {
		domain = cleanHost(domain)
		if domain == "" {
			continue
		}
		urls = append(urls, s.publicScheme()+"://"+domain+"/")
	}
	return urls
}

func httpsDomainURLs(domains []string) []string {
	urls := make([]string, 0, len(domains))
	for _, domain := range domains {
		domain = cleanHost(domain)
		if domain == "" {
			continue
		}
		urls = append(urls, "https://"+domain+"/")
	}
	return urls
}

func (s *Server) absolutePublicURL(pathValue string) string {
	base := strings.TrimRight(s.cfg.Server.PublicBaseURL, "/")
	if base == "" {
		return pathValue
	}
	if !strings.HasPrefix(pathValue, "/") {
		pathValue = "/" + pathValue
	}
	return base + pathValue
}

func (s *Server) publicScheme() string {
	if s.cfg.Server.PublicBaseURL != "" {
		if parsed, err := url.Parse(s.cfg.Server.PublicBaseURL); err == nil && parsed.Scheme != "" {
			return parsed.Scheme
		}
	}
	if s.cfg.Cloudflare.RootDomain != "" {
		return "https"
	}
	return "http"
}

func sitePathCandidates(reqPath, mode string) []string {
	clean := strings.TrimPrefix(path.Clean("/"+strings.TrimPrefix(reqPath, "/")), "/")
	if clean == "." || clean == "" {
		return []string{"index.html"}
	}
	var candidates []string
	if strings.HasSuffix(reqPath, "/") {
		candidates = append(candidates, path.Join(clean, "index.html"))
	} else {
		candidates = append(candidates, clean)
		if path.Ext(clean) == "" {
			candidates = append(candidates, path.Join(clean, "index.html"))
		}
	}
	return candidates
}

func deploymentRules(dep *model.SiteDeployment, site *model.Site) siteRules {
	var rules siteRules
	if dep != nil && dep.RulesJSON != "" {
		_ = json.Unmarshal([]byte(dep.RulesJSON), &rules)
	}
	rules = normalizeSiteRules(rules)
	if rules.Mode == "" && site != nil {
		rules.Mode = site.Mode
	}
	if rules.NotFound == "" {
		rules.NotFound = "404.html"
	}
	return rules
}

func cleanRequestPath(reqPath string) string {
	reqPath = "/" + strings.TrimLeft(strings.ReplaceAll(reqPath, "\\", "/"), "/")
	cleaned := path.Clean(reqPath)
	if cleaned == "." {
		return "/"
	}
	if strings.HasSuffix(reqPath, "/") && cleaned != "/" {
		cleaned += "/"
	}
	return cleaned
}

func cleanSiteRulePath(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" || value == "*" {
		return "/*"
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	if strings.HasSuffix(value, "*") {
		prefix := strings.TrimSuffix(value, "*")
		cleaned := path.Clean(prefix)
		if strings.HasSuffix(prefix, "/") && cleaned != "/" {
			return cleaned + "/*"
		}
		return cleaned + "*"
	}
	return cleanRequestPath(value)
}

func siteRuleMatch(pattern, reqPath string) bool {
	pattern = cleanSiteRulePath(pattern)
	reqPath = cleanRequestPath(reqPath)
	if pattern == "/*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(reqPath, strings.TrimSuffix(pattern, "*"))
	}
	return pattern == reqPath
}

func siteHeadersForPath(rules siteRules, reqPath string) map[string]string {
	headers := map[string]string{}
	for _, rule := range rules.Headers {
		if siteRuleMatch(rule.Path, reqPath) {
			for key, value := range rule.Headers {
				key = strings.TrimSpace(key)
				if key != "" {
					headers[key] = value
				}
			}
		}
	}
	return headers
}

func eligibleZipFiles(files []*zip.File) []*zip.File {
	entries := make([]*zip.File, 0, len(files))
	for _, entry := range files {
		if entry.FileInfo().IsDir() {
			continue
		}
		name := strings.ReplaceAll(entry.Name, "\\", "/")
		if strings.HasPrefix(name, "__MACOSX/") || path.Base(name) == ".DS_Store" {
			continue
		}
		entries = append(entries, entry)
	}
	return entries
}

func inspectSiteZipEntries(entries []siteZipEntry) siteinspect.Report {
	files := make([]siteinspect.File, 0, len(entries))
	byPath := map[string]*zip.File{}
	for _, entry := range entries {
		files = append(files, siteinspect.File{Path: entry.path, Size: int64(entry.file.UncompressedSize64)})
		byPath[entry.path] = entry.file
	}
	return siteinspect.InspectFiles(files, func(filePath string, maxBytes int64) ([]byte, error) {
		entry := byPath[filePath]
		if entry == nil {
			return nil, os.ErrNotExist
		}
		rc, err := entry.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(io.LimitReader(rc, maxBytes))
	})
}

func readSiteZipEntries(files []*zip.File) ([]siteZipEntry, siteRules, error) {
	var (
		entries []siteZipEntry
		rules   siteRules
		seen    = map[string]bool{}
	)
	for _, entry := range eligibleZipFiles(files) {
		rel, err := storage.CleanObjectPath(entry.Name)
		if err != nil {
			return nil, siteRules{}, fmt.Errorf("invalid zip path %q: %w", entry.Name, err)
		}
		if rel == siteConfigFile {
			parsed, err := readSiteRules(entry)
			if err != nil {
				return nil, siteRules{}, err
			}
			rules = parsed
			continue
		}
		if seen[rel] {
			return nil, siteRules{}, fmt.Errorf("duplicate site file %q", rel)
		}
		seen[rel] = true
		entries = append(entries, siteZipEntry{file: entry, path: rel})
	}
	if len(entries) == 0 {
		return nil, siteRules{}, fmt.Errorf("zip bundle contains no files")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	return entries, normalizeSiteRules(rules), nil
}

func readSiteRules(entry *zip.File) (siteRules, error) {
	rc, err := entry.Open()
	if err != nil {
		return siteRules{}, err
	}
	defer rc.Close()
	raw, err := io.ReadAll(io.LimitReader(rc, 1<<20))
	if err != nil {
		return siteRules{}, err
	}
	var rules siteRules
	if err := json.Unmarshal(raw, &rules); err != nil {
		return siteRules{}, fmt.Errorf("invalid %s: %w", siteConfigFile, err)
	}
	return rules, nil
}

func normalizeSiteRules(rules siteRules) siteRules {
	rules.Mode = firstNonEmpty(normalizeSiteMode(rules.Mode), "")
	if rules.NotFound != "" {
		if cleaned, err := storage.CleanObjectPath(rules.NotFound); err == nil {
			rules.NotFound = cleaned
		}
	}
	for i := range rules.Redirects {
		rules.Redirects[i].From = cleanSiteRulePath(rules.Redirects[i].From)
		if rules.Redirects[i].Status == 0 {
			rules.Redirects[i].Status = http.StatusFound
		}
		if !inSet(strconv.Itoa(rules.Redirects[i].Status), "301", "302", "307", "308") {
			rules.Redirects[i].Status = http.StatusFound
		}
	}
	for i := range rules.Rewrites {
		rules.Rewrites[i].From = cleanSiteRulePath(rules.Rewrites[i].From)
		if cleaned, err := storage.CleanObjectPath(rules.Rewrites[i].To); err == nil {
			rules.Rewrites[i].To = "/" + cleaned
		}
	}
	for i := range rules.Headers {
		rules.Headers[i].Path = cleanSiteRulePath(rules.Headers[i].Path)
	}
	for i := range rules.Delivery {
		rules.Delivery[i].Path = cleanSiteRulePath(rules.Delivery[i].Path)
		rules.Delivery[i].Mode = normalizeSiteDeliveryMode(rules.Delivery[i].Mode)
	}
	return rules
}

func normalizeSiteDeliveryMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "origin", "redirect", "auto":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "auto"
	}
}

func siteDeliveryMode(rules siteRules, objectPath string) string {
	clean := strings.TrimPrefix(path.Clean("/"+strings.TrimPrefix(objectPath, "/")), "/")
	if clean == "" || clean == "." || clean == "index.html" {
		return "origin"
	}
	mode := "redirect"
	reqPath := "/" + clean
	for _, rule := range rules.Delivery {
		if siteRuleMatch(rule.Path, reqPath) {
			switch rule.Mode {
			case "origin":
				mode = "origin"
			case "redirect", "auto", "":
				mode = "redirect"
			}
		}
	}
	return mode
}

func (s *Server) checkDeploymentFileCount(environment string, count int) error {
	limit := defaultProductionSiteFiles
	if environment == model.SiteEnvironmentPreview {
		limit = defaultPreviewSiteFiles
	}
	if count <= 0 {
		return fmt.Errorf("site deployment contains no files")
	}
	if s.overclockMode() {
		return nil
	}
	if count > limit {
		return fmt.Errorf("%s deployment allows at most %d files, got %d", environment, limit, count)
	}
	return nil
}

func siteDeploymentRootKey(siteID, deploymentID, filePath string) string {
	return storage.JoinKey("sites", siteID, "deployments", deploymentID, "root", filePath)
}

func statLocalFile(filePath, name string) (*stagedFile, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	hash := sha256.New()
	var first bytes.Buffer
	tee := io.TeeReader(io.LimitReader(f, 512), &first)
	n1, err := io.Copy(hash, tee)
	if err != nil {
		return nil, err
	}
	n2, err := io.Copy(hash, f)
	if err != nil {
		return nil, err
	}
	ctype := http.DetectContentType(first.Bytes())
	if byExt := mimeByName(name); byExt != "" {
		ctype = byExt
	}
	return &stagedFile{
		Path:        filePath,
		Size:        n1 + n2,
		SHA256:      hex.EncodeToString(hash.Sum(nil)),
		ContentType: ctype,
	}, nil
}

func writeTempPayload(dir, pattern string, payload []byte, name string) (string, *stagedFile, error) {
	tmp, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", nil, err
	}
	tmpPath := tmp.Name()
	_, err = tmp.Write(payload)
	if err := closeErr(tmp, err); err != nil {
		_ = os.Remove(tmpPath)
		return "", nil, err
	}
	stat, err := statLocalFile(tmpPath, name)
	if err != nil {
		_ = os.Remove(tmpPath)
		return "", nil, err
	}
	return tmpPath, stat, nil
}

func firstFormFile(r *http.Request, names ...string) (multipart.File, *multipart.FileHeader, error) {
	for _, name := range names {
		f, h, err := r.FormFile(name)
		if err == nil {
			return f, h, nil
		}
	}
	return nil, nil, http.ErrMissingFile
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

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return false
	}
	return true
}

func decodeOptionalJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return true
		}
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": message})
}

func setDynamicRedirectNoStore(h http.Header) {
	h.Set("Cache-Control", "no-store")
	h.Set("CDN-Cache-Control", "no-store")
	h.Set("Cloudflare-CDN-Cache-Control", "no-store")
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
