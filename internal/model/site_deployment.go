package model

import (
	"time"

	"supercdn/internal/siteinspect"
)

const (
	SiteEnvironmentProduction = "production"
	SiteEnvironmentPreview    = "preview"

	SiteDeploymentTargetOriginAssisted   = "origin_assisted"
	SiteDeploymentTargetCloudflareStatic = "cloudflare_static"
	SiteDeploymentTargetHybridEdge       = "hybrid_edge"

	SiteDeploymentQueued     = "queued"
	SiteDeploymentProcessing = "processing"
	SiteDeploymentReady      = "ready"
	SiteDeploymentActive     = "active"
	SiteDeploymentFailed     = "failed"
	SiteDeploymentExpired    = "expired"

	JobDeploySite = "deploy_site"
)

type SiteDeployment struct {
	ID               string                      `json:"id"`
	SiteID           string                      `json:"site_id"`
	Environment      string                      `json:"environment"`
	Status           string                      `json:"status"`
	RouteProfile     string                      `json:"route_profile"`
	DeploymentTarget string                      `json:"deployment_target"`
	RoutingPolicy    string                      `json:"routing_policy,omitempty"`
	ResourceFailover bool                        `json:"resource_failover"`
	Version          string                      `json:"version"`
	Active           bool                        `json:"active"`
	Pinned           bool                        `json:"pinned"`
	ArtifactObjectID int64                       `json:"artifact_object_id,omitempty"`
	ArtifactKey      string                      `json:"artifact_key,omitempty"`
	ArtifactSHA256   string                      `json:"artifact_sha256,omitempty"`
	ArtifactSize     int64                       `json:"artifact_size,omitempty"`
	ManifestObjectID int64                       `json:"manifest_object_id,omitempty"`
	ManifestKey      string                      `json:"manifest_key,omitempty"`
	FileCount        int                         `json:"file_count"`
	TotalSize        int64                       `json:"total_size"`
	ManifestJSON     string                      `json:"manifest_json,omitempty"`
	RulesJSON        string                      `json:"rules_json,omitempty"`
	LastError        string                      `json:"last_error,omitempty"`
	SiteDomains      []string                    `json:"site_domains,omitempty"`
	ProductionURL    string                      `json:"production_url,omitempty"`
	ProductionURLs   []string                    `json:"production_urls,omitempty"`
	PreviewURL       string                      `json:"preview_url,omitempty"`
	CloudflareStatic *CloudflareStaticDeployment `json:"cloudflare_static,omitempty"`
	HybridEdge       *HybridEdgeDeployment       `json:"hybrid_edge,omitempty"`
	Inspect          *siteinspect.Report         `json:"inspect,omitempty"`
	DeliverySummary  map[string]int              `json:"delivery_summary,omitempty"`
	CreatedAt        time.Time                   `json:"created_at"`
	UpdatedAt        time.Time                   `json:"updated_at"`
	ReadyAt          time.Time                   `json:"ready_at,omitempty"`
	ActivatedAt      time.Time                   `json:"activated_at,omitempty"`
	ExpiresAt        time.Time                   `json:"expires_at,omitempty"`
}

type CloudflareStaticDeployment struct {
	WorkerName         string    `json:"worker_name"`
	VersionID          string    `json:"version_id,omitempty"`
	Domains            []string  `json:"domains,omitempty"`
	URLs               []string  `json:"urls,omitempty"`
	CompatibilityDate  string    `json:"compatibility_date,omitempty"`
	AssetsSHA256       string    `json:"assets_sha256,omitempty"`
	CachePolicy        string    `json:"cache_policy,omitempty"`
	HeadersGenerated   bool      `json:"headers_generated,omitempty"`
	NotFoundHandling   string    `json:"not_found_handling,omitempty"`
	VerificationStatus string    `json:"verification_status,omitempty"`
	VerifiedAt         time.Time `json:"verified_at,omitempty"`
	PublishedAt        time.Time `json:"published_at,omitempty"`
}

type HybridEdgeDeployment struct {
	WorkerName          string    `json:"worker_name"`
	VersionID           string    `json:"version_id,omitempty"`
	Domains             []string  `json:"domains,omitempty"`
	URLs                []string  `json:"urls,omitempty"`
	CompatibilityDate   string    `json:"compatibility_date,omitempty"`
	AssetsSHA256        string    `json:"assets_sha256,omitempty"`
	CachePolicy         string    `json:"cache_policy,omitempty"`
	HeadersGenerated    bool      `json:"headers_generated,omitempty"`
	NotFoundHandling    string    `json:"not_found_handling,omitempty"`
	VerificationStatus  string    `json:"verification_status,omitempty"`
	VerifiedAt          time.Time `json:"verified_at,omitempty"`
	PublishedAt         time.Time `json:"published_at,omitempty"`
	KVNamespaceID       string    `json:"kv_namespace_id,omitempty"`
	KVNamespace         string    `json:"kv_namespace,omitempty"`
	KeyPrefix           string    `json:"key_prefix,omitempty"`
	ManifestSHA256      string    `json:"manifest_sha256,omitempty"`
	ManifestSize        int       `json:"manifest_size,omitempty"`
	ManifestMode        string    `json:"manifest_mode,omitempty"`
	DefaultCacheControl string    `json:"default_cache_control,omitempty"`
	EntryOriginFallback bool      `json:"entry_origin_fallback,omitempty"`
	ActiveKey           bool      `json:"active_key,omitempty"`
	DeploymentKey       bool      `json:"deployment_key,omitempty"`
}

type SiteDeploymentFile struct {
	DeploymentID string    `json:"deployment_id"`
	Path         string    `json:"path"`
	ObjectID     int64     `json:"object_id"`
	Size         int64     `json:"size"`
	SHA256       string    `json:"sha256"`
	ContentType  string    `json:"content_type"`
	CacheControl string    `json:"cache_control"`
	CreatedAt    time.Time `json:"created_at"`
}
