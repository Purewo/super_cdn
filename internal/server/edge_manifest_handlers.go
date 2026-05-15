package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	"supercdn/internal/config"
	"supercdn/internal/model"
	"supercdn/internal/storage"
)

type edgeManifest struct {
	Version          int                          `json:"version"`
	Kind             string                       `json:"kind"`
	SiteID           string                       `json:"site_id"`
	DeploymentID     string                       `json:"deployment_id"`
	Environment      string                       `json:"environment"`
	Status           string                       `json:"status"`
	RouteProfile     string                       `json:"route_profile"`
	DeploymentTarget string                       `json:"deployment_target"`
	RoutingPolicy    string                       `json:"routing_policy,omitempty"`
	ResourceFailover bool                         `json:"resource_failover"`
	Mode             string                       `json:"mode"`
	GeneratedAtUTC   string                       `json:"generated_at_utc"`
	Rules            siteRules                    `json:"rules,omitempty"`
	Routes           map[string]edgeManifestRoute `json:"routes"`
	Fallback         *edgeManifestRoute           `json:"fallback,omitempty"`
	NotFound         *edgeManifestRoute           `json:"not_found,omitempty"`
	Warnings         []string                     `json:"warnings,omitempty"`
}

type edgeManifestRoute struct {
	Type               string               `json:"type"`
	Delivery           string               `json:"delivery"`
	File               string               `json:"file"`
	Status             int                  `json:"status"`
	Location           string               `json:"location,omitempty"`
	ContentType        string               `json:"content_type,omitempty"`
	CacheControl       string               `json:"cache_control,omitempty"`
	ObjectCacheControl string               `json:"object_cache_control,omitempty"`
	Size               int64                `json:"size"`
	SHA256             string               `json:"sha256,omitempty"`
	ObjectID           int64                `json:"object_id"`
	ObjectKey          string               `json:"object_key,omitempty"`
	IPFS               []edgeManifestIPFS   `json:"ipfs,omitempty"`
	GatewayFallbacks   []string             `json:"gateway_fallbacks,omitempty"`
	RoutingPolicy      *edgeRoutingPolicy   `json:"routing_policy,omitempty"`
	Candidates         []edgeRouteCandidate `json:"candidates,omitempty"`
	Headers            map[string]string    `json:"headers,omitempty"`
}

type edgeRoutingPolicy struct {
	Name               string                    `json:"name"`
	Mode               string                    `json:"mode"`
	DefaultRegionGroup string                    `json:"default_region_group"`
	Sources            []edgeRoutingPolicySource `json:"sources"`
}

type edgeRoutingPolicySource struct {
	Target       string `json:"target"`
	RegionGroup  string `json:"region_group"`
	Weight       int    `json:"weight"`
	Priority     int    `json:"priority"`
	FallbackOnly bool   `json:"fallback_only,omitempty"`
}

type edgeRouteCandidate struct {
	Target       string            `json:"target"`
	TargetType   string            `json:"target_type,omitempty"`
	Type         string            `json:"type"`
	RegionGroup  string            `json:"region_group"`
	Weight       int               `json:"weight"`
	Priority     int               `json:"priority"`
	FallbackOnly bool              `json:"fallback_only,omitempty"`
	URL          string            `json:"url"`
	Status       string            `json:"status"`
	IPFS         *edgeManifestIPFS `json:"ipfs,omitempty"`
}

type edgeManifestIPFS struct {
	Target        string `json:"target"`
	Provider      string `json:"provider"`
	CID           string `json:"cid"`
	GatewayURL    string `json:"gateway_url,omitempty"`
	PinStatus     string `json:"pin_status,omitempty"`
	ProviderPinID string `json:"provider_pin_id,omitempty"`
}

type edgeRouteCandidateEvaluation struct {
	Target        string            `json:"target"`
	TargetType    string            `json:"target_type,omitempty"`
	Type          string            `json:"type,omitempty"`
	RegionGroup   string            `json:"region_group,omitempty"`
	Weight        int               `json:"weight,omitempty"`
	Priority      int               `json:"priority,omitempty"`
	FallbackOnly  bool              `json:"fallback_only,omitempty"`
	URL           string            `json:"url,omitempty"`
	Status        string            `json:"status"`
	ReplicaStatus string            `json:"replica_status,omitempty"`
	Reason        string            `json:"reason,omitempty"`
	Selected      bool              `json:"selected,omitempty"`
	IPFS          *edgeManifestIPFS `json:"ipfs,omitempty"`
}

type routeExplainResponse struct {
	SiteID           string                         `json:"site_id"`
	DeploymentID     string                         `json:"deployment_id"`
	Path             string                         `json:"path"`
	MatchedPath      string                         `json:"matched_path,omitempty"`
	MatchType        string                         `json:"match_type,omitempty"`
	RouteProfile     string                         `json:"route_profile"`
	DeploymentTarget string                         `json:"deployment_target"`
	RoutingPolicy    string                         `json:"routing_policy,omitempty"`
	ResourceFailover bool                           `json:"resource_failover"`
	RegionGroup      string                         `json:"region_group,omitempty"`
	HashKey          string                         `json:"hash_key,omitempty"`
	Route            edgeManifestRoute              `json:"route"`
	Selection        *routeExplainSelection         `json:"selection,omitempty"`
	Candidates       []edgeRouteCandidateEvaluation `json:"candidates,omitempty"`
	Warnings         []string                       `json:"warnings,omitempty"`
}

type routeExplainSelection struct {
	Target      string `json:"target,omitempty"`
	TargetType  string `json:"target_type,omitempty"`
	Type        string `json:"type,omitempty"`
	RegionGroup string `json:"region_group,omitempty"`
	URL         string `json:"url,omitempty"`
	Reason      string `json:"reason"`
}

type routeExplainOptions struct {
	Path     string
	Country  string
	ClientIP string
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

func (s *Server) handleExportSiteEdgeManifest(w http.ResponseWriter, r *http.Request) {
	siteID := cleanID(r.PathValue("id"))
	deploymentID := cleanDeploymentID(r.PathValue("deployment"))
	site, ok := s.getSiteForAPI(w, r, siteID)
	if !ok {
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
	site, ok := s.getSiteForAPI(w, r, siteID)
	if !ok {
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
	if !s.auditMutation(w, r, auditActionSiteEdgeManifestPublish, "site:"+site.ID+";deployment:"+dep.ID) {
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleExplainSiteRoute(w http.ResponseWriter, r *http.Request) {
	siteID := cleanID(r.PathValue("id"))
	site, ok := s.getSiteForAPI(w, r, siteID)
	if !ok {
		return
	}
	deploymentID := cleanDeploymentID(r.URL.Query().Get("deployment"))
	var dep *model.SiteDeployment
	var err error
	if deploymentID != "" {
		dep, err = s.db.GetSiteDeployment(r.Context(), deploymentID)
		if err != nil || dep.SiteID != site.ID {
			writeError(w, http.StatusNotFound, "deployment not found")
			return
		}
	} else {
		dep, err = s.db.ActiveSiteDeployment(r.Context(), site.ID)
		if err != nil {
			writeError(w, http.StatusNotFound, "active deployment not found")
			return
		}
	}
	if dep.Status != model.SiteDeploymentReady && dep.Status != model.SiteDeploymentActive {
		writeError(w, http.StatusBadRequest, "deployment is not ready")
		return
	}
	resp, err := s.explainSiteRoute(r.Context(), site, dep, routeExplainOptions{
		Path:     r.URL.Query().Get("path"),
		Country:  r.URL.Query().Get("country"),
		ClientIP: r.URL.Query().Get("client_ip"),
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
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
	routingPolicyName := strings.TrimSpace(firstNonEmpty(dep.RoutingPolicy, site.RoutingPolicy))
	var routingPolicy *config.RoutingPolicy
	if routingPolicyName != "" {
		profile, ok := s.cfg.Profile(dep.RouteProfile)
		if !ok {
			return nil, fmt.Errorf("unknown route_profile %q", dep.RouteProfile)
		}
		policy, err := s.routingPolicyForProfile(routingPolicyName, dep.RouteProfile, profile)
		if err != nil {
			return nil, err
		}
		routingPolicy = &policy
	}
	manifest := &edgeManifest{
		Version:          1,
		Kind:             "supercdn-edge-manifest",
		SiteID:           site.ID,
		DeploymentID:     dep.ID,
		Environment:      dep.Environment,
		Status:           dep.Status,
		RouteProfile:     dep.RouteProfile,
		DeploymentTarget: dep.DeploymentTarget,
		RoutingPolicy:    routingPolicyName,
		ResourceFailover: dep.ResourceFailover,
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
		route, warnings := s.edgeManifestRouteForFile(ctx, rules, dep.RouteProfile, dep.ResourceFailover, file, obj, http.StatusOK, routingPolicy)
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
			route, warnings := s.edgeManifestRouteForFile(ctx, rules, dep.RouteProfile, dep.ResourceFailover, file, objects[file.Path], http.StatusOK, routingPolicy)
			manifest.Warnings = append(manifest.Warnings, warnings...)
			manifest.Fallback = &route
		} else {
			manifest.Warnings = append(manifest.Warnings, "spa mode is enabled but index.html is not present")
		}
	}
	notFoundPath := firstNonEmpty(rules.NotFound, "404.html")
	if file, ok := fileByPath[notFoundPath]; ok {
		route, warnings := s.edgeManifestRouteForFile(ctx, rules, dep.RouteProfile, dep.ResourceFailover, file, objects[file.Path], http.StatusNotFound, routingPolicy)
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

func (s *Server) explainSiteRoute(ctx context.Context, site *model.Site, dep *model.SiteDeployment, opts routeExplainOptions) (routeExplainResponse, error) {
	reqPath := cleanRequestPath(opts.Path)
	if strings.TrimSpace(opts.Path) == "" {
		return routeExplainResponse{}, fmt.Errorf("path is required")
	}
	rules := deploymentRules(dep, site)
	file, obj, matchType, status, err := s.siteDeploymentFileForRouteExplain(ctx, dep, rules, reqPath)
	if err != nil {
		return routeExplainResponse{}, err
	}
	routingPolicyName := strings.TrimSpace(firstNonEmpty(dep.RoutingPolicy, site.RoutingPolicy))
	var routingPolicy *config.RoutingPolicy
	if routingPolicyName != "" {
		profile, ok := s.cfg.Profile(dep.RouteProfile)
		if !ok {
			return routeExplainResponse{}, fmt.Errorf("unknown route_profile %q", dep.RouteProfile)
		}
		policy, err := s.routingPolicyForProfile(routingPolicyName, dep.RouteProfile, profile)
		if err != nil {
			return routeExplainResponse{}, err
		}
		routingPolicy = &policy
	}
	route, warnings := s.edgeManifestRouteForFile(ctx, rules, dep.RouteProfile, dep.ResourceFailover, file, obj, status, routingPolicy)
	resp := routeExplainResponse{
		SiteID:           site.ID,
		DeploymentID:     dep.ID,
		Path:             reqPath,
		MatchedPath:      edgeRoutePathForFile(file.Path),
		MatchType:        matchType,
		RouteProfile:     dep.RouteProfile,
		DeploymentTarget: dep.DeploymentTarget,
		RoutingPolicy:    routingPolicyName,
		ResourceFailover: dep.ResourceFailover,
		Route:            route,
		Warnings:         warnings,
	}
	if status != http.StatusOK || siteDeliveryMode(rules, file.Path) != "redirect" {
		return resp, nil
	}
	if routingPolicy != nil {
		decisionReq := routeExplainDecisionRequest(ctx, reqPath, opts)
		resp.RegionGroup = requestRegionGroup(*routingPolicy, decisionReq)
		resp.HashKey = routingHashKey(*routingPolicy, decisionReq)
		evaluations, _ := s.routingPolicyCandidateEvaluations(ctx, *routingPolicy, obj)
		selected, reason, ok := selectRoutingCandidateForRequest(*routingPolicy, readyCandidatesFromEvaluations(evaluations), decisionReq)
		if ok {
			markSelectedEvaluation(evaluations, selected.Target)
			resp.Selection = routeExplainSelectionFromCandidate(selected, reason)
		} else {
			resp.Selection = &routeExplainSelection{Reason: reason}
		}
		resp.Candidates = evaluations
		return resp, nil
	}
	if dep.ResourceFailover {
		evaluations, _ := s.resourceFailoverCandidateEvaluations(ctx, dep.RouteProfile, obj)
		if selected, ok := firstReadyEvaluation(evaluations); ok {
			markSelectedEvaluation(evaluations, selected.Target)
			resp.Selection = routeExplainSelectionFromCandidate(selected, "failover_order")
		} else {
			resp.Selection = &routeExplainSelection{Reason: "no_ready_candidates"}
		}
		resp.Candidates = evaluations
	}
	return resp, nil
}

func (s *Server) siteDeploymentFileForRouteExplain(ctx context.Context, dep *model.SiteDeployment, rules siteRules, reqPath string) (model.SiteDeploymentFile, *model.Object, string, int, error) {
	files, err := s.db.ListSiteDeploymentFiles(ctx, dep.ID)
	if err != nil {
		return model.SiteDeploymentFile{}, nil, "", 0, err
	}
	byPath := map[string]model.SiteDeploymentFile{}
	for _, file := range files {
		byPath[file.Path] = file
	}
	for _, candidate := range sitePathCandidates(reqPath, firstNonEmpty(rules.Mode, "standard")) {
		if file, ok := byPath[candidate]; ok {
			obj, err := s.db.GetObject(ctx, file.ObjectID)
			if err != nil {
				return model.SiteDeploymentFile{}, nil, "", 0, err
			}
			return file, obj, "file", http.StatusOK, nil
		}
	}
	if rules.Mode == "spa" {
		if file, ok := byPath["index.html"]; ok {
			obj, err := s.db.GetObject(ctx, file.ObjectID)
			if err != nil {
				return model.SiteDeploymentFile{}, nil, "", 0, err
			}
			return file, obj, "spa_fallback", http.StatusOK, nil
		}
	}
	notFoundPath := firstNonEmpty(rules.NotFound, "404.html")
	if file, ok := byPath[notFoundPath]; ok {
		obj, err := s.db.GetObject(ctx, file.ObjectID)
		if err != nil {
			return model.SiteDeploymentFile{}, nil, "", 0, err
		}
		return file, obj, "not_found", http.StatusNotFound, nil
	}
	return model.SiteDeploymentFile{}, nil, "", 0, fmt.Errorf("no deployment file matches %s", reqPath)
}

func routeExplainDecisionRequest(ctx context.Context, reqPath string, opts routeExplainOptions) *http.Request {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://route-explain.local"+reqPath, nil)
	if country := strings.ToUpper(strings.TrimSpace(opts.Country)); country != "" {
		req.Header.Set("CF-IPCountry", country)
	}
	if clientIP := strings.TrimSpace(opts.ClientIP); clientIP != "" {
		req.Header.Set("CF-Connecting-IP", clientIP)
		req.RemoteAddr = clientIP
	}
	return req
}

func routeExplainSelectionFromCandidate(candidate edgeRouteCandidate, reason string) *routeExplainSelection {
	return &routeExplainSelection{
		Target:      candidate.Target,
		TargetType:  candidate.TargetType,
		Type:        candidate.Type,
		RegionGroup: candidate.RegionGroup,
		URL:         candidate.URL,
		Reason:      reason,
	}
}

func readyCandidatesFromEvaluations(evaluations []edgeRouteCandidateEvaluation) []edgeRouteCandidate {
	out := make([]edgeRouteCandidate, 0, len(evaluations))
	for _, evaluation := range evaluations {
		if candidate, ok := evaluation.readyCandidate(); ok {
			out = append(out, candidate)
		}
	}
	return out
}

func firstReadyEvaluation(evaluations []edgeRouteCandidateEvaluation) (edgeRouteCandidate, bool) {
	for _, evaluation := range evaluations {
		if candidate, ok := evaluation.readyCandidate(); ok {
			return candidate, true
		}
	}
	return edgeRouteCandidate{}, false
}

func markSelectedEvaluation(evaluations []edgeRouteCandidateEvaluation, target string) {
	for i := range evaluations {
		if evaluations[i].Target == target {
			evaluations[i].Selected = true
			return
		}
	}
}

func (s *Server) edgeManifestRouteForFile(ctx context.Context, rules siteRules, profileName string, resourceFailover bool, file model.SiteDeploymentFile, obj *model.Object, status int, routingPolicy *config.RoutingPolicy) (edgeManifestRoute, []string) {
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
	ipfsPins, gatewayFallbacks, warnings := s.edgeManifestIPFSForObject(ctx, obj)
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
		IPFS:               ipfsPins,
		GatewayFallbacks:   gatewayFallbacks,
		Headers:            headers,
	}
	if status != http.StatusOK || delivery != "redirect" {
		return route, warnings
	}
	if routingPolicy != nil {
		candidates, candidateWarnings := s.routingPolicyCandidates(ctx, *routingPolicy, obj)
		warnings = append(warnings, candidateWarnings...)
		if len(candidates) >= 2 {
			route.Type = "smart"
			route.Status = http.StatusFound
			route.Location = candidates[0].URL
			route.CacheControl = "no-store"
			route.RoutingPolicy = edgeRoutingPolicySnapshot(*routingPolicy)
			route.Candidates = candidates
			return route, warnings
		}
		if len(candidates) == 1 {
			route = edgeManifestRouteForSingleRoutingCandidate(route, candidates[0], cacheControl, routingPolicy)
			warnings = append(warnings, fmt.Sprintf("routing_policy %q has 1 ready candidate for %s; route will use degraded single-source delivery", routingPolicy.Name, file.Path))
			return route, warnings
		}
		warnings = append(warnings, fmt.Sprintf("routing_policy %q has %d ready candidates for %s; route will use single-source delivery", routingPolicy.Name, len(candidates), file.Path))
	}
	if resourceFailover && routingPolicy == nil {
		candidates, candidateWarnings := s.resourceFailoverCandidates(ctx, profileName, obj)
		warnings = append(warnings, candidateWarnings...)
		route.Type = "failover"
		route.Delivery = "failover"
		route.CacheControl = "no-store"
		if len(candidates) > 0 {
			route.Status = http.StatusOK
			route.Location = candidates[0].URL
			route.CacheControl = cacheControl
			route.Candidates = candidates
			return route, warnings
		}
		route.Status = http.StatusBadGateway
		warnings = append(warnings, fmt.Sprintf("resource_failover has no ready candidates for %s; route will return edge error", file.Path))
		return route, warnings
	}
	target, err := s.objectPrimaryRedirectURL(ctx, obj)
	if err != nil || target == "" {
		if err == nil {
			err = fmt.Errorf("direct storage URL is empty")
		}
		warnings = append(warnings, fmt.Sprintf("primary redirect URL unavailable for %s: %v; route will use origin delivery", file.Path, err))
		return route, warnings
	}
	if len(gatewayFallbacks) > 0 && isIPFSGatewayTarget(target, gatewayFallbacks) {
		route.Type = "ipfs"
		route.Status = http.StatusOK
		route.Location = target
		route.CacheControl = cacheControl
		return route, warnings
	}
	route.Type = "redirect"
	route.Status = http.StatusFound
	route.Location = target
	route.CacheControl = "no-store"
	return route, warnings
}

func (s *Server) edgeManifestIPFSForObject(ctx context.Context, obj *model.Object) ([]edgeManifestIPFS, []string, []string) {
	if obj == nil || obj.ID == 0 {
		return nil, nil, nil
	}
	pins, err := s.db.IPFSPins(ctx, obj.ID)
	if err != nil {
		return nil, nil, []string{fmt.Sprintf("IPFS metadata unavailable for object %d: %v", obj.ID, err)}
	}
	if len(pins) == 0 {
		return nil, nil, nil
	}
	seenGateways := map[string]bool{}
	out := make([]edgeManifestIPFS, 0, len(pins))
	gateways := make([]string, 0, len(pins))
	for _, pin := range pins {
		if pin.CID == "" {
			continue
		}
		out = append(out, edgeManifestIPFS{
			Target:        pin.Target,
			Provider:      pin.Provider,
			CID:           pin.CID,
			GatewayURL:    pin.GatewayURL,
			PinStatus:     pin.PinStatus,
			ProviderPinID: pin.ProviderPinID,
		})
		if pin.GatewayURL != "" && !seenGateways[pin.GatewayURL] {
			seenGateways[pin.GatewayURL] = true
			gateways = append(gateways, pin.GatewayURL)
		}
	}
	if len(out) == 0 {
		return nil, nil, nil
	}
	return out, gateways, nil
}

func isIPFSGatewayTarget(target string, gatewayFallbacks []string) bool {
	if target == "" {
		return false
	}
	for _, fallback := range gatewayFallbacks {
		if target == fallback {
			return true
		}
	}
	_, ok := storage.IPFSCIDFromLocator(target)
	return ok
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
