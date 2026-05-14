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
