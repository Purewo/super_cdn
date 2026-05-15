package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"supercdn/internal/config"
	"supercdn/internal/db"
	"supercdn/internal/edgeheaders"
	"supercdn/internal/model"
	"supercdn/internal/storage"
)

type doctorResponse struct {
	Status            string                      `json:"status"`
	CheckedAtUTC      string                      `json:"checked_at_utc"`
	Auth              doctorAuthStatus            `json:"auth"`
	Server            doctorServerStatus          `json:"server"`
	Checks            []doctorCheck               `json:"checks"`
	ResourceLibraries []resourceLibraryStatusView `json:"resource_libraries,omitempty"`
	RoutingPolicies   []routingPolicyStatusView   `json:"routing_policies,omitempty"`
	Warnings          []string                    `json:"warnings,omitempty"`
	NextCommands      []string                    `json:"next_commands,omitempty"`
}

type doctorAuthStatus struct {
	Root        bool   `json:"root"`
	UserID      int64  `json:"user_id,omitempty"`
	UserName    string `json:"user_name,omitempty"`
	WorkspaceID string `json:"workspace_id"`
	Role        string `json:"role"`
}

type doctorServerStatus struct {
	StorageTargetCount    int    `json:"storage_target_count"`
	RouteProfileCount     int    `json:"route_profile_count"`
	ResourceLibraryCount  int    `json:"resource_library_count"`
	RoutingPolicyCount    int    `json:"routing_policy_count"`
	MaxActiveTransfers    int    `json:"max_active_transfers"`
	OverclockMode         bool   `json:"overclock_mode,omitempty"`
	StagingDirInitialized bool   `json:"staging_dir_initialized"`
	SchemaVersion         string `json:"schema_version,omitempty"`
}

type doctorCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (s *Server) handleDoctor(w http.ResponseWriter, r *http.Request) {
	includeResources, err := queryBool(r, "resources", true)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	includeRouting, err := queryBool(r, "routing", true)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.doctorReport(r.Context(), includeResources, includeRouting))
}

func (s *Server) doctorReport(ctx context.Context, includeResources, includeRouting bool) doctorResponse {
	principal := currentPrincipal(ctx)
	stagingInitialized := false
	if info, err := os.Stat(s.staging); err == nil && info.IsDir() {
		stagingInitialized = true
	}
	resp := doctorResponse{
		Status:       "ok",
		CheckedAtUTC: time.Now().UTC().Format(time.RFC3339),
		Auth: doctorAuthStatus{
			Root:        principal.Root,
			UserID:      principal.UserID,
			UserName:    principal.UserName,
			WorkspaceID: principal.WorkspaceID,
			Role:        principal.Role,
		},
		Server: doctorServerStatus{
			StorageTargetCount:    len(s.stores.Names()),
			RouteProfileCount:     len(s.cfg.RouteProfiles),
			ResourceLibraryCount:  s.resourceLibraryCount(),
			RoutingPolicyCount:    len(s.cfg.RoutingPolicies),
			MaxActiveTransfers:    cap(s.transferSem),
			OverclockMode:         s.overclockMode(),
			StagingDirInitialized: stagingInitialized,
			SchemaVersion:         s.db.SchemaVersion(),
		},
	}
	resp.addCheck("auth", "ok", "token accepted", "")
	if err := s.db.SQL().PingContext(ctx); err != nil {
		resp.addCheck("database", "error", "database ping failed", err.Error())
	} else {
		resp.addCheck("database", "ok", "database is reachable", "")
	}
	s.addStorageDoctorChecks(&resp)
	if includeResources {
		s.addResourceDoctorChecks(ctx, principal, &resp)
	}
	if includeRouting {
		s.addRoutingDoctorChecks(ctx, &resp)
	}
	return resp
}

func (s *Server) resourceLibraryCount() int {
	count := len(s.cfg.ResourceLibraries)
	for _, library := range s.cfg.CloudflareLibrariesEffective() {
		if s.cfg.CloudflareLibraryHasStorage(library) {
			count++
		}
	}
	return count
}

func (s *Server) addStorageDoctorChecks(resp *doctorResponse) {
	if len(s.stores.Names()) == 0 {
		resp.addCheck("storage_targets", "error", "no storage targets are configured", "")
	} else {
		resp.addCheck("storage_targets", "ok", fmt.Sprintf("%d storage target(s) configured", len(s.stores.Names())), "")
	}
	if !resp.Server.StagingDirInitialized {
		resp.addCheck("staging", "error", "server staging directory is not initialized", "")
	} else {
		resp.addCheck("staging", "ok", "server staging directory is initialized", "")
	}
	if len(s.cfg.RouteProfiles) == 0 {
		resp.addCheck("route_profiles", "warning", "no route profiles are configured", "")
		return
	}
	missing := make([]string, 0)
	for _, profile := range s.cfg.RouteProfiles {
		if strings.TrimSpace(profile.Primary) == "" {
			missing = append(missing, profile.Name+": primary storage is empty")
		} else if _, ok := s.stores.Get(profile.Primary); !ok {
			missing = append(missing, profile.Name+": primary "+profile.Primary+" is not configured")
		}
		for _, backup := range profile.Backups {
			backup = strings.TrimSpace(backup)
			if backup == "" {
				continue
			}
			if _, ok := s.stores.Get(backup); !ok {
				missing = append(missing, profile.Name+": backup "+backup+" is not configured")
			}
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		resp.addCheck("route_profiles", "error", strings.Join(missing, "; "), "")
		return
	}
	resp.addCheck("route_profiles", "ok", fmt.Sprintf("%d route profile(s) have configured storage targets", len(s.cfg.RouteProfiles)), "")
}

func (s *Server) addResourceDoctorChecks(ctx context.Context, principal authPrincipal, resp *doctorResponse) {
	if !principal.Root {
		message := "resource library diagnostics require a root token"
		resp.addCheck("resource_status", "warning", message, "")
		resp.Warnings = append(resp.Warnings, message)
		return
	}
	libraries, err := s.resolveResourceStatusTargets(nil)
	if err != nil {
		resp.addCheck("resource_status", "warning", "resource status is unavailable", err.Error())
		resp.Warnings = append(resp.Warnings, err.Error())
		return
	}
	views, err := s.resourceLibraryStatusViews(ctx, libraries, nil)
	if err != nil {
		resp.addCheck("resource_status", "error", "resource status query failed", err.Error())
		return
	}
	resp.ResourceLibraries = views
	resp.NextCommands = append(resp.NextCommands, "supercdnctl resource-status")
	unknown, unhealthy := resourceDoctorStatusCounts(views)
	switch {
	case unhealthy > 0:
		resp.addCheck("resource_status", "warning", fmt.Sprintf("%d resource binding(s) are not healthy", unhealthy), "")
		resp.NextCommands = append(resp.NextCommands, "supercdnctl health-check -libraries <library>")
	case unknown > 0:
		resp.addCheck("resource_status", "warning", fmt.Sprintf("%d resource binding(s) have no cached health result", unknown), "")
		resp.NextCommands = append(resp.NextCommands, "supercdnctl health-check -libraries <library>")
	default:
		resp.addCheck("resource_status", "ok", fmt.Sprintf("%d resource library target(s) reported", len(views)), "")
	}
}

func (s *Server) addRoutingDoctorChecks(ctx context.Context, resp *doctorResponse) {
	if len(s.cfg.RoutingPolicies) == 0 {
		resp.addCheck("routing_policies", "ok", "no explicit routing policies are configured", "")
		return
	}
	errorCount := 0
	resp.RoutingPolicies = make([]routingPolicyStatusView, 0, len(s.cfg.RoutingPolicies))
	for _, policy := range s.cfg.RoutingPolicies {
		view := s.routingPolicyStatusView(ctx, policy)
		errorCount += len(view.Errors)
		resp.RoutingPolicies = append(resp.RoutingPolicies, view)
	}
	resp.NextCommands = append(resp.NextCommands, "supercdnctl routing-policy-status")
	if errorCount > 0 {
		resp.addCheck("routing_policies", "error", fmt.Sprintf("%d routing policy error(s) found", errorCount), "")
		return
	}
	resp.addCheck("routing_policies", "ok", fmt.Sprintf("%d routing policy record(s) configured", len(resp.RoutingPolicies)), "")
}

func resourceDoctorStatusCounts(views []resourceLibraryStatusView) (unknown int, unhealthy int) {
	for _, view := range views {
		for _, binding := range view.Bindings {
			status := strings.ToLower(strings.TrimSpace(binding.Status))
			switch status {
			case "", "unknown":
				unknown++
			case "ok", "configured":
			default:
				unhealthy++
			}
		}
	}
	return unknown, unhealthy
}

func (r *doctorResponse) addCheck(name, status, message, errMessage string) {
	if r.Status == "" {
		r.Status = "ok"
	}
	r.Checks = append(r.Checks, doctorCheck{
		Name:    name,
		Status:  status,
		Message: message,
		Error:   errMessage,
	})
	r.Status = worseDoctorStatus(r.Status, status)
}

func worseDoctorStatus(current, next string) string {
	if doctorStatusRank(next) > doctorStatusRank(current) {
		return next
	}
	return current
}

func doctorStatusRank(status string) int {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "error":
		return 3
	case "warning":
		return 2
	default:
		return 1
	}
}

type cdnDoctorResponse struct {
	Status             string                         `json:"status"`
	CheckedAtUTC       string                         `json:"checked_at_utc"`
	Bucket             model.AssetBucket              `json:"bucket"`
	RouteProfile       cdnDoctorRouteProfile          `json:"route_profile"`
	Path               string                         `json:"path,omitempty"`
	PublicURL          string                         `json:"public_url,omitempty"`
	StorageURL         string                         `json:"storage_url,omitempty"`
	StorageURLRedacted bool                           `json:"storage_url_redacted,omitempty"`
	Object             *cdnDoctorObject               `json:"object,omitempty"`
	Replicas           []cdnDoctorReplica             `json:"replicas,omitempty"`
	IPFS               []edgeManifestIPFS             `json:"ipfs,omitempty"`
	Selection          *routeExplainSelection         `json:"selection,omitempty"`
	Candidates         []edgeRouteCandidateEvaluation `json:"candidates,omitempty"`
	Checks             []doctorCheck                  `json:"checks"`
	Warnings           []string                       `json:"warnings,omitempty"`
	Recommendations    []doctorRecommendation         `json:"recommendations,omitempty"`
	NextCommands       []string                       `json:"next_commands,omitempty"`
}

type cdnDoctorRouteProfile struct {
	Name                string   `json:"name"`
	Primary             string   `json:"primary,omitempty"`
	Backups             []string `json:"backups,omitempty"`
	AllowRedirect       bool     `json:"allow_redirect"`
	DefaultCacheControl string   `json:"default_cache_control,omitempty"`
	RoutingPolicy       string   `json:"routing_policy,omitempty"`
	Targets             []string `json:"targets,omitempty"`
}

type cdnDoctorObject struct {
	LogicalPath   string    `json:"logical_path"`
	ObjectID      int64     `json:"object_id"`
	AssetType     string    `json:"asset_type"`
	PhysicalKey   string    `json:"physical_key"`
	Size          int64     `json:"size"`
	SHA256        string    `json:"sha256"`
	ContentType   string    `json:"content_type"`
	CacheControl  string    `json:"cache_control,omitempty"`
	RouteProfile  string    `json:"route_profile"`
	PrimaryTarget string    `json:"primary_target"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type cdnDoctorReplica struct {
	Target      string            `json:"target"`
	TargetType  string            `json:"target_type,omitempty"`
	Status      string            `json:"status"`
	DirectURL   string            `json:"direct_url,omitempty"`
	URLRedacted bool              `json:"url_redacted,omitempty"`
	LastError   string            `json:"last_error,omitempty"`
	UpdatedAt   time.Time         `json:"updated_at"`
	IPFS        *edgeManifestIPFS `json:"ipfs,omitempty"`
}

type siteDoctorResponse struct {
	Status              string                 `json:"status"`
	CheckedAtUTC        string                 `json:"checked_at_utc"`
	Site                model.Site             `json:"site"`
	Deployment          *model.SiteDeployment  `json:"deployment,omitempty"`
	Path                string                 `json:"path,omitempty"`
	ProductionURL       string                 `json:"production_url,omitempty"`
	ProductionURLs      []string               `json:"production_urls,omitempty"`
	PreviewURL          string                 `json:"preview_url,omitempty"`
	Route               *routeExplainResponse  `json:"route,omitempty"`
	ExpectedEdgeHeaders map[string]string      `json:"expected_edge_headers,omitempty"`
	Checks              []doctorCheck          `json:"checks"`
	Warnings            []string               `json:"warnings,omitempty"`
	Recommendations     []doctorRecommendation `json:"recommendations,omitempty"`
	NextCommands        []string               `json:"next_commands,omitempty"`
}

type doctorRecommendation struct {
	Action  string `json:"action"`
	Level   string `json:"level"`
	Summary string `json:"summary"`
	Reason  string `json:"reason,omitempty"`
	Command string `json:"command,omitempty"`
}

func (s *Server) handleCDNDoctor(w http.ResponseWriter, r *http.Request) {
	bucket, ok := s.getAssetBucketForAPI(w, r, cleanBucketSlug(r.PathValue("slug")))
	if !ok {
		return
	}
	pathValue := strings.TrimSpace(r.URL.Query().Get("path"))
	if pathValue != "" {
		cleaned, err := storage.CleanObjectPath(pathValue)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		pathValue = cleaned
	}
	resp, err := s.cdnDoctorReport(r.Context(), bucket, routeExplainOptions{
		Path:     pathValue,
		Country:  r.URL.Query().Get("country"),
		ClientIP: r.URL.Query().Get("client_ip"),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleSiteDoctor(w http.ResponseWriter, r *http.Request) {
	siteID := cleanID(r.PathValue("id"))
	site, ok := s.getSiteForAPI(w, r, siteID)
	if !ok {
		return
	}
	resp, err := s.siteDoctorReport(r.Context(), site, cleanDeploymentID(r.URL.Query().Get("deployment")), routeExplainOptions{
		Path:     r.URL.Query().Get("path"),
		Country:  r.URL.Query().Get("country"),
		ClientIP: r.URL.Query().Get("client_ip"),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) cdnDoctorReport(ctx context.Context, bucket *model.AssetBucket, opts routeExplainOptions) (cdnDoctorResponse, error) {
	resp := cdnDoctorResponse{
		Status:       "ok",
		CheckedAtUTC: time.Now().UTC().Format(time.RFC3339),
		Bucket:       *bucket,
		Path:         strings.TrimSpace(opts.Path),
		NextCommands: []string{
			"supercdnctl list-bucket -bucket " + bucket.Slug,
		},
	}
	if resp.Path != "" {
		resp.PublicURL = s.assetBucketPublicURL("", bucket.Slug, resp.Path)
	}
	if bucket.Status != model.AssetBucketActive {
		resp.addCheck("bucket_status", "warning", "bucket is "+firstNonEmpty(bucket.Status, "unknown"), "")
	} else {
		resp.addCheck("bucket_status", "ok", "bucket is active", "")
	}
	profile, ok := s.cfg.Profile(bucket.RouteProfile)
	if !ok {
		resp.addCheck("route_profile", "error", "route profile is not configured", bucket.RouteProfile)
		return resp, nil
	}
	resp.RouteProfile = cdnDoctorRouteProfile{
		Name:                bucket.RouteProfile,
		Primary:             profile.Primary,
		Backups:             profile.Backups,
		AllowRedirect:       profile.AllowRedirect,
		DefaultCacheControl: profile.DefaultCacheControl,
		RoutingPolicy:       bucket.RoutingPolicy,
		Targets:             routeProfileFailoverTargets(profile),
	}
	s.addCDNDoctorRouteProfileChecks(&resp, bucket, profile)
	if resp.Path == "" {
		resp.addCheck("object", "skipped", "-path was not provided; object, replica and selected-route checks were skipped", "")
		resp.addRecommendation("inspect_object_route", "info", "Run cdn-doctor with a concrete object path before changing CDN lines.", "", "supercdnctl cdn-doctor -bucket "+bucket.Slug+" -path <path>")
		return resp, nil
	}
	item, err := s.db.GetAssetBucketObject(ctx, bucket.Slug, resp.Path)
	if err != nil {
		if db.IsNotFound(err) || errors.Is(err, sql.ErrNoRows) {
			resp.addCheck("object", "error", "bucket object not found", resp.Path)
			resp.NextCommands = append(resp.NextCommands, "supercdnctl upload-bucket -bucket "+bucket.Slug+" -file <file> -path "+resp.Path)
			resp.addRecommendation("upload_missing_object", "error", "Upload or restore this object before attempting any CDN line switch.", "The bucket path is not tracked in Super CDN.", "supercdnctl upload-bucket -bucket "+bucket.Slug+" -file <file> -path "+resp.Path)
			return resp, nil
		}
		return resp, err
	}
	obj, err := s.db.GetObject(ctx, item.ObjectID)
	if err != nil {
		resp.addCheck("object", "error", "bucket object metadata points to a missing object", err.Error())
		return resp, nil
	}
	resp.Object = &cdnDoctorObject{
		LogicalPath:   item.LogicalPath,
		ObjectID:      item.ObjectID,
		AssetType:     item.AssetType,
		PhysicalKey:   item.PhysicalKey,
		Size:          item.Size,
		SHA256:        item.SHA256,
		ContentType:   item.ContentType,
		CacheControl:  firstNonEmpty(obj.CacheControl, bucket.DefaultCacheControl),
		RouteProfile:  obj.RouteProfile,
		PrimaryTarget: obj.PrimaryTarget,
		CreatedAt:     item.CreatedAt,
		UpdatedAt:     item.UpdatedAt,
	}
	resp.addCheck("object", "ok", "bucket object is tracked", "")
	if pins, _, warnings := s.edgeManifestIPFSForObject(ctx, obj); len(pins) > 0 || len(warnings) > 0 {
		resp.IPFS = pins
		resp.Warnings = append(resp.Warnings, warnings...)
	}
	s.addCDNDoctorReplicaChecks(ctx, &resp, obj)
	s.addCDNDoctorSelection(ctx, &resp, bucket, profile, obj, opts)
	return resp, nil
}

func (s *Server) addCDNDoctorRouteProfileChecks(resp *cdnDoctorResponse, bucket *model.AssetBucket, profile config.RouteProfile) {
	missing := make([]string, 0)
	for _, target := range routeProfileFailoverTargets(profile) {
		if _, ok := s.stores.Get(target); !ok {
			missing = append(missing, target)
		}
	}
	if strings.TrimSpace(profile.Primary) == "" {
		missing = append(missing, "primary target is empty")
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		resp.addCheck("route_profile", "error", "route profile references missing storage target(s)", strings.Join(missing, ", "))
		resp.addRecommendation("fix_route_profile", "error", "Fix the route profile before switching traffic.", "Missing storage targets: "+strings.Join(missing, ", "), "supercdnctl doctor")
		return
	}
	resp.addCheck("route_profile", "ok", "route profile storage targets are configured", "")
	if strings.TrimSpace(bucket.RoutingPolicy) == "" {
		return
	}
	policy, err := s.routingPolicyForProfile(bucket.RoutingPolicy, bucket.RouteProfile, profile)
	if err != nil {
		resp.addCheck("routing_policy", "error", "routing policy is not usable for this bucket", err.Error())
		return
	}
	resp.RouteProfile.RoutingPolicy = policy.Name
	resp.addCheck("routing_policy", "ok", "routing policy is configured", "")
}

func (s *Server) addCDNDoctorReplicaChecks(ctx context.Context, resp *cdnDoctorResponse, obj *model.Object) {
	replicas, err := s.hydrateReplicasIPFS(ctx, obj.ID)
	if err != nil {
		resp.addCheck("replicas", "error", "replica query failed", err.Error())
		return
	}
	if len(replicas) == 0 {
		resp.addCheck("replicas", "error", "object has no replicas", "")
		resp.NextCommands = append(resp.NextCommands, "supercdnctl repair-replicas -object-id "+strconv.FormatInt(obj.ID, 10))
		resp.addRecommendation("repair_replicas", "error", "Repair replicas before changing the active CDN line.", "The object has no recorded replicas.", "supercdnctl repair-replicas -object-id "+strconv.FormatInt(obj.ID, 10))
		return
	}
	ready := 0
	for _, replica := range replicas {
		view := cdnDoctorReplica{
			Target:    replica.Target,
			Status:    firstNonEmpty(replica.Status, "unknown"),
			LastError: replica.LastError,
			UpdatedAt: replica.UpdatedAt,
			IPFS:      ipfsPinToEdgeManifest(replica.IPFS),
		}
		if store, ok := s.stores.Get(replica.Target); ok {
			view.TargetType = store.Type()
		}
		if replica.Status == model.ReplicaReady {
			ready++
			if direct, err := s.objectReplicaDirectURL(ctx, obj, replica); err == nil && direct != "" {
				view.DirectURL, view.URLRedacted = redactDiagnosticURL(direct)
				if replica.Target == obj.PrimaryTarget && resp.StorageURL == "" {
					resp.StorageURL = view.DirectURL
					resp.StorageURLRedacted = view.URLRedacted
				}
			} else if err != nil && view.LastError == "" {
				view.LastError = err.Error()
			}
		}
		resp.Replicas = append(resp.Replicas, view)
	}
	if ready == 0 {
		resp.addCheck("replicas", "error", "object has no ready replicas", "")
		resp.NextCommands = append(resp.NextCommands, "supercdnctl repair-replicas -object-id "+strconv.FormatInt(obj.ID, 10))
		resp.addRecommendation("repair_replicas", "error", "Repair replicas before changing the active CDN line.", "No replica is ready for delivery.", "supercdnctl repair-replicas -object-id "+strconv.FormatInt(obj.ID, 10))
		return
	}
	resp.addCheck("replicas", "ok", fmt.Sprintf("%d/%d replica(s) are ready", ready, len(replicas)), "")
}

func (s *Server) addCDNDoctorSelection(ctx context.Context, resp *cdnDoctorResponse, bucket *model.AssetBucket, profile config.RouteProfile, obj *model.Object, opts routeExplainOptions) {
	if strings.TrimSpace(bucket.RoutingPolicy) != "" {
		policy, err := s.routingPolicyForProfile(bucket.RoutingPolicy, bucket.RouteProfile, profile)
		if err != nil {
			resp.addCheck("selection", "error", "routing policy cannot select a candidate", err.Error())
			return
		}
		decisionReq := routeExplainDecisionRequest(ctx, "/"+resp.Path, opts)
		evaluations, warnings := s.routingPolicyCandidateEvaluations(ctx, policy, obj)
		resp.Warnings = append(resp.Warnings, warnings...)
		if selected, reason, ok := selectRoutingCandidateForRequest(policy, readyCandidatesFromEvaluations(evaluations), decisionReq); ok {
			markSelectedEvaluation(evaluations, selected.Target)
			resp.Selection = redactRouteExplainSelection(routeExplainSelectionFromCandidate(selected, reason))
			resp.addCheck("selection", "ok", "routing policy selected "+selected.Target, "")
		} else {
			resp.Selection = &routeExplainSelection{Reason: "no_ready_candidates"}
			resp.addCheck("selection", "warning", "routing policy has no ready candidate", "")
		}
		resp.Candidates = redactRouteCandidateEvaluations(evaluations)
		resp.addCDNDoctorCandidateRecommendations(evaluations, policy.Name, obj.ID)
		resp.NextCommands = append(resp.NextCommands, "supercdnctl routing-policy-status -policy "+policy.Name)
		return
	}
	evaluations, warnings := s.resourceFailoverCandidateEvaluations(ctx, bucket.RouteProfile, obj)
	resp.Warnings = append(resp.Warnings, warnings...)
	if selected, ok := firstReadyEvaluation(evaluations); ok {
		markSelectedEvaluation(evaluations, selected.Target)
		resp.Selection = redactRouteExplainSelection(routeExplainSelectionFromCandidate(selected, "primary_or_failover_order"))
		resp.addCheck("selection", "ok", "selected "+selected.Target+" from route profile order", "")
	} else {
		resp.Selection = &routeExplainSelection{Reason: "no_ready_candidates"}
		resp.addCheck("selection", "warning", "route profile has no ready direct candidate", "")
	}
	resp.Candidates = redactRouteCandidateEvaluations(evaluations)
	resp.addCDNDoctorCandidateRecommendations(evaluations, "", obj.ID)
}

func ipfsPinToEdgeManifest(pin *model.IPFSPin) *edgeManifestIPFS {
	if pin == nil || pin.CID == "" {
		return nil
	}
	return &edgeManifestIPFS{
		Target:        pin.Target,
		Provider:      pin.Provider,
		CID:           pin.CID,
		GatewayURL:    pin.GatewayURL,
		PinStatus:     pin.PinStatus,
		ProviderPinID: pin.ProviderPinID,
	}
}

func redactRouteExplainSelection(selection *routeExplainSelection) *routeExplainSelection {
	if selection == nil {
		return nil
	}
	out := *selection
	out.URL, _ = redactDiagnosticURL(out.URL)
	return &out
}

func redactRouteCandidateEvaluations(evaluations []edgeRouteCandidateEvaluation) []edgeRouteCandidateEvaluation {
	out := make([]edgeRouteCandidateEvaluation, len(evaluations))
	for i, evaluation := range evaluations {
		out[i] = evaluation
		out[i].URL, _ = redactDiagnosticURL(out[i].URL)
	}
	return out
}

func (r *cdnDoctorResponse) addCheck(name, status, message, errMessage string) {
	if r.Status == "" {
		r.Status = "ok"
	}
	r.Checks = append(r.Checks, doctorCheck{
		Name:    name,
		Status:  status,
		Message: message,
		Error:   errMessage,
	})
	r.Status = worseDoctorStatus(r.Status, status)
}

func (r *cdnDoctorResponse) addRecommendation(action, level, summary, reason, command string) {
	r.Recommendations = append(r.Recommendations, doctorRecommendation{
		Action:  action,
		Level:   level,
		Summary: summary,
		Reason:  reason,
		Command: command,
	})
}

func (r *cdnDoctorResponse) addCDNDoctorCandidateRecommendations(evaluations []edgeRouteCandidateEvaluation, policyName string, objectID int64) {
	ready := readyEvaluationCount(evaluations)
	healthTargets := unhealthyEvaluationTargets(evaluations)
	if len(healthTargets) > 0 {
		r.addRecommendation("check_resource_health", "warning", "Run a health check before manually switching traffic.", "Candidate target(s) are skipped by recent health failures: "+strings.Join(healthTargets, ", "), "supercdnctl health-check -libraries "+strings.Join(healthTargets, ",")+" -force")
	}
	if ready == 0 {
		r.addRecommendation("repair_replicas", "error", "Do not switch CDN lines until at least one candidate is ready.", "No ready delivery candidate is available.", "supercdnctl repair-replicas -object-id "+strconv.FormatInt(objectID, 10))
		return
	}
	if len(evaluations) > 1 && ready == 1 {
		r.addRecommendation("manual_switch_not_ready", "warning", "Keep this as a manual recovery decision; backup candidates are not fully ready.", "Only one candidate can currently serve the object.", "supercdnctl repair-replicas -object-id "+strconv.FormatInt(objectID, 10))
		return
	}
	if ready > 1 {
		command := "supercdnctl resource-status"
		if policyName != "" {
			command = "supercdnctl routing-policy-status -policy " + policyName
		}
		r.addRecommendation("manual_switch_available", "info", "Multiple ready candidates exist; review them and switch explicitly if needed.", "Super CDN reports candidates, but cross-line changes should stay user-confirmed.", command)
	}
}

func (s *Server) siteDoctorReport(ctx context.Context, site *model.Site, deploymentID string, opts routeExplainOptions) (siteDoctorResponse, error) {
	siteView := s.siteView(site)
	resp := siteDoctorResponse{
		Status:         "ok",
		CheckedAtUTC:   time.Now().UTC().Format(time.RFC3339),
		Site:           siteView,
		Path:           strings.TrimSpace(opts.Path),
		ProductionURL:  siteView.URL,
		ProductionURLs: siteView.URLs,
		NextCommands: []string{
			"supercdnctl list-deployments -site " + site.ID,
		},
	}
	if siteView.Status != model.SiteStatusActive {
		resp.addCheck("site_status", "warning", "site is "+firstNonEmpty(siteView.Status, "unknown"), "")
	} else {
		resp.addCheck("site_status", "ok", "site is active", "")
	}
	dep, depErr := s.siteDoctorDeployment(ctx, site, deploymentID)
	if depErr != nil {
		resp.addCheck("deployment", "error", "deployment unavailable", depErr.Error())
		resp.NextCommands = append(resp.NextCommands, "supercdnctl deploy-site -site "+site.ID+" -dir <dist>")
		return resp, nil
	}
	depView := s.siteDeploymentView(ctx, dep)
	resp.Deployment = &depView
	resp.PreviewURL = depView.PreviewURL
	if depView.ProductionURL != "" {
		resp.ProductionURL = depView.ProductionURL
		resp.ProductionURLs = depView.ProductionURLs
	}
	if dep.Status != model.SiteDeploymentReady && dep.Status != model.SiteDeploymentActive {
		resp.addCheck("deployment", "error", "deployment is not ready", dep.Status)
		resp.addRecommendation("wait_or_redeploy", "error", "Do not switch routing until the selected deployment is ready.", "Deployment status is "+dep.Status+".", "supercdnctl list-deployments -site "+site.ID)
		return resp, nil
	}
	if !dep.Active && deploymentID == "" {
		resp.addCheck("deployment", "warning", "selected deployment is not active production", dep.ID)
	} else {
		resp.addCheck("deployment", "ok", "deployment is ready", "")
	}
	if resp.Path == "" {
		resp.addCheck("route", "skipped", "-path was not provided; route, candidate and expected-header checks were skipped", "")
		resp.ExpectedEdgeHeaders = siteDoctorExpectedHeaders(dep, nil)
		resp.addRecommendation("inspect_site_route", "info", "Run site-doctor with a concrete asset path before changing site routing.", "", "supercdnctl site-doctor -site "+site.ID+" -path /assets/<file>")
		return resp, nil
	}
	route, err := s.explainSiteRoute(ctx, site, dep, opts)
	if err != nil {
		resp.addCheck("route", "error", "route explanation failed", err.Error())
		resp.NextCommands = append(resp.NextCommands, "supercdnctl route-explain -site "+site.ID+" -path "+resp.Path)
		resp.addRecommendation("inspect_route_error", "error", "Fix route explanation errors before changing site traffic.", err.Error(), "supercdnctl route-explain -site "+site.ID+" -path "+resp.Path)
		return resp, nil
	}
	route = redactRouteExplainResponse(route)
	resp.Route = &route
	resp.Warnings = append(resp.Warnings, route.Warnings...)
	resp.ExpectedEdgeHeaders = siteDoctorExpectedHeaders(dep, &route)
	if len(route.Candidates) > 0 {
		if route.Selection != nil && route.Selection.Target != "" {
			resp.addCheck("route", "ok", "route selected "+route.Selection.Target, "")
		} else {
			resp.addCheck("route", "warning", "route has candidates but no selected target", "")
		}
	} else if route.Route.Type == "origin" {
		resp.addCheck("route", "ok", "route uses origin/static entry delivery", "")
	} else {
		resp.addCheck("route", "ok", "route resolved as "+route.Route.Type, "")
	}
	resp.NextCommands = append(resp.NextCommands, "supercdnctl route-explain -site "+site.ID+" -path "+resp.Path)
	resp.NextCommands = append(resp.NextCommands, "supercdnctl probe-site -site "+site.ID)
	resp.addSiteDoctorRouteRecommendations(dep, &route)
	return resp, nil
}

func (s *Server) siteDoctorDeployment(ctx context.Context, site *model.Site, deploymentID string) (*model.SiteDeployment, error) {
	if deploymentID != "" {
		dep, err := s.db.GetSiteDeployment(ctx, deploymentID)
		if err != nil || dep.SiteID != site.ID {
			if err == nil {
				err = sql.ErrNoRows
			}
			return nil, fmt.Errorf("deployment %q not found", deploymentID)
		}
		return dep, nil
	}
	dep, err := s.db.ActiveSiteDeployment(ctx, site.ID)
	if err != nil {
		return nil, fmt.Errorf("active production deployment not found")
	}
	return dep, nil
}

func redactRouteExplainResponse(resp routeExplainResponse) routeExplainResponse {
	resp.Route = redactEdgeManifestRoute(resp.Route)
	resp.Selection = redactRouteExplainSelection(resp.Selection)
	resp.Candidates = redactRouteCandidateEvaluations(resp.Candidates)
	return resp
}

func redactEdgeManifestRoute(route edgeManifestRoute) edgeManifestRoute {
	route.Location, _ = redactDiagnosticURL(route.Location)
	for i := range route.GatewayFallbacks {
		route.GatewayFallbacks[i], _ = redactDiagnosticURL(route.GatewayFallbacks[i])
	}
	for i := range route.Candidates {
		route.Candidates[i].URL, _ = redactDiagnosticURL(route.Candidates[i].URL)
		if route.Candidates[i].IPFS != nil {
			redacted := redactEdgeManifestIPFS(*route.Candidates[i].IPFS)
			route.Candidates[i].IPFS = &redacted
		}
	}
	for i := range route.IPFS {
		route.IPFS[i] = redactEdgeManifestIPFS(route.IPFS[i])
	}
	return route
}

func redactEdgeManifestIPFS(pin edgeManifestIPFS) edgeManifestIPFS {
	pin.GatewayURL, _ = redactDiagnosticURL(pin.GatewayURL)
	return pin
}

func siteDoctorExpectedHeaders(dep *model.SiteDeployment, route *routeExplainResponse) map[string]string {
	if dep == nil {
		return nil
	}
	headers := map[string]string{}
	target := firstNonEmpty(dep.DeploymentTarget, model.SiteDeploymentTargetOriginAssisted)
	switch target {
	case model.SiteDeploymentTargetCloudflareStatic:
		headers[edgeheaders.HeaderSource] = edgeheaders.SourceCloudflareStatic
	case model.SiteDeploymentTargetHybridEdge:
		headers[edgeheaders.HeaderManifest] = edgeheaders.ManifestRoute
		if route != nil && route.Route.File != "" {
			headers[edgeheaders.HeaderFile] = route.Route.File
		}
		if route != nil {
			if source := expectedEdgeSourceForRoute(route.Route); source != "" {
				headers[edgeheaders.HeaderSource] = source
			}
			if route.Selection != nil && route.Selection.Target != "" {
				headers[edgeheaders.HeaderRouteTarget] = route.Selection.Target
			}
		}
	case model.SiteDeploymentTargetOriginAssisted:
		if route != nil && route.Route.Type != "origin" {
			headers[edgeheaders.HeaderRedirect] = edgeheaders.RedirectStorage
			if route.RoutingPolicy != "" {
				headers[edgeheaders.HeaderRoutePolicy] = route.RoutingPolicy
			}
			if route.Selection != nil && route.Selection.Target != "" {
				headers[edgeheaders.HeaderRouteTarget] = route.Selection.Target
			}
		}
	}
	if len(headers) == 0 {
		return nil
	}
	return headers
}

func expectedEdgeSourceForRoute(route edgeManifestRoute) string {
	switch route.Type {
	case "ipfs":
		return edgeheaders.SourceIPFSGateway
	case "failover":
		return edgeheaders.SourceResourceFailover
	case "redirect", "smart":
		return edgeheaders.SourceManifest
	case "origin":
		return edgeheaders.SourceCloudflareStatic
	default:
		return ""
	}
}

func (r *siteDoctorResponse) addCheck(name, status, message, errMessage string) {
	if r.Status == "" {
		r.Status = "ok"
	}
	r.Checks = append(r.Checks, doctorCheck{
		Name:    name,
		Status:  status,
		Message: message,
		Error:   errMessage,
	})
	r.Status = worseDoctorStatus(r.Status, status)
}

func (r *siteDoctorResponse) addRecommendation(action, level, summary, reason, command string) {
	r.Recommendations = append(r.Recommendations, doctorRecommendation{
		Action:  action,
		Level:   level,
		Summary: summary,
		Reason:  reason,
		Command: command,
	})
}

func (r *siteDoctorResponse) addSiteDoctorRouteRecommendations(dep *model.SiteDeployment, route *routeExplainResponse) {
	if dep == nil || route == nil {
		return
	}
	switch dep.DeploymentTarget {
	case model.SiteDeploymentTargetOriginAssisted:
		r.addRecommendation("prefer_edge_entry", "info", "Origin-assisted delivery is a compatibility path; prefer Cloudflare Static or hybrid_edge for mature production traffic.", "", "supercdnctl update-site -site "+dep.SiteID+" -dir <dist> -target cloudflare_static")
	case model.SiteDeploymentTargetCloudflareStatic:
		r.addRecommendation("rollback_boundary", "info", "Cloudflare Static rollback must republish the desired assets instead of metadata-only promotion.", "", "supercdnctl update-site -site "+dep.SiteID+" -dir <dist> -target cloudflare_static")
	case model.SiteDeploymentTargetHybridEdge:
		if route.RoutingPolicy != "" || route.ResourceFailover {
			r.addRecommendation("refresh_manifest_after_review", "info", "If health or signed locators changed, refresh the active edge manifest after reviewing candidates.", "", "supercdnctl refresh-edge-manifest -site "+dep.SiteID)
		}
	}
	if len(route.Candidates) == 0 {
		return
	}
	ready := readyEvaluationCount(route.Candidates)
	healthTargets := unhealthyEvaluationTargets(route.Candidates)
	if len(healthTargets) > 0 {
		r.addRecommendation("check_resource_health", "warning", "Run a health check before manually switching this site route.", "Candidate target(s) are skipped by recent health failures: "+strings.Join(healthTargets, ", "), "supercdnctl health-check -libraries "+strings.Join(healthTargets, ",")+" -force")
	}
	if ready == 0 {
		r.addRecommendation("repair_route_replicas", "error", "Do not switch site traffic until at least one route candidate is ready.", "No ready candidate is available for this path.", "supercdnctl route-explain -site "+dep.SiteID+" -path "+route.Path)
		return
	}
	if len(route.Candidates) > 1 && ready == 1 {
		r.addRecommendation("manual_switch_not_ready", "warning", "Keep this as a manual recovery decision; backup candidates are not fully ready.", "Only one candidate can currently serve this path.", "supercdnctl route-explain -site "+dep.SiteID+" -path "+route.Path)
		return
	}
	if ready > 1 {
		r.addRecommendation("manual_switch_available", "info", "Multiple ready candidates exist; review route-explain and confirm before switching policy.", "Super CDN exposes candidate state but does not silently change cross-line policy for the user.", "supercdnctl route-explain -site "+dep.SiteID+" -path "+route.Path)
	}
}

func readyEvaluationCount(evaluations []edgeRouteCandidateEvaluation) int {
	ready := 0
	for _, item := range evaluations {
		if item.Status == model.ReplicaReady && item.URL != "" {
			ready++
		}
	}
	return ready
}

func unhealthyEvaluationTargets(evaluations []edgeRouteCandidateEvaluation) []string {
	var out []string
	for _, item := range evaluations {
		if strings.Contains(strings.ToLower(item.Reason), "skipped by health") {
			out = append(out, item.Target)
		}
	}
	sort.Strings(out)
	return out
}
