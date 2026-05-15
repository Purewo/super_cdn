package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"supercdn/internal/siteprobe"
)

func listDeployments(c client, args []string) error {
	fs := flag.NewFlagSet("list-deployments", flag.ExitOnError)
	site := fs.String("site", "", "site id")
	limit := fs.Int("limit", 100, "max deployments")
	_ = fs.Parse(args)
	if *site == "" {
		return errors.New("-site is required")
	}
	return c.do(http.MethodGet, "/api/v1/sites/"+url.PathEscape(*site)+"/deployments?limit="+url.QueryEscape(fmt.Sprint(*limit)), nil, "")
}

func getDeployment(c client, args []string) error {
	fs := flag.NewFlagSet("deployment", flag.ExitOnError)
	site := fs.String("site", "", "site id")
	deployment := fs.String("deployment", "", "deployment id")
	_ = fs.Parse(args)
	if *site == "" || *deployment == "" {
		return errors.New("-site and -deployment are required")
	}
	return c.do(http.MethodGet, "/api/v1/sites/"+url.PathEscape(*site)+"/deployments/"+url.PathEscape(*deployment), nil, "")
}

func exportEdgeManifest(c client, args []string) error {
	fs := flag.NewFlagSet("export-edge-manifest", flag.ExitOnError)
	site := fs.String("site", "", "site id")
	deployment := fs.String("deployment", "", "deployment id")
	out := fs.String("out", "", "optional output file; stdout when empty")
	_ = fs.Parse(args)
	if *site == "" || *deployment == "" {
		return errors.New("-site and -deployment are required")
	}
	raw, err := c.doRaw(http.MethodGet, "/api/v1/sites/"+url.PathEscape(*site)+"/deployments/"+url.PathEscape(*deployment)+"/edge-manifest", nil, "")
	if err != nil {
		return err
	}
	if *out == "" {
		return printJSON(raw)
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, raw, "", "  ") == nil {
		raw = append(pretty.Bytes(), '\n')
	}
	return os.WriteFile(*out, raw, 0o644)
}

func publishEdgeManifest(c client, args []string) error {
	fs := flag.NewFlagSet("publish-edge-manifest", flag.ExitOnError)
	site := fs.String("site", "", "site id")
	deployment := fs.String("deployment", "", "deployment id")
	domains := fs.String("domains", "", "comma-separated bound domains to publish keys for; empty means all site domains")
	cfAccount := fs.String("cloudflare-account", "", "Cloudflare account name; defaults by domain match")
	cfLibrary := fs.String("cloudflare-library", "", "Cloudflare library name")
	kvNamespaceID := fs.String("kv-namespace-id", "", "Cloudflare Workers KV namespace id")
	kvNamespace := fs.String("kv-namespace", "", "Cloudflare Workers KV namespace title; resolved to id by account")
	keyPrefix := fs.String("key-prefix", "", "KV key prefix; defaults to sites/")
	activeKey := fs.Bool("active-key", false, "publish sites/{host}/active/edge-manifest; defaults to true only for active deployments")
	deploymentKey := fs.Bool("deployment-key", true, "publish sites/{host}/deployments/{deployment}/edge-manifest")
	dryRun := fs.Bool("dry-run", true, "plan KV writes without modifying Cloudflare; pass -dry-run=false to publish")
	_ = fs.Parse(args)
	if *site == "" || *deployment == "" {
		return errors.New("-site and -deployment are required")
	}
	req := map[string]any{
		"domains":            splitCSV(*domains),
		"cloudflare_account": *cfAccount,
		"cloudflare_library": *cfLibrary,
		"kv_namespace_id":    *kvNamespaceID,
		"kv_namespace":       *kvNamespace,
		"key_prefix":         *keyPrefix,
		"deployment_key":     *deploymentKey,
		"dry_run":            *dryRun,
	}
	if flagWasSet(fs, "active-key") {
		req["active_key"] = *activeKey
	}
	return c.doJSON(http.MethodPost, "/api/v1/sites/"+url.PathEscape(*site)+"/deployments/"+url.PathEscape(*deployment)+"/edge-manifest/publish", req)
}

type edgeManifestRefreshResponse struct {
	Status        string                      `json:"status"`
	SiteID        string                      `json:"site_id"`
	DeploymentID  string                      `json:"deployment_id"`
	URL           string                      `json:"url,omitempty"`
	Deployment    probeDeployment             `json:"deployment"`
	EdgeManifest  edgeManifestPublishResponse `json:"edge_manifest"`
	Probe         *siteprobe.Report           `json:"probe,omitempty"`
	ProbeRedacted bool                        `json:"probe_redacted,omitempty"`
}

func refreshEdgeManifest(c client, args []string) error {
	fs := flag.NewFlagSet("refresh-edge-manifest", flag.ExitOnError)
	site := fs.String("site", "", "site id")
	deployment := fs.String("deployment", "", "deployment id; defaults to active production deployment")
	domains := fs.String("domains", "", "comma-separated bound domains to refresh; empty means all site domains")
	kvNamespaceID := fs.String("kv-namespace-id", "", "Cloudflare Workers KV namespace id")
	kvNamespace := fs.String("kv-namespace", "supercdn-edge-manifest", "Cloudflare Workers KV namespace title")
	deploymentKey := fs.Bool("deployment-key", true, "publish sites/{host}/deployments/{deployment}/edge-manifest")
	dryRun := fs.Bool("dry-run", false, "plan KV writes without modifying Cloudflare")
	probe := fs.Bool("probe", true, "run a site probe after refreshing the manifest")
	probeURL := fs.String("probe-url", "", "absolute public site URL to probe; defaults to the deployment production URL")
	spaPath := fs.String("spa-path", "", "optional SPA route path to verify as HTML")
	origin := fs.String("origin", "", "Origin header for redirected asset checks; defaults to the probe URL origin")
	resolver := fs.String("resolver", "", "DNS resolver for HTTP probes, for example 1.1.1.1:53")
	maxAssets := fs.Int("max-assets", 20, "maximum JS/CSS assets to probe from index HTML")
	timeout := fs.Duration("timeout", 30*time.Second, "overall probe timeout")
	hybridChecks := fs.Bool("hybrid-checks", true, "require Cloudflare Static HTML and edge-manifest asset first hops during probe")
	redactProbeURLs := fs.Bool("redact-probe-urls", true, "redact query values for signed URLs from the embedded probe report")
	_ = fs.Parse(args)
	if *site == "" {
		return errors.New("-site is required")
	}
	dep, err := loadProbeDeployment(c, *site, *deployment)
	if err != nil {
		return err
	}
	if dep.ID == "" {
		return errors.New("deployment response did not include id")
	}
	edgeManifest, err := c.publishEdgeManifestForDeployment(edgeManifestPublishOptions{
		Site:          *site,
		Deployment:    dep.ID,
		Domains:       cleanDomains(splitCSV(*domains)),
		KVNamespaceID: *kvNamespaceID,
		KVNamespace:   *kvNamespace,
		ActiveKey:     dep.Active,
		DeploymentKey: *deploymentKey,
		DryRun:        *dryRun,
	})
	if err != nil {
		return err
	}
	status := edgeManifest.Status
	if status == "" {
		status = "ok"
	}
	resp := edgeManifestRefreshResponse{
		Status:       status,
		SiteID:       *site,
		DeploymentID: dep.ID,
		Deployment:   dep,
		EdgeManifest: edgeManifest,
	}
	if *probe {
		targetURL := strings.TrimSpace(*probeURL)
		if targetURL == "" {
			targetURL, err = deploymentProbeURL(dep, false)
			if err != nil {
				return err
			}
		}
		resp.URL = targetURL
		report, err := runSiteProbe(*resolver, siteprobe.Options{
			URL:                       targetURL,
			Origin:                    *origin,
			SPAPath:                   *spaPath,
			MaxAssets:                 *maxAssets,
			Timeout:                   *timeout,
			RequireEdgeStaticHTML:     *hybridChecks,
			RequireEdgeManifestAssets: *hybridChecks,
		})
		if err != nil {
			return err
		}
		if *redactProbeURLs {
			report = redactSignedProbeReport(report)
			resp.URL = redactSignedURL(resp.URL)
			resp.ProbeRedacted = true
		}
		resp.Probe = &report
		if !report.OK {
			raw, _ := json.Marshal(resp)
			_ = printJSON(raw)
			return errors.New("site probe failed")
		}
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	return printJSON(raw)
}

type cloudflareStaticPublishResponse struct {
	Status            string   `json:"status"`
	DryRun            bool     `json:"dry_run"`
	Worker            string   `json:"worker"`
	AssetsDir         string   `json:"assets_dir"`
	SourceDir         string   `json:"source_dir,omitempty"`
	Domains           []string `json:"domains,omitempty"`
	CompatibilityDate string   `json:"compatibility_date"`
	CachePolicy       string   `json:"cache_policy,omitempty"`
	NotFoundHandling  string   `json:"not_found_handling,omitempty"`
	WranglerConfig    string   `json:"wrangler_config,omitempty"`
	HeadersFile       string   `json:"headers_file,omitempty"`
	HeadersSource     string   `json:"headers_source,omitempty"`
	HeadersGenerated  bool     `json:"headers_generated,omitempty"`
	Command           []string `json:"command"`
	Output            string   `json:"output,omitempty"`
	ExitCode          int      `json:"exit_code,omitempty"`
}

type cloudflareStaticPublishOptions struct {
	Site              string
	WorkerName        string
	Dir               string
	Domains           []string
	CompatibilityDate string
	EnvFile           string
	Wrangler          string
	WranglerPrefix    string
	Message           string
	CachePolicy       string
	NotFoundHandling  string
	DryRun            bool
}
