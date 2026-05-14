package server

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"supercdn/internal/cloudflare"
	"supercdn/internal/config"
	"supercdn/internal/db"
	"supercdn/internal/model"
)

func (s *Server) siteDomainsFromRequest(siteID string, requested []string, defaultID string, randomDefault, skipDefault bool, allocateDefault *bool) ([]string, error) {
	domains := make([]string, 0, len(requested)+1)
	seen := map[string]bool{}
	add := func(host string) error {
		host = cleanHost(host)
		if host == "" {
			return nil
		}
		if !validDomainName(host) {
			return fmt.Errorf("invalid domain %q", host)
		}
		if !seen[host] {
			seen[host] = true
			domains = append(domains, host)
		}
		return nil
	}
	shouldAllocate := s.cfg.Cloudflare.SiteDomainSuffix != "" && !skipDefault
	if allocateDefault != nil {
		shouldAllocate = *allocateDefault
	}
	if shouldAllocate {
		host, err := s.defaultSiteDomain(siteID, defaultID, randomDefault)
		if err != nil {
			return nil, err
		}
		if err := add(host); err != nil {
			return nil, err
		}
	}
	for _, host := range requested {
		if err := add(host); err != nil {
			return nil, err
		}
	}
	return domains, nil
}

func (s *Server) defaultSiteDomain(siteID, requestedID string, randomDefault bool) (string, error) {
	suffix := cleanHost(s.cfg.Cloudflare.SiteDomainSuffix)
	if suffix == "" {
		return "", fmt.Errorf("cloudflare.site_domain_suffix is not configured")
	}
	label := cleanDomainLabel(requestedID)
	if label == "" {
		label = cleanDomainLabel(siteID)
	}
	if randomDefault {
		randomPart, err := randomDomainPart()
		if err != nil {
			return "", err
		}
		if label == "" {
			label = randomPart
		} else {
			maxPrefix := 63 - len(randomPart) - 1
			if len(label) > maxPrefix {
				label = strings.Trim(label[:maxPrefix], "-")
			}
			if label == "" {
				label = randomPart
			} else {
				label = label + "-" + randomPart
			}
		}
	}
	if label == "" {
		return "", fmt.Errorf("default domain id is required")
	}
	return label + "." + suffix, nil
}

func (s *Server) defaultCloudflareStaticDomain(siteID string) (string, error) {
	root := cleanHost(s.cfg.Cloudflare.RootDomain)
	if root == "" {
		return s.defaultSiteDomain(siteID, "", false)
	}
	label := cleanDomainLabel(siteID)
	if label == "" {
		return "", fmt.Errorf("default domain id is required")
	}
	return label + "." + root, nil
}

func (s *Server) domainStatus(ctx context.Context, host string) domainStatusResponse {
	account, library, accountOK := s.cloudflareAccountForHost(host, "", "")
	cf := s.cloudflareClientForAccount(account)
	resp := domainStatusResponse{
		Host:                 host,
		CloudflareAccount:    account.Name,
		CloudflareLibrary:    library.Name,
		CloudflareConfigured: cf.Configured(),
		RootDomain:           account.RootDomain,
		SiteDomainSuffix:     account.SiteDomainSuffix,
		InManagedZone:        accountOK && accountInManagedCloudflareZone(account, host),
	}
	if site, err := s.db.SiteByHost(ctx, host); err == nil {
		resp.Bound = true
		resp.SiteID = site.ID
	} else if err != nil && !db.IsNotFound(err) {
		resp.Errors = append(resp.Errors, err.Error())
	}
	if !accountOK {
		resp.Errors = append(resp.Errors, "no matching cloudflare account for host")
		return resp
	}
	if !resp.CloudflareConfigured {
		resp.Errors = append(resp.Errors, "cloudflare zone_id/api_token not configured")
		return resp
	}
	exact, err := cf.ListDNSRecords(ctx, host)
	if err != nil {
		resp.Errors = append(resp.Errors, err.Error())
	} else {
		resp.ExactRecords = exact
	}
	for _, wildcard := range managedWildcardCandidates(account, host) {
		records, err := cf.ListDNSRecords(ctx, wildcard)
		if err != nil {
			resp.Errors = append(resp.Errors, err.Error())
			continue
		}
		resp.WildcardRecords = append(resp.WildcardRecords, records...)
	}
	return resp
}

func (s *Server) syncSiteWorkerRoutes(ctx context.Context, site *model.Site, req syncWorkerRoutesRequest) (syncWorkerRoutesResponse, error) {
	domains, err := s.siteBoundDomains(site, req.Domains)
	if err != nil {
		return syncWorkerRoutesResponse{}, err
	}
	account, library, err := s.cloudflareAccountForDomains(domains, req.CloudflareAccount, req.CloudflareLibrary)
	if err != nil {
		return syncWorkerRoutesResponse{}, err
	}
	script := firstNonEmpty(strings.TrimSpace(req.Script), account.WorkerScript, "supercdn-edge")
	patterns := make([]string, 0, len(domains))
	for _, domain := range domains {
		patterns = append(patterns, domain+"/*")
	}
	resp := syncWorkerRoutesResponse{
		SiteID:            site.ID,
		CloudflareAccount: account.Name,
		CloudflareLibrary: library.Name,
		Script:            script,
		Domains:           domains,
		Patterns:          patterns,
		DryRun:            req.DryRun,
		Force:             req.Force,
		Status:            "planned",
		Warnings:          []string{"Cloudflare Worker routes only run for proxied DNS records; DNS-only records bypass the Worker."},
	}
	cf := s.cloudflareClientForAccount(account)
	if !cf.Configured() {
		if req.DryRun {
			for _, pattern := range patterns {
				resp.Routes = append(resp.Routes, cloudflare.WorkerRouteSyncResult{
					Pattern: pattern,
					Script:  script,
					Action:  "create",
					DryRun:  true,
				})
			}
			resp.Warnings = append(resp.Warnings, "cloudflare zone_id/api_token not configured; returning local route plan only")
			return resp, nil
		}
		resp.Status = "skipped"
		resp.Errors = append(resp.Errors, "cloudflare zone_id/api_token not configured")
		return resp, nil
	}
	results, err := cf.SyncWorkerRoutes(ctx, patterns, script, cloudflare.SyncWorkerRouteOptions{DryRun: req.DryRun, Force: req.Force})
	if err != nil {
		return syncWorkerRoutesResponse{}, err
	}
	resp.Routes = results
	resp.Status = "ok"
	for _, result := range results {
		if result.Error != "" {
			resp.Status = "partial"
			resp.Errors = append(resp.Errors, result.Pattern+": "+result.Error)
		}
	}
	if req.DryRun && resp.Status == "ok" {
		resp.Status = "planned"
	}
	return resp, nil
}

func (s *Server) syncSiteDNS(ctx context.Context, site *model.Site, req syncSiteDNSRequest) (syncSiteDNSResponse, error) {
	domains, err := s.siteBoundDomains(site, req.Domains)
	if err != nil {
		return syncSiteDNSResponse{}, err
	}
	account, library, err := s.cloudflareAccountForDomains(domains, req.CloudflareAccount, req.CloudflareLibrary)
	if err != nil {
		return syncSiteDNSResponse{}, err
	}
	target := cleanDNSTarget(firstNonEmpty(req.Target, s.defaultSiteDNSTarget(account)))
	if target == "" {
		return syncSiteDNSResponse{}, fmt.Errorf("dns target is required; set -target or cloudflare.site_dns_target")
	}
	recordType := strings.ToUpper(strings.TrimSpace(req.Type))
	if recordType == "" {
		recordType = inferDNSRecordType(target)
	}
	if err := validateDNSRecordTarget(recordType, target); err != nil {
		return syncSiteDNSResponse{}, err
	}
	proxied := true
	if req.Proxied != nil {
		proxied = *req.Proxied
	}
	ttl := req.TTL
	if ttl <= 0 {
		ttl = 1
	}
	records := make([]cloudflare.DNSRecord, 0, len(domains))
	for _, domain := range domains {
		if recordType == "CNAME" && strings.EqualFold(domain, target) {
			return syncSiteDNSResponse{}, fmt.Errorf("cannot create CNAME %q pointing to itself", domain)
		}
		records = append(records, cloudflare.DNSRecord{
			Type:    recordType,
			Name:    domain,
			Content: target,
			Proxied: proxied,
			TTL:     ttl,
		})
	}
	resp := syncSiteDNSResponse{
		SiteID:            site.ID,
		CloudflareAccount: account.Name,
		CloudflareLibrary: library.Name,
		Domains:           domains,
		Type:              recordType,
		Target:            target,
		Proxied:           proxied,
		TTL:               ttl,
		DryRun:            req.DryRun,
		Force:             req.Force,
		Status:            "planned",
		Warnings:          []string{"Cloudflare Worker routes only run for proxied DNS records; DNS-only records bypass the Worker."},
	}
	cf := s.cloudflareClientForAccount(account)
	if !cf.Configured() {
		if req.DryRun {
			for _, record := range records {
				resp.Records = append(resp.Records, cloudflare.DNSRecordSyncResult{
					Name:    record.Name,
					Type:    record.Type,
					Content: record.Content,
					Proxied: record.Proxied,
					TTL:     record.TTL,
					Action:  "create",
					DryRun:  true,
				})
			}
			resp.Warnings = append(resp.Warnings, "cloudflare zone_id/api_token not configured; returning local DNS plan only")
			return resp, nil
		}
		resp.Status = "skipped"
		resp.Errors = append(resp.Errors, "cloudflare zone_id/api_token not configured")
		return resp, nil
	}
	results, err := cf.SyncDNSRecords(ctx, records, cloudflare.SyncDNSRecordOptions{DryRun: req.DryRun, Force: req.Force})
	if err != nil {
		return syncSiteDNSResponse{}, err
	}
	resp.Records = results
	resp.Status = "ok"
	for _, result := range results {
		if result.Error != "" {
			resp.Status = "partial"
			resp.Errors = append(resp.Errors, result.Name+": "+result.Error)
		}
	}
	if req.DryRun && resp.Status == "ok" {
		resp.Status = "planned"
	}
	return resp, nil
}

func (s *Server) siteBoundDomains(site *model.Site, requested []string) ([]string, error) {
	if site == nil {
		return nil, fmt.Errorf("site is required")
	}
	allowed := map[string]bool{}
	for _, domain := range site.Domains {
		domain = cleanHost(domain)
		if domain != "" {
			allowed[domain] = true
		}
	}
	source := site.Domains
	if len(requested) > 0 {
		source = requested
	}
	domains := make([]string, 0, len(source))
	seen := map[string]bool{}
	for _, domain := range source {
		domain = cleanHost(domain)
		if domain == "" {
			continue
		}
		if !allowed[domain] {
			return nil, fmt.Errorf("domain %q is not bound to site %q", domain, site.ID)
		}
		if !seen[domain] {
			seen[domain] = true
			domains = append(domains, domain)
		}
	}
	if len(domains) == 0 {
		return nil, fmt.Errorf("site has no bound domains")
	}
	return domains, nil
}

func (s *Server) defaultSiteDNSTarget(account config.CloudflareAccountConfig) string {
	if target := cleanDNSTarget(account.SiteDNSTarget); target != "" {
		return target
	}
	if s.cfg.Server.PublicBaseURL != "" {
		if parsed, err := url.Parse(s.cfg.Server.PublicBaseURL); err == nil && parsed.Hostname() != "" {
			return cleanDNSTarget(parsed.Hostname())
		}
	}
	return cleanDNSTarget(account.RootDomain)
}
