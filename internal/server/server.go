package server

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"supercdn/internal/config"
	"supercdn/internal/db"
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
	s.apiMux.HandleFunc("GET /audit-events", s.handleAuditEvents)
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
	s.apiMux.HandleFunc("POST /asset-buckets/{slug}/objects/primary-target", s.handleSwitchAssetBucketObjectPrimaryTarget)
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
	s.apiMux.HandleFunc("POST /sites/{id}/files/primary-target", s.handleSwitchActiveSiteFilePrimaryTarget)
	s.apiMux.HandleFunc("POST /sites/{id}/deployments/{deployment}/files/primary-target", s.handleSwitchDeploymentSiteFilePrimaryTarget)
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
