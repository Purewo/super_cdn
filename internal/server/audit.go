package server

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"supercdn/internal/db"
	"supercdn/internal/model"
)

type auditEventsResponse struct {
	Events []model.AuditEvent `json:"events"`
	Limit  int                `json:"limit"`
}

const (
	auditActionAuthInviteCreate = "auth.invite.create"
	auditActionAuthInviteAccept = "auth.invite.accept"
	auditActionAuthTokenCreate  = "auth.token.create"
	auditActionAuthTokenRevoke  = "auth.token.revoke"

	auditActionQuotaRequestCreate  = "quota.request.create"
	auditActionQuotaRequestApprove = "quota.request.approve"
	auditActionQuotaRequestReject  = "quota.request.reject"
	auditActionQuotaSet            = "quota.set"

	auditActionAssetBucketCreate              = "asset_bucket.create"
	auditActionAssetBucketDelete              = "asset_bucket.delete"
	auditActionAssetBucketPurge               = "asset_bucket.purge"
	auditActionAssetBucketWarmup              = "asset_bucket.warmup"
	auditActionAssetBucketObjectUpload        = "asset_bucket.object.upload"
	auditActionAssetBucketObjectDelete        = "asset_bucket.object.delete"
	auditActionAssetBucketPrimarySwitch       = "asset_bucket.object.primary_target.switch"
	auditActionAssetBucketPrimarySwitchReject = "asset_bucket.object.primary_target.switch.rejected"

	auditActionSiteCreate                  = "site.create"
	auditActionSiteOnline                  = "site.online"
	auditActionSiteOffline                 = "site.offline"
	auditActionSiteDelete                  = "site.delete"
	auditActionSiteDomainsBind             = "site.domains.bind"
	auditActionSitePurge                   = "site.purge"
	auditActionSiteDeploymentCreate        = "site.deployment.create"
	auditActionSiteDeploymentPurge         = "site.deployment.purge"
	auditActionSiteDeploymentPromote       = "site.deployment.promote"
	auditActionSiteDeploymentPromoteReject = "site.deployment.promote.rejected"
	auditActionSiteDeploymentDelete        = "site.deployment.delete"
	auditActionSiteFilePrimarySwitch       = "site.deployment.file.primary_target.switch"
	auditActionSiteFilePrimarySwitchReject = "site.deployment.file.primary_target.switch.rejected"
	auditActionSiteEdgeManifestPublish     = "site.edge_manifest.publish"

	auditActionCloudflareStaticRecord         = "site.deployment.cloudflare_static.record"
	auditActionCloudflareStaticRecovery       = "site.deployment.cloudflare_static.recovery"
	auditActionCloudflareStaticRecoveryReject = "site.deployment.cloudflare_static.recovery.rejected"
	auditActionCloudflareStaticActivate       = "site.deployment.cloudflare_static.activate"
	auditActionCloudflareStaticActivateReject = "site.deployment.cloudflare_static.activate.rejected"
	auditActionCloudflareStaticRollback       = "site.deployment.cloudflare_static.rollback"

	auditActionHybridEdgeEvidence        = "site.deployment.hybrid_edge.evidence"
	auditActionHybridEdgeEvidenceReject  = "site.deployment.hybrid_edge.evidence.rejected"
	auditActionHybridEdgeRollback        = "site.deployment.hybrid_edge.rollback"
	auditActionHybridEdgeWriteback       = "site.deployment.hybrid_edge.writeback"
	auditActionHybridEdgeWritebackReject = "site.deployment.hybrid_edge.writeback.rejected"

	auditActionCloudflareDNSSync          = "cloudflare.dns.sync"
	auditActionCloudflareWorkerRoutesSync = "cloudflare.worker_routes.sync"
	auditActionCloudflareR2Sync           = "cloudflare.r2.sync"
	auditActionCloudflareR2Provision      = "cloudflare.r2.provision"
	auditActionCloudflareR2CredsCreate    = "cloudflare.r2.credentials.create"

	auditActionGCDelete = "gc.delete"
	auditActionGCDryRun = "gc.dry_run"

	auditActionIPFSPinsRefresh              = "ipfs.pins.refresh"
	auditActionResourceLibraryInit          = "resource_library.init"
	auditActionResourceLibraryHealthWrite   = "resource_library.health_check.write_probe"
	auditActionResourceLibraryEndToEndProbe = "resource_library.e2e_probe"
)

func (s *Server) handleAuditEvents(w http.ResponseWriter, r *http.Request) {
	principal := currentPrincipal(r.Context())
	workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
	if !principal.Root {
		if workspaceID != "" && workspaceID != principal.WorkspaceID {
			writeError(w, http.StatusForbidden, "cannot read audit events from another workspace")
			return
		}
		workspaceID = principal.WorkspaceID
	}
	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = parsed
	}
	events, err := s.db.AuditEventsFiltered(r.Context(), db.AuditEventFilter{
		WorkspaceID:      workspaceID,
		Action:           r.URL.Query().Get("action"),
		ResourceContains: r.URL.Query().Get("resource"),
		Limit:            limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, auditEventsResponse{Events: events, Limit: limit})
}

func (s *Server) auditMutation(w http.ResponseWriter, r *http.Request, action, resource string) bool {
	if err := s.recordAuditEvent(r.Context(), action, resource); err != nil {
		writeError(w, http.StatusInternalServerError, "audit event write failed: "+err.Error())
		return false
	}
	return true
}

func (s *Server) auditRejectedMutation(r *http.Request, action, resource string) {
	if err := s.recordAuditEvent(r.Context(), action, resource); err != nil {
		s.logger.Warn("audit event write failed for rejected mutation", "action", action, "resource", resource, "error", err)
	}
}

func (s *Server) recordAuditEvent(ctx context.Context, action, resource string) error {
	principal := currentPrincipal(ctx)
	workspaceID := principal.WorkspaceID
	if workspaceID == "" {
		workspaceID = model.DefaultWorkspaceID
	}
	_, err := s.db.CreateAuditEvent(ctx, model.AuditEvent{
		WorkspaceID: workspaceID,
		UserID:      principal.UserID,
		Action:      action,
		Resource:    resource,
	})
	return err
}
