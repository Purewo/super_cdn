package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"supercdn/internal/cloudflare"
	"supercdn/internal/config"
	"supercdn/internal/model"
)

func (s *Server) purgeSiteDeploymentCache(ctx context.Context, site *model.Site, dep *model.SiteDeployment, req purgeSiteCacheRequest) purgeSiteCacheResponse {
	account, library, accountErr := s.purgeCloudflareAccount(site, req)
	resp := purgeSiteCacheResponse{
		SiteID:            site.ID,
		DeploymentID:      dep.ID,
		Active:            dep.Active,
		CloudflareAccount: account.Name,
		CloudflareLibrary: library.Name,
		DryRun:            req.DryRun,
		Status:            "planned",
	}
	if accountErr != nil {
		resp.Status = "failed"
		resp.Errors = append(resp.Errors, accountErr.Error())
		return resp
	}
	urls, warnings, err := s.siteDeploymentPurgeURLs(ctx, site, dep)
	resp.URLs = urls
	resp.URLCount = len(urls)
	resp.Warnings = append(resp.Warnings, warnings...)
	if err != nil {
		resp.Status = "failed"
		resp.Errors = append(resp.Errors, err.Error())
		return resp
	}
	if req.DryRun {
		return resp
	}
	cf := s.cloudflareClientForAccount(account)
	if !cf.Configured() {
		resp.Status = "skipped"
		resp.Errors = append(resp.Errors, "cloudflare zone_id/api_token not configured")
		return resp
	}
	batches, err := cf.PurgeCacheBatches(ctx, urls)
	if err != nil {
		resp.Status = "failed"
		resp.Errors = append(resp.Errors, err.Error())
		return resp
	}
	resp.Batches = batches
	resp.Status = "ok"
	for _, batch := range batches {
		if batch.Error != "" {
			resp.Status = "partial"
			resp.Errors = append(resp.Errors, fmt.Sprintf("batch %d: %s", batch.Batch, batch.Error))
		}
	}
	return resp
}

func (s *Server) cloudflareAccountForCacheBase(baseURL, requestedAccount, requestedLibrary string) (config.CloudflareAccountConfig, config.CloudflareLibraryConfig, error) {
	if strings.TrimSpace(requestedAccount) != "" {
		account, ok := s.cfg.CloudflareAccountByName(requestedAccount)
		if !ok {
			return config.CloudflareAccountConfig{}, config.CloudflareLibraryConfig{}, fmt.Errorf("cloudflare account not found")
		}
		library, _ := s.cloudflareLibraryForAccount(account.Name, requestedLibrary)
		return account, library, nil
	}
	host := publicURLHost(baseURL)
	if host != "" {
		if account, library, ok := s.cloudflareAccountForHost(host, requestedAccount, requestedLibrary); ok {
			return account, library, nil
		}
	}
	if strings.TrimSpace(requestedLibrary) != "" {
		library, ok := s.cfg.CloudflareLibraryByName(requestedLibrary)
		if !ok {
			return config.CloudflareAccountConfig{}, config.CloudflareLibraryConfig{}, fmt.Errorf("cloudflare library not found")
		}
		for _, binding := range library.Bindings {
			if account, ok := s.cfg.CloudflareAccountByName(binding.Account); ok {
				return account, library, nil
			}
		}
		return config.CloudflareAccountConfig{}, config.CloudflareLibraryConfig{}, fmt.Errorf("cloudflare library has no account bindings")
	}
	account, ok := s.cfg.DefaultCloudflareAccount()
	if !ok {
		return config.CloudflareAccountConfig{}, config.CloudflareLibraryConfig{}, fmt.Errorf("cloudflare account is not configured")
	}
	library, _ := s.cloudflareLibraryForAccount(account.Name, "")
	return account, library, nil
}

func (s *Server) siteDeploymentPurgeURLs(ctx context.Context, site *model.Site, dep *model.SiteDeployment) ([]string, []string, error) {
	var warnings []string
	if site == nil || dep == nil {
		return nil, nil, fmt.Errorf("site and deployment are required")
	}
	if len(site.Domains) == 0 {
		return nil, nil, fmt.Errorf("site has no bound domains")
	}
	if !dep.Active {
		warnings = append(warnings, "deployment is not the active production deployment; site-domain URLs currently serve the active deployment")
	}
	filePaths, err := s.siteDeploymentFilePaths(ctx, dep)
	if err != nil {
		return nil, warnings, err
	}
	if len(filePaths) == 0 {
		return nil, warnings, fmt.Errorf("deployment has no files")
	}
	var urls []string
	for _, domain := range site.Domains {
		domain = cleanHost(domain)
		if domain == "" {
			continue
		}
		base := s.publicScheme() + "://" + domain
		for _, filePath := range filePaths {
			for _, purgePath := range sitePurgePathsForFile(filePath) {
				urls = append(urls, base+purgePath)
			}
		}
	}
	urls = uniqueStrings(urls)
	if len(urls) == 0 {
		return nil, warnings, fmt.Errorf("no purge URLs generated")
	}
	return urls, warnings, nil
}

func (s *Server) purgeCloudflareAccount(site *model.Site, req purgeSiteCacheRequest) (config.CloudflareAccountConfig, config.CloudflareLibraryConfig, error) {
	domains, err := s.siteBoundDomains(site, nil)
	if err != nil {
		return config.CloudflareAccountConfig{}, config.CloudflareLibraryConfig{}, err
	}
	return s.cloudflareAccountForDomains(domains, req.CloudflareAccount, req.CloudflareLibrary)
}

func (s *Server) cloudflareClient() *cloudflare.Client {
	account, _ := s.cfg.DefaultCloudflareAccount()
	return s.cloudflareClientForAccount(account)
}

func (s *Server) cloudflareClientForAccount(account config.CloudflareAccountConfig) *cloudflare.Client {
	return cloudflare.New(account.ToCloudflareConfig(), http.DefaultClient)
}

func (s *Server) cloudflareR2ClientForAccount(account config.CloudflareAccountConfig) *cloudflare.Client {
	return cloudflare.New(account.ToCloudflareR2Config(), http.DefaultClient)
}

func (s *Server) cloudflareStatusForAccount(ctx context.Context, account config.CloudflareAccountConfig) cloudflare.Status {
	status := s.cloudflareClientForAccount(account).Status(ctx)
	status.R2 = s.cloudflareR2ClientForAccount(account).StatusWithR2Checks(ctx, cloudflare.R2CheckOptions{
		Bucket:        account.R2.Bucket,
		PublicBaseURL: account.R2.PublicBaseURL,
	}).R2
	return status
}

func (s *Server) cloudflareAccountForDomains(domains []string, requestedAccount, requestedLibrary string) (config.CloudflareAccountConfig, config.CloudflareLibraryConfig, error) {
	var selected *config.CloudflareAccountConfig
	var selectedLibrary config.CloudflareLibraryConfig
	for _, domain := range domains {
		account, library, ok := s.cloudflareAccountForHost(domain, requestedAccount, requestedLibrary)
		if !ok {
			return config.CloudflareAccountConfig{}, config.CloudflareLibraryConfig{}, fmt.Errorf("no matching cloudflare account for domain %q", domain)
		}
		if selected == nil {
			accountCopy := account
			selected = &accountCopy
			selectedLibrary = library
			continue
		}
		if selected.Name != account.Name {
			return config.CloudflareAccountConfig{}, config.CloudflareLibraryConfig{}, fmt.Errorf("domains span multiple cloudflare accounts; run the sync per account or pass -domains for one account")
		}
	}
	if selected == nil {
		return config.CloudflareAccountConfig{}, config.CloudflareLibraryConfig{}, fmt.Errorf("no domains to match cloudflare account")
	}
	return *selected, selectedLibrary, nil
}

func (s *Server) cloudflareAccountForHost(host, requestedAccount, requestedLibrary string) (config.CloudflareAccountConfig, config.CloudflareLibraryConfig, bool) {
	host = cleanHost(host)
	if strings.TrimSpace(requestedAccount) != "" {
		account, ok := s.cfg.CloudflareAccountByName(requestedAccount)
		if !ok {
			return config.CloudflareAccountConfig{}, config.CloudflareLibraryConfig{}, false
		}
		library, _ := s.cloudflareLibraryForAccount(account.Name, requestedLibrary)
		return account, library, accountInManagedCloudflareZone(account, host)
	}
	accounts := s.cfg.CloudflareAccountsEffective()
	var library config.CloudflareLibraryConfig
	if requestedLibrary != "" {
		lib, ok := s.cfg.CloudflareLibraryByName(requestedLibrary)
		if !ok {
			return config.CloudflareAccountConfig{}, config.CloudflareLibraryConfig{}, false
		}
		library = lib
		accounts = make([]config.CloudflareAccountConfig, 0, len(lib.Bindings))
		for _, binding := range lib.Bindings {
			if account, ok := s.cfg.CloudflareAccountByName(binding.Account); ok {
				accounts = append(accounts, account)
			}
		}
	}
	account, ok := bestCloudflareAccountForHost(accounts, host)
	if !ok {
		if fallback, fallbackOK := s.cfg.DefaultCloudflareAccount(); fallbackOK && len(accounts) == 1 {
			account = fallback
			ok = accountInManagedCloudflareZone(account, host)
		}
	}
	if !ok {
		return config.CloudflareAccountConfig{}, library, false
	}
	if library.Name == "" {
		library, _ = s.cloudflareLibraryForAccount(account.Name, requestedLibrary)
	}
	return account, library, true
}

func (s *Server) cloudflareLibraryForAccount(accountName, requestedLibrary string) (config.CloudflareLibraryConfig, bool) {
	if requestedLibrary != "" {
		library, ok := s.cfg.CloudflareLibraryByName(requestedLibrary)
		return library, ok
	}
	for _, library := range s.cfg.CloudflareLibrariesEffective() {
		for _, binding := range library.Bindings {
			if binding.Account == accountName {
				return library, true
			}
		}
	}
	return config.CloudflareLibraryConfig{}, false
}

func (s *Server) inManagedCloudflareZone(host string) bool {
	account, ok := s.cfg.DefaultCloudflareAccount()
	return ok && accountInManagedCloudflareZone(account, host)
}

func (s *Server) managedWildcardCandidates(host string) []string {
	account, _ := s.cfg.DefaultCloudflareAccount()
	return managedWildcardCandidates(account, host)
}

func bestCloudflareAccountForHost(accounts []config.CloudflareAccountConfig, host string) (config.CloudflareAccountConfig, bool) {
	var best config.CloudflareAccountConfig
	bestLen := -1
	for _, account := range accounts {
		for _, suffix := range []string{cleanHost(account.SiteDomainSuffix), cleanHost(account.RootDomain)} {
			if suffix == "" {
				continue
			}
			if (host == suffix || strings.HasSuffix(host, "."+suffix)) && len(suffix) > bestLen {
				best = account
				bestLen = len(suffix)
			}
		}
	}
	return best, bestLen >= 0
}

func accountInManagedCloudflareZone(account config.CloudflareAccountConfig, host string) bool {
	root := cleanHost(account.RootDomain)
	if root == "" {
		return false
	}
	return host == root || strings.HasSuffix(host, "."+root)
}

func managedWildcardCandidates(account config.CloudflareAccountConfig, host string) []string {
	parent := domainParent(host)
	var out []string
	for _, suffix := range []string{cleanHost(account.SiteDomainSuffix), cleanHost(account.RootDomain)} {
		if suffix != "" && parent == suffix {
			out = append(out, "*."+suffix)
		}
	}
	return out
}
