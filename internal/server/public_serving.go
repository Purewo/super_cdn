package server

import (
	"context"
	"crypto/subtle"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"supercdn/internal/edgeheaders"
	"supercdn/internal/model"
	"supercdn/internal/storage"
)

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
	bucketConfig, err := s.db.GetAssetBucket(r.Context(), bucket)
	if err != nil {
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
	if target, policyName, reason, candidate, ok := s.bucketRoutingRedirect(r.Context(), r, bucketConfig, obj); ok {
		setDynamicRedirectNoStore(w.Header())
		w.Header().Set(edgeheaders.HeaderRedirect, edgeheaders.RedirectStorage)
		w.Header().Set(edgeheaders.HeaderRoutePolicy, policyName)
		w.Header().Set(edgeheaders.HeaderRouteTarget, candidate.Target)
		w.Header().Set(edgeheaders.HeaderRouteReason, reason)
		http.Redirect(w, r, target, http.StatusFound)
		return
	}
	s.streamObject(w, r, obj, http.StatusOK)
}

func (s *Server) bucketRoutingRedirect(ctx context.Context, r *http.Request, bucket *model.AssetBucket, obj *model.Object) (string, string, string, edgeRouteCandidate, bool) {
	if bucket == nil || obj == nil || strings.TrimSpace(bucket.RoutingPolicy) == "" {
		return "", "", "", edgeRouteCandidate{}, false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return "", "", "", edgeRouteCandidate{}, false
	}
	if r.Header.Get("Range") != "" {
		return "", "", "", edgeRouteCandidate{}, false
	}
	profile, ok := s.cfg.Profile(bucket.RouteProfile)
	if !ok {
		return "", "", "", edgeRouteCandidate{}, false
	}
	policy, err := s.routingPolicyForProfile(bucket.RoutingPolicy, bucket.RouteProfile, profile)
	if err != nil {
		s.logger.Warn("bucket routing policy unavailable", "bucket", bucket.Slug, "policy", bucket.RoutingPolicy, "error", err)
		return "", "", "", edgeRouteCandidate{}, false
	}
	candidates, warnings := s.routingPolicyCandidates(ctx, policy, obj)
	for _, warning := range warnings {
		s.logger.Warn("bucket routing candidate warning", "bucket", bucket.Slug, "object_id", obj.ID, "warning", warning)
	}
	if len(candidates) < 2 {
		return "", "", "", edgeRouteCandidate{}, false
	}
	selected, reason, ok := selectRoutingCandidateForRequest(policy, candidates, r)
	if !ok || selected.URL == "" {
		return "", "", "", edgeRouteCandidate{}, false
	}
	return selected.URL, policy.Name, reason, selected, true
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
	w.Header().Set(edgeheaders.HeaderRedirect, edgeheaders.RedirectStorage)
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
	if site.Status == model.SiteStatusOffline {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-SuperCDN-Site-Status", model.SiteStatusOffline)
		writeError(w, http.StatusGone, "site is offline")
		return
	}
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
		if target, policyName, reason, candidate, ok := s.siteRoutingRedirect(r.Context(), r, site, dep, obj); ok {
			setDynamicRedirectNoStore(w.Header())
			w.Header().Set(edgeheaders.HeaderRedirect, edgeheaders.RedirectStorage)
			w.Header().Set(edgeheaders.HeaderRoutePolicy, policyName)
			w.Header().Set(edgeheaders.HeaderRouteTarget, candidate.Target)
			w.Header().Set(edgeheaders.HeaderRouteReason, reason)
			http.Redirect(w, r, target, http.StatusFound)
			return
		}
		resourceFailover := dep != nil && dep.ResourceFailover
		target, err := s.objectPrimaryRedirectURL(r.Context(), obj)
		if resourceFailover && (err != nil || target == "") {
			target, err = s.objectRedirectURL(r.Context(), obj)
		}
		if err == nil && target != "" {
			setDynamicRedirectNoStore(w.Header())
			w.Header().Set(edgeheaders.HeaderRedirect, edgeheaders.RedirectStorage)
			http.Redirect(w, r, target, http.StatusFound)
			return
		}
		s.logger.Warn("site file storage redirect unavailable", "site", site.ID, "deployment", dep.ID, "path", objectPath, "object_id", obj.ID, "err", err)
		if resourceFailover {
			writeError(w, http.StatusBadGateway, "resource_failover has no ready storage target")
			return
		}
	}
	s.streamObjectNoRedirect(w, r, obj, status)
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

func (s *Server) siteRoutingRedirect(ctx context.Context, r *http.Request, site *model.Site, dep *model.SiteDeployment, obj *model.Object) (string, string, string, edgeRouteCandidate, bool) {
	if site == nil || dep == nil || obj == nil {
		return "", "", "", edgeRouteCandidate{}, false
	}
	policyName := strings.TrimSpace(firstNonEmpty(dep.RoutingPolicy, site.RoutingPolicy))
	if policyName == "" {
		return "", "", "", edgeRouteCandidate{}, false
	}
	profile, ok := s.cfg.Profile(dep.RouteProfile)
	if !ok {
		return "", "", "", edgeRouteCandidate{}, false
	}
	policy, err := s.routingPolicyForProfile(policyName, dep.RouteProfile, profile)
	if err != nil {
		s.logger.Warn("site routing policy unavailable", "site", site.ID, "deployment", dep.ID, "policy", policyName, "error", err)
		return "", "", "", edgeRouteCandidate{}, false
	}
	candidates, warnings := s.routingPolicyCandidates(ctx, policy, obj)
	for _, warning := range warnings {
		s.logger.Warn("site routing candidate warning", "site", site.ID, "deployment", dep.ID, "object_id", obj.ID, "warning", warning)
	}
	if len(candidates) == 0 {
		return "", "", "", edgeRouteCandidate{}, false
	}
	selected, reason, ok := selectRoutingCandidateForRequest(policy, candidates, r)
	if !ok || selected.URL == "" {
		return "", "", "", edgeRouteCandidate{}, false
	}
	return selected.URL, policy.Name, reason, selected, true
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
		if target := s.objectReplicaRedirectURL(r, obj, replica, store, statusOverride); target != "" {
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

func (s *Server) streamObjectNoRedirect(w http.ResponseWriter, r *http.Request, obj *model.Object, statusOverride int) {
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

func (s *Server) objectReplicaRedirectURL(r *http.Request, obj *model.Object, replica model.Replica, store storage.Store, statusOverride int) string {
	if statusOverride != http.StatusOK || r.Header.Get("Range") != "" || replica.Locator == "" {
		return ""
	}
	profile, ok := s.cfg.Profile(obj.RouteProfile)
	if !ok || !profile.AllowRedirect {
		return ""
	}
	if target := directLocatorURL(replica.Locator); target != "" {
		return target
	}
	return s.ipfsGatewayURLForReplica(r.Context(), replica, store)
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
		if target, err := s.objectReplicaDirectURL(ctx, obj, replica); err == nil && target != "" {
			return target, nil
		}
	}
	return "", fmt.Errorf("direct storage URL not available for object %d", obj.ID)
}

func (s *Server) objectPrimaryRedirectURL(ctx context.Context, obj *model.Object) (string, error) {
	if obj == nil || obj.ID == 0 {
		return "", fmt.Errorf("object is required")
	}
	primary := strings.TrimSpace(obj.PrimaryTarget)
	if primary == "" {
		return "", fmt.Errorf("primary storage target is empty for object %d", obj.ID)
	}
	replicas, err := s.db.Replicas(ctx, obj.ID)
	if err != nil {
		return "", err
	}
	for _, replica := range replicas {
		if replica.Target != primary {
			continue
		}
		if replica.Status != model.ReplicaReady {
			return "", fmt.Errorf("primary replica %q is %s for object %d", primary, firstNonEmpty(replica.Status, "unknown"), obj.ID)
		}
		if target, err := s.objectReplicaDirectURL(ctx, obj, replica); err == nil && target != "" {
			return target, nil
		} else if err != nil {
			return "", fmt.Errorf("primary replica %q direct URL unavailable for object %d: %w", primary, obj.ID, err)
		}
	}
	return "", fmt.Errorf("ready primary replica %q not found for object %d", primary, obj.ID)
}

func (s *Server) objectReplicaDirectURL(ctx context.Context, obj *model.Object, replica model.Replica) (string, error) {
	var lastErr error
	store, ok := s.stores.Get(replica.Target)
	if ok {
		stat, err := store.Stat(ctx, obj.Key)
		if err == nil {
			if target := directLocatorURL(stat.Locator); target != "" {
				return target, nil
			}
		} else {
			lastErr = err
		}
	}
	if target := directLocatorURL(replica.Locator); target != "" {
		return target, nil
	}
	if ok {
		if target := s.ipfsGatewayURLForReplica(ctx, replica, store); target != "" {
			return target, nil
		}
		if public := store.PublicURL(obj.Key); public != "" {
			if target := directLocatorURL(public); target != "" {
				return target, nil
			}
		}
	} else {
		lastErr = fmt.Errorf("storage target %q is not configured", replica.Target)
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("direct storage URL not available for object %d replica %q", obj.ID, replica.Target)
}
