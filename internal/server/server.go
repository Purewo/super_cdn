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
		if !s.authorized(r) {
			writeError(w, http.StatusUnauthorized, "missing or invalid bearer token")
			return
		}
		http.StripPrefix("/api/v1", s.apiMux).ServeHTTP(w, r)
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
	s.apiMux.HandleFunc("POST /preflight/upload", s.handlePreflightUpload)
	s.apiMux.HandleFunc("POST /preflight/site-deploy", s.handlePreflightSiteDeploy)
	s.apiMux.HandleFunc("POST /init/resource-libraries", s.handleInitResourceLibraries)
	s.apiMux.HandleFunc("GET /init/jobs/{id}", s.handleGetInitJob)
	s.apiMux.HandleFunc("GET /resource-libraries/status", s.handleResourceLibraryStatus)
	s.apiMux.HandleFunc("POST /resource-libraries/health-check", s.handleResourceLibraryHealthCheck)
	s.apiMux.HandleFunc("POST /resource-libraries/e2e-probe", s.handleResourceLibraryE2EProbe)
	s.apiMux.HandleFunc("POST /asset-buckets", s.handleCreateAssetBucket)
	s.apiMux.HandleFunc("GET /asset-buckets", s.handleListAssetBuckets)
	s.apiMux.HandleFunc("GET /asset-buckets/{slug}", s.handleGetAssetBucket)
	s.apiMux.HandleFunc("DELETE /asset-buckets/{slug}", s.handleDeleteAssetBucket)
	s.apiMux.HandleFunc("POST /asset-buckets/{slug}/init", s.handleInitAssetBucket)
	s.apiMux.HandleFunc("POST /asset-buckets/{slug}/purge", s.handlePurgeAssetBucketCache)
	s.apiMux.HandleFunc("POST /asset-buckets/{slug}/warmup", s.handleWarmupAssetBucket)
	s.apiMux.HandleFunc("POST /asset-buckets/{slug}/objects", s.handleUploadBucketObject)
	s.apiMux.HandleFunc("GET /asset-buckets/{slug}/objects", s.handleListBucketObjects)
	s.apiMux.HandleFunc("DELETE /asset-buckets/{slug}/objects", s.handleDeleteBucketObject)
	s.apiMux.HandleFunc("POST /assets", s.handleUploadAsset)
	s.apiMux.HandleFunc("POST /sites", s.handleCreateSite)
	s.apiMux.HandleFunc("POST /sites/{id}/domains", s.handleBindSiteDomains)
	s.apiMux.HandleFunc("POST /sites/{id}/dns", s.handleSyncSiteDNS)
	s.apiMux.HandleFunc("POST /sites/{id}/worker-routes", s.handleSyncSiteWorkerRoutes)
	s.apiMux.HandleFunc("POST /sites/{id}/purge", s.handlePurgeSiteCache)
	s.apiMux.HandleFunc("GET /sites/{id}/deployment-target", s.handleResolveSiteDeploymentTarget)
	s.apiMux.HandleFunc("GET /domains/{host}/status", s.handleDomainStatus)
	s.apiMux.HandleFunc("GET /cloudflare/status", s.handleCloudflareStatus)
	s.apiMux.HandleFunc("POST /cloudflare/r2/sync", s.handleSyncCloudflareR2)
	s.apiMux.HandleFunc("POST /cloudflare/r2/provision", s.handleProvisionCloudflareR2)
	s.apiMux.HandleFunc("POST /cloudflare/r2/credentials", s.handleCreateCloudflareR2Credentials)
	s.apiMux.HandleFunc("POST /sites/{id}/deployments", s.handleCreateSiteDeployment)
	s.apiMux.HandleFunc("POST /sites/{id}/cloudflare-static/deployments", s.handleRecordCloudflareStaticDeployment)
	s.apiMux.HandleFunc("GET /sites/{id}/deployments", s.handleListSiteDeployments)
	s.apiMux.HandleFunc("GET /sites/{id}/deployments/{deployment}", s.handleGetSiteDeployment)
	s.apiMux.HandleFunc("GET /sites/{id}/deployments/{deployment}/edge-manifest", s.handleExportSiteEdgeManifest)
	s.apiMux.HandleFunc("POST /sites/{id}/deployments/{deployment}/edge-manifest/publish", s.handlePublishSiteEdgeManifest)
	s.apiMux.HandleFunc("POST /sites/{id}/deployments/{deployment}/promote", s.handlePromoteSiteDeployment)
	s.apiMux.HandleFunc("POST /sites/{id}/deployments/{deployment}/purge", s.handlePurgeSiteDeploymentCache)
	s.apiMux.HandleFunc("DELETE /sites/{id}/deployments/{deployment}", s.handleDeleteSiteDeployment)
	s.apiMux.HandleFunc("POST /sites/{id}/gc", s.handleSiteGC)
	s.apiMux.HandleFunc("GET /jobs/{id}", s.handleGetJob)
	s.apiMux.HandleFunc("GET /objects/{id}/replicas", s.handleObjectReplicas)
	s.apiMux.HandleFunc("POST /cache/purge", s.handlePurgeCache)
}

func (s *Server) authorized(r *http.Request) bool {
	return r.Header.Get("Authorization") == "Bearer "+s.cfg.Server.AdminToken
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.ID = cleanID(req.ID)
	if req.ID == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	project, err := s.db.CreateProject(r.Context(), req.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, project)
}

type preflightRequest struct {
	RouteProfile    string `json:"route_profile"`
	SiteID          string `json:"site_id"`
	TotalSize       int64  `json:"total_size"`
	LargestFileSize int64  `json:"largest_file_size"`
	BatchFileCount  int    `json:"batch_file_count"`
}

var defaultResourceLibraryInitDirs = []string{
	"_supercdn",
	"_supercdn/manifests",
	"_supercdn/locks",
	"_supercdn/jobs",
	"assets",
	"assets/buckets",
	"assets/objects",
	"assets/manifests",
	"assets/tmp",
	"sites",
	"sites/artifacts",
	"sites/bundles",
	"sites/deployments",
	"sites/releases",
	"sites/manifests",
	"sites/tmp",
}

const initMarkerPath = "_supercdn/init.json"

type initResourceLibrariesRequest struct {
	Libraries   []string `json:"libraries"`
	Directories []string `json:"directories"`
	DryRun      bool     `json:"dry_run"`
}

type initResourceLibrariesPayload struct {
	Libraries      []string `json:"libraries"`
	Directories    []string `json:"directories"`
	MarkerPath     string   `json:"marker_path"`
	RequestedAtUTC string   `json:"requested_at_utc"`
}

type initResourceLibrariesResult struct {
	DryRun      bool                 `json:"dry_run"`
	Directories []string             `json:"directories"`
	Libraries   []storage.InitResult `json:"libraries"`
}

type resourceLibraryHealthRequest struct {
	Libraries          []string `json:"libraries"`
	WriteProbe         bool     `json:"write_probe"`
	Force              bool     `json:"force"`
	MinIntervalSeconds int      `json:"min_interval_seconds"`
}

type resourceLibraryStatusResponse struct {
	Libraries []resourceLibraryStatusView `json:"libraries"`
}

type resourceLibraryStatusView struct {
	Name     string                       `json:"name"`
	Bindings []resourceLibraryBindingView `json:"bindings"`
}

type resourceLibraryBindingView struct {
	Name       string                       `json:"name"`
	Path       string                       `json:"path"`
	MountPoint string                       `json:"mount_point,omitempty"`
	Status     string                       `json:"status"`
	Skipped    bool                         `json:"skipped,omitempty"`
	SkipReason string                       `json:"skip_reason,omitempty"`
	Health     *model.ResourceLibraryHealth `json:"health,omitempty"`
}

type resourceLibraryHealthResponse struct {
	WriteProbe         bool                        `json:"write_probe"`
	MinIntervalSeconds int                         `json:"min_interval_seconds"`
	Libraries          []resourceLibraryStatusView `json:"libraries"`
}

type resourceLibraryE2EProbeRequest struct {
	RouteProfile string `json:"route_profile"`
	ProjectID    string `json:"project_id"`
	Path         string `json:"path"`
	Keep         bool   `json:"keep"`
}

type resourceLibraryE2EProbeResult struct {
	OK              bool     `json:"ok"`
	RouteProfile    string   `json:"route_profile"`
	PrimaryTarget   string   `json:"primary_target"`
	ProjectID       string   `json:"project_id"`
	ObjectPath      string   `json:"object_path"`
	Key             string   `json:"key"`
	Size            int64    `json:"size"`
	SHA256          string   `json:"sha256"`
	ObjectID        int64    `json:"object_id,omitempty"`
	UploadLatencyMS int64    `json:"upload_latency_ms"`
	ReadLatencyMS   int64    `json:"read_latency_ms"`
	HTTPStatus      int      `json:"http_status"`
	ETag            string   `json:"etag,omitempty"`
	CleanupRemote   string   `json:"cleanup_remote,omitempty"`
	CleanupDB       string   `json:"cleanup_db,omitempty"`
	Errors          []string `json:"errors,omitempty"`
}

type createAssetBucketRequest struct {
	Slug                string   `json:"slug"`
	Name                string   `json:"name"`
	Description         string   `json:"description"`
	RouteProfile        string   `json:"route_profile"`
	AllowedTypes        []string `json:"allowed_types"`
	MaxCapacityBytes    int64    `json:"max_capacity_bytes"`
	MaxFileSizeBytes    int64    `json:"max_file_size_bytes"`
	DefaultCacheControl string   `json:"default_cache_control"`
	Status              string   `json:"status"`
}

type initAssetBucketRequest struct {
	DryRun bool `json:"dry_run"`
}

type initAssetBucketResult struct {
	Bucket      string              `json:"bucket"`
	DryRun      bool                `json:"dry_run"`
	Directories []string            `json:"directories"`
	Result      *storage.InitResult `json:"result,omitempty"`
	Status      string              `json:"status"`
	Reason      string              `json:"reason,omitempty"`
}

type deleteReplicaResult struct {
	Target string `json:"target"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type deleteBucketObjectResult struct {
	Bucket       string                `json:"bucket"`
	LogicalPath  string                `json:"logical_path"`
	ObjectID     int64                 `json:"object_id,omitempty"`
	Key          string                `json:"key,omitempty"`
	DeleteRemote bool                  `json:"delete_remote"`
	Remote       []deleteReplicaResult `json:"remote,omitempty"`
	DeletedLocal bool                  `json:"deleted_local"`
	Errors       []string              `json:"errors,omitempty"`
}

type deleteAssetBucketResult struct {
	Bucket         string                     `json:"bucket"`
	DeleteObjects  bool                       `json:"delete_objects"`
	DeleteRemote   bool                       `json:"delete_remote"`
	ObjectCount    int                        `json:"object_count"`
	Objects        []deleteBucketObjectResult `json:"objects,omitempty"`
	DeletedBucket  bool                       `json:"deleted_bucket"`
	DeletedProject bool                       `json:"deleted_project,omitempty"`
	Errors         []string                   `json:"errors,omitempty"`
}

type assetBucketCacheRequest struct {
	Path              string   `json:"path"`
	Paths             []string `json:"paths"`
	Prefix            string   `json:"prefix"`
	All               bool     `json:"all"`
	Limit             int      `json:"limit"`
	BaseURL           string   `json:"base_url"`
	CloudflareAccount string   `json:"cloudflare_account"`
	CloudflareLibrary string   `json:"cloudflare_library"`
	DryRun            bool     `json:"dry_run"`
	Method            string   `json:"method"`
}

type purgeAssetBucketCacheResponse struct {
	Bucket            string                        `json:"bucket"`
	CloudflareAccount string                        `json:"cloudflare_account,omitempty"`
	CloudflareLibrary string                        `json:"cloudflare_library,omitempty"`
	DryRun            bool                          `json:"dry_run"`
	Status            string                        `json:"status"`
	URLCount          int                           `json:"url_count"`
	URLs              []string                      `json:"urls,omitempty"`
	Batches           []cloudflare.PurgeBatchResult `json:"batches,omitempty"`
	Warnings          []string                      `json:"warnings,omitempty"`
	Errors            []string                      `json:"errors,omitempty"`
}

type warmupAssetBucketResponse struct {
	Bucket   string               `json:"bucket"`
	DryRun   bool                 `json:"dry_run"`
	Method   string               `json:"method"`
	Status   string               `json:"status"`
	URLCount int                  `json:"url_count"`
	URLs     []string             `json:"urls,omitempty"`
	Results  []bucketWarmupResult `json:"results,omitempty"`
	Warnings []string             `json:"warnings,omitempty"`
	Errors   []string             `json:"errors,omitempty"`
}

type bucketWarmupResult struct {
	URL       string `json:"url"`
	Method    string `json:"method"`
	Status    string `json:"status"`
	HTTPCode  int    `json:"http_code,omitempty"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
	Error     string `json:"error,omitempty"`
}

type syncCloudflareR2Request struct {
	CloudflareAccount string   `json:"cloudflare_account"`
	CloudflareLibrary string   `json:"cloudflare_library"`
	All               bool     `json:"all"`
	DryRun            *bool    `json:"dry_run,omitempty"`
	Force             bool     `json:"force"`
	SyncCORS          *bool    `json:"sync_cors,omitempty"`
	SyncDomain        *bool    `json:"sync_domain,omitempty"`
	CORSOrigins       []string `json:"cors_origins"`
	CORSMethods       []string `json:"cors_methods"`
	CORSHeaders       []string `json:"cors_headers"`
	CORSExposeHeaders []string `json:"cors_expose_headers"`
	CORSMaxAgeSeconds int      `json:"cors_max_age_seconds"`
}

type syncCloudflareR2Response struct {
	DryRun   bool                            `json:"dry_run"`
	Force    bool                            `json:"force"`
	Status   string                          `json:"status"`
	Accounts []syncCloudflareR2AccountResult `json:"accounts"`
	Warnings []string                        `json:"warnings,omitempty"`
	Errors   []string                        `json:"errors,omitempty"`
}

type syncCloudflareR2AccountResult struct {
	Account       string                  `json:"account"`
	Default       bool                    `json:"default"`
	Library       string                  `json:"library,omitempty"`
	Bucket        string                  `json:"bucket,omitempty"`
	PublicBaseURL string                  `json:"public_base_url,omitempty"`
	Result        cloudflare.R2SyncResult `json:"result"`
}

type provisionCloudflareR2Request struct {
	CloudflareAccount string   `json:"cloudflare_account"`
	CloudflareLibrary string   `json:"cloudflare_library"`
	All               bool     `json:"all"`
	Bucket            string   `json:"bucket"`
	PublicBaseURL     string   `json:"public_base_url"`
	LocationHint      string   `json:"location_hint"`
	Jurisdiction      string   `json:"jurisdiction"`
	StorageClass      string   `json:"storage_class"`
	DryRun            *bool    `json:"dry_run,omitempty"`
	Force             bool     `json:"force"`
	SyncCORS          *bool    `json:"sync_cors,omitempty"`
	SyncDomain        *bool    `json:"sync_domain,omitempty"`
	CORSOrigins       []string `json:"cors_origins"`
	CORSMethods       []string `json:"cors_methods"`
	CORSHeaders       []string `json:"cors_headers"`
	CORSExposeHeaders []string `json:"cors_expose_headers"`
	CORSMaxAgeSeconds int      `json:"cors_max_age_seconds"`
}

type provisionCloudflareR2Response struct {
	DryRun   bool                                 `json:"dry_run"`
	Force    bool                                 `json:"force"`
	Status   string                               `json:"status"`
	Accounts []provisionCloudflareR2AccountResult `json:"accounts"`
	Warnings []string                             `json:"warnings,omitempty"`
	Errors   []string                             `json:"errors,omitempty"`
}

type provisionCloudflareR2AccountResult struct {
	Account       string                       `json:"account"`
	Default       bool                         `json:"default"`
	Library       string                       `json:"library,omitempty"`
	Bucket        string                       `json:"bucket,omitempty"`
	PublicBaseURL string                       `json:"public_base_url,omitempty"`
	Result        cloudflare.R2ProvisionResult `json:"result"`
}

type createCloudflareR2CredentialsRequest struct {
	CloudflareAccount   string `json:"cloudflare_account"`
	CloudflareLibrary   string `json:"cloudflare_library"`
	All                 bool   `json:"all"`
	Bucket              string `json:"bucket"`
	Jurisdiction        string `json:"jurisdiction"`
	TokenName           string `json:"token_name"`
	PermissionGroupName string `json:"permission_group_name"`
	DryRun              *bool  `json:"dry_run,omitempty"`
	Force               bool   `json:"force"`
}

type createCloudflareR2CredentialsResponse struct {
	DryRun   bool                                         `json:"dry_run"`
	Force    bool                                         `json:"force"`
	Status   string                                       `json:"status"`
	Accounts []createCloudflareR2CredentialsAccountResult `json:"accounts"`
	Warnings []string                                     `json:"warnings,omitempty"`
	Errors   []string                                     `json:"errors,omitempty"`
}

type createCloudflareR2CredentialsAccountResult struct {
	Account       string                         `json:"account"`
	Default       bool                           `json:"default"`
	Library       string                         `json:"library,omitempty"`
	Bucket        string                         `json:"bucket,omitempty"`
	PublicBaseURL string                         `json:"public_base_url,omitempty"`
	Result        cloudflare.R2CredentialsResult `json:"result"`
}

type cloudflareR2SyncTarget struct {
	Account config.CloudflareAccountConfig
	Library string
}

type createSiteRequest struct {
	ID                    string   `json:"id"`
	Name                  string   `json:"name"`
	Mode                  string   `json:"mode"`
	RouteProfile          string   `json:"route_profile"`
	DeploymentTarget      string   `json:"deployment_target"`
	Domains               []string `json:"domains"`
	DefaultDomainID       string   `json:"default_domain_id"`
	RandomDefaultDomain   bool     `json:"random_default_domain"`
	SkipDefaultDomain     bool     `json:"skip_default_domain"`
	AllocateDefaultDomain *bool    `json:"allocate_default_domain,omitempty"`
}

type recordCloudflareStaticDeploymentRequest struct {
	Environment        string   `json:"environment"`
	RouteProfile       string   `json:"route_profile"`
	DeploymentTarget   string   `json:"deployment_target"`
	Mode               string   `json:"mode"`
	WorkerName         string   `json:"worker_name"`
	VersionID          string   `json:"version_id"`
	Domains            []string `json:"domains"`
	CompatibilityDate  string   `json:"compatibility_date"`
	AssetsSHA256       string   `json:"assets_sha256"`
	CachePolicy        string   `json:"cache_policy"`
	HeadersGenerated   bool     `json:"headers_generated"`
	NotFoundHandling   string   `json:"not_found_handling"`
	VerificationStatus string   `json:"verification_status"`
	VerifiedAtUTC      string   `json:"verified_at_utc"`
	FileCount          int      `json:"file_count"`
	TotalSize          int64    `json:"total_size"`
	PublishedAtUTC     string   `json:"published_at_utc"`
	Promote            bool     `json:"promote"`
	Pinned             bool     `json:"pinned"`
}

type siteDeploymentTargetResponse struct {
	SiteID           string   `json:"site_id"`
	SiteExists       bool     `json:"site_exists"`
	RouteProfile     string   `json:"route_profile"`
	DeploymentTarget string   `json:"deployment_target"`
	Source           string   `json:"source"`
	Domains          []string `json:"domains,omitempty"`
	DefaultDomain    string   `json:"default_domain,omitempty"`
}

type bindSiteDomainsRequest struct {
	Domains               []string `json:"domains"`
	Append                bool     `json:"append"`
	DefaultDomainID       string   `json:"default_domain_id"`
	RandomDefaultDomain   bool     `json:"random_default_domain"`
	SkipDefaultDomain     bool     `json:"skip_default_domain"`
	AllocateDefaultDomain *bool    `json:"allocate_default_domain,omitempty"`
}

type domainStatusResponse struct {
	Host                 string                 `json:"host"`
	SiteID               string                 `json:"site_id,omitempty"`
	Bound                bool                   `json:"bound"`
	CloudflareAccount    string                 `json:"cloudflare_account,omitempty"`
	CloudflareLibrary    string                 `json:"cloudflare_library,omitempty"`
	CloudflareConfigured bool                   `json:"cloudflare_configured"`
	RootDomain           string                 `json:"root_domain,omitempty"`
	SiteDomainSuffix     string                 `json:"site_domain_suffix,omitempty"`
	InManagedZone        bool                   `json:"in_managed_zone"`
	ExactRecords         []cloudflare.DNSRecord `json:"exact_records,omitempty"`
	WildcardRecords      []cloudflare.DNSRecord `json:"wildcard_records,omitempty"`
	Errors               []string               `json:"errors,omitempty"`
}

type syncWorkerRoutesRequest struct {
	Domains           []string `json:"domains"`
	CloudflareAccount string   `json:"cloudflare_account"`
	CloudflareLibrary string   `json:"cloudflare_library"`
	Script            string   `json:"script"`
	DryRun            bool     `json:"dry_run"`
	Force             bool     `json:"force"`
}

type syncWorkerRoutesResponse struct {
	SiteID            string                             `json:"site_id"`
	CloudflareAccount string                             `json:"cloudflare_account"`
	CloudflareLibrary string                             `json:"cloudflare_library,omitempty"`
	Script            string                             `json:"script"`
	Domains           []string                           `json:"domains"`
	Patterns          []string                           `json:"patterns"`
	DryRun            bool                               `json:"dry_run"`
	Force             bool                               `json:"force"`
	Status            string                             `json:"status"`
	Routes            []cloudflare.WorkerRouteSyncResult `json:"routes"`
	Warnings          []string                           `json:"warnings,omitempty"`
	Errors            []string                           `json:"errors,omitempty"`
}

type syncSiteDNSRequest struct {
	Domains           []string `json:"domains"`
	CloudflareAccount string   `json:"cloudflare_account"`
	CloudflareLibrary string   `json:"cloudflare_library"`
	Type              string   `json:"type"`
	Target            string   `json:"target"`
	Proxied           *bool    `json:"proxied,omitempty"`
	TTL               int      `json:"ttl"`
	DryRun            bool     `json:"dry_run"`
	Force             bool     `json:"force"`
}

type syncSiteDNSResponse struct {
	SiteID            string                           `json:"site_id"`
	CloudflareAccount string                           `json:"cloudflare_account"`
	CloudflareLibrary string                           `json:"cloudflare_library,omitempty"`
	Domains           []string                         `json:"domains"`
	Type              string                           `json:"type"`
	Target            string                           `json:"target"`
	Proxied           bool                             `json:"proxied"`
	TTL               int                              `json:"ttl"`
	DryRun            bool                             `json:"dry_run"`
	Force             bool                             `json:"force"`
	Status            string                           `json:"status"`
	Records           []cloudflare.DNSRecordSyncResult `json:"records"`
	Warnings          []string                         `json:"warnings,omitempty"`
	Errors            []string                         `json:"errors,omitempty"`
}

type purgeSiteCacheRequest struct {
	CloudflareAccount string `json:"cloudflare_account"`
	CloudflareLibrary string `json:"cloudflare_library"`
	DryRun            bool   `json:"dry_run"`
}

type purgeSiteCacheResponse struct {
	SiteID            string                        `json:"site_id"`
	DeploymentID      string                        `json:"deployment_id"`
	Active            bool                          `json:"active"`
	CloudflareAccount string                        `json:"cloudflare_account,omitempty"`
	CloudflareLibrary string                        `json:"cloudflare_library,omitempty"`
	DryRun            bool                          `json:"dry_run"`
	Status            string                        `json:"status"`
	URLCount          int                           `json:"url_count"`
	URLs              []string                      `json:"urls,omitempty"`
	Batches           []cloudflare.PurgeBatchResult `json:"batches,omitempty"`
	Warnings          []string                      `json:"warnings,omitempty"`
	Errors            []string                      `json:"errors,omitempty"`
}

const (
	defaultPreviewSiteFiles    = 300
	defaultProductionSiteFiles = 1000
	siteConfigFile             = "supercdn.site.json"
	overclockWarning           = "overclock mode is enabled: configured size, capacity, file-count, daily-upload, health, and transfer-slot limits are ignored; this can cause unpredictable or catastrophic results"
)

type deploySitePayload struct {
	SiteID       string `json:"site_id"`
	DeploymentID string `json:"deployment_id"`
	StagedPath   string `json:"staged_path"`
	FileName     string `json:"file_name"`
	Promote      bool   `json:"promote"`
}

type siteDeployManifest struct {
	Version          int                               `json:"version"`
	Kind             string                            `json:"kind,omitempty"`
	StorageLayout    string                            `json:"storage_layout,omitempty"`
	SiteID           string                            `json:"site_id"`
	DeploymentID     string                            `json:"deployment_id"`
	Environment      string                            `json:"environment"`
	RouteProfile     string                            `json:"route_profile"`
	DeploymentTarget string                            `json:"deployment_target"`
	CreatedAtUTC     string                            `json:"created_at_utc"`
	FileCount        int                               `json:"file_count"`
	TotalSize        int64                             `json:"total_size"`
	Files            []siteDeployManifestFile          `json:"files"`
	Rules            siteRules                         `json:"rules,omitempty"`
	Inspect          *siteinspect.Report               `json:"inspect,omitempty"`
	DeliverySummary  map[string]int                    `json:"delivery_summary,omitempty"`
	ArtifactSHA256   string                            `json:"artifact_sha256,omitempty"`
	ArtifactSize     int64                             `json:"artifact_size,omitempty"`
	CloudflareStatic *model.CloudflareStaticDeployment `json:"cloudflare_static,omitempty"`
}

type siteDeployManifestFile struct {
	Path         string `json:"path"`
	Size         int64  `json:"size"`
	SHA256       string `json:"sha256"`
	ContentType  string `json:"content_type"`
	CacheControl string `json:"cache_control"`
	Delivery     string `json:"delivery"`
	ObjectID     int64  `json:"object_id"`
}

type edgeManifest struct {
	Version          int                          `json:"version"`
	Kind             string                       `json:"kind"`
	SiteID           string                       `json:"site_id"`
	DeploymentID     string                       `json:"deployment_id"`
	Environment      string                       `json:"environment"`
	Status           string                       `json:"status"`
	RouteProfile     string                       `json:"route_profile"`
	DeploymentTarget string                       `json:"deployment_target"`
	Mode             string                       `json:"mode"`
	GeneratedAtUTC   string                       `json:"generated_at_utc"`
	Rules            siteRules                    `json:"rules,omitempty"`
	Routes           map[string]edgeManifestRoute `json:"routes"`
	Fallback         *edgeManifestRoute           `json:"fallback,omitempty"`
	NotFound         *edgeManifestRoute           `json:"not_found,omitempty"`
	Warnings         []string                     `json:"warnings,omitempty"`
}

type edgeManifestRoute struct {
	Type               string            `json:"type"`
	Delivery           string            `json:"delivery"`
	File               string            `json:"file"`
	Status             int               `json:"status"`
	Location           string            `json:"location,omitempty"`
	ContentType        string            `json:"content_type,omitempty"`
	CacheControl       string            `json:"cache_control,omitempty"`
	ObjectCacheControl string            `json:"object_cache_control,omitempty"`
	Size               int64             `json:"size"`
	SHA256             string            `json:"sha256,omitempty"`
	ObjectID           int64             `json:"object_id"`
	ObjectKey          string            `json:"object_key,omitempty"`
	Headers            map[string]string `json:"headers,omitempty"`
}

type publishEdgeManifestRequest struct {
	Domains           []string `json:"domains"`
	CloudflareAccount string   `json:"cloudflare_account"`
	CloudflareLibrary string   `json:"cloudflare_library"`
	KVNamespaceID     string   `json:"kv_namespace_id"`
	KVNamespace       string   `json:"kv_namespace"`
	KeyPrefix         string   `json:"key_prefix"`
	ActiveKey         *bool    `json:"active_key,omitempty"`
	DeploymentKey     *bool    `json:"deployment_key,omitempty"`
	DryRun            *bool    `json:"dry_run,omitempty"`
}

type publishEdgeManifestResponse struct {
	SiteID            string                `json:"site_id"`
	DeploymentID      string                `json:"deployment_id"`
	Active            bool                  `json:"active"`
	CloudflareAccount string                `json:"cloudflare_account"`
	CloudflareLibrary string                `json:"cloudflare_library,omitempty"`
	KVNamespaceID     string                `json:"kv_namespace_id,omitempty"`
	KVNamespace       string                `json:"kv_namespace,omitempty"`
	KeyPrefix         string                `json:"key_prefix"`
	Domains           []string              `json:"domains"`
	DryRun            bool                  `json:"dry_run"`
	Status            string                `json:"status"`
	ManifestSize      int                   `json:"manifest_size"`
	ManifestSHA256    string                `json:"manifest_sha256"`
	Writes            []edgeManifestKVWrite `json:"writes"`
	ManifestWarnings  []string              `json:"manifest_warnings,omitempty"`
	Warnings          []string              `json:"warnings,omitempty"`
	Errors            []string              `json:"errors,omitempty"`
}

type edgeManifestKVWrite struct {
	Domain string `json:"domain"`
	Key    string `json:"key"`
	Kind   string `json:"kind"`
	Action string `json:"action"`
	DryRun bool   `json:"dry_run,omitempty"`
	Size   int    `json:"size"`
	SHA256 string `json:"sha256"`
	Error  string `json:"error,omitempty"`
}

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

func (s *Server) handlePreflightUpload(w http.ResponseWriter, r *http.Request) {
	var req preflightRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.RouteProfile = firstNonEmpty(req.RouteProfile, "overseas")
	profile, ok := s.cfg.Profile(req.RouteProfile)
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown route_profile")
		return
	}
	result, err := s.preflightProfile(r.Context(), req.RouteProfile, profile, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handlePreflightSiteDeploy(w http.ResponseWriter, r *http.Request) {
	var req preflightRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.SiteID = cleanID(req.SiteID)
	if req.SiteID == "" {
		writeError(w, http.StatusBadRequest, "site_id is required")
		return
	}
	profileName := req.RouteProfile
	site, err := s.db.GetSite(r.Context(), req.SiteID)
	if err == nil {
		profileName = firstNonEmpty(profileName, site.RouteProfile)
	} else if !errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	profileName = firstNonEmpty(profileName, "overseas")
	profile, ok := s.cfg.Profile(profileName)
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown route_profile")
		return
	}
	if err := s.checkSiteFileCount(req.BatchFileCount); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := s.preflightProfile(r.Context(), profileName, profile, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleInitResourceLibraries(w http.ResponseWriter, r *http.Request) {
	var req initResourceLibrariesRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	dirs, err := normalizeInitDirectories(req.Directories)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	libraries, err := s.resolveResourceLibraries(req.Libraries)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	payload := initResourceLibrariesPayload{
		Libraries:      libraries,
		Directories:    dirs,
		MarkerPath:     initMarkerPath,
		RequestedAtUTC: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if req.DryRun {
		result, err := s.initResourceLibraries(r.Context(), payload, true)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
		return
	}
	raw, _ := json.Marshal(payload)
	job, err := s.db.CreateJob(r.Context(), model.JobInitResourceLibraries, string(raw))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"job":            job,
		"max_concurrent": cap(s.transferSem),
		"status":         "queued",
	})
}

func (s *Server) handleResourceLibraryStatus(w http.ResponseWriter, r *http.Request) {
	library := strings.TrimSpace(r.URL.Query().Get("library"))
	libraries, err := s.resolveResourceLibraries(optionalLibrary(library))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	views, err := s.resourceLibraryStatusViews(r.Context(), libraries, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resourceLibraryStatusResponse{Libraries: views})
}

func (s *Server) handleResourceLibraryHealthCheck(w http.ResponseWriter, r *http.Request) {
	var req resourceLibraryHealthRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	minInterval := req.MinIntervalSeconds
	if minInterval <= 0 {
		minInterval = s.cfg.Limits.ResourceHealthMinIntervalSeconds
	}
	libraries, err := s.resolveResourceLibraries(req.Libraries)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	skipped := map[string]string{}
	for _, library := range libraries {
		if !req.Force && s.resourceLibraryHealthFresh(r.Context(), library, minInterval) {
			skipped[library] = fmt.Sprintf("cached health is newer than %d seconds", minInterval)
			continue
		}
		if err := s.runResourceLibraryHealthCheck(r.Context(), library, req.WriteProbe); err != nil {
			s.logger.Warn("resource library health check failed", "library", library, "error", err)
		}
	}
	views, err := s.resourceLibraryStatusViews(r.Context(), libraries, skipped)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resourceLibraryHealthResponse{
		WriteProbe:         req.WriteProbe,
		MinIntervalSeconds: minInterval,
		Libraries:          views,
	})
}

func (s *Server) handleResourceLibraryE2EProbe(w http.ResponseWriter, r *http.Request) {
	var req resourceLibraryE2EProbeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.RouteProfile = firstNonEmpty(req.RouteProfile, "china_all")
	result, err := s.runResourceLibraryE2EProbe(r.Context(), req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, result)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleCreateAssetBucket(w http.ResponseWriter, r *http.Request) {
	var req createAssetBucketRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	slug := cleanBucketSlug(req.Slug)
	if slug == "" {
		writeError(w, http.StatusBadRequest, "slug is required")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = slug
	}
	profileName := firstNonEmpty(req.RouteProfile, "china_all")
	if _, ok := s.cfg.Profile(profileName); !ok {
		writeError(w, http.StatusBadRequest, "unknown route_profile")
		return
	}
	allowed, err := normalizeAssetTypes(req.AllowedTypes)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.MaxCapacityBytes < 0 || req.MaxFileSizeBytes < 0 {
		writeError(w, http.StatusBadRequest, "bucket limits must be non-negative")
		return
	}
	status := firstNonEmpty(req.Status, model.AssetBucketActive)
	if status != model.AssetBucketActive && status != model.AssetBucketDisabled {
		writeError(w, http.StatusBadRequest, "status must be active or disabled")
		return
	}
	bucket, err := s.db.CreateAssetBucket(r.Context(), model.AssetBucket{
		Slug:                slug,
		Name:                name,
		Description:         strings.TrimSpace(req.Description),
		RouteProfile:        profileName,
		AllowedTypes:        allowed,
		MaxCapacityBytes:    req.MaxCapacityBytes,
		MaxFileSizeBytes:    req.MaxFileSizeBytes,
		DefaultCacheControl: strings.TrimSpace(req.DefaultCacheControl),
		Status:              status,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, bucket)
}

func (s *Server) handleListAssetBuckets(w http.ResponseWriter, r *http.Request) {
	buckets, err := s.db.ListAssetBuckets(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"buckets": buckets})
}

func (s *Server) handleGetAssetBucket(w http.ResponseWriter, r *http.Request) {
	bucket, err := s.db.GetAssetBucket(r.Context(), cleanBucketSlug(r.PathValue("slug")))
	if err != nil {
		if db.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "bucket not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, bucket)
}

func (s *Server) handleDeleteAssetBucket(w http.ResponseWriter, r *http.Request) {
	slug := cleanBucketSlug(r.PathValue("slug"))
	if slug == "" {
		writeError(w, http.StatusBadRequest, "bucket is required")
		return
	}
	bucket, err := s.db.GetAssetBucket(r.Context(), slug)
	if err != nil {
		if db.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "bucket not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	force, err := queryBool(r, "force", false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	deleteObjects, err := queryBool(r, "delete_objects", false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	deleteRemote, err := queryBool(r, "delete_remote", true)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if force {
		deleteObjects = true
	}
	result, err := s.deleteAssetBucket(r.Context(), bucket, deleteObjects, deleteRemote)
	if err != nil {
		status := http.StatusBadGateway
		if db.IsNotFound(err) {
			status = http.StatusNotFound
		} else if strings.Contains(err.Error(), "not empty") {
			status = http.StatusConflict
		}
		if result != nil {
			writeJSON(w, status, result)
		} else {
			writeError(w, status, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleInitAssetBucket(w http.ResponseWriter, r *http.Request) {
	var req initAssetBucketRequest
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	bucket, err := s.db.GetAssetBucket(r.Context(), cleanBucketSlug(r.PathValue("slug")))
	if err != nil {
		if db.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "bucket not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	result, err := s.initAssetBucket(r.Context(), bucket, req.DryRun)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handlePurgeAssetBucketCache(w http.ResponseWriter, r *http.Request) {
	bucket, err := s.db.GetAssetBucket(r.Context(), cleanBucketSlug(r.PathValue("slug")))
	if err != nil {
		if db.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "bucket not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var req assetBucketCacheRequest
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	writeJSON(w, http.StatusOK, s.purgeAssetBucketCache(r.Context(), bucket, req))
}

func (s *Server) handleWarmupAssetBucket(w http.ResponseWriter, r *http.Request) {
	bucket, err := s.db.GetAssetBucket(r.Context(), cleanBucketSlug(r.PathValue("slug")))
	if err != nil {
		if db.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "bucket not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var req assetBucketCacheRequest
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	writeJSON(w, http.StatusOK, s.warmupAssetBucket(r.Context(), bucket, req))
}

func (s *Server) handleUploadBucketObject(w http.ResponseWriter, r *http.Request) {
	slug := cleanBucketSlug(r.PathValue("slug"))
	bucket, err := s.db.GetAssetBucket(r.Context(), slug)
	if err != nil {
		if db.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "bucket not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !s.overclockMode() && s.cfg.Limits.MaxUploadBytes > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, s.cfg.Limits.MaxUploadBytes)
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart upload: "+err.Error())
		return
	}
	logicalPath, err := storage.CleanObjectPath(r.FormValue("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file field is required")
		return
	}
	defer file.Close()
	staged, err := s.stageUpload(file, header.Filename)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer os.Remove(staged.Path)
	item, obj, jobs, err := s.putBucketObjectFromStaged(r.Context(), bucket, logicalPath, staged, header.Filename, r.FormValue("asset_type"), r.FormValue("cache_control"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	publicURL := s.assetBucketPublicURL("", bucket.Slug, item.LogicalPath)
	urls := []string{publicURL}
	resp := map[string]any{
		"bucket":        bucket.Slug,
		"object":        obj,
		"bucket_object": item,
		"jobs":          jobs,
		"url":           item.URL,
		"public_url":    publicURL,
	}
	if cdnURL, err := s.objectRedirectURL(r.Context(), obj); err == nil {
		resp["cdn_url"] = cdnURL
		resp["storage_url"] = cdnURL
		urls = append(urls, cdnURL)
	}
	resp["urls"] = uniqueStrings(urls)
	writeJSON(w, http.StatusCreated, s.withOverclockWarning(resp))
}

func (s *Server) handleListBucketObjects(w http.ResponseWriter, r *http.Request) {
	slug := cleanBucketSlug(r.PathValue("slug"))
	if _, err := s.db.GetAssetBucket(r.Context(), slug); err != nil {
		if db.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "bucket not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	prefix := strings.TrimPrefix(strings.ReplaceAll(r.URL.Query().Get("prefix"), "\\", "/"), "/")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := s.db.ListAssetBucketObjects(r.Context(), slug, prefix, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"objects": items})
}

func (s *Server) handleDeleteBucketObject(w http.ResponseWriter, r *http.Request) {
	slug := cleanBucketSlug(r.PathValue("slug"))
	if slug == "" {
		writeError(w, http.StatusBadRequest, "bucket is required")
		return
	}
	logicalPath, err := storage.CleanObjectPath(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	deleteRemote, err := queryBool(r, "delete_remote", true)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := s.deleteBucketObject(r.Context(), slug, logicalPath, deleteRemote)
	if err != nil {
		status := http.StatusBadGateway
		if db.IsNotFound(err) {
			status = http.StatusNotFound
		}
		if result != nil {
			writeJSON(w, status, result)
		} else {
			writeError(w, status, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleUploadAsset(w http.ResponseWriter, r *http.Request) {
	if !s.overclockMode() && s.cfg.Limits.MaxUploadBytes > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, s.cfg.Limits.MaxUploadBytes)
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart upload: "+err.Error())
		return
	}
	projectID := cleanID(r.FormValue("project_id"))
	objectPath, err := storage.CleanObjectPath(r.FormValue("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	profileName := firstNonEmpty(r.FormValue("route_profile"), "overseas")
	profile, ok := s.cfg.Profile(profileName)
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown route_profile")
		return
	}
	if projectID == "" {
		writeError(w, http.StatusBadRequest, "project_id is required")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file field is required")
		return
	}
	defer file.Close()
	staged, err := s.stageUpload(file, header.Filename)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer os.Remove(staged.Path)
	if _, err := s.preflightProfile(r.Context(), profileName, profile, preflightRequest{
		TotalSize:       staged.Size,
		LargestFileSize: staged.Size,
		BatchFileCount:  1,
	}); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := s.db.CreateProject(r.Context(), projectID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cacheControl := firstNonEmpty(r.FormValue("cache_control"), profile.DefaultCacheControl, "public, max-age=3600")
	key := storage.JoinKey("objects", projectID, objectPath)
	obj, jobs, err := s.putObjectFromFile(r.Context(), putObjectInput{
		ProjectID:      projectID,
		ObjectPath:     objectPath,
		Key:            key,
		Profile:        profile,
		ProfileName:    profileName,
		CacheControl:   cacheControl,
		ContentType:    staged.ContentType,
		FilePath:       staged.Path,
		FileName:       firstNonEmpty(header.Filename, path.Base(objectPath)),
		Size:           staged.Size,
		SHA256:         staged.SHA256,
		BatchFileCount: 1,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, s.withOverclockWarning(map[string]any{
		"object": obj,
		"jobs":   jobs,
		"url":    "/o/" + projectID + "/" + objectPath,
	}))
}

func (s *Server) handleCreateSite(w http.ResponseWriter, r *http.Request) {
	var req createSiteRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.ID = cleanID(req.ID)
	req.Mode = firstNonEmpty(req.Mode, "standard")
	req.RouteProfile = firstNonEmpty(req.RouteProfile, "overseas")
	if req.ID == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	if req.Mode != "standard" && req.Mode != "spa" {
		writeError(w, http.StatusBadRequest, "mode must be standard or spa")
		return
	}
	profile, ok := s.cfg.Profile(req.RouteProfile)
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown route_profile")
		return
	}
	deploymentTarget, err := normalizeDeploymentTarget(req.DeploymentTarget)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if deploymentTarget == "" {
		deploymentTarget = defaultDeploymentTarget(profile)
	}
	domains, err := s.siteDomainsFromRequest(req.ID, req.Domains, req.DefaultDomainID, req.RandomDefaultDomain, req.SkipDefaultDomain, req.AllocateDefaultDomain)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	site, err := s.db.CreateSite(r.Context(), req.ID, strings.TrimSpace(req.Name), req.Mode, req.RouteProfile, deploymentTarget, domains)
	if err != nil {
		if strings.Contains(err.Error(), "already bound") {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.siteView(site))
}

func (s *Server) handleBindSiteDomains(w http.ResponseWriter, r *http.Request) {
	siteID := cleanID(r.PathValue("id"))
	if siteID == "" {
		writeError(w, http.StatusBadRequest, "site id is required")
		return
	}
	site, err := s.db.GetSite(r.Context(), siteID)
	if err != nil {
		writeError(w, http.StatusNotFound, "site not found")
		return
	}
	var req bindSiteDomainsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	domains, err := s.siteDomainsFromRequest(siteID, req.Domains, req.DefaultDomainID, req.RandomDefaultDomain, req.SkipDefaultDomain, req.AllocateDefaultDomain)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Append {
		domains = append(site.Domains, domains...)
	}
	if err := s.db.SetDomains(r.Context(), siteID, domains); err != nil {
		if strings.Contains(err.Error(), "already bound") {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	site, err = s.db.GetSite(r.Context(), siteID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.siteView(site))
}

func (s *Server) handleResolveSiteDeploymentTarget(w http.ResponseWriter, r *http.Request) {
	siteID := cleanID(r.PathValue("id"))
	if siteID == "" {
		writeError(w, http.StatusBadRequest, "site id is required")
		return
	}
	resp, err := s.resolveSiteDeploymentTarget(r.Context(), siteID, r.URL.Query().Get("route_profile"), firstNonEmpty(r.URL.Query().Get("deployment_target"), r.URL.Query().Get("target")))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) resolveSiteDeploymentTarget(ctx context.Context, siteID, requestedProfile, requestedTarget string) (siteDeploymentTargetResponse, error) {
	profileName := strings.TrimSpace(requestedProfile)
	target, err := normalizeDeploymentTarget(requestedTarget)
	if err != nil {
		return siteDeploymentTargetResponse{}, err
	}
	source := ""
	site, err := s.db.GetSite(ctx, siteID)
	siteExists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return siteDeploymentTargetResponse{}, err
	}
	if siteExists && profileName == "" {
		profileName = site.RouteProfile
	}
	profileName = firstNonEmpty(profileName, "overseas")
	profile, ok := s.cfg.Profile(profileName)
	if !ok {
		return siteDeploymentTargetResponse{}, fmt.Errorf("unknown route_profile")
	}
	if target != "" {
		source = "request"
	} else if siteExists && strings.TrimSpace(site.DeploymentTarget) != "" {
		target = site.DeploymentTarget
		source = "site"
	} else {
		target = defaultDeploymentTarget(profile)
		source = "route_profile"
	}
	resp := siteDeploymentTargetResponse{
		SiteID:           siteID,
		SiteExists:       siteExists,
		RouteProfile:     profileName,
		DeploymentTarget: target,
		Source:           source,
	}
	if siteExists && len(site.Domains) > 0 {
		resp.Domains = append([]string(nil), site.Domains...)
		resp.DefaultDomain = site.Domains[0]
	} else if target == model.SiteDeploymentTargetCloudflareStatic {
		if domain, err := s.defaultCloudflareStaticDomain(siteID); err == nil {
			resp.Domains = []string{domain}
			resp.DefaultDomain = domain
		}
	}
	return resp, nil
}

func (s *Server) handleSyncSiteWorkerRoutes(w http.ResponseWriter, r *http.Request) {
	siteID := cleanID(r.PathValue("id"))
	if siteID == "" {
		writeError(w, http.StatusBadRequest, "site id is required")
		return
	}
	site, err := s.db.GetSite(r.Context(), siteID)
	if err != nil {
		writeError(w, http.StatusNotFound, "site not found")
		return
	}
	var req syncWorkerRoutesRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.syncSiteWorkerRoutes(r.Context(), site, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleSyncSiteDNS(w http.ResponseWriter, r *http.Request) {
	siteID := cleanID(r.PathValue("id"))
	if siteID == "" {
		writeError(w, http.StatusBadRequest, "site id is required")
		return
	}
	site, err := s.db.GetSite(r.Context(), siteID)
	if err != nil {
		writeError(w, http.StatusNotFound, "site not found")
		return
	}
	var req syncSiteDNSRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.syncSiteDNS(r.Context(), site, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleDomainStatus(w http.ResponseWriter, r *http.Request) {
	host := cleanHost(r.PathValue("host"))
	if host == "" {
		writeError(w, http.StatusBadRequest, "domain is required")
		return
	}
	status := s.domainStatus(r.Context(), host)
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleCloudflareStatus(w http.ResponseWriter, r *http.Request) {
	accountName := strings.TrimSpace(r.URL.Query().Get("account"))
	if r.URL.Query().Get("all") == "true" {
		accounts := s.cfg.CloudflareAccountsEffective()
		statuses := make([]map[string]any, 0, len(accounts))
		for _, account := range accounts {
			statuses = append(statuses, map[string]any{
				"account": account.Name,
				"default": account.Default,
				"status":  s.cloudflareStatusForAccount(r.Context(), account),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"accounts":  statuses,
			"libraries": s.cfg.CloudflareLibrariesEffective(),
		})
		return
	}
	if accountName != "" {
		account, ok := s.cfg.CloudflareAccountByName(accountName)
		if !ok {
			writeError(w, http.StatusNotFound, "cloudflare account not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"account": account.Name,
			"default": account.Default,
			"status":  s.cloudflareStatusForAccount(r.Context(), account),
		})
		return
	}
	if account, ok := s.cfg.DefaultCloudflareAccount(); ok {
		writeJSON(w, http.StatusOK, s.cloudflareStatusForAccount(r.Context(), account))
		return
	}
	writeJSON(w, http.StatusOK, s.cloudflareClient().Status(r.Context()))
}

func (s *Server) handleSyncCloudflareR2(w http.ResponseWriter, r *http.Request) {
	var req syncCloudflareR2Request
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	resp := s.syncCloudflareR2(r.Context(), req)
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleProvisionCloudflareR2(w http.ResponseWriter, r *http.Request) {
	var req provisionCloudflareR2Request
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	resp := s.provisionCloudflareR2(r.Context(), req)
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleCreateCloudflareR2Credentials(w http.ResponseWriter, r *http.Request) {
	var req createCloudflareR2CredentialsRequest
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	resp := s.createCloudflareR2Credentials(r.Context(), req)
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleCreateSiteDeployment(w http.ResponseWriter, r *http.Request) {
	siteID := cleanID(r.PathValue("id"))
	if siteID == "" {
		writeError(w, http.StatusBadRequest, "site id is required")
		return
	}
	dep, payload, err := s.createSiteDeploymentFromRequest(w, r, siteID, "", false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	raw, _ := json.Marshal(payload)
	job, err := s.db.CreateJob(r.Context(), model.JobDeploySite, string(raw))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	depView := s.siteDeploymentView(r.Context(), dep)
	writeJSON(w, http.StatusAccepted, s.withOverclockWarning(map[string]any{
		"deployment":    depView,
		"deployment_id": depView.ID,
		"job_id":        job.ID,
		"preview_url":   depView.PreviewURL,
	}))
}

func (s *Server) handleRecordCloudflareStaticDeployment(w http.ResponseWriter, r *http.Request) {
	siteID := cleanID(r.PathValue("id"))
	if siteID == "" {
		writeError(w, http.StatusBadRequest, "site id is required")
		return
	}
	var req recordCloudflareStaticDeploymentRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.recordCloudflareStaticDeployment(r.Context(), siteID, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) handleListSiteDeployments(w http.ResponseWriter, r *http.Request) {
	siteID := cleanID(r.PathValue("id"))
	if siteID == "" {
		writeError(w, http.StatusBadRequest, "site id is required")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	deployments, err := s.db.ListSiteDeployments(r.Context(), siteID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	views := make([]model.SiteDeployment, 0, len(deployments))
	for i := range deployments {
		views = append(views, s.siteDeploymentView(r.Context(), &deployments[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"deployments": views})
}

func (s *Server) handleGetSiteDeployment(w http.ResponseWriter, r *http.Request) {
	siteID := cleanID(r.PathValue("id"))
	deploymentID := cleanDeploymentID(r.PathValue("deployment"))
	dep, err := s.db.GetSiteDeployment(r.Context(), deploymentID)
	if err != nil || dep.SiteID != siteID {
		writeError(w, http.StatusNotFound, "deployment not found")
		return
	}
	writeJSON(w, http.StatusOK, s.siteDeploymentView(r.Context(), dep))
}

func (s *Server) handleExportSiteEdgeManifest(w http.ResponseWriter, r *http.Request) {
	siteID := cleanID(r.PathValue("id"))
	deploymentID := cleanDeploymentID(r.PathValue("deployment"))
	site, err := s.db.GetSite(r.Context(), siteID)
	if err != nil {
		writeError(w, http.StatusNotFound, "site not found")
		return
	}
	dep, err := s.db.GetSiteDeployment(r.Context(), deploymentID)
	if err != nil || dep.SiteID != site.ID {
		writeError(w, http.StatusNotFound, "deployment not found")
		return
	}
	if dep.Status != model.SiteDeploymentReady && dep.Status != model.SiteDeploymentActive {
		writeError(w, http.StatusBadRequest, "deployment is not ready")
		return
	}
	manifest, err := s.buildSiteEdgeManifest(r.Context(), site, dep)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, manifest)
}

func (s *Server) handlePublishSiteEdgeManifest(w http.ResponseWriter, r *http.Request) {
	siteID := cleanID(r.PathValue("id"))
	deploymentID := cleanDeploymentID(r.PathValue("deployment"))
	site, err := s.db.GetSite(r.Context(), siteID)
	if err != nil {
		writeError(w, http.StatusNotFound, "site not found")
		return
	}
	dep, err := s.db.GetSiteDeployment(r.Context(), deploymentID)
	if err != nil || dep.SiteID != site.ID {
		writeError(w, http.StatusNotFound, "deployment not found")
		return
	}
	if dep.Status != model.SiteDeploymentReady && dep.Status != model.SiteDeploymentActive {
		writeError(w, http.StatusBadRequest, "deployment is not ready")
		return
	}
	var req publishEdgeManifestRequest
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	resp, err := s.publishSiteEdgeManifest(r.Context(), site, dep, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handlePurgeSiteCache(w http.ResponseWriter, r *http.Request) {
	siteID := cleanID(r.PathValue("id"))
	if siteID == "" {
		writeError(w, http.StatusBadRequest, "site id is required")
		return
	}
	site, err := s.db.GetSite(r.Context(), siteID)
	if err != nil {
		writeError(w, http.StatusNotFound, "site not found")
		return
	}
	dep, err := s.db.ActiveSiteDeployment(r.Context(), siteID)
	if err != nil {
		writeError(w, http.StatusNotFound, "active deployment not found")
		return
	}
	var req purgeSiteCacheRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	writeJSON(w, http.StatusOK, s.purgeSiteDeploymentCache(r.Context(), site, dep, req))
}

func (s *Server) handlePurgeSiteDeploymentCache(w http.ResponseWriter, r *http.Request) {
	siteID := cleanID(r.PathValue("id"))
	deploymentID := cleanDeploymentID(r.PathValue("deployment"))
	site, err := s.db.GetSite(r.Context(), siteID)
	if err != nil {
		writeError(w, http.StatusNotFound, "site not found")
		return
	}
	dep, err := s.db.GetSiteDeployment(r.Context(), deploymentID)
	if err != nil || dep.SiteID != siteID {
		writeError(w, http.StatusNotFound, "deployment not found")
		return
	}
	var req purgeSiteCacheRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	writeJSON(w, http.StatusOK, s.purgeSiteDeploymentCache(r.Context(), site, dep, req))
}

func (s *Server) handlePromoteSiteDeployment(w http.ResponseWriter, r *http.Request) {
	siteID := cleanID(r.PathValue("id"))
	deploymentID := cleanDeploymentID(r.PathValue("deployment"))
	dep, err := s.db.GetSiteDeployment(r.Context(), deploymentID)
	if err != nil || dep.SiteID != siteID {
		writeError(w, http.StatusNotFound, "deployment not found")
		return
	}
	if dep.Status != model.SiteDeploymentReady && dep.Status != model.SiteDeploymentActive {
		writeError(w, http.StatusBadRequest, "deployment is not ready")
		return
	}
	if dep.DeploymentTarget == model.SiteDeploymentTargetCloudflareStatic && !dep.Active {
		writeError(w, http.StatusConflict, "cloudflare_static deployments cannot be promoted by metadata alone; redeploy the desired assets or use a Cloudflare Worker rollback flow")
		return
	}
	activated, err := s.db.ActivateSiteDeployment(r.Context(), siteID, deploymentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.siteDeploymentView(r.Context(), activated))
}

func (s *Server) handleDeleteSiteDeployment(w http.ResponseWriter, r *http.Request) {
	siteID := cleanID(r.PathValue("id"))
	deploymentID := cleanDeploymentID(r.PathValue("deployment"))
	dep, err := s.db.GetSiteDeployment(r.Context(), deploymentID)
	if err != nil || dep.SiteID != siteID {
		writeError(w, http.StatusNotFound, "deployment not found")
		return
	}
	if dep.Active {
		writeError(w, http.StatusConflict, "active production deployment cannot be deleted")
		return
	}
	if dep.Pinned {
		writeError(w, http.StatusConflict, "pinned deployment cannot be deleted")
		return
	}
	if err := s.db.DeleteSiteDeployment(r.Context(), siteID, deploymentID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := map[string]any{"deleted": true, "deployment_id": deploymentID}
	if dep.DeploymentTarget == model.SiteDeploymentTargetCloudflareStatic {
		resp["warning"] = "deleted Super CDN metadata only; Cloudflare Worker versions and custom domains are not deleted by this command"
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleSiteGC(w http.ResponseWriter, r *http.Request) {
	siteID := cleanID(r.PathValue("id"))
	if siteID == "" {
		writeError(w, http.StatusBadRequest, "site id is required")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"site_id": siteID,
		"status":  "noop",
		"message": "site content GC is tracked by deployment manifests; destructive cleanup is intentionally manual in this version",
	})
}

func (s *Server) createSiteDeploymentFromRequest(w http.ResponseWriter, r *http.Request, siteID, forcedEnvironment string, forcedPromote bool) (*model.SiteDeployment, deploySitePayload, error) {
	if !s.overclockMode() && s.cfg.Limits.MaxUploadBytes > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, s.cfg.Limits.MaxUploadBytes)
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		return nil, deploySitePayload{}, fmt.Errorf("invalid multipart upload: %w", err)
	}
	rawEnvironment := firstNonEmpty(forcedEnvironment, r.FormValue("environment"))
	environment := normalizeSiteEnvironment(rawEnvironment)
	if environment == "" {
		if strings.TrimSpace(rawEnvironment) != "" {
			return nil, deploySitePayload{}, fmt.Errorf("environment must be production or preview")
		}
		environment = model.SiteEnvironmentPreview
	}
	promote := forcedPromote
	if !forcedPromote {
		if raw := strings.TrimSpace(r.FormValue("promote")); raw != "" {
			parsed, err := strconv.ParseBool(raw)
			if err != nil {
				return nil, deploySitePayload{}, fmt.Errorf("promote must be a boolean")
			}
			promote = parsed
		}
	}
	pinned, err := parseFormBool(r, "pinned", false)
	if err != nil {
		return nil, deploySitePayload{}, err
	}
	profileName := strings.TrimSpace(r.FormValue("route_profile"))
	deploymentTarget, err := normalizeDeploymentTarget(firstNonEmpty(r.FormValue("deployment_target"), r.FormValue("target")))
	if err != nil {
		return nil, deploySitePayload{}, err
	}
	site, err := s.db.GetSite(r.Context(), siteID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, deploySitePayload{}, err
		}
		profileName = firstNonEmpty(profileName, "overseas")
		profile, ok := s.cfg.Profile(profileName)
		if !ok {
			return nil, deploySitePayload{}, fmt.Errorf("unknown route_profile")
		}
		if deploymentTarget == "" {
			deploymentTarget = defaultDeploymentTarget(profile)
		}
		site, err = s.db.CreateSite(r.Context(), siteID, "", "standard", profileName, deploymentTarget, nil)
		if err != nil {
			return nil, deploySitePayload{}, err
		}
	}
	profileName = firstNonEmpty(profileName, site.RouteProfile, "overseas")
	profile, ok := s.cfg.Profile(profileName)
	if !ok {
		return nil, deploySitePayload{}, fmt.Errorf("unknown route_profile")
	}
	if deploymentTarget == "" {
		deploymentTarget = firstNonEmpty(site.DeploymentTarget, defaultDeploymentTarget(profile))
	}
	file, header, err := firstFormFile(r, "artifact", "bundle", "file")
	if err != nil {
		return nil, deploySitePayload{}, fmt.Errorf("artifact field is required")
	}
	defer file.Close()
	if !strings.HasSuffix(strings.ToLower(header.Filename), ".zip") {
		return nil, deploySitePayload{}, fmt.Errorf("site deployment expects a zip artifact")
	}
	staged, err := s.stageUpload(file, header.Filename)
	if err != nil {
		return nil, deploySitePayload{}, err
	}
	deploymentID := newDeploymentID()
	stagedPath := filepath.Join(s.staging, "site-deployment-"+deploymentID+".zip")
	if err := os.Rename(staged.Path, stagedPath); err != nil {
		_ = os.Remove(staged.Path)
		return nil, deploySitePayload{}, err
	}
	expiresAt := time.Time{}
	if environment == model.SiteEnvironmentPreview && !pinned {
		expiresAt = time.Now().UTC().Add(7 * 24 * time.Hour)
	}
	dep, err := s.db.CreateSiteDeployment(r.Context(), model.SiteDeployment{
		ID:               deploymentID,
		SiteID:           siteID,
		Environment:      environment,
		Status:           model.SiteDeploymentQueued,
		RouteProfile:     profileName,
		DeploymentTarget: deploymentTarget,
		Version:          deploymentID,
		Pinned:           pinned,
		ExpiresAt:        expiresAt,
	})
	if err != nil {
		_ = os.Remove(stagedPath)
		return nil, deploySitePayload{}, err
	}
	return dep, deploySitePayload{
		SiteID:       siteID,
		DeploymentID: deploymentID,
		StagedPath:   stagedPath,
		FileName:     header.Filename,
		Promote:      promote,
	}, nil
}

func (s *Server) processSiteDeployment(ctx context.Context, payload deploySitePayload) (*model.SiteDeployment, error) {
	defer os.Remove(payload.StagedPath)
	dep, err := s.db.GetSiteDeployment(ctx, payload.DeploymentID)
	if err != nil {
		return nil, err
	}
	if err := s.db.UpdateSiteDeploymentStatus(ctx, dep.ID, model.SiteDeploymentProcessing, ""); err != nil {
		return nil, err
	}
	ready, err := s.buildSiteDeployment(ctx, dep, payload)
	if err != nil {
		_ = s.db.UpdateSiteDeploymentStatus(ctx, dep.ID, model.SiteDeploymentFailed, err.Error())
		return nil, err
	}
	if payload.Promote {
		ready, err = s.db.ActivateSiteDeployment(ctx, ready.SiteID, ready.ID)
		if err != nil {
			_ = s.db.UpdateSiteDeploymentStatus(ctx, dep.ID, model.SiteDeploymentFailed, err.Error())
			return nil, err
		}
	}
	view := s.siteDeploymentView(ctx, ready)
	return &view, nil
}

func (s *Server) recordCloudflareStaticDeployment(ctx context.Context, siteID string, req recordCloudflareStaticDeploymentRequest) (model.SiteDeployment, error) {
	req.WorkerName = strings.TrimSpace(req.WorkerName)
	if req.WorkerName == "" {
		return model.SiteDeployment{}, fmt.Errorf("worker_name is required")
	}
	if req.FileCount < 0 {
		return model.SiteDeployment{}, fmt.Errorf("file_count must be non-negative")
	}
	if req.TotalSize < 0 {
		return model.SiteDeployment{}, fmt.Errorf("total_size must be non-negative")
	}
	rawEnvironment := strings.TrimSpace(req.Environment)
	environment := normalizeSiteEnvironment(rawEnvironment)
	if environment == "" {
		if rawEnvironment != "" {
			return model.SiteDeployment{}, fmt.Errorf("environment must be production or preview")
		}
		environment = model.SiteEnvironmentProduction
	}
	target, err := normalizeDeploymentTarget(req.DeploymentTarget)
	if err != nil {
		return model.SiteDeployment{}, err
	}
	if target == "" {
		target = model.SiteDeploymentTargetCloudflareStatic
	}
	if target != model.SiteDeploymentTargetCloudflareStatic {
		return model.SiteDeployment{}, fmt.Errorf("cloudflare static deployment requires deployment_target cloudflare_static")
	}
	site, err := s.db.GetSite(ctx, siteID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return model.SiteDeployment{}, err
	}
	profileName := firstNonEmpty(strings.TrimSpace(req.RouteProfile), "overseas")
	if site != nil {
		profileName = firstNonEmpty(strings.TrimSpace(req.RouteProfile), site.RouteProfile, "overseas")
	}
	if _, ok := s.cfg.Profile(profileName); !ok {
		return model.SiteDeployment{}, fmt.Errorf("unknown route_profile")
	}
	mode := normalizeSiteMode(req.Mode)
	if mode == "" {
		if strings.TrimSpace(req.Mode) != "" {
			return model.SiteDeployment{}, fmt.Errorf("mode must be standard or spa")
		}
		mode = "standard"
		if site != nil {
			mode = firstNonEmpty(site.Mode, "standard")
		}
	}
	requestedDomains, err := s.siteDomainsFromRequest(siteID, req.Domains, "", false, true, boolPtr(false))
	if err != nil {
		return model.SiteDeployment{}, err
	}
	if site == nil {
		site, err = s.db.CreateSite(ctx, siteID, "", mode, profileName, target, requestedDomains)
		if err != nil {
			return model.SiteDeployment{}, err
		}
	} else {
		domains := mergeDomains(site.Domains, requestedDomains)
		site, err = s.db.CreateSite(ctx, siteID, site.Name, mode, profileName, target, domains)
		if err != nil {
			return model.SiteDeployment{}, err
		}
	}
	publishedAt := time.Now().UTC()
	if strings.TrimSpace(req.PublishedAtUTC) != "" {
		parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(req.PublishedAtUTC))
		if err != nil {
			return model.SiteDeployment{}, fmt.Errorf("published_at_utc must be RFC3339: %w", err)
		}
		publishedAt = parsed.UTC()
	}
	var verifiedAt time.Time
	if strings.TrimSpace(req.VerifiedAtUTC) != "" {
		parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(req.VerifiedAtUTC))
		if err != nil {
			return model.SiteDeployment{}, fmt.Errorf("verified_at_utc must be RFC3339: %w", err)
		}
		verifiedAt = parsed.UTC()
	}
	static := model.CloudflareStaticDeployment{
		WorkerName:         req.WorkerName,
		VersionID:          strings.TrimSpace(req.VersionID),
		Domains:            requestedDomains,
		URLs:               httpsDomainURLs(requestedDomains),
		CompatibilityDate:  strings.TrimSpace(req.CompatibilityDate),
		AssetsSHA256:       strings.TrimSpace(req.AssetsSHA256),
		CachePolicy:        strings.TrimSpace(req.CachePolicy),
		HeadersGenerated:   req.HeadersGenerated,
		NotFoundHandling:   strings.TrimSpace(req.NotFoundHandling),
		VerificationStatus: strings.TrimSpace(req.VerificationStatus),
		VerifiedAt:         verifiedAt,
		PublishedAt:        publishedAt,
	}
	deploymentID := newDeploymentID()
	manifest := siteDeployManifest{
		Version:          3,
		Kind:             "supercdn-cloudflare-static-deployment",
		StorageLayout:    "cloudflare_static",
		SiteID:           siteID,
		DeploymentID:     deploymentID,
		Environment:      environment,
		RouteProfile:     profileName,
		DeploymentTarget: target,
		CreatedAtUTC:     publishedAt.Format(time.RFC3339Nano),
		FileCount:        req.FileCount,
		TotalSize:        req.TotalSize,
		CloudflareStatic: &static,
		DeliverySummary:  map[string]int{"cloudflare_static": req.FileCount},
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return model.SiteDeployment{}, err
	}
	dep, err := s.db.CreateSiteDeployment(ctx, model.SiteDeployment{
		ID:               deploymentID,
		SiteID:           siteID,
		Environment:      environment,
		Status:           model.SiteDeploymentReady,
		RouteProfile:     profileName,
		DeploymentTarget: target,
		Version:          deploymentID,
		Pinned:           req.Pinned,
		FileCount:        req.FileCount,
		TotalSize:        req.TotalSize,
		ManifestJSON:     string(raw),
		ReadyAt:          publishedAt,
	})
	if err != nil {
		return model.SiteDeployment{}, err
	}
	if req.Promote {
		dep, err = s.db.ActivateSiteDeployment(ctx, siteID, dep.ID)
		if err != nil {
			return model.SiteDeployment{}, err
		}
	}
	return s.siteDeploymentView(ctx, dep), nil
}

type siteZipEntry struct {
	file *zip.File
	path string
}

func (s *Server) buildSiteDeployment(ctx context.Context, dep *model.SiteDeployment, payload deploySitePayload) (*model.SiteDeployment, error) {
	site, err := s.db.GetSite(ctx, dep.SiteID)
	if err != nil {
		return nil, err
	}
	profile, ok := s.cfg.Profile(dep.RouteProfile)
	if !ok {
		return nil, fmt.Errorf("unknown route_profile %q", dep.RouteProfile)
	}
	reader, err := zip.OpenReader(payload.StagedPath)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	entries, rules, err := readSiteZipEntries(reader.File)
	if err != nil {
		return nil, err
	}
	if rules.Mode == "" {
		rules.Mode = site.Mode
	}
	if rules.NotFound == "" {
		rules.NotFound = "404.html"
	}
	if err := s.checkDeploymentFileCount(dep.Environment, len(entries)); err != nil {
		return nil, err
	}
	var totalSize, largestFileSize int64
	for _, entry := range entries {
		size := int64(entry.file.UncompressedSize64)
		totalSize += size
		if size > largestFileSize {
			largestFileSize = size
		}
	}
	inspect := inspectSiteZipEntries(entries)
	if _, err := s.preflightProfile(ctx, dep.RouteProfile, profile, preflightRequest{
		TotalSize:       totalSize,
		LargestFileSize: largestFileSize,
		BatchFileCount:  len(entries),
	}); err != nil {
		return nil, err
	}
	artifact, err := statLocalFile(payload.StagedPath, payload.FileName)
	if err != nil {
		return nil, err
	}
	artifactKey := storage.JoinKey("sites", dep.SiteID, "artifacts", dep.ID+".zip")
	artifactObj, _, err := s.putObjectFromFile(ctx, putObjectInput{
		ProjectID:      "site-artifacts:" + dep.SiteID,
		ObjectPath:     dep.ID + ".zip",
		Key:            artifactKey,
		Profile:        profile,
		ProfileName:    dep.RouteProfile,
		CacheControl:   firstNonEmpty(profile.DefaultCacheControl, "private, max-age=0"),
		ContentType:    "application/zip",
		FilePath:       payload.StagedPath,
		FileName:       payload.FileName,
		Size:           artifact.Size,
		SHA256:         artifact.SHA256,
		BatchFileCount: len(entries) + 1,
	})
	if err != nil {
		return nil, err
	}
	manifest := siteDeployManifest{
		Version:          2,
		StorageLayout:    "verbatim",
		SiteID:           dep.SiteID,
		DeploymentID:     dep.ID,
		Environment:      dep.Environment,
		RouteProfile:     dep.RouteProfile,
		DeploymentTarget: dep.DeploymentTarget,
		CreatedAtUTC:     time.Now().UTC().Format(time.RFC3339Nano),
		Rules:            rules,
		Inspect:          &inspect,
		DeliverySummary:  map[string]int{},
		ArtifactSHA256:   artifact.SHA256,
		ArtifactSize:     artifact.Size,
	}
	rulesRaw, _ := json.Marshal(rules)
	for _, entry := range entries {
		obj, file, err := s.putSiteZipEntry(ctx, dep, profile, entry, len(entries))
		if err != nil {
			return nil, err
		}
		if err := s.db.AddSiteDeploymentFile(ctx, file); err != nil {
			return nil, err
		}
		manifest.Files = append(manifest.Files, siteDeployManifestFile{
			Path:         file.Path,
			Size:         file.Size,
			SHA256:       file.SHA256,
			ContentType:  file.ContentType,
			CacheControl: file.CacheControl,
			Delivery:     siteDeliveryMode(rules, file.Path),
			ObjectID:     obj.ID,
		})
		manifest.DeliverySummary[siteDeliveryMode(rules, file.Path)]++
		manifest.FileCount++
		manifest.TotalSize += file.Size
	}
	manifestRaw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	manifestPath, manifestStat, err := writeTempPayload(s.staging, "site-manifest-*", manifestRaw, "manifest.json")
	if err != nil {
		return nil, err
	}
	defer os.Remove(manifestPath)
	manifestKey := storage.JoinKey("sites", dep.SiteID, "manifests", dep.ID+".json")
	manifestObj, _, err := s.putObjectFromFile(ctx, putObjectInput{
		ProjectID:      "site-manifests:" + dep.SiteID,
		ObjectPath:     dep.ID + ".json",
		Key:            manifestKey,
		Profile:        profile,
		ProfileName:    dep.RouteProfile,
		CacheControl:   firstNonEmpty(profile.DefaultCacheControl, "public, max-age=300"),
		ContentType:    "application/json",
		FilePath:       manifestPath,
		FileName:       "manifest.json",
		Size:           manifestStat.Size,
		SHA256:         manifestStat.SHA256,
		BatchFileCount: 1,
	})
	if err != nil {
		return nil, err
	}
	dep.ArtifactObjectID = artifactObj.ID
	dep.ArtifactKey = artifactKey
	dep.ArtifactSHA256 = artifact.SHA256
	dep.ArtifactSize = artifact.Size
	dep.ManifestObjectID = manifestObj.ID
	dep.ManifestKey = manifestKey
	dep.FileCount = manifest.FileCount
	dep.TotalSize = manifest.TotalSize
	dep.ManifestJSON = string(manifestRaw)
	dep.RulesJSON = string(rulesRaw)
	return s.db.MarkSiteDeploymentReady(ctx, *dep)
}

func (s *Server) putSiteZipEntry(ctx context.Context, dep *model.SiteDeployment, profile config.RouteProfile, entry siteZipEntry, batchFileCount int) (*model.Object, model.SiteDeploymentFile, error) {
	rc, err := entry.file.Open()
	if err != nil {
		return nil, model.SiteDeploymentFile{}, err
	}
	staged, err := s.stageUpload(rc, entry.path)
	_ = rc.Close()
	if err != nil {
		return nil, model.SiteDeploymentFile{}, err
	}
	defer os.Remove(staged.Path)
	key := siteDeploymentRootKey(dep.SiteID, dep.ID, entry.path)
	contentType := firstNonEmpty(mimeByName(entry.path), staged.ContentType)
	obj, _, err := s.putObjectFromFile(ctx, putObjectInput{
		ProjectID:      "site-deployment:" + dep.SiteID + ":" + dep.ID,
		ObjectPath:     entry.path,
		Key:            key,
		Profile:        profile,
		ProfileName:    dep.RouteProfile,
		CacheControl:   firstNonEmpty(profile.DefaultCacheControl, "public, max-age=300"),
		ContentType:    contentType,
		FilePath:       staged.Path,
		FileName:       path.Base(entry.path),
		Size:           staged.Size,
		SHA256:         staged.SHA256,
		BatchFileCount: batchFileCount,
	})
	if err != nil {
		return nil, model.SiteDeploymentFile{}, err
	}
	return obj, model.SiteDeploymentFile{
		DeploymentID: dep.ID,
		Path:         entry.path,
		ObjectID:     obj.ID,
		Size:         obj.Size,
		SHA256:       obj.SHA256,
		ContentType:  obj.ContentType,
		CacheControl: obj.CacheControl,
	}, nil
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid job id")
		return
	}
	job, err := s.db.GetJob(r.Context(), id)
	if err != nil {
		if db.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "job not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleGetInitJob(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid job id")
		return
	}
	job, err := s.db.GetJob(r.Context(), id)
	if err != nil {
		if db.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "job not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if job.Type != model.JobInitResourceLibraries {
		writeError(w, http.StatusNotFound, "init job not found")
		return
	}
	writeJSON(w, http.StatusOK, jobView(job))
}

func (s *Server) handleObjectReplicas(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid object id")
		return
	}
	replicas, err := s.db.Replicas(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, replicas)
}

func (s *Server) handlePurgeCache(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URLs              []string `json:"urls"`
		CloudflareAccount string   `json:"cloudflare_account"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	account, ok := s.cfg.CloudflareAccountByName(req.CloudflareAccount)
	if !ok {
		writeError(w, http.StatusBadRequest, "cloudflare account not found")
		return
	}
	cf := s.cloudflareClientForAccount(account)
	if !cf.Configured() {
		writeJSON(w, http.StatusOK, map[string]any{"status": "skipped", "reason": "cloudflare zone_id/api_token not configured"})
		return
	}
	raw, err := cf.PurgeCache(r.Context(), req.URLs)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func (s *Server) servePublic(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if strings.HasPrefix(r.URL.Path, "/o/") {
		s.serveAsset(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/a/") {
		s.serveBucketAsset(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/dl/") {
		s.serveDownloadRedirect(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/p/") {
		s.serveSitePreview(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/s/") {
		s.serveSiteDebug(w, r)
		return
	}
	s.serveSiteByHost(w, r)
}

func (s *Server) serveAsset(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/o/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 {
		writeError(w, http.StatusNotFound, "asset not found")
		return
	}
	projectID := cleanID(parts[0])
	objectPath, err := storage.CleanObjectPath(parts[1])
	if err != nil {
		writeError(w, http.StatusNotFound, "asset not found")
		return
	}
	obj, err := s.db.GetObjectByProjectPath(r.Context(), projectID, objectPath)
	if err != nil {
		writeError(w, http.StatusNotFound, "asset not found")
		return
	}
	s.streamObject(w, r, obj, http.StatusOK)
}

func (s *Server) serveBucketAsset(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/a/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 {
		writeError(w, http.StatusNotFound, "asset not found")
		return
	}
	bucket := cleanBucketSlug(parts[0])
	objectPath, err := storage.CleanObjectPath(parts[1])
	if err != nil || bucket == "" {
		writeError(w, http.StatusNotFound, "asset not found")
		return
	}
	item, err := s.db.GetAssetBucketObject(r.Context(), bucket, objectPath)
	if err != nil {
		writeError(w, http.StatusNotFound, "asset not found")
		return
	}
	obj, err := s.db.GetObject(r.Context(), item.ObjectID)
	if err != nil {
		writeError(w, http.StatusNotFound, "asset not found")
		return
	}
	s.streamObject(w, r, obj, http.StatusOK)
}

func (s *Server) serveDownloadRedirect(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/dl/")
	parts := strings.SplitN(rest, "/", 4)
	if len(parts) != 4 || parts[0] != "sites" {
		writeError(w, http.StatusNotFound, "download not found")
		return
	}
	siteID := cleanID(parts[1])
	deploymentID := cleanDeploymentID(parts[2])
	objectPath, err := storage.CleanObjectPath(parts[3])
	if err != nil {
		writeError(w, http.StatusNotFound, "download not found")
		return
	}
	dep, err := s.db.GetSiteDeployment(r.Context(), deploymentID)
	if err != nil || dep.SiteID != siteID {
		writeError(w, http.StatusNotFound, "download not found")
		return
	}
	obj, err := s.db.SiteDeploymentFileObject(r.Context(), dep.ID, objectPath)
	if err != nil {
		writeError(w, http.StatusNotFound, "download not found")
		return
	}
	target, err := s.objectRedirectURL(r.Context(), obj)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.logger.Info("site direct download redirect", "site", siteID, "deployment", deploymentID, "path", objectPath, "object_id", obj.ID, "target", obj.PrimaryTarget)
	w.Header().Set("Cache-Control", firstNonEmpty(obj.CacheControl, "public, max-age=300"))
	w.Header().Set("X-SuperCDN-Redirect", "storage")
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *Server) serveSiteDebug(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/s/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "site not found")
		return
	}
	site, err := s.db.GetSite(r.Context(), cleanID(parts[0]))
	if err != nil {
		writeError(w, http.StatusNotFound, "site not found")
		return
	}
	reqPath := "/"
	if len(parts) == 2 {
		reqPath = "/" + parts[1]
	}
	s.serveSitePath(w, r, site, reqPath)
}

func (s *Server) serveSitePreview(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/p/")
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) < 2 {
		writeError(w, http.StatusNotFound, "deployment not found")
		return
	}
	siteID := cleanID(parts[0])
	deploymentID := cleanDeploymentID(parts[1])
	site, err := s.db.GetSite(r.Context(), siteID)
	if err != nil {
		writeError(w, http.StatusNotFound, "site not found")
		return
	}
	dep, err := s.db.GetSiteDeployment(r.Context(), deploymentID)
	if err != nil || dep.SiteID != site.ID || dep.Status == model.SiteDeploymentFailed || dep.Status == model.SiteDeploymentQueued || dep.Status == model.SiteDeploymentProcessing {
		writeError(w, http.StatusNotFound, "deployment not found")
		return
	}
	reqPath := "/"
	if len(parts) == 3 {
		reqPath = "/" + parts[2]
	}
	s.serveSiteDeploymentPath(w, r, site, dep, reqPath, true)
}

func (s *Server) serveSiteByHost(w http.ResponseWriter, r *http.Request) {
	host := cleanHost(r.Host)
	if forwarded := cleanHost(r.Header.Get("X-Forwarded-Host")); forwarded != "" {
		host = forwarded
	}
	if host == "" {
		writeError(w, http.StatusNotFound, "site not found")
		return
	}
	site, err := s.db.SiteByHost(r.Context(), host)
	if err != nil {
		writeError(w, http.StatusNotFound, "site not found")
		return
	}
	s.serveSitePath(w, r, site, r.URL.Path)
}

func (s *Server) serveSitePath(w http.ResponseWriter, r *http.Request, site *model.Site, reqPath string) {
	if dep, err := s.db.ActiveSiteDeployment(r.Context(), site.ID); err == nil {
		s.serveSiteDeploymentPath(w, r, site, dep, reqPath, false)
		return
	}
	writeError(w, http.StatusNotFound, "active deployment not found")
}

func (s *Server) serveSiteDeploymentPath(w http.ResponseWriter, r *http.Request, site *model.Site, dep *model.SiteDeployment, reqPath string, preview bool) {
	rules := deploymentRules(dep, site)
	cleanReq := cleanRequestPath(reqPath)
	for _, rule := range rules.Redirects {
		if siteRuleMatch(rule.From, cleanReq) {
			http.Redirect(w, r, rule.To, rule.Status)
			return
		}
	}
	servePath := cleanReq
	for _, rule := range rules.Rewrites {
		if siteRuleMatch(rule.From, cleanReq) && rule.To != "" {
			servePath = rule.To
			break
		}
	}
	if preview {
		w.Header().Set("X-Robots-Tag", "noindex")
	}
	mode := firstNonEmpty(rules.Mode, site.Mode, "standard")
	candidates := sitePathCandidates(servePath, mode)
	for _, candidate := range candidates {
		obj, err := s.db.SiteDeploymentFileObject(r.Context(), dep.ID, candidate)
		if err == nil {
			s.writeSiteFile(w, r, site, dep, obj, rules, candidate, http.StatusOK, preview)
			return
		}
	}
	if mode == "spa" {
		if obj, err := s.db.SiteDeploymentFileObject(r.Context(), dep.ID, "index.html"); err == nil {
			s.writeSiteFile(w, r, site, dep, obj, rules, "index.html", http.StatusOK, preview)
			return
		}
	}
	notFound := firstNonEmpty(rules.NotFound, "404.html")
	if obj, err := s.db.SiteDeploymentFileObject(r.Context(), dep.ID, notFound); err == nil {
		s.writeSiteFile(w, r, site, dep, obj, rules, notFound, http.StatusNotFound, preview)
		return
	}
	writeError(w, http.StatusNotFound, "not found")
}

func (s *Server) writeSiteFile(w http.ResponseWriter, r *http.Request, site *model.Site, dep *model.SiteDeployment, obj *model.Object, rules siteRules, objectPath string, status int, preview bool) {
	headers := siteHeadersForPath(rules, "/"+objectPath)
	for key, value := range headers {
		if strings.EqualFold(key, "Cache-Control") {
			copyObj := *obj
			copyObj.CacheControl = value
			obj = &copyObj
			continue
		}
		w.Header().Set(key, value)
	}
	if s.shouldRedirectSiteFile(r, rules, objectPath, status) {
		target, err := s.objectRedirectURL(r.Context(), obj)
		if err == nil && target != "" {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-SuperCDN-Redirect", "storage")
			http.Redirect(w, r, target, http.StatusFound)
			return
		}
		s.logger.Warn("site file storage redirect unavailable", "site", site.ID, "deployment", dep.ID, "path", objectPath, "object_id", obj.ID, "err", err)
	}
	s.streamObject(w, r, obj, status)
}

func (s *Server) shouldRedirectSiteFile(r *http.Request, rules siteRules, objectPath string, status int) bool {
	if status != http.StatusOK || r.Header.Get("Range") != "" {
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if s.edgeOriginDeliveryRequested(r) {
		return false
	}
	return siteDeliveryMode(rules, objectPath) == "redirect"
}

func (s *Server) edgeOriginDeliveryRequested(r *http.Request) bool {
	if !strings.EqualFold(r.Header.Get("X-SuperCDN-Origin-Delivery"), "origin") {
		return false
	}
	got := r.Header.Get("X-SuperCDN-Edge-Secret")
	for _, account := range s.cfg.CloudflareAccountsEffective() {
		secret := strings.TrimSpace(account.EdgeBypassSecret)
		if secret != "" && subtle.ConstantTimeCompare([]byte(got), []byte(secret)) == 1 {
			return true
		}
	}
	return false
}

func (s *Server) streamObject(w http.ResponseWriter, r *http.Request, obj *model.Object, statusOverride int) {
	replicas, err := s.db.Replicas(r.Context(), obj.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sort.SliceStable(replicas, func(i, j int) bool {
		if replicas[i].Target == obj.PrimaryTarget {
			return true
		}
		if replicas[j].Target == obj.PrimaryTarget {
			return false
		}
		return replicas[i].ID < replicas[j].ID
	})
	var lastErr error
	for _, replica := range replicas {
		if replica.Status != model.ReplicaReady {
			continue
		}
		store, ok := s.stores.Get(replica.Target)
		if !ok {
			continue
		}
		if target := s.objectReplicaRedirectURL(r, obj, replica, statusOverride); target != "" {
			http.Redirect(w, r, target, http.StatusFound)
			return
		}
		stream, err := store.Get(r.Context(), obj.Key, storage.GetOptions{Range: r.Header.Get("Range"), Locator: replica.Locator})
		if err != nil {
			lastErr = err
			continue
		}
		defer stream.Body.Close()
		s.writeObjectStream(w, r, obj, stream, statusOverride)
		return
	}
	if lastErr != nil {
		writeError(w, http.StatusBadGateway, lastErr.Error())
		return
	}
	writeError(w, http.StatusNotFound, "ready replica not found")
}

func (s *Server) objectReplicaRedirectURL(r *http.Request, obj *model.Object, replica model.Replica, statusOverride int) string {
	if statusOverride != http.StatusOK || r.Header.Get("Range") != "" || replica.Locator == "" {
		return ""
	}
	profile, ok := s.cfg.Profile(obj.RouteProfile)
	if !ok || !profile.AllowRedirect {
		return ""
	}
	return directLocatorURL(replica.Locator)
}

func (s *Server) objectRedirectURL(ctx context.Context, obj *model.Object) (string, error) {
	replicas, err := s.db.Replicas(ctx, obj.ID)
	if err != nil {
		return "", err
	}
	sort.SliceStable(replicas, func(i, j int) bool {
		if replicas[i].Target == obj.PrimaryTarget {
			return true
		}
		if replicas[j].Target == obj.PrimaryTarget {
			return false
		}
		return replicas[i].ID < replicas[j].ID
	})
	for _, replica := range replicas {
		if replica.Status != model.ReplicaReady {
			continue
		}
		store, ok := s.stores.Get(replica.Target)
		if ok {
			stat, err := store.Stat(ctx, obj.Key)
			if err == nil {
				if target := directLocatorURL(stat.Locator); target != "" {
					return target, nil
				}
			}
		}
		if target := directLocatorURL(replica.Locator); target != "" {
			return target, nil
		}
		if !ok {
			continue
		}
		if public := store.PublicURL(obj.Key); public != "" {
			if target := directLocatorURL(public); target != "" {
				return target, nil
			}
		}
	}
	return "", fmt.Errorf("direct storage URL not available for object %d", obj.ID)
}

func (s *Server) writeObjectStream(w http.ResponseWriter, r *http.Request, obj *model.Object, stream *storage.ObjectStream, statusOverride int) {
	if stream.ContentType != "" {
		w.Header().Set("Content-Type", stream.ContentType)
	} else if obj.ContentType != "" {
		w.Header().Set("Content-Type", obj.ContentType)
	}
	if cc := firstNonEmpty(obj.CacheControl, stream.CacheControl); cc != "" {
		w.Header().Set("Cache-Control", cc)
	}
	if obj.SHA256 != "" {
		w.Header().Set("ETag", `"`+obj.SHA256+`"`)
	} else if stream.ETag != "" {
		w.Header().Set("ETag", stream.ETag)
	}
	if !stream.LastModified.IsZero() {
		w.Header().Set("Last-Modified", stream.LastModified.UTC().Format(http.TimeFormat))
	}
	w.Header().Set("Accept-Ranges", "bytes")
	if stream.ContentRange != "" {
		w.Header().Set("Content-Range", stream.ContentRange)
	}
	if stream.Size >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(stream.Size, 10))
	}
	status := stream.StatusCode
	if status == 0 {
		status = http.StatusOK
	}
	if statusOverride != http.StatusOK && status == http.StatusOK {
		status = statusOverride
	}
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = io.Copy(w, stream.Body)
	}
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
			continue
		}
		view := resourceLibraryStatusView{Name: name}
		for i, binding := range config.Bindings {
			bindingName := bindingConfigName(binding, i)
			bindingView := resourceLibraryBindingView{
				Name:       bindingName,
				Path:       binding.Path,
				MountPoint: binding.MountPoint,
				Status:     "unknown",
			}
			if reason := skipped[name]; reason != "" {
				bindingView.Skipped = true
				bindingView.SkipReason = reason
			}
			if health, ok := healthByKey[name+"/"+bindingName]; ok {
				healthCopy := health
				bindingView.Status = health.Status
				bindingView.Health = &healthCopy
			}
			view.Bindings = append(view.Bindings, bindingView)
		}
		views = append(views, view)
	}
	return views, nil
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

func (s *Server) initAssetBucket(ctx context.Context, bucket *model.AssetBucket, dryRun bool) (*initAssetBucketResult, error) {
	dirs := bucketInitDirs(bucket.Slug)
	result := &initAssetBucketResult{Bucket: bucket.Slug, DryRun: dryRun, Directories: dirs, Status: "skipped"}
	profile, ok := s.cfg.Profile(bucket.RouteProfile)
	if !ok {
		return result, fmt.Errorf("unknown route_profile %q", bucket.RouteProfile)
	}
	store, ok := s.stores.Get(profile.Primary)
	if !ok {
		return result, fmt.Errorf("primary storage %q is not configured", profile.Primary)
	}
	initializer, ok := store.(storage.InitializableStore)
	if !ok {
		result.Reason = "primary storage does not support directory initialization"
		return result, nil
	}
	run := func() error {
		var err error
		result.Result, err = initializer.InitDirs(ctx, storage.InitOptions{Directories: dirs, DryRun: dryRun})
		return err
	}
	var err error
	if dryRun {
		err = run()
	} else {
		err = s.withTransferSlot(ctx, run)
	}
	if err != nil {
		result.Status = "error"
		return result, err
	}
	result.Status = "ok"
	return result, nil
}

func (s *Server) putBucketObjectFromStaged(ctx context.Context, bucket *model.AssetBucket, logicalPath string, staged *stagedFile, fileName, explicitType, cacheControl string) (*model.AssetBucketObject, *model.Object, []model.Job, error) {
	if bucket.Status != model.AssetBucketActive {
		return nil, nil, nil, fmt.Errorf("bucket %q is %s", bucket.Slug, bucket.Status)
	}
	profileName := firstNonEmpty(bucket.RouteProfile, "china_all")
	profile, ok := s.cfg.Profile(profileName)
	if !ok {
		return nil, nil, nil, fmt.Errorf("unknown route_profile %q", profileName)
	}
	assetType, err := classifyAssetType(logicalPath, fileName, staged.ContentType, explicitType)
	if err != nil {
		return nil, nil, nil, err
	}
	if !s.overclockMode() && !bucketAllowsType(bucket, assetType) {
		return nil, nil, nil, fmt.Errorf("bucket %q does not allow asset type %q", bucket.Slug, assetType)
	}
	if !s.overclockMode() && bucket.MaxFileSizeBytes > 0 && staged.Size > bucket.MaxFileSizeBytes {
		return nil, nil, nil, fmt.Errorf("bucket %q max_file_size_bytes is %d, got %d", bucket.Slug, bucket.MaxFileSizeBytes, staged.Size)
	}
	if !s.overclockMode() && bucket.MaxCapacityBytes > 0 {
		used, err := s.db.AssetBucketUsedBytes(ctx, bucket.Slug)
		if err != nil {
			return nil, nil, nil, err
		}
		if existing, err := s.db.GetAssetBucketObject(ctx, bucket.Slug, logicalPath); err == nil {
			used -= existing.Size
			if used < 0 {
				used = 0
			}
		}
		if used+staged.Size > bucket.MaxCapacityBytes {
			return nil, nil, nil, fmt.Errorf("bucket %q max_capacity_bytes is %d, current used %d, upload got %d", bucket.Slug, bucket.MaxCapacityBytes, used, staged.Size)
		}
	}
	if _, err := s.preflightProfile(ctx, profileName, profile, preflightRequest{
		TotalSize:       staged.Size,
		LargestFileSize: staged.Size,
		BatchFileCount:  1,
	}); err != nil {
		return nil, nil, nil, err
	}
	projectID := "bucket:" + bucket.Slug
	if _, err := s.db.CreateProject(ctx, projectID); err != nil {
		return nil, nil, nil, err
	}
	key := bucketPhysicalKey(bucket.Slug, assetType, logicalPath, fileName, staged.SHA256)
	obj, jobs, err := s.putObjectFromFile(ctx, putObjectInput{
		ProjectID:      projectID,
		ObjectPath:     logicalPath,
		Key:            key,
		Profile:        profile,
		ProfileName:    profileName,
		CacheControl:   firstNonEmpty(strings.TrimSpace(cacheControl), bucket.DefaultCacheControl, profile.DefaultCacheControl, "public, max-age=3600"),
		ContentType:    staged.ContentType,
		FilePath:       staged.Path,
		FileName:       firstNonEmpty(fileName, path.Base(logicalPath)),
		Size:           staged.Size,
		SHA256:         staged.SHA256,
		BatchFileCount: 1,
	})
	if err != nil {
		return nil, nil, nil, err
	}
	item, err := s.db.SaveAssetBucketObject(ctx, model.AssetBucketObject{
		BucketSlug:  bucket.Slug,
		LogicalPath: logicalPath,
		ObjectID:    obj.ID,
		AssetType:   assetType,
		PhysicalKey: key,
		Size:        staged.Size,
		SHA256:      staged.SHA256,
		ContentType: obj.ContentType,
	})
	if err != nil {
		return nil, nil, nil, err
	}
	return item, obj, jobs, nil
}

func (s *Server) deleteBucketObject(ctx context.Context, bucketSlug, logicalPath string, deleteRemote bool) (*deleteBucketObjectResult, error) {
	bucket, err := s.db.GetAssetBucket(ctx, bucketSlug)
	if err != nil {
		return nil, err
	}
	item, err := s.db.GetAssetBucketObject(ctx, bucket.Slug, logicalPath)
	if err != nil {
		return nil, err
	}
	return s.deleteBucketObjectItem(ctx, bucket, *item, deleteRemote)
}

func (s *Server) deleteAssetBucket(ctx context.Context, bucket *model.AssetBucket, deleteObjects, deleteRemote bool) (*deleteAssetBucketResult, error) {
	items, err := s.db.ListAllAssetBucketObjects(ctx, bucket.Slug)
	if err != nil {
		return nil, err
	}
	result := &deleteAssetBucketResult{
		Bucket:        bucket.Slug,
		DeleteObjects: deleteObjects,
		DeleteRemote:  deleteRemote,
		ObjectCount:   len(items),
	}
	if len(items) > 0 && !deleteObjects {
		err := fmt.Errorf("bucket %q is not empty; pass delete_objects=true or force=true", bucket.Slug)
		result.Errors = append(result.Errors, err.Error())
		return result, err
	}
	for _, item := range items {
		deleted, err := s.deleteBucketObjectItem(ctx, bucket, item, deleteRemote)
		if deleted != nil {
			result.Objects = append(result.Objects, *deleted)
		}
		if err != nil {
			result.Errors = append(result.Errors, err.Error())
		}
	}
	if len(result.Errors) > 0 {
		return result, errors.New(strings.Join(result.Errors, "; "))
	}
	if err := s.db.DeleteAssetBucket(ctx, bucket.Slug); err != nil {
		result.Errors = append(result.Errors, err.Error())
		return result, err
	}
	result.DeletedBucket = true
	if err := s.db.DeleteProject(ctx, "bucket:"+bucket.Slug); err == nil {
		result.DeletedProject = true
	} else if !db.IsNotFound(err) {
		result.Errors = append(result.Errors, err.Error())
		return result, err
	}
	return result, nil
}

func (s *Server) deleteBucketObjectItem(ctx context.Context, bucket *model.AssetBucket, item model.AssetBucketObject, deleteRemote bool) (*deleteBucketObjectResult, error) {
	result := &deleteBucketObjectResult{
		Bucket:       bucket.Slug,
		LogicalPath:  item.LogicalPath,
		ObjectID:     item.ObjectID,
		DeleteRemote: deleteRemote,
	}
	obj, err := s.db.GetObject(ctx, item.ObjectID)
	if err != nil {
		if db.IsNotFound(err) {
			if deleteErr := s.db.DeleteAssetBucketObject(ctx, bucket.Slug, item.LogicalPath); deleteErr != nil {
				result.Errors = append(result.Errors, deleteErr.Error())
				return result, deleteErr
			}
			result.DeletedLocal = true
			return result, nil
		}
		result.Errors = append(result.Errors, err.Error())
		return result, err
	}
	result.Key = obj.Key
	if deleteRemote {
		if err := s.withTransferSlot(ctx, func() error {
			return s.deleteObjectRemoteReplicas(ctx, obj, result)
		}); err != nil {
			result.Errors = append(result.Errors, err.Error())
			return result, err
		}
	}
	if err := s.db.DeleteObject(ctx, obj.ID); err != nil {
		result.Errors = append(result.Errors, err.Error())
		return result, err
	}
	result.DeletedLocal = true
	return result, nil
}

func (s *Server) deleteObjectRemoteReplicas(ctx context.Context, obj *model.Object, result *deleteBucketObjectResult) error {
	replicas, err := s.db.Replicas(ctx, obj.ID)
	if err != nil {
		return err
	}
	targets := map[string]bool{}
	if obj.PrimaryTarget != "" {
		targets[obj.PrimaryTarget] = true
	}
	for _, replica := range replicas {
		if replica.Target != "" {
			targets[replica.Target] = true
		}
	}
	names := make([]string, 0, len(targets))
	for target := range targets {
		names = append(names, target)
	}
	sort.Strings(names)
	var errs []string
	for _, target := range names {
		item := deleteReplicaResult{Target: target}
		store, ok := s.stores.Get(target)
		if !ok {
			item.Status = "error"
			item.Error = fmt.Sprintf("storage %q is not configured", target)
			errs = append(errs, item.Error)
			result.Remote = append(result.Remote, item)
			continue
		}
		if err := store.Delete(ctx, obj.Key); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				item.Status = "not_found"
			} else {
				item.Status = "error"
				item.Error = err.Error()
				errs = append(errs, fmt.Sprintf("%s: %s", target, err.Error()))
			}
		} else {
			item.Status = "deleted"
		}
		result.Remote = append(result.Remote, item)
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
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
	var jobs []model.Job
	for _, target := range in.Profile.Backups {
		if target == "" || target == in.Profile.Primary {
			continue
		}
		if _, ok := s.stores.Get(target); !ok {
			return nil, nil, fmt.Errorf("backup storage %q is not configured", target)
		}
		if _, err := s.db.UpsertReplica(ctx, obj.ID, target, model.ReplicaPending, "", ""); err != nil {
			return nil, nil, err
		}
		payload, _ := json.Marshal(replicatePayload{ObjectID: obj.ID, Target: target})
		job, err := s.db.CreateJob(ctx, model.JobReplicateObject, string(payload))
		if err != nil {
			return nil, nil, err
		}
		jobs = append(jobs, *job)
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

type replicatePayload struct {
	ObjectID int64  `json:"object_id"`
	Target   string `json:"target"`
}

func (s *Server) replicateObject(ctx context.Context, payload replicatePayload) error {
	obj, err := s.db.GetObject(ctx, payload.ObjectID)
	if err != nil {
		return err
	}
	target, ok := s.stores.Get(payload.Target)
	if !ok {
		return fmt.Errorf("target storage %q is not configured", payload.Target)
	}
	replicas, err := s.db.Replicas(ctx, obj.ID)
	if err != nil {
		return err
	}
	sort.SliceStable(replicas, func(i, j int) bool {
		if replicas[i].Target == obj.PrimaryTarget {
			return true
		}
		if replicas[j].Target == obj.PrimaryTarget {
			return false
		}
		return replicas[i].ID < replicas[j].ID
	})
	var sourceReplica *model.Replica
	for i := range replicas {
		if replicas[i].Target != payload.Target && replicas[i].Status == model.ReplicaReady {
			sourceReplica = &replicas[i]
			break
		}
	}
	if sourceReplica == nil {
		return fmt.Errorf("no ready source replica for object %d", obj.ID)
	}
	source, ok := s.stores.Get(sourceReplica.Target)
	if !ok {
		return fmt.Errorf("source storage %q is not configured", sourceReplica.Target)
	}
	stream, err := source.Get(ctx, obj.Key, storage.GetOptions{Locator: sourceReplica.Locator})
	if err != nil {
		_, _ = s.db.UpsertReplica(ctx, obj.ID, payload.Target, model.ReplicaFailed, "", err.Error())
		return err
	}
	defer stream.Body.Close()
	tmp, err := os.CreateTemp(s.staging, "replica-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := io.Copy(tmp, stream.Body); closeErr(tmp, err) != nil {
		return closeErr(tmp, err)
	}
	var locator string
	err = s.withTransferSlot(ctx, func() error {
		var putErr error
		locator, putErr = target.Put(ctx, storage.PutOptions{
			Key:            obj.Key,
			FilePath:       tmpPath,
			ContentType:    obj.ContentType,
			CacheControl:   obj.CacheControl,
			SHA256:         obj.SHA256,
			Size:           obj.Size,
			FileName:       path.Base(obj.Path),
			BatchFileCount: 1,
			IgnoreLimits:   s.overclockMode(),
		})
		return putErr
	})
	if err != nil {
		_, _ = s.db.UpsertReplica(ctx, obj.ID, payload.Target, model.ReplicaFailed, "", err.Error())
		return err
	}
	_, err = s.db.UpsertReplica(ctx, obj.ID, payload.Target, model.ReplicaReady, locator, "")
	return err
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

func (s *Server) purgeAssetBucketCache(ctx context.Context, bucket *model.AssetBucket, req assetBucketCacheRequest) purgeAssetBucketCacheResponse {
	resp := purgeAssetBucketCacheResponse{Bucket: bucket.Slug, DryRun: req.DryRun, Status: "planned"}
	urls, warnings, err := s.assetBucketCacheURLs(ctx, bucket.Slug, req)
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
	account, library, err := s.cloudflareAccountForCacheBase(firstNonEmpty(req.BaseURL, s.cfg.Server.PublicBaseURL), req.CloudflareAccount, req.CloudflareLibrary)
	resp.CloudflareAccount = account.Name
	resp.CloudflareLibrary = library.Name
	if err != nil {
		resp.Status = "failed"
		resp.Errors = append(resp.Errors, err.Error())
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

func (s *Server) warmupAssetBucket(ctx context.Context, bucket *model.AssetBucket, req assetBucketCacheRequest) warmupAssetBucketResponse {
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodHead
	}
	resp := warmupAssetBucketResponse{Bucket: bucket.Slug, DryRun: req.DryRun, Method: method, Status: "planned"}
	if method != http.MethodHead && method != http.MethodGet {
		resp.Status = "failed"
		resp.Errors = append(resp.Errors, "method must be HEAD or GET")
		return resp
	}
	urls, warnings, err := s.assetBucketCacheURLs(ctx, bucket.Slug, req)
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
	client := &http.Client{Timeout: 30 * time.Second}
	resp.Results = make([]bucketWarmupResult, 0, len(urls))
	resp.Status = "ok"
	for _, warmURL := range urls {
		result := s.warmupURL(ctx, client, method, warmURL)
		resp.Results = append(resp.Results, result)
		if result.Status != "ok" {
			resp.Status = "partial"
			resp.Errors = append(resp.Errors, warmURL+": "+result.Error)
		}
	}
	return resp
}

func (s *Server) warmupURL(ctx context.Context, client *http.Client, method, warmURL string) bucketWarmupResult {
	result := bucketWarmupResult{URL: warmURL, Method: method, Status: "ok"}
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, method, warmURL, nil)
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		return result
	}
	req.Header.Set("User-Agent", "SuperCDN-Warmup/1.0")
	resp, err := client.Do(req)
	result.LatencyMS = elapsedMS(start)
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		return result
	}
	defer resp.Body.Close()
	result.HTTPCode = resp.StatusCode
	if method == http.MethodGet {
		_, _ = io.Copy(io.Discard, resp.Body)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 399 {
		result.Status = "failed"
		result.Error = resp.Status
	}
	return result
}

func (s *Server) assetBucketCacheURLs(ctx context.Context, bucketSlug string, req assetBucketCacheRequest) ([]string, []string, error) {
	items, warnings, err := s.assetBucketCacheObjects(ctx, bucketSlug, req)
	if err != nil {
		return nil, warnings, err
	}
	urls := make([]string, 0, len(items))
	for _, item := range items {
		urls = append(urls, s.assetBucketPublicURL(req.BaseURL, item.BucketSlug, item.LogicalPath))
	}
	urls = uniqueStrings(urls)
	if len(urls) == 0 {
		return nil, warnings, fmt.Errorf("no asset bucket URLs generated")
	}
	return urls, warnings, nil
}

func (s *Server) assetBucketCacheObjects(ctx context.Context, bucketSlug string, req assetBucketCacheRequest) ([]model.AssetBucketObject, []string, error) {
	var warnings []string
	paths := append([]string{}, req.Paths...)
	if strings.TrimSpace(req.Path) != "" {
		paths = append(paths, req.Path)
	}
	if len(paths) > 0 {
		items := make([]model.AssetBucketObject, 0, len(paths))
		seen := map[string]bool{}
		for _, p := range paths {
			cleaned, err := storage.CleanObjectPath(p)
			if err != nil {
				return nil, warnings, err
			}
			if seen[cleaned] {
				continue
			}
			seen[cleaned] = true
			item, err := s.db.GetAssetBucketObject(ctx, bucketSlug, cleaned)
			if err != nil {
				return nil, warnings, fmt.Errorf("bucket object %q not found", cleaned)
			}
			items = append(items, *item)
		}
		return items, warnings, nil
	}
	prefix, err := storage.CleanDirectoryPath(req.Prefix)
	if err != nil {
		return nil, warnings, err
	}
	if prefix != "" {
		limit := req.Limit
		if limit <= 0 {
			limit = 1000
			warnings = append(warnings, "prefix selection defaulted to limit=1000")
		}
		items, err := s.db.ListAssetBucketObjects(ctx, bucketSlug, prefix, limit)
		return items, warnings, err
	}
	if req.All {
		items, err := s.db.ListAllAssetBucketObjects(ctx, bucketSlug)
		return items, warnings, err
	}
	return nil, warnings, fmt.Errorf("select at least one bucket object with path, paths, prefix, or all=true")
}

func (s *Server) assetBucketPublicURL(baseURL, bucketSlug, logicalPath string) string {
	escaped := "/a/" + url.PathEscape(bucketSlug) + "/" + escapeURLPath(logicalPath)
	if strings.TrimSpace(baseURL) != "" {
		return strings.TrimRight(strings.TrimSpace(baseURL), "/") + escaped
	}
	return s.absolutePublicURL(escaped)
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

func (s *Server) siteDeploymentFilePaths(ctx context.Context, dep *model.SiteDeployment) ([]string, error) {
	if dep.ManifestJSON != "" {
		var manifest siteDeployManifest
		if err := json.Unmarshal([]byte(dep.ManifestJSON), &manifest); err == nil && len(manifest.Files) > 0 {
			out := make([]string, 0, len(manifest.Files))
			for _, file := range manifest.Files {
				out = append(out, file.Path)
			}
			return out, nil
		}
	}
	files, err := s.db.ListSiteDeploymentFiles(ctx, dep.ID)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(files))
	for _, file := range files {
		out = append(out, file.Path)
	}
	return out, nil
}

func (s *Server) buildSiteEdgeManifest(ctx context.Context, site *model.Site, dep *model.SiteDeployment) (*edgeManifest, error) {
	if site == nil || dep == nil {
		return nil, fmt.Errorf("site and deployment are required")
	}
	files, err := s.db.ListSiteDeploymentFiles(ctx, dep.ID)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("deployment has no files")
	}
	rules := deploymentRules(dep, site)
	mode := firstNonEmpty(rules.Mode, site.Mode, "standard")
	manifest := &edgeManifest{
		Version:          1,
		Kind:             "supercdn-edge-manifest",
		SiteID:           site.ID,
		DeploymentID:     dep.ID,
		Environment:      dep.Environment,
		Status:           dep.Status,
		RouteProfile:     dep.RouteProfile,
		DeploymentTarget: dep.DeploymentTarget,
		Mode:             mode,
		GeneratedAtUTC:   time.Now().UTC().Format(time.RFC3339Nano),
		Rules:            rules,
		Routes:           map[string]edgeManifestRoute{},
	}
	objects := map[string]*model.Object{}
	fileByPath := map[string]model.SiteDeploymentFile{}
	for _, file := range files {
		obj, err := s.db.GetObject(ctx, file.ObjectID)
		if err != nil {
			return nil, fmt.Errorf("load object for %s: %w", file.Path, err)
		}
		objects[file.Path] = obj
		fileByPath[file.Path] = file
		route, warnings := s.edgeManifestRouteForFile(ctx, rules, file, obj, http.StatusOK)
		manifest.Warnings = append(manifest.Warnings, warnings...)
		addEdgeManifestRoute(manifest.Routes, edgeRoutePathForFile(file.Path), route, true)
	}
	for _, file := range files {
		route, ok := manifest.Routes[edgeRoutePathForFile(file.Path)]
		if !ok {
			continue
		}
		for _, alias := range edgeRouteAliasesForFile(file.Path) {
			addEdgeManifestRoute(manifest.Routes, alias, route, false)
		}
	}
	if mode == "spa" {
		if file, ok := fileByPath["index.html"]; ok {
			route, warnings := s.edgeManifestRouteForFile(ctx, rules, file, objects[file.Path], http.StatusOK)
			manifest.Warnings = append(manifest.Warnings, warnings...)
			manifest.Fallback = &route
		} else {
			manifest.Warnings = append(manifest.Warnings, "spa mode is enabled but index.html is not present")
		}
	}
	notFoundPath := firstNonEmpty(rules.NotFound, "404.html")
	if file, ok := fileByPath[notFoundPath]; ok {
		route, warnings := s.edgeManifestRouteForFile(ctx, rules, file, objects[file.Path], http.StatusNotFound)
		manifest.Warnings = append(manifest.Warnings, warnings...)
		manifest.NotFound = &route
	}
	return manifest, nil
}

func (s *Server) publishSiteEdgeManifest(ctx context.Context, site *model.Site, dep *model.SiteDeployment, req publishEdgeManifestRequest) (publishEdgeManifestResponse, error) {
	dryRun := true
	if req.DryRun != nil {
		dryRun = *req.DryRun
	}
	domains, err := s.siteBoundDomains(site, req.Domains)
	if err != nil {
		return publishEdgeManifestResponse{}, err
	}
	account, library, err := s.cloudflareAccountForDomains(domains, req.CloudflareAccount, req.CloudflareLibrary)
	if err != nil {
		return publishEdgeManifestResponse{}, err
	}
	manifest, err := s.buildSiteEdgeManifest(ctx, site, dep)
	if err != nil {
		return publishEdgeManifestResponse{}, err
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return publishEdgeManifestResponse{}, err
	}
	raw = append(raw, '\n')
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	keyPrefix := edgeManifestKVKeyPrefix(req.KeyPrefix)
	publishActive := boolPtrValue(req.ActiveKey, dep.Active)
	publishDeployment := boolPtrValue(req.DeploymentKey, true)
	if !publishActive && !publishDeployment {
		return publishEdgeManifestResponse{}, fmt.Errorf("at least one of active_key or deployment_key must be enabled")
	}
	resp := publishEdgeManifestResponse{
		SiteID:            site.ID,
		DeploymentID:      dep.ID,
		Active:            dep.Active,
		CloudflareAccount: account.Name,
		CloudflareLibrary: library.Name,
		KVNamespaceID:     strings.TrimSpace(req.KVNamespaceID),
		KVNamespace:       strings.TrimSpace(req.KVNamespace),
		KeyPrefix:         keyPrefix,
		Domains:           domains,
		DryRun:            dryRun,
		Status:            "planned",
		ManifestSize:      len(raw),
		ManifestSHA256:    hash,
		ManifestWarnings:  manifest.Warnings,
	}
	for _, domain := range domains {
		if publishDeployment {
			resp.Writes = append(resp.Writes, edgeManifestKVWrite{
				Domain: domain,
				Key:    edgeManifestDeploymentKVKey(keyPrefix, domain, dep.ID),
				Kind:   "deployment",
				DryRun: dryRun,
				Size:   len(raw),
				SHA256: hash,
			})
		}
		if publishActive {
			resp.Writes = append(resp.Writes, edgeManifestKVWrite{
				Domain: domain,
				Key:    edgeManifestActiveKVKey(keyPrefix, domain),
				Kind:   "active",
				DryRun: dryRun,
				Size:   len(raw),
				SHA256: hash,
			})
		}
	}
	if len(resp.Writes) == 0 {
		return publishEdgeManifestResponse{}, fmt.Errorf("no edge manifest KV keys generated")
	}
	cf := s.cloudflareClientForAccount(account)
	if resp.KVNamespaceID == "" && resp.KVNamespace != "" {
		if !cf.AccountConfigured() {
			resp.Warnings = append(resp.Warnings, "cloudflare account_id/api_token not configured; KV namespace title cannot be resolved")
		} else if namespace, err := cf.FindKVNamespace(ctx, resp.KVNamespace); err != nil {
			resp.Warnings = append(resp.Warnings, err.Error())
		} else {
			resp.KVNamespaceID = namespace.ID
			resp.KVNamespace = namespace.Title
		}
	}
	if resp.KVNamespaceID == "" {
		message := "kv_namespace_id is required to publish; pass -kv-namespace-id or -kv-namespace"
		if dryRun {
			resp.Warnings = append(resp.Warnings, message)
		} else {
			resp.Errors = append(resp.Errors, message)
			resp.Status = "skipped"
		}
	}
	if !dryRun && resp.KVNamespaceID != "" {
		resp.Status = "ok"
		for i := range resp.Writes {
			if err := cf.PutKVValue(ctx, resp.KVNamespaceID, resp.Writes[i].Key, raw); err != nil {
				resp.Writes[i].Action = "error"
				resp.Writes[i].Error = err.Error()
				resp.Errors = append(resp.Errors, resp.Writes[i].Key+": "+err.Error())
				resp.Status = "partial"
				continue
			}
			resp.Writes[i].Action = "put"
		}
	} else {
		for i := range resp.Writes {
			if resp.Status == "skipped" {
				resp.Writes[i].Action = "skipped"
			} else {
				resp.Writes[i].Action = "planned"
			}
		}
	}
	if dryRun && resp.Status == "planned" {
		return resp, nil
	}
	if len(resp.Errors) > 0 && resp.Status == "ok" {
		resp.Status = "partial"
	}
	return resp, nil
}

func (s *Server) edgeManifestRouteForFile(ctx context.Context, rules siteRules, file model.SiteDeploymentFile, obj *model.Object, status int) (edgeManifestRoute, []string) {
	headers := siteHeadersForPath(rules, "/"+file.Path)
	cacheControl := firstNonEmpty(file.CacheControl, obj.CacheControl)
	for key, value := range headers {
		if strings.EqualFold(key, "Cache-Control") {
			cacheControl = value
			delete(headers, key)
		}
	}
	if len(headers) == 0 {
		headers = nil
	}
	delivery := siteDeliveryMode(rules, file.Path)
	route := edgeManifestRoute{
		Type:               "origin",
		Delivery:           delivery,
		File:               file.Path,
		Status:             status,
		ContentType:        firstNonEmpty(file.ContentType, obj.ContentType),
		CacheControl:       cacheControl,
		ObjectCacheControl: firstNonEmpty(file.CacheControl, obj.CacheControl),
		Size:               file.Size,
		SHA256:             file.SHA256,
		ObjectID:           file.ObjectID,
		ObjectKey:          obj.Key,
		Headers:            headers,
	}
	if status != http.StatusOK || delivery != "redirect" {
		return route, nil
	}
	target, err := s.objectRedirectURL(ctx, obj)
	if err != nil || target == "" {
		if err == nil {
			err = fmt.Errorf("direct storage URL is empty")
		}
		return route, []string{fmt.Sprintf("redirect URL unavailable for %s: %v; route will use origin delivery", file.Path, err)}
	}
	route.Type = "redirect"
	route.Status = http.StatusFound
	route.Location = target
	route.CacheControl = "no-store"
	return route, nil
}

func addEdgeManifestRoute(routes map[string]edgeManifestRoute, routePath string, route edgeManifestRoute, overwrite bool) {
	routePath = cleanRequestPath(routePath)
	if _, ok := routes[routePath]; ok && !overwrite {
		return
	}
	routes[routePath] = route
}

func edgeRoutePathForFile(filePath string) string {
	return "/" + strings.TrimPrefix(filePath, "/")
}

func edgeRouteAliasesForFile(filePath string) []string {
	clean := strings.TrimPrefix(path.Clean("/"+strings.TrimPrefix(filePath, "/")), "/")
	switch {
	case clean == "index.html":
		return []string{"/"}
	case strings.HasSuffix(clean, "/index.html"):
		dir := strings.TrimSuffix(clean, "/index.html")
		return []string{"/" + dir, "/" + dir + "/"}
	default:
		return nil
	}
}

func edgeManifestKVKeyPrefix(value string) string {
	value = strings.Trim(strings.ReplaceAll(strings.TrimSpace(value), "\\", "/"), "/")
	if value == "" {
		return "sites/"
	}
	return value + "/"
}

func edgeManifestActiveKVKey(prefix, domain string) string {
	return edgeManifestKVKeyPrefix(prefix) + cleanHost(domain) + "/active/edge-manifest"
}

func edgeManifestDeploymentKVKey(prefix, domain, deploymentID string) string {
	return edgeManifestKVKeyPrefix(prefix) + cleanHost(domain) + "/deployments/" + cleanDeploymentID(deploymentID) + "/edge-manifest"
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

func normalizeAssetTypes(values []string) ([]string, error) {
	if len(values) == 0 {
		return []string{model.AssetTypeImage, model.AssetTypeVideo, model.AssetTypeDocument, model.AssetTypeArchive, model.AssetTypeOther}, nil
	}
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if !validAssetType(value) {
			return nil, fmt.Errorf("invalid asset type %q", value)
		}
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("at least one asset type is required")
	}
	return out, nil
}

func validAssetType(value string) bool {
	switch value {
	case model.AssetTypeImage, model.AssetTypeVideo, model.AssetTypeDocument, model.AssetTypeArchive, model.AssetTypeOther:
		return true
	default:
		return false
	}
}

func bucketAllowsType(bucket *model.AssetBucket, assetType string) bool {
	allowed := bucket.AllowedTypes
	if len(allowed) == 0 {
		return true
	}
	for _, value := range allowed {
		if value == assetType {
			return true
		}
	}
	return false
}

func classifyAssetType(logicalPath, fileName, contentType, explicit string) (string, error) {
	explicit = strings.ToLower(strings.TrimSpace(explicit))
	if explicit != "" {
		if !validAssetType(explicit) {
			return "", fmt.Errorf("invalid asset_type %q", explicit)
		}
		return explicit, nil
	}
	ext := strings.ToLower(path.Ext(firstNonEmpty(logicalPath, fileName)))
	ct := strings.ToLower(contentType)
	switch {
	case strings.HasPrefix(ct, "image/") || inSet(ext, ".jpg", ".jpeg", ".png", ".gif", ".webp", ".avif", ".svg", ".bmp", ".ico"):
		return model.AssetTypeImage, nil
	case strings.HasPrefix(ct, "video/") || inSet(ext, ".mp4", ".mkv", ".mov", ".webm", ".avi", ".m4v", ".flv"):
		return model.AssetTypeVideo, nil
	case inSet(ext, ".zip", ".rar", ".7z", ".tar", ".gz", ".bz2", ".xz") ||
		strings.Contains(ct, "zip") || strings.Contains(ct, "x-7z") || strings.Contains(ct, "x-rar") || strings.Contains(ct, "gzip"):
		return model.AssetTypeArchive, nil
	case strings.HasPrefix(ct, "text/") || inSet(ext, ".md", ".markdown", ".txt", ".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx", ".csv", ".json"):
		return model.AssetTypeDocument, nil
	default:
		return model.AssetTypeOther, nil
	}
}

func bucketTypeDir(assetType string) string {
	switch assetType {
	case model.AssetTypeImage:
		return "images"
	case model.AssetTypeVideo:
		return "videos"
	case model.AssetTypeDocument:
		return "documents"
	case model.AssetTypeArchive:
		return "archives"
	default:
		return "other"
	}
}

func bucketInitDirs(slug string) []string {
	base := storage.JoinKey("assets", "buckets", slug)
	return []string{
		base,
		storage.JoinKey(base, "images"),
		storage.JoinKey(base, "videos"),
		storage.JoinKey(base, "documents"),
		storage.JoinKey(base, "archives"),
		storage.JoinKey(base, "other"),
		storage.JoinKey(base, "tmp"),
	}
}

func bucketPhysicalKey(slug, assetType, logicalPath, fileName, sha string) string {
	ext := strings.ToLower(path.Ext(firstNonEmpty(logicalPath, fileName)))
	prefix := sha
	if len(prefix) > 2 {
		prefix = prefix[:2]
	}
	now := time.Now().UTC()
	return storage.JoinKey(
		"assets",
		"buckets",
		slug,
		bucketTypeDir(assetType),
		now.Format("2006"),
		now.Format("01"),
		prefix,
		sha+ext,
	)
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
