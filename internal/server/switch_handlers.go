package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"supercdn/internal/config"
	"supercdn/internal/db"
	"supercdn/internal/model"
	"supercdn/internal/storage"
)

type primaryTargetSwitchRequest struct {
	Path                  string `json:"path"`
	Target                string `json:"target"`
	ExpectedCurrentTarget string `json:"expected_current_target,omitempty"`
	DryRun                *bool  `json:"dry_run,omitempty"`
	Confirm               string `json:"confirm,omitempty"`
}

type primaryTargetSwitchResponse struct {
	Status            string        `json:"status"`
	Mode              string        `json:"mode"`
	Resource          string        `json:"resource"`
	DeploymentID      string        `json:"deployment_id,omitempty"`
	Path              string        `json:"path"`
	File              string        `json:"file,omitempty"`
	ObjectID          int64         `json:"object_id"`
	PreviousTarget    string        `json:"previous_target"`
	Target            string        `json:"target"`
	DryRun            bool          `json:"dry_run"`
	EffectiveNow      bool          `json:"effective_now"`
	TargetURL         string        `json:"target_url,omitempty"`
	TargetURLRedacted bool          `json:"target_url_redacted,omitempty"`
	Checks            []doctorCheck `json:"checks,omitempty"`
	Warnings          []string      `json:"warnings,omitempty"`
	NextCommands      []string      `json:"next_commands,omitempty"`
	RollbackCommand   string        `json:"rollback_command,omitempty"`
}

type primarySwitchError struct {
	status  int
	message string
}

func (e *primarySwitchError) Error() string {
	return e.message
}

func switchError(status int, format string, args ...any) error {
	return &primarySwitchError{status: status, message: fmt.Sprintf(format, args...)}
}

func (s *Server) handleSwitchAssetBucketObjectPrimaryTarget(w http.ResponseWriter, r *http.Request) {
	var req primaryTargetSwitchRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	bucket, ok := s.getAssetBucketForAPI(w, r, cleanBucketSlug(r.PathValue("slug")))
	if !ok {
		return
	}
	resp, err := s.switchAssetBucketObjectPrimaryTarget(r.Context(), bucket, req)
	if err != nil {
		s.auditRejectedMutation(r, auditActionAssetBucketPrimarySwitchReject, primarySwitchAuditResource("asset_bucket:"+bucket.Slug, "", req.Path, req.Target, err))
		writePrimarySwitchError(w, err)
		return
	}
	if !resp.DryRun && resp.Status == "switched" {
		if !s.auditMutation(w, r, auditActionAssetBucketPrimarySwitch, "asset_bucket:"+bucket.Slug+";path:"+resp.Path+";target:"+resp.Target) {
			return
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleSwitchActiveSiteFilePrimaryTarget(w http.ResponseWriter, r *http.Request) {
	s.handleSwitchSiteFilePrimaryTarget(w, r, "")
}

func (s *Server) handleSwitchDeploymentSiteFilePrimaryTarget(w http.ResponseWriter, r *http.Request) {
	s.handleSwitchSiteFilePrimaryTarget(w, r, cleanDeploymentID(r.PathValue("deployment")))
}

func (s *Server) handleSwitchSiteFilePrimaryTarget(w http.ResponseWriter, r *http.Request, deploymentID string) {
	var req primaryTargetSwitchRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	site, ok := s.getSiteForAPI(w, r, cleanID(r.PathValue("id")))
	if !ok {
		return
	}
	resp, err := s.switchSiteFilePrimaryTarget(r.Context(), site, deploymentID, req)
	if err != nil {
		s.auditRejectedMutation(r, auditActionSiteFilePrimarySwitchReject, primarySwitchAuditResource("site:"+site.ID, deploymentID, req.Path, req.Target, err))
		writePrimarySwitchError(w, err)
		return
	}
	if !resp.DryRun && resp.Status == "switched" {
		if !s.auditMutation(w, r, auditActionSiteFilePrimarySwitch, "site:"+site.ID+";deployment:"+resp.DeploymentID+";file:"+firstNonEmpty(resp.File, resp.Path)+";target:"+resp.Target) {
			return
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func writePrimarySwitchError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	var switchErr *primarySwitchError
	if errors.As(err, &switchErr) && switchErr.status > 0 {
		status = switchErr.status
	}
	writeError(w, status, err.Error())
}

func primarySwitchAuditResource(prefix, deploymentID, pathValue, target string, err error) string {
	parts := []string{prefix}
	if strings.TrimSpace(deploymentID) != "" {
		parts = append(parts, "deployment:"+strings.TrimSpace(deploymentID))
	}
	if strings.TrimSpace(pathValue) != "" {
		parts = append(parts, "path:"+strings.TrimSpace(pathValue))
	}
	if strings.TrimSpace(target) != "" {
		parts = append(parts, "target:"+strings.TrimSpace(target))
	}
	var switchErr *primarySwitchError
	if errors.As(err, &switchErr) && switchErr.status > 0 {
		parts = append(parts, fmt.Sprintf("status:%d", switchErr.status))
	}
	return strings.Join(parts, ";")
}

func (s *Server) switchAssetBucketObjectPrimaryTarget(ctx context.Context, bucket *model.AssetBucket, req primaryTargetSwitchRequest) (*primaryTargetSwitchResponse, error) {
	if strings.TrimSpace(bucket.RoutingPolicy) != "" {
		return nil, switchError(http.StatusConflict, "bucket %q uses routing_policy %q; primary_target switching would not control normal routing", bucket.Slug, bucket.RoutingPolicy)
	}
	logicalPath, err := storage.CleanObjectPath(req.Path)
	if err != nil {
		return nil, err
	}
	if logicalPath == "" {
		return nil, switchError(http.StatusBadRequest, "path is required")
	}
	item, err := s.db.GetAssetBucketObject(ctx, bucket.Slug, logicalPath)
	if err != nil {
		if db.IsNotFound(err) {
			return nil, switchError(http.StatusNotFound, "bucket object %q not found", logicalPath)
		}
		return nil, err
	}
	obj, err := s.db.GetObject(ctx, item.ObjectID)
	if err != nil {
		return nil, err
	}
	profile, ok := s.cfg.Profile(bucket.RouteProfile)
	if !ok {
		return nil, switchError(http.StatusConflict, "route_profile %q is not configured", bucket.RouteProfile)
	}
	dryRun, err := primarySwitchDryRun(req)
	if err != nil {
		return nil, err
	}
	resp, err := s.switchObjectPrimaryTarget(ctx, primarySwitchScope{
		Mode:         "bucket",
		Resource:     bucket.Slug,
		Path:         logicalPath,
		ProfileName:  bucket.RouteProfile,
		Profile:      profile,
		Object:       obj,
		Target:       req.Target,
		ExpectedFrom: req.ExpectedCurrentTarget,
		DryRun:       dryRun,
		EffectiveNow: true,
	})
	if err != nil {
		return nil, err
	}
	if resp.Status == "switched" {
		resp.RollbackCommand = switchApplyRollbackCommand("bucket", bucket.Slug, "", logicalPath, resp.PreviousTarget, resp.Target)
	}
	return resp, nil
}

func (s *Server) switchSiteFilePrimaryTarget(ctx context.Context, site *model.Site, deploymentID string, req primaryTargetSwitchRequest) (*primaryTargetSwitchResponse, error) {
	dep, err := s.siteDeploymentForPrimarySwitch(ctx, site, deploymentID)
	if err != nil {
		return nil, err
	}
	if dep.DeploymentTarget == model.SiteDeploymentTargetCloudflareStatic {
		return nil, switchError(http.StatusConflict, "cloudflare_static deployments cannot be switched by metadata; redeploy the desired assets or use a Cloudflare rollback flow")
	}
	if strings.TrimSpace(firstNonEmpty(dep.RoutingPolicy, site.RoutingPolicy)) != "" {
		return nil, switchError(http.StatusConflict, "deployment %q uses routing_policy; primary_target switching would not control normal routing", dep.ID)
	}
	if dep.ResourceFailover {
		return nil, switchError(http.StatusConflict, "deployment %q uses resource_failover; primary_target switching would not control failover routing", dep.ID)
	}
	rules := deploymentRules(dep, site)
	file, obj, _, _, err := s.siteDeploymentFileForRouteExplain(ctx, dep, rules, req.Path)
	if err != nil {
		return nil, err
	}
	profile, ok := s.cfg.Profile(dep.RouteProfile)
	if !ok {
		return nil, switchError(http.StatusConflict, "route_profile %q is not configured", dep.RouteProfile)
	}
	dryRun, err := primarySwitchDryRun(req)
	if err != nil {
		return nil, err
	}
	effectiveNow := dep.Active && dep.Environment == model.SiteEnvironmentProduction && dep.DeploymentTarget != model.SiteDeploymentTargetHybridEdge
	resp, err := s.switchObjectPrimaryTarget(ctx, primarySwitchScope{
		Mode:         "site",
		Resource:     site.ID,
		DeploymentID: dep.ID,
		Path:         strings.TrimSpace(req.Path),
		File:         file.Path,
		ProfileName:  dep.RouteProfile,
		Profile:      profile,
		Object:       obj,
		Target:       req.Target,
		ExpectedFrom: req.ExpectedCurrentTarget,
		DryRun:       dryRun,
		EffectiveNow: effectiveNow,
	})
	if err != nil {
		return nil, err
	}
	if dep.DeploymentTarget == model.SiteDeploymentTargetHybridEdge {
		resp.Warnings = append(resp.Warnings, "hybrid_edge traffic uses the active edge manifest; run refresh-edge-manifest after this switch to publish the new primary target")
		resp.NextCommands = append(resp.NextCommands, "supercdnctl refresh-edge-manifest -site "+site.ID)
	}
	if !dep.Active || dep.Environment != model.SiteEnvironmentProduction {
		resp.Warnings = append(resp.Warnings, "deployment is not the active production deployment; switch affects metadata for this deployment only")
	}
	if resp.Status == "switched" {
		resp.RollbackCommand = switchApplyRollbackCommand("site", site.ID, dep.ID, firstNonEmpty(strings.TrimSpace(req.Path), file.Path), resp.PreviousTarget, resp.Target)
	}
	return resp, nil
}

func (s *Server) siteDeploymentForPrimarySwitch(ctx context.Context, site *model.Site, deploymentID string) (*model.SiteDeployment, error) {
	if strings.TrimSpace(deploymentID) == "" {
		dep, err := s.db.ActiveSiteDeployment(ctx, site.ID)
		if err != nil {
			return nil, switchError(http.StatusNotFound, "active production deployment not found for site %q", site.ID)
		}
		return dep, nil
	}
	dep, err := s.db.GetSiteDeployment(ctx, deploymentID)
	if err != nil || dep.SiteID != site.ID {
		return nil, switchError(http.StatusNotFound, "deployment %q not found for site %q", deploymentID, site.ID)
	}
	return dep, nil
}

type primarySwitchScope struct {
	Mode         string
	Resource     string
	DeploymentID string
	Path         string
	File         string
	ProfileName  string
	Profile      config.RouteProfile
	Object       *model.Object
	Target       string
	ExpectedFrom string
	DryRun       bool
	EffectiveNow bool
}

func (s *Server) switchObjectPrimaryTarget(ctx context.Context, scope primarySwitchScope) (*primaryTargetSwitchResponse, error) {
	if scope.Object == nil || scope.Object.ID == 0 {
		return nil, switchError(http.StatusBadRequest, "object is required")
	}
	target := strings.TrimSpace(scope.Target)
	if target == "" {
		return nil, switchError(http.StatusBadRequest, "target is required")
	}
	if !stringInSlice(target, routeProfileFailoverTargets(scope.Profile)) {
		return nil, switchError(http.StatusBadRequest, "target %q is not part of route_profile %q", target, scope.ProfileName)
	}
	if strings.TrimSpace(scope.ExpectedFrom) != "" && strings.TrimSpace(scope.ExpectedFrom) != scope.Object.PrimaryTarget {
		return nil, switchError(http.StatusConflict, "expected current target %q, got %q", strings.TrimSpace(scope.ExpectedFrom), scope.Object.PrimaryTarget)
	}
	store, ok := s.stores.Get(target)
	if !ok {
		return nil, switchError(http.StatusConflict, "target %q is not configured", target)
	}
	replica, ok, err := s.readyReplicaForTarget(ctx, scope.Object.ID, target)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, switchError(http.StatusConflict, "target %q has no ready replica for object %d", target, scope.Object.ID)
	}
	resp := &primaryTargetSwitchResponse{
		Status:         "planned",
		Mode:           scope.Mode,
		Resource:       scope.Resource,
		DeploymentID:   scope.DeploymentID,
		Path:           scope.Path,
		File:           scope.File,
		ObjectID:       scope.Object.ID,
		PreviousTarget: scope.Object.PrimaryTarget,
		Target:         target,
		DryRun:         scope.DryRun,
		EffectiveNow:   scope.EffectiveNow && !scope.DryRun,
		Checks: []doctorCheck{
			{Name: "target_in_route_profile", Status: "ok", Message: "target is part of route_profile " + scope.ProfileName},
			{Name: "target_configured", Status: "ok", Message: "target storage is configured as " + store.Type()},
			{Name: "replica_ready", Status: "ok", Message: "target replica is ready"},
		},
	}
	if direct, err := s.objectReplicaDirectURL(ctx, scope.Object, replica); err == nil && direct != "" {
		resp.TargetURL, resp.TargetURLRedacted = redactDiagnosticURL(direct)
	} else if err != nil {
		resp.Warnings = append(resp.Warnings, "target direct URL unavailable; server-side streaming may still work: "+err.Error())
	}
	if target == scope.Object.PrimaryTarget {
		resp.Status = "noop"
		resp.EffectiveNow = false
		return resp, nil
	}
	if scope.DryRun {
		resp.NextCommands = append(resp.NextCommands, switchApplyCommand(scope.Mode, scope.Resource, scope.DeploymentID, scope.Path, target, scope.Object.PrimaryTarget, false))
		return resp, nil
	}
	updated, err := s.db.UpdateObjectPrimaryTarget(ctx, scope.Object.ID, target)
	if err != nil {
		return nil, err
	}
	resp.Status = "switched"
	resp.PreviousTarget = scope.Object.PrimaryTarget
	resp.Target = updated.PrimaryTarget
	resp.NextCommands = append(resp.NextCommands, switchApplyCommand(scope.Mode, scope.Resource, scope.DeploymentID, scope.Path, scope.Object.PrimaryTarget, updated.PrimaryTarget, false))
	return resp, nil
}

func (s *Server) readyReplicaForTarget(ctx context.Context, objectID int64, target string) (model.Replica, bool, error) {
	replicas, err := s.db.Replicas(ctx, objectID)
	if err != nil {
		return model.Replica{}, false, err
	}
	for _, replica := range replicas {
		if replica.Target == target {
			return replica, replica.Status == model.ReplicaReady, nil
		}
	}
	return model.Replica{}, false, nil
}

func primarySwitchDryRun(req primaryTargetSwitchRequest) (bool, error) {
	dryRun := true
	if req.DryRun != nil {
		dryRun = *req.DryRun
	}
	if !dryRun && strings.TrimSpace(req.Confirm) != "switch" {
		return false, switchError(http.StatusBadRequest, "confirm must be \"switch\" when dry_run=false")
	}
	return dryRun, nil
}

func switchApplyRollbackCommand(mode, resource, deployment, pathValue, previous, current string) string {
	if previous == "" || current == "" {
		return ""
	}
	return switchApplyCommand(mode, resource, deployment, pathValue, previous, current, true)
}

func switchApplyCommand(mode, resource, deployment, pathValue, target, expected string, dryRun bool) string {
	parts := []string{"supercdnctl switch-apply"}
	switch mode {
	case "bucket":
		parts = append(parts, "-bucket "+powershellCommandArg(resource))
	case "site":
		parts = append(parts, "-site "+powershellCommandArg(resource))
		if deployment != "" {
			parts = append(parts, "-deployment "+powershellCommandArg(deployment))
		}
	}
	parts = append(parts, "-path "+powershellCommandArg(pathValue), "-target "+powershellCommandArg(target))
	if expected != "" {
		parts = append(parts, "-expected-current "+powershellCommandArg(expected))
	}
	if !dryRun {
		parts = append(parts, "-dry-run=false", "-confirm switch")
	}
	return strings.Join(parts, " ")
}

func powershellCommandArg(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "''"
	}
	if !strings.ContainsAny(value, " \t\r\n'\"`$&|;()<>[]{}") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func stringInSlice(value string, values []string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}
