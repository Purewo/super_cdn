package server

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"

	"supercdn/internal/model"
	"supercdn/internal/siteinspect"
	"supercdn/internal/storage"
)

func (s *Server) siteView(site *model.Site) model.Site {
	if site == nil {
		return model.Site{}
	}
	view := *site
	if view.DeploymentTarget == "" {
		view.DeploymentTarget = model.SiteDeploymentTargetOriginAssisted
	}
	if view.Status == "" {
		view.Status = model.SiteStatusActive
	}
	view.URLs = s.siteDomainURLs(site.Domains)
	if len(view.URLs) > 0 {
		view.URL = view.URLs[0]
	}
	return view
}

func (s *Server) siteDeploymentView(ctx context.Context, dep *model.SiteDeployment) model.SiteDeployment {
	if dep == nil {
		return model.SiteDeployment{}
	}
	view := *dep
	if view.DeploymentTarget == "" {
		view.DeploymentTarget = model.SiteDeploymentTargetOriginAssisted
	}
	view.PreviewURL = s.absolutePublicURL("/p/" + dep.SiteID + "/" + dep.ID + "/")
	if dep.ManifestJSON != "" {
		var manifest siteDeployManifest
		if json.Unmarshal([]byte(dep.ManifestJSON), &manifest) == nil {
			view.Inspect = manifest.Inspect
			view.DeliverySummary = manifest.DeliverySummary
			view.CloudflareStatic = manifest.CloudflareStatic
			view.HybridEdge = manifest.HybridEdge
		}
	}
	if site, err := s.db.GetSite(ctx, dep.SiteID); err == nil {
		view.SiteDomains = site.Domains
		if dep.Environment == model.SiteEnvironmentProduction && dep.Active {
			if view.CloudflareStatic != nil && len(view.CloudflareStatic.URLs) > 0 {
				view.ProductionURLs = view.CloudflareStatic.URLs
			} else {
				view.ProductionURLs = s.siteDomainURLs(site.Domains)
			}
			if len(view.ProductionURLs) > 0 {
				view.ProductionURL = view.ProductionURLs[0]
			}
		}
	}
	return view
}

func (s *Server) siteDomainURLs(domains []string) []string {
	urls := make([]string, 0, len(domains))
	for _, domain := range domains {
		domain = cleanHost(domain)
		if domain == "" {
			continue
		}
		urls = append(urls, s.publicScheme()+"://"+domain+"/")
	}
	return urls
}

func httpsDomainURLs(domains []string) []string {
	urls := make([]string, 0, len(domains))
	for _, domain := range domains {
		domain = cleanHost(domain)
		if domain == "" {
			continue
		}
		urls = append(urls, "https://"+domain+"/")
	}
	return urls
}

func (s *Server) absolutePublicURL(pathValue string) string {
	base := strings.TrimRight(s.cfg.Server.PublicBaseURL, "/")
	if base == "" {
		return pathValue
	}
	if !strings.HasPrefix(pathValue, "/") {
		pathValue = "/" + pathValue
	}
	return base + pathValue
}

func (s *Server) publicScheme() string {
	if s.cfg.Server.PublicBaseURL != "" {
		if parsed, err := url.Parse(s.cfg.Server.PublicBaseURL); err == nil && parsed.Scheme != "" {
			return parsed.Scheme
		}
	}
	if s.cfg.Cloudflare.RootDomain != "" {
		return "https"
	}
	return "http"
}

func sitePathCandidates(reqPath, mode string) []string {
	clean := strings.TrimPrefix(path.Clean("/"+strings.TrimPrefix(reqPath, "/")), "/")
	if clean == "." || clean == "" {
		return []string{"index.html"}
	}
	var candidates []string
	if strings.HasSuffix(reqPath, "/") {
		candidates = append(candidates, path.Join(clean, "index.html"))
	} else {
		candidates = append(candidates, clean)
		if path.Ext(clean) == "" {
			candidates = append(candidates, path.Join(clean, "index.html"))
		}
	}
	return candidates
}

func deploymentRules(dep *model.SiteDeployment, site *model.Site) siteRules {
	var rules siteRules
	if dep != nil && dep.RulesJSON != "" {
		_ = json.Unmarshal([]byte(dep.RulesJSON), &rules)
	}
	rules = normalizeSiteRules(rules)
	if rules.Mode == "" && site != nil {
		rules.Mode = site.Mode
	}
	if rules.NotFound == "" {
		rules.NotFound = "404.html"
	}
	return rules
}

func cleanRequestPath(reqPath string) string {
	reqPath = "/" + strings.TrimLeft(strings.ReplaceAll(reqPath, "\\", "/"), "/")
	cleaned := path.Clean(reqPath)
	if cleaned == "." {
		return "/"
	}
	if strings.HasSuffix(reqPath, "/") && cleaned != "/" {
		cleaned += "/"
	}
	return cleaned
}

func cleanSiteRulePath(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" || value == "*" {
		return "/*"
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	if strings.HasSuffix(value, "*") {
		prefix := strings.TrimSuffix(value, "*")
		cleaned := path.Clean(prefix)
		if strings.HasSuffix(prefix, "/") && cleaned != "/" {
			return cleaned + "/*"
		}
		return cleaned + "*"
	}
	return cleanRequestPath(value)
}

func siteRuleMatch(pattern, reqPath string) bool {
	pattern = cleanSiteRulePath(pattern)
	reqPath = cleanRequestPath(reqPath)
	if pattern == "/*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(reqPath, strings.TrimSuffix(pattern, "*"))
	}
	return pattern == reqPath
}

func siteHeadersForPath(rules siteRules, reqPath string) map[string]string {
	headers := map[string]string{}
	for _, rule := range rules.Headers {
		if siteRuleMatch(rule.Path, reqPath) {
			for key, value := range rule.Headers {
				key = strings.TrimSpace(key)
				if key != "" {
					headers[key] = value
				}
			}
		}
	}
	return headers
}

func eligibleZipFiles(files []*zip.File) []*zip.File {
	entries := make([]*zip.File, 0, len(files))
	for _, entry := range files {
		if entry.FileInfo().IsDir() {
			continue
		}
		name := strings.ReplaceAll(entry.Name, "\\", "/")
		if strings.HasPrefix(name, "__MACOSX/") || path.Base(name) == ".DS_Store" {
			continue
		}
		entries = append(entries, entry)
	}
	return entries
}

func inspectSiteZipEntries(entries []siteZipEntry) siteinspect.Report {
	files := make([]siteinspect.File, 0, len(entries))
	byPath := map[string]*zip.File{}
	for _, entry := range entries {
		files = append(files, siteinspect.File{Path: entry.path, Size: int64(entry.file.UncompressedSize64)})
		byPath[entry.path] = entry.file
	}
	return siteinspect.InspectFiles(files, func(filePath string, maxBytes int64) ([]byte, error) {
		entry := byPath[filePath]
		if entry == nil {
			return nil, os.ErrNotExist
		}
		rc, err := entry.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(io.LimitReader(rc, maxBytes))
	})
}

func readSiteZipEntries(files []*zip.File) ([]siteZipEntry, siteRules, error) {
	var (
		entries []siteZipEntry
		rules   siteRules
		seen    = map[string]bool{}
	)
	for _, entry := range eligibleZipFiles(files) {
		rel, err := storage.CleanObjectPath(entry.Name)
		if err != nil {
			return nil, siteRules{}, fmt.Errorf("invalid zip path %q: %w", entry.Name, err)
		}
		if rel == siteConfigFile {
			parsed, err := readSiteRules(entry)
			if err != nil {
				return nil, siteRules{}, err
			}
			rules = parsed
			continue
		}
		if seen[rel] {
			return nil, siteRules{}, fmt.Errorf("duplicate site file %q", rel)
		}
		seen[rel] = true
		entries = append(entries, siteZipEntry{file: entry, path: rel})
	}
	if len(entries) == 0 {
		return nil, siteRules{}, fmt.Errorf("zip bundle contains no files")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	return entries, normalizeSiteRules(rules), nil
}

func readSiteRules(entry *zip.File) (siteRules, error) {
	rc, err := entry.Open()
	if err != nil {
		return siteRules{}, err
	}
	defer rc.Close()
	raw, err := io.ReadAll(io.LimitReader(rc, 1<<20))
	if err != nil {
		return siteRules{}, err
	}
	var rules siteRules
	if err := json.Unmarshal(raw, &rules); err != nil {
		return siteRules{}, fmt.Errorf("invalid %s: %w", siteConfigFile, err)
	}
	return rules, nil
}

func normalizeSiteRules(rules siteRules) siteRules {
	rules.Mode = firstNonEmpty(normalizeSiteMode(rules.Mode), "")
	if rules.NotFound != "" {
		if cleaned, err := storage.CleanObjectPath(rules.NotFound); err == nil {
			rules.NotFound = cleaned
		}
	}
	for i := range rules.Redirects {
		rules.Redirects[i].From = cleanSiteRulePath(rules.Redirects[i].From)
		if rules.Redirects[i].Status == 0 {
			rules.Redirects[i].Status = http.StatusFound
		}
		if !inSet(strconv.Itoa(rules.Redirects[i].Status), "301", "302", "307", "308") {
			rules.Redirects[i].Status = http.StatusFound
		}
	}
	for i := range rules.Rewrites {
		rules.Rewrites[i].From = cleanSiteRulePath(rules.Rewrites[i].From)
		if cleaned, err := storage.CleanObjectPath(rules.Rewrites[i].To); err == nil {
			rules.Rewrites[i].To = "/" + cleaned
		}
	}
	for i := range rules.Headers {
		rules.Headers[i].Path = cleanSiteRulePath(rules.Headers[i].Path)
	}
	for i := range rules.Delivery {
		rules.Delivery[i].Path = cleanSiteRulePath(rules.Delivery[i].Path)
		rules.Delivery[i].Mode = normalizeSiteDeliveryMode(rules.Delivery[i].Mode)
	}
	return rules
}

func normalizeSiteDeliveryMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "origin", "redirect", "auto":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "auto"
	}
}

func siteDeliveryMode(rules siteRules, objectPath string) string {
	clean := strings.TrimPrefix(path.Clean("/"+strings.TrimPrefix(objectPath, "/")), "/")
	if clean == "" || clean == "." || clean == "index.html" {
		return "origin"
	}
	mode := "redirect"
	reqPath := "/" + clean
	for _, rule := range rules.Delivery {
		if siteRuleMatch(rule.Path, reqPath) {
			switch rule.Mode {
			case "origin":
				mode = "origin"
			case "redirect", "auto", "":
				mode = "redirect"
			}
		}
	}
	return mode
}

func (s *Server) checkDeploymentFileCount(environment string, count int) error {
	limit := defaultProductionSiteFiles
	if environment == model.SiteEnvironmentPreview {
		limit = defaultPreviewSiteFiles
	}
	if count <= 0 {
		return fmt.Errorf("site deployment contains no files")
	}
	if s.overclockMode() {
		return nil
	}
	if count > limit {
		return fmt.Errorf("%s deployment allows at most %d files, got %d", environment, limit, count)
	}
	return nil
}
