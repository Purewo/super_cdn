package server

import (
	"archive/zip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"supercdn/internal/config"
	"supercdn/internal/model"
	"supercdn/internal/siteinspect"
	"supercdn/internal/storage"
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
	RoutingPolicy    string                            `json:"routing_policy,omitempty"`
	ResourceFailover bool                              `json:"resource_failover"`
	CreatedAtUTC     string                            `json:"created_at_utc"`
	FileCount        int                               `json:"file_count"`
	TotalSize        int64                             `json:"total_size"`
	Files            []siteDeployManifestFile          `json:"files"`
	Rules            siteRules                         `json:"rules,omitempty"`
	Inspect          *siteinspect.Report               `json:"inspect,omitempty"`
	DeliverySummary  map[string]int                    `json:"delivery_summary,omitempty"`
	ArtifactSHA256   string                            `json:"artifact_sha256,omitempty"`
	ArtifactSize     int64                             `json:"artifact_size,omitempty"`
	Operation        string                            `json:"operation,omitempty"`
	RollbackTarget   string                            `json:"rollback_target_deployment,omitempty"`
	CloudflareStatic *model.CloudflareStaticDeployment `json:"cloudflare_static,omitempty"`
	HybridEdge       *model.HybridEdgeDeployment       `json:"hybrid_edge,omitempty"`
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

type recordCloudflareStaticDeploymentRequest struct {
	Environment        string   `json:"environment"`
	RouteProfile       string   `json:"route_profile"`
	DeploymentTarget   string   `json:"deployment_target"`
	RoutingPolicy      string   `json:"routing_policy"`
	ResourceFailover   bool     `json:"resource_failover"`
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
	Operation          string   `json:"operation"`
	RollbackTarget     string   `json:"rollback_target_deployment"`
}

type recoverCloudflareStaticDeploymentRequest struct {
	recordCloudflareStaticDeploymentRequest
	Confirm  string `json:"confirm"`
	ProbeURL string `json:"probe_url"`
}

type activateCloudflareStaticDeploymentRequest struct {
	Confirm            string   `json:"confirm"`
	ProbeURL           string   `json:"probe_url"`
	WorkerName         string   `json:"worker_name"`
	VersionID          string   `json:"version_id"`
	Domains            []string `json:"domains"`
	AssetsSHA256       string   `json:"assets_sha256"`
	FileCount          int      `json:"file_count"`
	TotalSize          int64    `json:"total_size"`
	VerificationStatus string   `json:"verification_status"`
	VerifiedAtUTC      string   `json:"verified_at_utc"`
}

type recordHybridEdgeEvidenceRequest struct {
	WorkerName          string   `json:"worker_name"`
	VersionID           string   `json:"version_id"`
	Domains             []string `json:"domains"`
	CompatibilityDate   string   `json:"compatibility_date"`
	AssetsSHA256        string   `json:"assets_sha256"`
	CachePolicy         string   `json:"cache_policy"`
	HeadersGenerated    bool     `json:"headers_generated"`
	NotFoundHandling    string   `json:"not_found_handling"`
	VerificationStatus  string   `json:"verification_status"`
	VerifiedAtUTC       string   `json:"verified_at_utc"`
	PublishedAtUTC      string   `json:"published_at_utc"`
	KVNamespaceID       string   `json:"kv_namespace_id"`
	KVNamespace         string   `json:"kv_namespace"`
	KeyPrefix           string   `json:"key_prefix"`
	ManifestSHA256      string   `json:"manifest_sha256"`
	ManifestSize        int      `json:"manifest_size"`
	ManifestMode        string   `json:"manifest_mode"`
	DefaultCacheControl string   `json:"default_cache_control"`
	EntryOriginFallback bool     `json:"entry_origin_fallback"`
	ActiveKey           bool     `json:"active_key"`
	DeploymentKey       bool     `json:"deployment_key"`
	Operation           string   `json:"operation"`
	RollbackTarget      string   `json:"rollback_target_deployment"`
}

func (s *Server) handleCreateSiteDeployment(w http.ResponseWriter, r *http.Request) {
	siteID := cleanID(r.PathValue("id"))
	if siteID == "" {
		writeError(w, http.StatusBadRequest, "site id is required")
		return
	}
	if !s.ensureSiteAccessIfExists(w, r, siteID) {
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
	if !s.auditMutation(w, r, "site.deployment.create", "site:"+siteID+";deployment:"+depView.ID) {
		return
	}
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
	if !s.ensureSiteAccessIfExists(w, r, siteID) {
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
	action, resource := cloudflareStaticRecordAudit(siteID, resp.ID, req)
	if !s.auditMutation(w, r, action, resource) {
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) handleRecoverCloudflareStaticDeployment(w http.ResponseWriter, r *http.Request) {
	siteID := cleanID(r.PathValue("id"))
	if siteID == "" {
		writeError(w, http.StatusBadRequest, "site id is required")
		return
	}
	if !s.ensureSiteAccessIfExists(w, r, siteID) {
		return
	}
	var req recoverCloudflareStaticDeploymentRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.recoverCloudflareStaticDeployment(r.Context(), siteID, req)
	if err != nil {
		s.auditRejectedMutation(r, "site.deployment.cloudflare_static.recovery.rejected", cloudflareStaticRecoveryAuditResource(siteID, err))
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.auditMutation(w, r, "site.deployment.cloudflare_static.recovery", "site:"+siteID+";deployment:"+resp.ID) {
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) handleActivateCloudflareStaticDeployment(w http.ResponseWriter, r *http.Request) {
	siteID := cleanID(r.PathValue("id"))
	deploymentID := cleanDeploymentID(r.PathValue("deployment"))
	if _, ok := s.getSiteForAPI(w, r, siteID); !ok {
		return
	}
	var req activateCloudflareStaticDeploymentRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.activateCloudflareStaticDeployment(r.Context(), siteID, deploymentID, req)
	if err != nil {
		s.auditRejectedMutation(r, "site.deployment.cloudflare_static.activate.rejected", cloudflareStaticActivationAuditResource(siteID, deploymentID, err))
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.auditMutation(w, r, "site.deployment.cloudflare_static.activate", "site:"+siteID+";deployment:"+deploymentID) {
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleRecordHybridEdgeEvidence(w http.ResponseWriter, r *http.Request) {
	siteID := cleanID(r.PathValue("id"))
	deploymentID := cleanDeploymentID(r.PathValue("deployment"))
	if _, ok := s.getSiteForAPI(w, r, siteID); !ok {
		return
	}
	var req recordHybridEdgeEvidenceRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.recordHybridEdgeEvidence(r.Context(), siteID, deploymentID, req)
	if err != nil {
		s.auditRejectedMutation(r, "site.deployment.hybrid_edge.evidence.rejected", "site:"+siteID+";deployment:"+deploymentID+";reason:"+auditReason(err))
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	action, resource := hybridEdgeEvidenceAudit(siteID, deploymentID, req)
	if !s.auditMutation(w, r, action, resource) {
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleListSiteDeployments(w http.ResponseWriter, r *http.Request) {
	siteID := cleanID(r.PathValue("id"))
	if siteID == "" {
		writeError(w, http.StatusBadRequest, "site id is required")
		return
	}
	if _, ok := s.getSiteForAPI(w, r, siteID); !ok {
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
	if _, ok := s.getSiteForAPI(w, r, siteID); !ok {
		return
	}
	dep, err := s.db.GetSiteDeployment(r.Context(), deploymentID)
	if err != nil || dep.SiteID != siteID {
		writeError(w, http.StatusNotFound, "deployment not found")
		return
	}
	writeJSON(w, http.StatusOK, s.siteDeploymentView(r.Context(), dep))
}

func (s *Server) handlePurgeSiteCache(w http.ResponseWriter, r *http.Request) {
	siteID := cleanID(r.PathValue("id"))
	if siteID == "" {
		writeError(w, http.StatusBadRequest, "site id is required")
		return
	}
	site, ok := s.getSiteForAPI(w, r, siteID)
	if !ok {
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
	resp := s.purgeSiteDeploymentCache(r.Context(), site, dep, req)
	if !s.auditMutation(w, r, "site.purge", "site:"+site.ID+";deployment:"+dep.ID) {
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handlePurgeSiteDeploymentCache(w http.ResponseWriter, r *http.Request) {
	siteID := cleanID(r.PathValue("id"))
	deploymentID := cleanDeploymentID(r.PathValue("deployment"))
	site, ok := s.getSiteForAPI(w, r, siteID)
	if !ok {
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
	resp := s.purgeSiteDeploymentCache(r.Context(), site, dep, req)
	if !s.auditMutation(w, r, "site.deployment.purge", "site:"+site.ID+";deployment:"+dep.ID) {
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handlePromoteSiteDeployment(w http.ResponseWriter, r *http.Request) {
	siteID := cleanID(r.PathValue("id"))
	deploymentID := cleanDeploymentID(r.PathValue("deployment"))
	if _, ok := s.getSiteForAPI(w, r, siteID); !ok {
		return
	}
	dep, err := s.db.GetSiteDeployment(r.Context(), deploymentID)
	if err != nil || dep.SiteID != siteID {
		writeError(w, http.StatusNotFound, "deployment not found")
		return
	}
	if dep.Status != model.SiteDeploymentReady && dep.Status != model.SiteDeploymentActive {
		writeError(w, http.StatusBadRequest, "deployment is not ready")
		return
	}
	if !dep.Active {
		switch dep.DeploymentTarget {
		case model.SiteDeploymentTargetCloudflareStatic:
			s.auditRejectedMutation(r, "site.deployment.promote.rejected", "site:"+siteID+";deployment:"+deploymentID+";target:"+dep.DeploymentTarget+";reason:metadata_only_blocked")
			writeError(w, http.StatusConflict, "cloudflare_static deployments cannot be promoted by metadata alone; redeploy the desired assets or use a Cloudflare Worker rollback flow")
			return
		case model.SiteDeploymentTargetHybridEdge:
			s.auditRejectedMutation(r, "site.deployment.promote.rejected", "site:"+siteID+";deployment:"+deploymentID+";target:"+dep.DeploymentTarget+";reason:metadata_only_blocked")
			writeError(w, http.StatusConflict, "hybrid_edge deployments cannot be promoted by metadata alone; rerun deploy-site -target hybrid_edge so Worker assets and the active KV manifest are republished together")
			return
		}
	}
	activated, err := s.db.ActivateSiteDeployment(r.Context(), siteID, deploymentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !s.auditMutation(w, r, "site.deployment.promote", "site:"+siteID+";deployment:"+deploymentID) {
		return
	}
	writeJSON(w, http.StatusOK, s.siteDeploymentView(r.Context(), activated))
}

func (s *Server) handleDeleteSiteDeployment(w http.ResponseWriter, r *http.Request) {
	siteID := cleanID(r.PathValue("id"))
	deploymentID := cleanDeploymentID(r.PathValue("deployment"))
	if _, ok := s.getSiteForAPI(w, r, siteID); !ok {
		return
	}
	dep, err := s.db.GetSiteDeployment(r.Context(), deploymentID)
	if err != nil || dep.SiteID != siteID {
		writeError(w, http.StatusNotFound, "deployment not found")
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
	dryRun, err := queryBool(r, "dry_run", false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if dryRun {
		view := s.siteDeploymentView(r.Context(), dep)
		writeJSON(w, http.StatusOK, buildDeleteSiteDeploymentPlan(&view, deleteObjects, deleteRemote))
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
	result := &deleteSiteDeploymentResult{
		SiteID:        siteID,
		DeploymentID:  deploymentID,
		DeleteObjects: deleteObjects,
		DeleteRemote:  deleteRemote,
	}
	if deleteObjects {
		deleted, err := s.deleteSiteDeploymentObjects(r.Context(), dep, deleteRemote)
		if deleted != nil {
			result.Objects = deleted
			result.ObjectCount = len(deleted)
			for _, item := range deleted {
				result.Errors = append(result.Errors, item.Errors...)
			}
		}
		if err != nil {
			if len(result.Errors) == 0 {
				result.Errors = append(result.Errors, err.Error())
			}
			writeJSON(w, http.StatusBadGateway, result)
			return
		}
	}
	if err := s.db.DeleteSiteDeployment(r.Context(), siteID, deploymentID); err != nil {
		result.Errors = append(result.Errors, err.Error())
		writeJSON(w, http.StatusInternalServerError, result)
		return
	}
	result.DeletedDeployment = true
	result.Deleted = true
	if dep.DeploymentTarget == model.SiteDeploymentTargetCloudflareStatic || dep.DeploymentTarget == model.SiteDeploymentTargetHybridEdge {
		result.Warning = "deleted Super CDN metadata only; Cloudflare Worker versions, custom domains and KV entries are not deleted by this command"
	}
	if !s.auditMutation(w, r, "site.deployment.delete", "site:"+siteID+";deployment:"+deploymentID) {
		return
	}
	writeJSON(w, http.StatusOK, result)
}

type deleteSiteDeploymentPlanResult struct {
	SiteID                 string   `json:"site_id"`
	DeploymentID           string   `json:"deployment_id"`
	Status                 string   `json:"status,omitempty"`
	DeploymentTarget       string   `json:"deployment_target,omitempty"`
	Active                 bool     `json:"active"`
	Pinned                 bool     `json:"pinned"`
	DryRun                 bool     `json:"dry_run"`
	DeleteObjects          bool     `json:"delete_objects"`
	DeleteRemote           bool     `json:"delete_remote"`
	SafeToRun              bool     `json:"safe_to_run"`
	RemoteCleanupSupported bool     `json:"remote_cleanup_supported"`
	RemoteCleanupBlockers  []string `json:"remote_cleanup_blockers,omitempty"`
	Warnings               []string `json:"warnings,omitempty"`
	Evidence               struct {
		FileCount        int                               `json:"file_count,omitempty"`
		ArtifactSHA256   string                            `json:"artifact_sha256,omitempty"`
		ManifestKey      string                            `json:"manifest_key,omitempty"`
		CloudflareStatic *model.CloudflareStaticDeployment `json:"cloudflare_static,omitempty"`
		HybridEdge       *model.HybridEdgeDeployment       `json:"hybrid_edge,omitempty"`
	} `json:"evidence"`
}

func buildDeleteSiteDeploymentPlan(dep *model.SiteDeployment, deleteObjects, deleteRemote bool) deleteSiteDeploymentPlanResult {
	out := deleteSiteDeploymentPlanResult{
		SiteID:                 dep.SiteID,
		DeploymentID:           dep.ID,
		Status:                 string(dep.Status),
		DeploymentTarget:       dep.DeploymentTarget,
		Active:                 dep.Active,
		Pinned:                 dep.Pinned,
		DryRun:                 true,
		DeleteObjects:          deleteObjects,
		DeleteRemote:           deleteRemote,
		SafeToRun:              true,
		RemoteCleanupSupported: deleteObjects && deleteRemote,
	}
	out.Evidence.FileCount = dep.FileCount
	out.Evidence.ArtifactSHA256 = dep.ArtifactSHA256
	out.Evidence.ManifestKey = dep.ManifestKey
	out.Evidence.CloudflareStatic = dep.CloudflareStatic
	out.Evidence.HybridEdge = dep.HybridEdge
	if dep.Active {
		out.SafeToRun = false
		out.Warnings = append(out.Warnings, "active production deployment cannot be deleted")
	}
	if dep.Pinned {
		out.SafeToRun = false
		out.Warnings = append(out.Warnings, "pinned deployment cannot be deleted")
	}
	if dep.DeploymentTarget == model.SiteDeploymentTargetCloudflareStatic || dep.DeploymentTarget == model.SiteDeploymentTargetHybridEdge {
		out.RemoteCleanupSupported = false
		out.RemoteCleanupBlockers = cloudflareDeleteRemoteCleanupBlockers(dep.DeploymentTarget)
		out.Warnings = append(out.Warnings, "delete-deployment removes Super CDN metadata only; Cloudflare Worker versions, custom domains and KV entries are not deleted")
	}
	return out
}

func cloudflareDeleteRemoteCleanupBlockers(target string) []string {
	blockers := []string{
		"delete-deployment does not delete Cloudflare Worker versions, custom domains or KV entries",
		"remote Cloudflare cleanup requires an operator to verify no active deployment, Worker route, custom domain or KV key still references the resources",
	}
	if target == model.SiteDeploymentTargetHybridEdge {
		blockers = append(blockers, "hybrid_edge cleanup must verify both deployment and active KV manifest keys before deleting KV entries")
	}
	return blockers
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
		site, err = s.db.CreateSiteInWorkspace(r.Context(), workspaceForContext(r.Context()), siteID, "", "standard", profileName, deploymentTarget, "", nil)
		if err != nil {
			return nil, deploySitePayload{}, err
		}
	} else if !principalCanAccessWorkspace(currentPrincipal(r.Context()), site.WorkspaceID) {
		return nil, deploySitePayload{}, fmt.Errorf("site not found")
	}
	profileName = firstNonEmpty(profileName, site.RouteProfile, "overseas")
	profile, ok := s.cfg.Profile(profileName)
	if !ok {
		return nil, deploySitePayload{}, fmt.Errorf("unknown route_profile")
	}
	if deploymentTarget == "" {
		deploymentTarget = firstNonEmpty(site.DeploymentTarget, defaultDeploymentTarget(profile))
	}
	routingPolicy := strings.TrimSpace(r.FormValue("routing_policy"))
	if routingPolicy == "" {
		routingPolicy = strings.TrimSpace(site.RoutingPolicy)
	}
	if routingPolicy != "" {
		if _, err := s.routingPolicyForProfile(routingPolicy, profileName, profile); err != nil {
			return nil, deploySitePayload{}, err
		}
	}
	resourceFailover, err := parseFormBool(r, "resource_failover", false)
	if err != nil {
		return nil, deploySitePayload{}, err
	}
	if resourceFailover {
		if err := validateResourceFailoverProfile(profileName, profile); err != nil {
			return nil, deploySitePayload{}, err
		}
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
		RoutingPolicy:    routingPolicy,
		ResourceFailover: resourceFailover,
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
	if req.ResourceFailover {
		return model.SiteDeployment{}, fmt.Errorf("resource_failover is not supported for cloudflare_static deployments")
	}
	operation, rollbackTarget, err := s.validateCloudflareStaticRecordOperation(ctx, siteID, req)
	if err != nil {
		return model.SiteDeployment{}, err
	}
	site, err := s.db.GetSite(ctx, siteID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return model.SiteDeployment{}, err
	}
	if site != nil && !principalCanAccessWorkspace(currentPrincipal(ctx), site.WorkspaceID) {
		return model.SiteDeployment{}, fmt.Errorf("site not found")
	}
	workspaceID := workspaceForContext(ctx)
	profileName := firstNonEmpty(strings.TrimSpace(req.RouteProfile), "overseas")
	if site != nil {
		profileName = firstNonEmpty(strings.TrimSpace(req.RouteProfile), site.RouteProfile, "overseas")
	}
	profile, ok := s.cfg.Profile(profileName)
	if !ok {
		return model.SiteDeployment{}, fmt.Errorf("unknown route_profile")
	}
	routingPolicy := strings.TrimSpace(req.RoutingPolicy)
	if routingPolicy == "" && site != nil {
		routingPolicy = strings.TrimSpace(site.RoutingPolicy)
	}
	if routingPolicy != "" {
		if _, err := s.routingPolicyForProfile(routingPolicy, profileName, profile); err != nil {
			return model.SiteDeployment{}, err
		}
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
		site, err = s.db.CreateSiteInWorkspace(ctx, workspaceID, siteID, "", mode, profileName, target, routingPolicy, requestedDomains)
		if err != nil {
			return model.SiteDeployment{}, err
		}
	} else {
		domains := mergeDomains(site.Domains, requestedDomains)
		site, err = s.db.CreateSiteInWorkspace(ctx, site.WorkspaceID, siteID, site.Name, mode, profileName, target, routingPolicy, domains)
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
		RoutingPolicy:    routingPolicy,
		ResourceFailover: false,
		CreatedAtUTC:     publishedAt.Format(time.RFC3339Nano),
		FileCount:        req.FileCount,
		TotalSize:        req.TotalSize,
		Operation:        operation,
		RollbackTarget:   rollbackTarget,
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
		RoutingPolicy:    routingPolicy,
		ResourceFailover: false,
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

func (s *Server) recoverCloudflareStaticDeployment(ctx context.Context, siteID string, req recoverCloudflareStaticDeploymentRequest) (model.SiteDeployment, error) {
	if strings.TrimSpace(req.Confirm) != "recover" {
		return model.SiteDeployment{}, fmt.Errorf("confirm must be recover")
	}
	if req.Promote {
		return model.SiteDeployment{}, fmt.Errorf("cloudflare_static recovery records metadata only; activation is not supported by this endpoint")
	}
	if strings.TrimSpace(req.WorkerName) == "" {
		return model.SiteDeployment{}, fmt.Errorf("worker_name is required")
	}
	if strings.TrimSpace(req.VersionID) == "" {
		return model.SiteDeployment{}, fmt.Errorf("version_id is required")
	}
	domains := mergeDomains(req.Domains)
	if len(domains) == 0 {
		return model.SiteDeployment{}, fmt.Errorf("domains are required")
	}
	if strings.TrimSpace(req.ProbeURL) == "" {
		return model.SiteDeployment{}, fmt.Errorf("probe_url is required")
	}
	if strings.TrimSpace(req.AssetsSHA256) == "" {
		return model.SiteDeployment{}, fmt.Errorf("assets_sha256 is required")
	}
	if req.FileCount <= 0 {
		return model.SiteDeployment{}, fmt.Errorf("file_count must be positive")
	}
	if !strings.EqualFold(strings.TrimSpace(req.VerificationStatus), "ok") {
		return model.SiteDeployment{}, fmt.Errorf("verification_status must be ok")
	}
	if strings.TrimSpace(req.VerifiedAtUTC) == "" {
		return model.SiteDeployment{}, fmt.Errorf("verified_at_utc is required")
	}
	recordReq := req.recordCloudflareStaticDeploymentRequest
	recordReq.Domains = domains
	recordReq.DeploymentTarget = model.SiteDeploymentTargetCloudflareStatic
	recordReq.ResourceFailover = false
	recordReq.VerificationStatus = "ok"
	recordReq.Promote = false
	recordReq.Operation = ""
	recordReq.RollbackTarget = ""
	return s.recordCloudflareStaticDeployment(ctx, siteID, recordReq)
}

func (s *Server) activateCloudflareStaticDeployment(ctx context.Context, siteID, deploymentID string, req activateCloudflareStaticDeploymentRequest) (model.SiteDeployment, error) {
	if strings.TrimSpace(req.Confirm) != "activate" {
		return model.SiteDeployment{}, fmt.Errorf("confirm must be activate")
	}
	dep, err := s.db.GetSiteDeployment(ctx, deploymentID)
	if err != nil || dep.SiteID != siteID {
		return model.SiteDeployment{}, fmt.Errorf("deployment not found")
	}
	if dep.DeploymentTarget != model.SiteDeploymentTargetCloudflareStatic {
		return model.SiteDeployment{}, fmt.Errorf("deployment target must be cloudflare_static")
	}
	if dep.Status != model.SiteDeploymentReady && dep.Status != model.SiteDeploymentActive {
		return model.SiteDeployment{}, fmt.Errorf("deployment is not ready")
	}
	view := s.siteDeploymentView(ctx, dep)
	evidence := view.CloudflareStatic
	if evidence == nil {
		return model.SiteDeployment{}, fmt.Errorf("cloudflare_static evidence is missing")
	}
	if strings.TrimSpace(req.ProbeURL) == "" {
		return model.SiteDeployment{}, fmt.Errorf("probe_url is required")
	}
	if !strings.EqualFold(strings.TrimSpace(req.VerificationStatus), "ok") {
		return model.SiteDeployment{}, fmt.Errorf("verification_status must be ok")
	}
	if strings.TrimSpace(req.VerifiedAtUTC) == "" {
		return model.SiteDeployment{}, fmt.Errorf("verified_at_utc is required")
	}
	if _, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(req.VerifiedAtUTC)); err != nil {
		return model.SiteDeployment{}, fmt.Errorf("verified_at_utc must be RFC3339: %w", err)
	}
	if strings.TrimSpace(req.WorkerName) == "" || strings.TrimSpace(req.WorkerName) != evidence.WorkerName {
		return model.SiteDeployment{}, fmt.Errorf("worker_name does not match deployment evidence")
	}
	if strings.TrimSpace(evidence.VersionID) != "" && strings.TrimSpace(req.VersionID) != evidence.VersionID {
		return model.SiteDeployment{}, fmt.Errorf("version_id does not match deployment evidence")
	}
	if strings.TrimSpace(evidence.AssetsSHA256) == "" || strings.TrimSpace(req.AssetsSHA256) != evidence.AssetsSHA256 {
		return model.SiteDeployment{}, fmt.Errorf("assets_sha256 does not match deployment evidence")
	}
	if req.FileCount <= 0 || req.FileCount != dep.FileCount {
		return model.SiteDeployment{}, fmt.Errorf("file_count does not match deployment evidence")
	}
	if req.TotalSize < 0 || req.TotalSize != dep.TotalSize {
		return model.SiteDeployment{}, fmt.Errorf("total_size does not match deployment evidence")
	}
	if !sameDomainSet(req.Domains, evidence.Domains) {
		return model.SiteDeployment{}, fmt.Errorf("domains do not match deployment evidence")
	}
	if !probeURLMatchesDomains(req.ProbeURL, evidence.Domains) {
		return model.SiteDeployment{}, fmt.Errorf("probe_url host does not match deployment domains")
	}
	activated, err := s.db.ActivateSiteDeployment(ctx, siteID, deploymentID)
	if err != nil {
		return model.SiteDeployment{}, err
	}
	return s.siteDeploymentView(ctx, activated), nil
}

func (s *Server) recordHybridEdgeEvidence(ctx context.Context, siteID, deploymentID string, req recordHybridEdgeEvidenceRequest) (model.SiteDeployment, error) {
	dep, err := s.db.GetSiteDeployment(ctx, deploymentID)
	if err != nil || dep.SiteID != siteID {
		return model.SiteDeployment{}, fmt.Errorf("deployment not found")
	}
	if dep.DeploymentTarget != model.SiteDeploymentTargetHybridEdge {
		return model.SiteDeployment{}, fmt.Errorf("deployment target must be hybrid_edge")
	}
	if dep.Status != model.SiteDeploymentReady && dep.Status != model.SiteDeploymentActive {
		return model.SiteDeployment{}, fmt.Errorf("deployment is not ready")
	}
	operation, rollbackTarget, err := s.validateHybridEdgeEvidenceOperation(ctx, siteID, req)
	if err != nil {
		return model.SiteDeployment{}, err
	}
	if strings.TrimSpace(req.WorkerName) == "" {
		return model.SiteDeployment{}, fmt.Errorf("worker_name is required")
	}
	domains := mergeDomains(req.Domains)
	if len(domains) == 0 {
		return model.SiteDeployment{}, fmt.Errorf("domains are required")
	}
	if strings.TrimSpace(req.AssetsSHA256) == "" {
		return model.SiteDeployment{}, fmt.Errorf("assets_sha256 is required")
	}
	if strings.TrimSpace(req.KVNamespaceID) == "" {
		return model.SiteDeployment{}, fmt.Errorf("kv_namespace_id is required")
	}
	if strings.TrimSpace(req.ManifestSHA256) == "" {
		return model.SiteDeployment{}, fmt.Errorf("manifest_sha256 is required")
	}
	if req.ManifestSize <= 0 {
		return model.SiteDeployment{}, fmt.Errorf("manifest_size must be positive")
	}
	if !strings.EqualFold(strings.TrimSpace(req.VerificationStatus), "ok") {
		return model.SiteDeployment{}, fmt.Errorf("verification_status must be ok")
	}
	if strings.TrimSpace(req.VerifiedAtUTC) == "" {
		return model.SiteDeployment{}, fmt.Errorf("verified_at_utc is required")
	}
	verifiedAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(req.VerifiedAtUTC))
	if err != nil {
		return model.SiteDeployment{}, fmt.Errorf("verified_at_utc must be RFC3339: %w", err)
	}
	publishedAt := time.Now().UTC()
	if strings.TrimSpace(req.PublishedAtUTC) != "" {
		parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(req.PublishedAtUTC))
		if err != nil {
			return model.SiteDeployment{}, fmt.Errorf("published_at_utc must be RFC3339: %w", err)
		}
		publishedAt = parsed.UTC()
	}
	manifest := siteDeployManifest{
		Version:          3,
		Kind:             "supercdn-hybrid-edge-deployment",
		StorageLayout:    "hybrid_edge",
		SiteID:           siteID,
		DeploymentID:     deploymentID,
		Environment:      dep.Environment,
		RouteProfile:     dep.RouteProfile,
		DeploymentTarget: dep.DeploymentTarget,
		RoutingPolicy:    dep.RoutingPolicy,
		ResourceFailover: dep.ResourceFailover,
		FileCount:        dep.FileCount,
		TotalSize:        dep.TotalSize,
		ArtifactSHA256:   dep.ArtifactSHA256,
		ArtifactSize:     dep.ArtifactSize,
		Operation:        operation,
		RollbackTarget:   rollbackTarget,
	}
	if strings.TrimSpace(dep.ManifestJSON) != "" {
		_ = json.Unmarshal([]byte(dep.ManifestJSON), &manifest)
	}
	hybrid := model.HybridEdgeDeployment{
		WorkerName:          strings.TrimSpace(req.WorkerName),
		VersionID:           strings.TrimSpace(req.VersionID),
		Domains:             domains,
		URLs:                httpsDomainURLs(domains),
		CompatibilityDate:   strings.TrimSpace(req.CompatibilityDate),
		AssetsSHA256:        strings.TrimSpace(req.AssetsSHA256),
		CachePolicy:         strings.TrimSpace(req.CachePolicy),
		HeadersGenerated:    req.HeadersGenerated,
		NotFoundHandling:    strings.TrimSpace(req.NotFoundHandling),
		VerificationStatus:  "ok",
		VerifiedAt:          verifiedAt.UTC(),
		PublishedAt:         publishedAt,
		KVNamespaceID:       strings.TrimSpace(req.KVNamespaceID),
		KVNamespace:         strings.TrimSpace(req.KVNamespace),
		KeyPrefix:           strings.TrimSpace(req.KeyPrefix),
		ManifestSHA256:      strings.TrimSpace(req.ManifestSHA256),
		ManifestSize:        req.ManifestSize,
		ManifestMode:        strings.TrimSpace(req.ManifestMode),
		DefaultCacheControl: strings.TrimSpace(req.DefaultCacheControl),
		EntryOriginFallback: req.EntryOriginFallback,
		ActiveKey:           req.ActiveKey,
		DeploymentKey:       req.DeploymentKey,
	}
	manifest.HybridEdge = &hybrid
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return model.SiteDeployment{}, err
	}
	updated, err := s.db.UpdateSiteDeploymentManifest(ctx, deploymentID, string(raw))
	if err != nil {
		return model.SiteDeployment{}, err
	}
	return s.siteDeploymentView(ctx, updated), nil
}

func (s *Server) validateHybridEdgeEvidenceOperation(ctx context.Context, siteID string, req recordHybridEdgeEvidenceRequest) (string, string, error) {
	operation := strings.TrimSpace(req.Operation)
	switch operation {
	case "", "deploy":
		return "", "", nil
	case "rollback_apply":
		target := cleanDeploymentID(req.RollbackTarget)
		if target == "" {
			return "", "", fmt.Errorf("rollback_target_deployment is required for rollback_apply")
		}
		dep, err := s.db.GetSiteDeployment(ctx, target)
		if err != nil || dep.SiteID != siteID {
			return "", "", fmt.Errorf("rollback_target_deployment not found")
		}
		if dep.DeploymentTarget != model.SiteDeploymentTargetHybridEdge {
			return "", "", fmt.Errorf("rollback_target_deployment must be hybrid_edge")
		}
		if dep.Status != model.SiteDeploymentReady && dep.Status != model.SiteDeploymentActive {
			return "", "", fmt.Errorf("rollback_target_deployment is not ready")
		}
		return operation, target, nil
	default:
		return "", "", fmt.Errorf("operation must be deploy or rollback_apply")
	}
}

func (s *Server) validateCloudflareStaticRecordOperation(ctx context.Context, siteID string, req recordCloudflareStaticDeploymentRequest) (string, string, error) {
	operation := strings.TrimSpace(req.Operation)
	switch operation {
	case "", "deploy":
		return "", "", nil
	case "rollback_apply":
		target := cleanDeploymentID(req.RollbackTarget)
		if target == "" {
			return "", "", fmt.Errorf("rollback_target_deployment is required for rollback_apply")
		}
		dep, err := s.db.GetSiteDeployment(ctx, target)
		if err != nil || dep.SiteID != siteID {
			return "", "", fmt.Errorf("rollback_target_deployment not found")
		}
		if dep.DeploymentTarget != model.SiteDeploymentTargetCloudflareStatic {
			return "", "", fmt.Errorf("rollback_target_deployment must be cloudflare_static")
		}
		if dep.Status != model.SiteDeploymentReady && dep.Status != model.SiteDeploymentActive {
			return "", "", fmt.Errorf("rollback_target_deployment is not ready")
		}
		return operation, target, nil
	default:
		return "", "", fmt.Errorf("operation must be deploy or rollback_apply")
	}
}

func cloudflareStaticRecordAudit(siteID, deploymentID string, req recordCloudflareStaticDeploymentRequest) (string, string) {
	if strings.TrimSpace(req.Operation) == "rollback_apply" {
		target := cleanDeploymentID(req.RollbackTarget)
		return "site.deployment.cloudflare_static.rollback", "site:" + siteID + ";deployment:" + deploymentID + ";target:" + target
	}
	return "site.deployment.cloudflare_static.record", "site:" + siteID + ";deployment:" + deploymentID
}

func hybridEdgeEvidenceAudit(siteID, deploymentID string, req recordHybridEdgeEvidenceRequest) (string, string) {
	if strings.TrimSpace(req.Operation) == "rollback_apply" {
		target := cleanDeploymentID(req.RollbackTarget)
		return "site.deployment.hybrid_edge.rollback", "site:" + siteID + ";deployment:" + deploymentID + ";target:" + target
	}
	return "site.deployment.hybrid_edge.evidence", "site:" + siteID + ";deployment:" + deploymentID
}

func auditReason(err error) string {
	reason := "unknown"
	if err != nil {
		reason = strings.ToLower(strings.TrimSpace(err.Error()))
		reason = strings.NewReplacer(" ", "_", ";", "_", ":", "_", "\n", "_", "\r", "_", "\t", "_").Replace(reason)
		if len(reason) > 120 {
			reason = reason[:120]
		}
	}
	return reason
}

func cloudflareStaticRecoveryAuditResource(siteID string, err error) string {
	return "site:" + siteID + ";reason:" + auditReason(err)
}

func cloudflareStaticActivationAuditResource(siteID, deploymentID string, err error) string {
	return "site:" + siteID + ";deployment:" + deploymentID + ";reason:" + auditReason(err)
}

func sameDomainSet(a, b []string) bool {
	left := mergeDomains(a)
	right := mergeDomains(b)
	if len(left) != len(right) {
		return false
	}
	seen := map[string]bool{}
	for _, value := range left {
		seen[value] = true
	}
	for _, value := range right {
		if !seen[value] {
			return false
		}
	}
	return true
}

func probeURLMatchesDomains(raw string, domains []string) bool {
	host := publicURLHost(raw)
	if host == "" {
		return false
	}
	for _, domain := range domains {
		if host == cleanHost(domain) {
			return true
		}
	}
	return false
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
		RoutingPolicy:    dep.RoutingPolicy,
		ResourceFailover: dep.ResourceFailover,
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
