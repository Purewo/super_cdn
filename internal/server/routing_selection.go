package server

import (
	"context"
	"fmt"
	"hash/fnv"
	"net/http"
	"sort"
	"strings"

	"supercdn/internal/config"
	"supercdn/internal/model"
	"supercdn/internal/storage"
)

func edgeRoutingPolicySnapshot(policy config.RoutingPolicy) *edgeRoutingPolicy {
	out := edgeRoutingPolicy{
		Name:               policy.Name,
		Mode:               policy.Mode,
		DefaultRegionGroup: policy.DefaultRegionGroup,
		Sources:            make([]edgeRoutingPolicySource, 0, len(policy.Sources)),
	}
	for _, source := range policy.Sources {
		out.Sources = append(out.Sources, edgeRoutingPolicySource{
			Target:       source.Target,
			RegionGroup:  source.RegionGroup,
			Weight:       source.Weight,
			Priority:     source.Priority,
			FallbackOnly: source.FallbackOnly,
		})
	}
	return &out
}

func (s *Server) routingPolicyCandidates(ctx context.Context, policy config.RoutingPolicy, obj *model.Object) ([]edgeRouteCandidate, []string) {
	evaluations, warnings := s.routingPolicyCandidateEvaluations(ctx, policy, obj)
	return readyCandidatesFromEvaluations(evaluations), warnings
}

func (s *Server) routingPolicyCandidateEvaluations(ctx context.Context, policy config.RoutingPolicy, obj *model.Object) ([]edgeRouteCandidateEvaluation, []string) {
	if obj == nil || obj.ID == 0 {
		return nil, []string{"routing policy candidates unavailable: object is missing"}
	}
	replicas, err := s.hydrateReplicasIPFS(ctx, obj.ID)
	if err != nil {
		return nil, []string{fmt.Sprintf("routing policy %q replicas unavailable for object %d: %v", policy.Name, obj.ID, err)}
	}
	byTarget := map[string]model.Replica{}
	for _, replica := range replicas {
		if replica.Target != "" {
			byTarget[replica.Target] = replica
		}
	}
	var (
		out      []edgeRouteCandidateEvaluation
		warnings []string
	)
	for _, source := range policy.Sources {
		item := edgeRouteCandidateEvaluation{
			Target:       source.Target,
			RegionGroup:  firstNonEmpty(source.RegionGroup, policy.DefaultRegionGroup, "overseas"),
			Weight:       positiveWeight(source.Weight),
			Priority:     source.Priority,
			FallbackOnly: source.FallbackOnly,
			Status:       "skipped",
		}
		store, ok := s.stores.Get(source.Target)
		if !ok {
			item.Reason = "storage target is not configured"
			warnings = append(warnings, fmt.Sprintf("routing_policy %q source %q is not configured", policy.Name, source.Target))
			out = append(out, item)
			continue
		}
		item.TargetType = store.Type()
		if reason, unhealthy := s.recentResourceLibraryHealthFailure(ctx, source.Target); unhealthy {
			item.Reason = "skipped by health: " + reason
			warnings = append(warnings, fmt.Sprintf("routing_policy %q source %q skipped by health: %s", policy.Name, source.Target, reason))
			out = append(out, item)
			continue
		}
		replica, ok := byTarget[source.Target]
		if !ok {
			item.Reason = fmt.Sprintf("has no replica for object %d", obj.ID)
			warnings = append(warnings, fmt.Sprintf("routing_policy %q source %q has no replica for object %d", policy.Name, source.Target, obj.ID))
			out = append(out, item)
			continue
		}
		item.ReplicaStatus = firstNonEmpty(replica.Status, "unknown")
		if replica.Status != model.ReplicaReady {
			item.Reason = "replica is " + item.ReplicaStatus
			warnings = append(warnings, fmt.Sprintf("routing_policy %q source %q replica is %s", policy.Name, source.Target, item.ReplicaStatus))
			out = append(out, item)
			continue
		}
		targetURL, ipfs := s.routingCandidateURL(ctx, obj, replica, store)
		if targetURL == "" {
			item.Reason = fmt.Sprintf("has no direct URL for object %d", obj.ID)
			warnings = append(warnings, fmt.Sprintf("routing_policy %q source %q has no direct URL for object %d", policy.Name, source.Target, obj.ID))
			out = append(out, item)
			continue
		}
		candidateType := "redirect"
		if ipfs != nil {
			candidateType = "ipfs"
		}
		item.Type = candidateType
		item.URL = targetURL
		item.Status = model.ReplicaReady
		item.Reason = "ready"
		item.IPFS = ipfs
		out = append(out, item)
	}
	return out, warnings
}

func (s *Server) resourceFailoverCandidates(ctx context.Context, profileName string, obj *model.Object) ([]edgeRouteCandidate, []string) {
	evaluations, warnings := s.resourceFailoverCandidateEvaluations(ctx, profileName, obj)
	return readyCandidatesFromEvaluations(evaluations), warnings
}

func (s *Server) resourceFailoverCandidateEvaluations(ctx context.Context, profileName string, obj *model.Object) ([]edgeRouteCandidateEvaluation, []string) {
	if obj == nil || obj.ID == 0 {
		return nil, []string{"resource_failover candidates unavailable: object is missing"}
	}
	profile, ok := s.cfg.Profile(profileName)
	if !ok {
		return nil, []string{fmt.Sprintf("resource_failover route_profile %q is not configured", profileName)}
	}
	targets := routeProfileFailoverTargets(profile)
	replicas, err := s.hydrateReplicasIPFS(ctx, obj.ID)
	if err != nil {
		return nil, []string{fmt.Sprintf("resource_failover replicas unavailable for object %d: %v", obj.ID, err)}
	}
	byTarget := map[string]model.Replica{}
	for _, replica := range replicas {
		if replica.Target != "" {
			byTarget[replica.Target] = replica
		}
	}
	var (
		out      []edgeRouteCandidateEvaluation
		warnings []string
	)
	for i, target := range targets {
		item := edgeRouteCandidateEvaluation{
			Target:      target,
			RegionGroup: "failover",
			Weight:      1,
			Priority:    i,
			Status:      "skipped",
		}
		store, ok := s.stores.Get(target)
		if !ok {
			item.Reason = "storage target is not configured"
			warnings = append(warnings, fmt.Sprintf("resource_failover source %q is not configured", target))
			out = append(out, item)
			continue
		}
		item.TargetType = store.Type()
		if reason, unhealthy := s.recentResourceLibraryHealthFailure(ctx, target); unhealthy {
			item.Reason = "skipped by health: " + reason
			warnings = append(warnings, fmt.Sprintf("resource_failover source %q skipped by health: %s", target, reason))
			out = append(out, item)
			continue
		}
		replica, ok := byTarget[target]
		if !ok {
			item.Reason = fmt.Sprintf("has no replica for object %d", obj.ID)
			warnings = append(warnings, fmt.Sprintf("resource_failover source %q has no replica for object %d", target, obj.ID))
			out = append(out, item)
			continue
		}
		item.ReplicaStatus = firstNonEmpty(replica.Status, "unknown")
		if replica.Status != model.ReplicaReady {
			item.Reason = "replica is " + item.ReplicaStatus
			warnings = append(warnings, fmt.Sprintf("resource_failover source %q replica is %s", target, item.ReplicaStatus))
			out = append(out, item)
			continue
		}
		targetURL, ipfs := s.routingCandidateURL(ctx, obj, replica, store)
		if targetURL == "" {
			item.Reason = fmt.Sprintf("has no direct URL for object %d", obj.ID)
			warnings = append(warnings, fmt.Sprintf("resource_failover source %q has no direct URL for object %d", target, obj.ID))
			out = append(out, item)
			continue
		}
		candidateType := "redirect"
		if ipfs != nil {
			candidateType = "ipfs"
		}
		item.Type = candidateType
		item.URL = targetURL
		item.Status = model.ReplicaReady
		item.Reason = "ready"
		item.IPFS = ipfs
		out = append(out, item)
	}
	return out, warnings
}

func (e edgeRouteCandidateEvaluation) readyCandidate() (edgeRouteCandidate, bool) {
	if e.Status != model.ReplicaReady || e.URL == "" {
		return edgeRouteCandidate{}, false
	}
	return edgeRouteCandidate{
		Target:       e.Target,
		TargetType:   e.TargetType,
		Type:         e.Type,
		RegionGroup:  e.RegionGroup,
		Weight:       positiveWeight(e.Weight),
		Priority:     e.Priority,
		FallbackOnly: e.FallbackOnly,
		URL:          e.URL,
		Status:       model.ReplicaReady,
		IPFS:         e.IPFS,
	}, true
}

func (s *Server) routingCandidateURL(ctx context.Context, obj *model.Object, replica model.Replica, store storage.Store) (string, *edgeManifestIPFS) {
	if store != nil {
		if stat, err := store.Stat(ctx, obj.Key); err == nil {
			if target := directLocatorURL(stat.Locator); target != "" {
				return target, nil
			}
		}
	}
	if target := directLocatorURL(replica.Locator); target != "" {
		return target, nil
	}
	if ipfs, ok := s.edgeManifestIPFSForReplica(ctx, replica, store); ok && ipfs.GatewayURL != "" {
		return ipfs.GatewayURL, &ipfs
	}
	if store != nil {
		if public := store.PublicURL(obj.Key); public != "" {
			if target := directLocatorURL(public); target != "" {
				return target, nil
			}
		}
	}
	return "", nil
}

func (s *Server) edgeManifestIPFSForReplica(ctx context.Context, replica model.Replica, store storage.Store) (edgeManifestIPFS, bool) {
	var pin model.IPFSPin
	if replica.IPFS != nil {
		pin = *replica.IPFS
	} else if saved, err := s.db.GetIPFSPin(ctx, replica.ObjectID, replica.Target); err == nil {
		pin = *saved
	} else if derived, ok := ipfsPinFromReplica(replica.ObjectID, replica.Target, store, replica.Locator); ok {
		pin = derived
	} else {
		return edgeManifestIPFS{}, false
	}
	if pin.CID == "" {
		return edgeManifestIPFS{}, false
	}
	gateway := firstNonEmpty(pin.GatewayURL, s.ipfsGatewayURLForReplica(ctx, replica, store))
	return edgeManifestIPFS{
		Target:        pin.Target,
		Provider:      pin.Provider,
		CID:           pin.CID,
		GatewayURL:    gateway,
		PinStatus:     pin.PinStatus,
		ProviderPinID: pin.ProviderPinID,
	}, true
}

func edgeManifestRouteForSingleRoutingCandidate(route edgeManifestRoute, candidate edgeRouteCandidate, cacheControl string, routingPolicy *config.RoutingPolicy) edgeManifestRoute {
	route.Location = candidate.URL
	route.RoutingPolicy = edgeRoutingPolicySnapshot(*routingPolicy)
	route.Candidates = []edgeRouteCandidate{candidate}
	if candidate.Type == "ipfs" {
		route.Type = "ipfs"
		route.Status = http.StatusOK
		route.CacheControl = cacheControl
		if candidate.IPFS != nil {
			route.IPFS = []edgeManifestIPFS{*candidate.IPFS}
			if candidate.IPFS.GatewayURL != "" {
				route.GatewayFallbacks = []string{candidate.IPFS.GatewayURL}
			}
		}
		return route
	}
	route.Type = "redirect"
	route.Status = http.StatusFound
	route.CacheControl = "no-store"
	return route
}

func positiveWeight(value int) int {
	if value <= 0 {
		return 1
	}
	return value
}

func selectRoutingCandidateForRequest(policy config.RoutingPolicy, candidates []edgeRouteCandidate, r *http.Request) (edgeRouteCandidate, string, bool) {
	candidates = readyRoutingCandidates(candidates)
	if len(candidates) == 0 {
		return edgeRouteCandidate{}, "no_ready_candidates", false
	}
	region := requestRegionGroup(policy, r)
	active := filterRoutingCandidates(candidates, func(candidate edgeRouteCandidate) bool {
		return !candidate.FallbackOnly
	})
	if len(active) == 0 {
		selected := weightedRoutingCandidate(candidates, routingHashKey(policy, r))
		return selected, "fallback_only", true
	}
	switch policy.Mode {
	case "global_accel":
		if regionCandidates := routingCandidatesForRegion(active, region); len(regionCandidates) > 0 {
			return firstPriorityRoutingCandidate(regionCandidates), "region:" + region, true
		}
		return firstPriorityRoutingCandidate(active), "region_fallback:" + region, true
	case "global_load_balance":
		if regionCandidates := routingCandidatesForRegion(active, region); len(regionCandidates) > 0 {
			return weightedRoutingCandidate(regionCandidates, routingHashKey(policy, r)), "region_balance:" + region, true
		}
		return weightedRoutingCandidate(active, routingHashKey(policy, r)), "region_balance_fallback:" + region, true
	default:
		return weightedRoutingCandidate(active, routingHashKey(policy, r)), "load_balance", true
	}
}

func readyRoutingCandidates(candidates []edgeRouteCandidate) []edgeRouteCandidate {
	return filterRoutingCandidates(candidates, func(candidate edgeRouteCandidate) bool {
		return candidate.URL != "" && candidate.Status == model.ReplicaReady
	})
}

func routingCandidatesForRegion(candidates []edgeRouteCandidate, region string) []edgeRouteCandidate {
	return filterRoutingCandidates(candidates, func(candidate edgeRouteCandidate) bool {
		return strings.EqualFold(candidate.RegionGroup, region)
	})
}

func filterRoutingCandidates(candidates []edgeRouteCandidate, keep func(edgeRouteCandidate) bool) []edgeRouteCandidate {
	out := make([]edgeRouteCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if keep(candidate) {
			out = append(out, candidate)
		}
	}
	return out
}

func firstPriorityRoutingCandidate(candidates []edgeRouteCandidate) edgeRouteCandidate {
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Priority != candidates[j].Priority {
			return candidates[i].Priority < candidates[j].Priority
		}
		if candidates[i].Weight != candidates[j].Weight {
			return candidates[i].Weight > candidates[j].Weight
		}
		return candidates[i].Target < candidates[j].Target
	})
	return candidates[0]
}

func weightedRoutingCandidate(candidates []edgeRouteCandidate, key string) edgeRouteCandidate {
	if len(candidates) == 1 {
		return candidates[0]
	}
	total := 0
	for _, candidate := range candidates {
		total += positiveWeight(candidate.Weight)
	}
	if total <= 0 {
		return candidates[0]
	}
	slot := int(hashString32(key) % uint32(total))
	for _, candidate := range candidates {
		slot -= positiveWeight(candidate.Weight)
		if slot < 0 {
			return candidate
		}
	}
	return candidates[len(candidates)-1]
}

func requestRegionGroup(policy config.RoutingPolicy, r *http.Request) string {
	if r != nil {
		if strings.EqualFold(strings.TrimSpace(r.Header.Get("CF-IPCountry")), "CN") {
			return "china"
		}
		if country := strings.TrimSpace(r.Header.Get("CF-IPCountry")); country != "" {
			return "overseas"
		}
	}
	return firstNonEmpty(policy.DefaultRegionGroup, "overseas")
}

func routingHashKey(policy config.RoutingPolicy, r *http.Request) string {
	var pathValue, client string
	if r != nil {
		if r.URL != nil {
			pathValue = r.URL.Path
		}
		client = firstNonEmpty(r.Header.Get("CF-Connecting-IP"), firstForwardedFor(r.Header.Get("X-Forwarded-For")), r.RemoteAddr)
	}
	return policy.Name + "|" + pathValue + "|" + client
}

func firstForwardedFor(value string) string {
	first, _, _ := strings.Cut(value, ",")
	return strings.TrimSpace(first)
}

func hashString32(value string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(value))
	return h.Sum32()
}
