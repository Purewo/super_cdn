package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"supercdn/internal/cloudflare"
	"supercdn/internal/model"
)

type createSiteRequest struct {
	ID                    string   `json:"id"`
	Name                  string   `json:"name"`
	Mode                  string   `json:"mode"`
	RouteProfile          string   `json:"route_profile"`
	DeploymentTarget      string   `json:"deployment_target"`
	RoutingPolicy         string   `json:"routing_policy"`
	Domains               []string `json:"domains"`
	DefaultDomainID       string   `json:"default_domain_id"`
	RandomDefaultDomain   bool     `json:"random_default_domain"`
	SkipDefaultDomain     bool     `json:"skip_default_domain"`
	AllocateDefaultDomain *bool    `json:"allocate_default_domain,omitempty"`
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
	routingPolicy := strings.TrimSpace(req.RoutingPolicy)
	if routingPolicy != "" {
		if _, err := s.routingPolicyForProfile(routingPolicy, req.RouteProfile, profile); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	domains, err := s.siteDomainsFromRequest(req.ID, req.Domains, req.DefaultDomainID, req.RandomDefaultDomain, req.SkipDefaultDomain, req.AllocateDefaultDomain)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	site, err := s.db.CreateSiteInWorkspace(r.Context(), workspaceForContext(r.Context()), req.ID, strings.TrimSpace(req.Name), req.Mode, req.RouteProfile, deploymentTarget, routingPolicy, domains)
	if err != nil {
		if strings.Contains(err.Error(), "already bound") {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		if strings.Contains(err.Error(), "belongs to another workspace") {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !s.auditMutation(w, r, "site.create", "site:"+site.ID) {
		return
	}
	writeJSON(w, http.StatusOK, s.siteView(site))
}

func (s *Server) handleListSites(w http.ResponseWriter, r *http.Request) {
	principal := currentPrincipal(r.Context())
	workspaceID := ""
	if !principal.Root {
		workspaceID = principal.WorkspaceID
	}
	sites, err := s.db.ListSitesInWorkspace(r.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	views := make([]model.Site, 0, len(sites))
	for i := range sites {
		site := sites[i]
		views = append(views, s.siteView(&site))
	}
	writeJSON(w, http.StatusOK, map[string]any{"sites": views})
}

func (s *Server) handleOfflineSite(w http.ResponseWriter, r *http.Request) {
	s.handleSetSiteStatus(w, r, model.SiteStatusOffline)
}

func (s *Server) handleOnlineSite(w http.ResponseWriter, r *http.Request) {
	s.handleSetSiteStatus(w, r, model.SiteStatusActive)
}

func (s *Server) handleSetSiteStatus(w http.ResponseWriter, r *http.Request, status string) {
	siteID := cleanID(r.PathValue("id"))
	if siteID == "" {
		writeError(w, http.StatusBadRequest, "site id is required")
		return
	}
	if _, ok := s.getSiteForAPI(w, r, siteID); !ok {
		return
	}
	site, err := s.db.SetSiteStatus(r.Context(), siteID, status)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	action := "site.online"
	if status == model.SiteStatusOffline {
		action = "site.offline"
	}
	if !s.auditMutation(w, r, action, "site:"+site.ID) {
		return
	}
	writeJSON(w, http.StatusOK, s.siteView(site))
}

func (s *Server) handleDeleteSite(w http.ResponseWriter, r *http.Request) {
	siteID := cleanID(r.PathValue("id"))
	if siteID == "" {
		writeError(w, http.StatusBadRequest, "site id is required")
		return
	}
	site, ok := s.getSiteForAPI(w, r, siteID)
	if !ok {
		return
	}
	force, err := queryBool(r, "force", false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !force {
		writeError(w, http.StatusBadRequest, "force=true is required to delete a site and all tracked resources")
		return
	}
	deleteRemote, err := queryBool(r, "delete_remote", true)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	result := s.deleteSite(r.Context(), site, deleteRemote)
	if len(result.Errors) > 0 {
		writeJSON(w, http.StatusBadGateway, result)
		return
	}
	if !s.auditMutation(w, r, "site.delete", "site:"+site.ID) {
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleBindSiteDomains(w http.ResponseWriter, r *http.Request) {
	siteID := cleanID(r.PathValue("id"))
	if siteID == "" {
		writeError(w, http.StatusBadRequest, "site id is required")
		return
	}
	site, ok := s.getSiteForAPI(w, r, siteID)
	if !ok {
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
	if !s.auditMutation(w, r, "site.domains.bind", "site:"+site.ID) {
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
	if !s.ensureSiteAccessIfExists(w, r, siteID) {
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
	site, ok := s.getSiteForAPI(w, r, siteID)
	if !ok {
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
	if !s.auditMutation(w, r, "cloudflare.worker_routes.sync", "site:"+site.ID) {
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
	site, ok := s.getSiteForAPI(w, r, siteID)
	if !ok {
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
	if !s.auditMutation(w, r, "cloudflare.dns.sync", "site:"+site.ID) {
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
