package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
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
	StorageTargetCount    int  `json:"storage_target_count"`
	RouteProfileCount     int  `json:"route_profile_count"`
	ResourceLibraryCount  int  `json:"resource_library_count"`
	RoutingPolicyCount    int  `json:"routing_policy_count"`
	MaxActiveTransfers    int  `json:"max_active_transfers"`
	OverclockMode         bool `json:"overclock_mode,omitempty"`
	StagingDirInitialized bool `json:"staging_dir_initialized"`
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
