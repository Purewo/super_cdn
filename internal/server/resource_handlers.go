package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"supercdn/internal/model"
	"supercdn/internal/storage"
)

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
	Name         string                       `json:"name"`
	TargetType   string                       `json:"target_type,omitempty"`
	Capabilities storage.Capabilities         `json:"capabilities"`
	Bindings     []resourceLibraryBindingView `json:"bindings"`
}

type resourceLibraryBindingView struct {
	Name         string                       `json:"name"`
	Path         string                       `json:"path"`
	MountPoint   string                       `json:"mount_point,omitempty"`
	TargetType   string                       `json:"target_type,omitempty"`
	Status       string                       `json:"status"`
	Capabilities storage.Capabilities         `json:"capabilities"`
	Skipped      bool                         `json:"skipped,omitempty"`
	SkipReason   string                       `json:"skip_reason,omitempty"`
	Health       *model.ResourceLibraryHealth `json:"health,omitempty"`
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

type routingPolicyStatusResponse struct {
	Policies []routingPolicyStatusView `json:"policies"`
}

type routingPolicyStatusView struct {
	Name               string                          `json:"name"`
	Mode               string                          `json:"mode"`
	DefaultRegionGroup string                          `json:"default_region_group"`
	SourceCount        int                             `json:"source_count"`
	Sources            []routingPolicySourceStatusView `json:"sources"`
	Errors             []string                        `json:"errors,omitempty"`
}

type routingPolicySourceStatusView struct {
	Target       string                       `json:"target"`
	TargetType   string                       `json:"target_type,omitempty"`
	RegionGroup  string                       `json:"region_group"`
	Weight       int                          `json:"weight"`
	Priority     int                          `json:"priority"`
	FallbackOnly bool                         `json:"fallback_only,omitempty"`
	Status       string                       `json:"status"`
	Health       *model.ResourceLibraryHealth `json:"health,omitempty"`
	Error        string                       `json:"error,omitempty"`
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
	if !s.auditMutation(w, r, auditActionResourceLibraryInit, "resource_library:"+strings.Join(libraries, ",")) {
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
	libraries, err := s.resolveResourceStatusTargets(optionalLibrary(library))
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

func (s *Server) handleRoutingPolicyStatus(w http.ResponseWriter, r *http.Request) {
	policies := make([]routingPolicyStatusView, 0, len(s.cfg.RoutingPolicies))
	for _, policy := range s.cfg.RoutingPolicies {
		policies = append(policies, s.routingPolicyStatusView(r.Context(), policy))
	}
	writeJSON(w, http.StatusOK, routingPolicyStatusResponse{Policies: policies})
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
	if req.WriteProbe {
		if !s.auditMutation(w, r, auditActionResourceLibraryHealthWrite, "resource_library:"+strings.Join(libraries, ",")) {
			return
		}
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
	if !s.auditMutation(w, r, auditActionResourceLibraryEndToEndProbe, "route_profile:"+req.RouteProfile) {
		return
	}
	writeJSON(w, http.StatusOK, result)
}
