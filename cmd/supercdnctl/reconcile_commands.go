package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"supercdn/internal/model"
	"supercdn/internal/siteprobe"
)

type reconcileDeploymentRecord struct {
	ID               string                            `json:"id"`
	SiteID           string                            `json:"site_id"`
	Environment      string                            `json:"environment"`
	Status           string                            `json:"status"`
	RouteProfile     string                            `json:"route_profile"`
	DeploymentTarget string                            `json:"deployment_target"`
	Active           bool                              `json:"active"`
	ProductionURL    string                            `json:"production_url"`
	ProductionURLs   []string                          `json:"production_urls"`
	PreviewURL       string                            `json:"preview_url"`
	SiteDomains      []string                          `json:"site_domains,omitempty"`
	CloudflareStatic *model.CloudflareStaticDeployment `json:"cloudflare_static,omitempty"`
	HybridEdge       *model.HybridEdgeDeployment       `json:"hybrid_edge,omitempty"`
}

type reconcileDeploymentReport struct {
	Status           string                   `json:"status"`
	Settled          bool                     `json:"settled"`
	SiteID           string                   `json:"site_id"`
	DeploymentID     string                   `json:"deployment_id"`
	DeploymentTarget string                   `json:"deployment_target"`
	URL              string                   `json:"url,omitempty"`
	Metadata         reconcileMetadataSummary `json:"metadata"`
	Provider         reconcileProviderSummary `json:"provider"`
	Probe            *siteprobe.Report        `json:"probe,omitempty"`
	Warnings         []string                 `json:"warnings,omitempty"`
	NextCommands     []string                 `json:"next_commands,omitempty"`
}

type reconcileMetadataSummary struct {
	Environment        string   `json:"environment"`
	Status             string   `json:"status"`
	Active             bool     `json:"active"`
	RouteProfile       string   `json:"route_profile,omitempty"`
	ProductionURL      string   `json:"production_url,omitempty"`
	ProductionURLs     []string `json:"production_urls,omitempty"`
	WorkerName         string   `json:"worker_name,omitempty"`
	VersionID          string   `json:"version_id,omitempty"`
	VerificationStatus string   `json:"verification_status,omitempty"`
	VerifiedAtUTC      string   `json:"verified_at_utc,omitempty"`
	KVNamespaceID      string   `json:"kv_namespace_id,omitempty"`
	ManifestSHA256     string   `json:"manifest_sha256,omitempty"`
}

type reconcileProviderSummary struct {
	Checked                    bool   `json:"checked"`
	Status                     string `json:"status"`
	HTMLStatus                 int    `json:"html_status,omitempty"`
	HTMLSource                 string `json:"html_source,omitempty"`
	AssetCount                 int    `json:"asset_count,omitempty"`
	AssetOK                    int    `json:"asset_ok,omitempty"`
	RequireEdgeStaticHTML      bool   `json:"require_edge_static_html,omitempty"`
	RequireEdgeManifestAssets  bool   `json:"require_edge_manifest_assets,omitempty"`
	RequireDirectAssets        bool   `json:"require_direct_assets,omitempty"`
	RequireHTMLRevalidate      bool   `json:"require_html_revalidate,omitempty"`
	RequireImmutableAssetCache bool   `json:"require_immutable_asset_cache,omitempty"`
	Error                      string `json:"error,omitempty"`
}

func reconcileDeployment(c client, args []string) error {
	fs := flag.NewFlagSet("reconcile-deployment", flag.ExitOnError)
	site := fs.String("site", "", "site id")
	deployment := fs.String("deployment", "", "deployment id; defaults to active production deployment")
	probeURL := fs.String("url", "", "absolute public URL to probe; defaults to deployment production URL")
	spaPath := fs.String("spa-path", "", "optional SPA route path to verify as HTML")
	resolver := fs.String("resolver", "", "DNS resolver for HTTP probes, for example 1.1.1.1:53")
	maxAssets := fs.Int("max-assets", 20, "maximum JS/CSS assets to probe from index HTML")
	timeout := fs.Duration("timeout", 30*time.Second, "overall probe timeout")
	redactURLs := fs.Bool("redact-urls", true, "redact query values for signed URLs from JSON output")
	_ = fs.Parse(args)
	if strings.TrimSpace(*site) == "" {
		return errors.New("-site is required")
	}
	dep, err := loadReconcileDeployment(c, strings.TrimSpace(*site), strings.TrimSpace(*deployment))
	if err != nil {
		return err
	}
	targetURL := strings.TrimSpace(*probeURL)
	if targetURL == "" {
		targetURL, err = reconcileDeploymentURL(dep)
		if err != nil {
			return err
		}
	}
	probeOpts := reconcileProbeOptions(dep, targetURL, *spaPath, *maxAssets, *timeout)
	report := buildReconcileDeploymentReport(dep, targetURL, probeOpts)
	if dep.DeploymentTarget == model.SiteDeploymentTargetCloudflareStatic || dep.DeploymentTarget == model.SiteDeploymentTargetHybridEdge {
		probe, probeErr := runSiteProbe(*resolver, probeOpts)
		if *redactURLs {
			probe = redactSignedProbeReport(probe)
		}
		report.Provider.Checked = true
		report.Provider.Status = probe.Status
		report.Provider.HTMLStatus = probe.HTML.StatusCode
		report.Provider.HTMLSource = probe.HTML.EdgeSource
		report.Provider.AssetCount = probe.Summary["assets_found"]
		report.Provider.AssetOK = probe.Summary["assets_ok"]
		if probeErr != nil {
			report.Provider.Error = probeErr.Error()
			report.Warnings = append(report.Warnings, probeErr.Error())
		}
		report.Probe = &probe
		if *redactURLs {
			report.URL = redactSignedURL(report.URL)
		}
		if probe.OK {
			report.Status = "ok"
			report.Settled = true
		} else {
			report.Status = "failed"
			report.Settled = false
			report.Warnings = append(report.Warnings, "provider state is not verified for this deployment")
		}
	} else {
		report.Status = "not_applicable"
		report.Settled = true
		report.Warnings = append(report.Warnings, "deployment target is not Cloudflare-backed; provider reconciliation is not required")
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return err
	}
	if err := printJSON(raw); err != nil {
		return err
	}
	if report.Status == "failed" {
		return errors.New("deployment reconciliation failed")
	}
	return nil
}

func loadReconcileDeployment(c client, site, deployment string) (reconcileDeploymentRecord, error) {
	if deployment != "" {
		raw, err := c.doRaw(http.MethodGet, "/api/v1/sites/"+url.PathEscape(site)+"/deployments/"+url.PathEscape(deployment), nil, "")
		if err != nil {
			return reconcileDeploymentRecord{}, err
		}
		var dep reconcileDeploymentRecord
		if err := json.Unmarshal(raw, &dep); err != nil {
			return reconcileDeploymentRecord{}, err
		}
		return dep, nil
	}
	raw, err := c.doRaw(http.MethodGet, "/api/v1/sites/"+url.PathEscape(site)+"/deployments?limit=100", nil, "")
	if err != nil {
		return reconcileDeploymentRecord{}, err
	}
	var resp struct {
		Deployments []reconcileDeploymentRecord `json:"deployments"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return reconcileDeploymentRecord{}, err
	}
	for _, dep := range resp.Deployments {
		if dep.Active && strings.EqualFold(dep.Environment, model.SiteEnvironmentProduction) {
			return dep, nil
		}
	}
	for _, dep := range resp.Deployments {
		if dep.Active {
			return dep, nil
		}
	}
	return reconcileDeploymentRecord{}, errors.New("active deployment not found")
}

func reconcileDeploymentURL(dep reconcileDeploymentRecord) (string, error) {
	if dep.ProductionURL != "" {
		return dep.ProductionURL, nil
	}
	if len(dep.ProductionURLs) > 0 && dep.ProductionURLs[0] != "" {
		return dep.ProductionURLs[0], nil
	}
	if dep.CloudflareStatic != nil {
		for _, raw := range dep.CloudflareStatic.URLs {
			if strings.TrimSpace(raw) != "" {
				return raw, nil
			}
		}
		for _, domain := range dep.CloudflareStatic.Domains {
			if host := strings.TrimSpace(domain); host != "" {
				return "https://" + host + "/", nil
			}
		}
	}
	if dep.HybridEdge != nil {
		for _, raw := range dep.HybridEdge.URLs {
			if strings.TrimSpace(raw) != "" {
				return raw, nil
			}
		}
		for _, domain := range dep.HybridEdge.Domains {
			if host := strings.TrimSpace(domain); host != "" {
				return "https://" + host + "/", nil
			}
		}
	}
	if dep.PreviewURL != "" {
		return dep.PreviewURL, nil
	}
	return "", fmt.Errorf("deployment %q has no probeable URL", dep.ID)
}

func reconcileProbeOptions(dep reconcileDeploymentRecord, targetURL, spaPath string, maxAssets int, timeout time.Duration) siteprobe.Options {
	opts := siteprobe.Options{
		URL:       targetURL,
		SPAPath:   spaPath,
		MaxAssets: maxAssets,
		Timeout:   timeout,
	}
	switch dep.DeploymentTarget {
	case model.SiteDeploymentTargetCloudflareStatic:
		opts.RequireEdgeStaticHTML = true
		opts.RequireDirectAssets = true
		if dep.CloudflareStatic != nil && dep.CloudflareStatic.HeadersGenerated {
			opts.RequireHTMLRevalidate = true
			opts.RequireImmutableAssetCache = true
		}
	case model.SiteDeploymentTargetHybridEdge:
		opts.RequireEdgeStaticHTML = true
		opts.RequireEdgeManifestAssets = true
	}
	return opts
}

func buildReconcileDeploymentReport(dep reconcileDeploymentRecord, targetURL string, opts siteprobe.Options) reconcileDeploymentReport {
	report := reconcileDeploymentReport{
		Status:           "planned",
		SiteID:           dep.SiteID,
		DeploymentID:     dep.ID,
		DeploymentTarget: firstNonEmpty(dep.DeploymentTarget, model.SiteDeploymentTargetOriginAssisted),
		URL:              targetURL,
		Metadata: reconcileMetadataSummary{
			Environment:    dep.Environment,
			Status:         dep.Status,
			Active:         dep.Active,
			RouteProfile:   dep.RouteProfile,
			ProductionURL:  dep.ProductionURL,
			ProductionURLs: dep.ProductionURLs,
		},
		Provider: reconcileProviderSummary{
			Status:                     "not_checked",
			RequireEdgeStaticHTML:      opts.RequireEdgeStaticHTML,
			RequireEdgeManifestAssets:  opts.RequireEdgeManifestAssets,
			RequireDirectAssets:        opts.RequireDirectAssets,
			RequireHTMLRevalidate:      opts.RequireHTMLRevalidate,
			RequireImmutableAssetCache: opts.RequireImmutableAssetCache,
		},
		NextCommands: reconcileNextCommands(dep, targetURL),
	}
	if dep.CloudflareStatic != nil {
		report.Metadata.WorkerName = dep.CloudflareStatic.WorkerName
		report.Metadata.VersionID = dep.CloudflareStatic.VersionID
		report.Metadata.VerificationStatus = dep.CloudflareStatic.VerificationStatus
		if !dep.CloudflareStatic.VerifiedAt.IsZero() {
			report.Metadata.VerifiedAtUTC = dep.CloudflareStatic.VerifiedAt.UTC().Format(time.RFC3339Nano)
		}
	}
	if dep.HybridEdge != nil {
		report.Metadata.WorkerName = dep.HybridEdge.WorkerName
		report.Metadata.VersionID = dep.HybridEdge.VersionID
		report.Metadata.VerificationStatus = dep.HybridEdge.VerificationStatus
		report.Metadata.KVNamespaceID = dep.HybridEdge.KVNamespaceID
		report.Metadata.ManifestSHA256 = dep.HybridEdge.ManifestSHA256
		if !dep.HybridEdge.VerifiedAt.IsZero() {
			report.Metadata.VerifiedAtUTC = dep.HybridEdge.VerifiedAt.UTC().Format(time.RFC3339Nano)
		}
	}
	if dep.DeploymentTarget == model.SiteDeploymentTargetCloudflareStatic && dep.CloudflareStatic == nil {
		report.Warnings = append(report.Warnings, "cloudflare_static deployment metadata has no Cloudflare evidence block")
	}
	if dep.DeploymentTarget == model.SiteDeploymentTargetHybridEdge && dep.HybridEdge == nil {
		report.Warnings = append(report.Warnings, "hybrid_edge deployment metadata has no Worker/KV/manifest evidence block")
	}
	if (dep.DeploymentTarget == model.SiteDeploymentTargetCloudflareStatic || dep.DeploymentTarget == model.SiteDeploymentTargetHybridEdge) && !dep.Active {
		report.Warnings = append(report.Warnings, dep.DeploymentTarget+" deployment is not active; probe may represent the current live domain state rather than an archived provider version")
	}
	return report
}

func reconcileNextCommands(dep reconcileDeploymentRecord, targetURL string) []string {
	probe := []string{
		"supercdnctl probe-site",
		"-site " + cliHintArg(dep.SiteID),
		"-deployment " + cliHintArg(dep.ID),
	}
	if (dep.DeploymentTarget == model.SiteDeploymentTargetCloudflareStatic || dep.DeploymentTarget == model.SiteDeploymentTargetHybridEdge) && !dep.Active && strings.TrimSpace(targetURL) != "" {
		probe = []string{"supercdnctl probe-site", "-url " + cliHintArg(targetURL)}
	}
	switch dep.DeploymentTarget {
	case model.SiteDeploymentTargetCloudflareStatic:
		if dep.Active {
			probe = append(probe, "-production")
		}
		probe = append(probe, "-require-edge-static-html", "-require-direct-assets")
		if dep.CloudflareStatic != nil && dep.CloudflareStatic.HeadersGenerated {
			probe = append(probe, "-require-html-revalidate", "-require-immutable-assets")
		}
	case model.SiteDeploymentTargetHybridEdge:
		if dep.Active {
			probe = append(probe, "-production")
		}
		probe = append(probe, "-require-edge-static-html", "-require-edge-manifest-assets")
	default:
		if strings.TrimSpace(targetURL) != "" {
			probe = []string{"supercdnctl probe-site", "-url " + cliHintArg(targetURL)}
		}
	}
	commands := []string{strings.Join(probe, " ")}
	if firstDomain := firstReconcileDomain(dep, targetURL); firstDomain != "" {
		commands = append(commands, "supercdnctl domain-status -domain "+cliHintArg(firstDomain))
	}
	if dep.DeploymentTarget == model.SiteDeploymentTargetHybridEdge {
		commands = append(commands, "supercdnctl refresh-edge-manifest -site "+cliHintArg(dep.SiteID)+" -deployment "+cliHintArg(dep.ID))
	}
	return commands
}

func firstReconcileDomain(dep reconcileDeploymentRecord, targetURL string) string {
	if len(dep.SiteDomains) > 0 {
		return dep.SiteDomains[0]
	}
	if len(dep.ProductionURLs) > 0 {
		if host := hostFromURL(dep.ProductionURLs[0]); host != "" {
			return host
		}
	}
	if host := hostFromURL(dep.ProductionURL); host != "" {
		return host
	}
	if dep.CloudflareStatic != nil {
		for _, domain := range dep.CloudflareStatic.Domains {
			if host := cleanReconcileDomain(domain); host != "" {
				return host
			}
		}
		for _, raw := range dep.CloudflareStatic.URLs {
			if host := hostFromURL(raw); host != "" {
				return host
			}
		}
	}
	if dep.HybridEdge != nil {
		for _, domain := range dep.HybridEdge.Domains {
			if host := cleanReconcileDomain(domain); host != "" {
				return host
			}
		}
		for _, raw := range dep.HybridEdge.URLs {
			if host := hostFromURL(raw); host != "" {
				return host
			}
		}
	}
	return hostFromURL(targetURL)
}

func cleanReconcileDomain(domain string) string {
	parsed, err := url.Parse(strings.TrimSpace(domain))
	if err == nil && parsed.Hostname() != "" {
		return parsed.Hostname()
	}
	return strings.Trim(strings.TrimSpace(domain), "/")
}

func hostFromURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}
