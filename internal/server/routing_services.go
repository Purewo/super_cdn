package server

import (
	"context"
	"fmt"
	"strings"

	"supercdn/internal/config"
	"supercdn/internal/model"
	"supercdn/internal/storage"
)

func (s *Server) routingPolicyStatusView(ctx context.Context, policy config.RoutingPolicy) routingPolicyStatusView {
	view := routingPolicyStatusView{
		Name:               policy.Name,
		Mode:               policy.Mode,
		DefaultRegionGroup: policy.DefaultRegionGroup,
		SourceCount:        len(policy.Sources),
	}
	if len(policy.Sources) < 2 {
		view.Errors = append(view.Errors, "routing policy requires at least two sources")
	}
	for _, source := range policy.Sources {
		item := routingPolicySourceStatusView{
			Target:       source.Target,
			RegionGroup:  source.RegionGroup,
			Weight:       source.Weight,
			Priority:     source.Priority,
			FallbackOnly: source.FallbackOnly,
			Status:       "configured",
		}
		if store, ok := s.stores.Get(source.Target); ok {
			item.TargetType = store.Type()
		} else {
			item.Status = "missing"
			item.Error = "storage target is not configured"
			view.Errors = append(view.Errors, source.Target+": "+item.Error)
		}
		if health, ok := s.routingPolicySourceHealth(ctx, source.Target); ok {
			item.Health = &health
			if health.Status != storage.HealthStatusOK {
				item.Status = health.Status
				item.Error = firstNonEmpty(health.LastError, health.Status)
			}
		}
		view.Sources = append(view.Sources, item)
	}
	return view
}

func (s *Server) routingPolicySourceHealth(ctx context.Context, target string) (model.ResourceLibraryHealth, bool) {
	config, ok := s.resourceLibraryConfig(target)
	if !ok || len(config.Bindings) == 0 {
		return model.ResourceLibraryHealth{}, false
	}
	health, err := s.db.GetResourceLibraryHealth(ctx, target, bindingConfigName(config.Bindings[0], 0))
	if err != nil || health == nil {
		return model.ResourceLibraryHealth{}, false
	}
	return *health, true
}

func (s *Server) routingPolicyForProfile(name, profileName string, profile config.RouteProfile) (config.RoutingPolicy, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return config.RoutingPolicy{}, fmt.Errorf("routing_policy is required")
	}
	policy, ok := s.cfg.RoutingPolicy(name)
	if !ok {
		return config.RoutingPolicy{}, fmt.Errorf("unknown routing_policy %q", name)
	}
	if len(policy.Sources) < 2 {
		return config.RoutingPolicy{}, fmt.Errorf("routing_policy %q requires at least two sources", name)
	}
	allowed := map[string]bool{}
	if profile.Primary != "" {
		allowed[profile.Primary] = true
	}
	for _, target := range profile.Backups {
		if target != "" {
			allowed[target] = true
		}
	}
	for _, source := range policy.Sources {
		if !allowed[source.Target] {
			return config.RoutingPolicy{}, fmt.Errorf("routing_policy %q source %q is not included in route_profile %q", name, source.Target, profileName)
		}
	}
	return policy, nil
}

func (s *Server) preflightProfile(ctx context.Context, profileName string, profile config.RouteProfile, req preflightRequest) (map[string]any, error) {
	if req.BatchFileCount <= 0 {
		req.BatchFileCount = 1
	}
	if req.LargestFileSize <= 0 {
		req.LargestFileSize = req.TotalSize
	}
	if req.TotalSize <= 0 {
		req.TotalSize = req.LargestFileSize
	}
	if req.TotalSize < 0 || req.LargestFileSize < 0 {
		return nil, fmt.Errorf("upload sizes must be non-negative")
	}
	if !s.overclockMode() && s.cfg.Limits.MaxUploadBytes > 0 && req.TotalSize > s.cfg.Limits.MaxUploadBytes {
		return nil, fmt.Errorf("server max_upload_bytes is %d bytes, upload total got %d bytes", s.cfg.Limits.MaxUploadBytes, req.TotalSize)
	}
	quota, err := s.checkUserUploadQuota(ctx, req.TotalSize)
	if err != nil {
		return nil, err
	}
	primary, ok := s.stores.Get(profile.Primary)
	if !ok {
		return nil, fmt.Errorf("primary storage %q is not configured", profile.Primary)
	}
	if !s.overclockMode() {
		if err := s.checkRecentResourceLibraryHealth(ctx, profile.Primary); err != nil {
			return nil, err
		}
	}
	result := s.withOverclockWarning(map[string]any{
		"ok":                true,
		"route_profile":     profileName,
		"primary_target":    profile.Primary,
		"total_size":        req.TotalSize,
		"largest_file_size": req.LargestFileSize,
		"batch_file_count":  req.BatchFileCount,
	})
	if quota != nil {
		result["user_quota"] = quotaView(quota)
	}
	if s.overclockMode() {
		result["limits_ignored"] = []string{
			"max_upload_bytes",
			"default_max_site_files",
			"deployment_file_count",
			"resource_health",
			"resource_library_capacity",
			"resource_library_file_size",
			"resource_library_batch_files",
			"resource_library_daily_upload",
			"asset_bucket_capacity",
			"asset_bucket_file_size",
			"asset_bucket_allowed_types",
			"transfer_slots",
		}
	}
	if preflight, ok := primary.(storage.PreflightStore); ok {
		preflightResult, err := preflight.PreflightPut(ctx, storage.PreflightOptions{
			TotalSize:       req.TotalSize,
			LargestFileSize: req.LargestFileSize,
			BatchFileCount:  req.BatchFileCount,
			IgnoreLimits:    s.overclockMode(),
		})
		if err != nil {
			return nil, err
		}
		result["primary"] = preflightResult
	} else {
		result["primary"] = storage.PreflightResult{
			Target:        primary.Name(),
			TargetType:    primary.Type(),
			OverclockMode: s.overclockMode(),
		}
	}
	return result, nil
}

func (s *Server) checkSiteFileCount(count int) error {
	if count <= 0 || s.cfg.Limits.DefaultMaxSiteFiles <= 0 || s.overclockMode() {
		return nil
	}
	if count > s.cfg.Limits.DefaultMaxSiteFiles {
		return fmt.Errorf("site deploy allows at most %d files in the first version, got %d; package larger sites before uploading", s.cfg.Limits.DefaultMaxSiteFiles, count)
	}
	return nil
}
