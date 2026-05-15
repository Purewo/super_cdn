package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"supercdn/internal/siteprobe"
)

type deploySiteCommandOptions struct {
	Command             string
	RequireExistingSite bool
}

func deploySite(c client, args []string) error {
	return deploySiteWithOptions(c, args, deploySiteCommandOptions{Command: "deploy-site"})
}

func updateSite(c client, args []string) error {
	return deploySiteWithOptions(c, args, deploySiteCommandOptions{Command: "update-site", RequireExistingSite: true})
}

func deploySiteWithOptions(c client, args []string, opts deploySiteCommandOptions) error {
	command := strings.TrimSpace(opts.Command)
	if command == "" {
		command = "deploy-site"
	}
	fs := flag.NewFlagSet(command, flag.ExitOnError)
	site := fs.String("site", "", "site id")
	dir := fs.String("dir", "", "dist directory")
	bundle := fs.String("bundle", "", "zip artifact to upload")
	env := fs.String("env", "production", "deployment environment: production or preview")
	promote := fs.Bool("promote", false, "promote the deployment to production after processing")
	pinned := fs.Bool("pinned", false, "prevent automatic deployment retention cleanup")
	wait := fs.Bool("wait", true, "wait for asynchronous deployment completion")
	timeout := fs.Duration("timeout", 30*time.Minute, "maximum time to wait")
	profile := fs.String("profile", "", "route profile override")
	target := fs.String("target", "", "deployment target override: origin_assisted, cloudflare_static, or hybrid_edge")
	routingPolicy := fs.String("routing-policy", "", "routing policy override; requires matching multi-source route profile")
	resourceFailover := fs.Bool("resource-failover", false, "enable explicit Web resource failover between route-profile storage targets")
	entryOriginFallback := fs.Bool("entry-origin-fallback", false, "temporarily fall back entry HTML/SPAs to Go origin when Cloudflare entry fails")
	domains := fs.String("domains", "", "comma-separated custom domains when -target cloudflare_static or hybrid_edge")
	staticName := fs.String("static-name", "", "Worker name when -target cloudflare_static; defaults to supercdn-{site}-static")
	edgeName := fs.String("edge-name", "", "Worker name when -target hybrid_edge; defaults to supercdn-{site}-edge")
	compatDate := fs.String("compatibility-date", time.Now().UTC().Format("2006-01-02"), "Workers compatibility date when -target cloudflare_static or hybrid_edge")
	staticMessage := fs.String("message", "", "Cloudflare deployment message when -target cloudflare_static or hybrid_edge")
	staticCachePolicy := fs.String("static-cache-policy", cloudflareStaticCachePolicyAuto, "Cloudflare Static cache policy: auto, force, or none")
	staticNotFoundHandling := fs.String("static-not-found-handling", "", "Cloudflare Static not_found_handling: none, 404-page, or single-page-application")
	staticSPA := fs.Bool("static-spa", false, "enable Cloudflare Static single-page-application fallback")
	staticVerify := fs.String("static-verify", cloudflareStaticVerifyWait, "Cloudflare Static readiness check: wait, warn, or none")
	staticVerifyTimeout := fs.Duration("static-verify-timeout", 2*time.Minute, "maximum time to wait for Cloudflare Static custom domains")
	staticVerifyInterval := fs.Duration("static-verify-interval", 5*time.Second, "delay between Cloudflare Static readiness probes")
	staticVerifySPAPath := fs.String("static-verify-spa-path", "", "SPA path to verify after Cloudflare Static publish; defaults to /__supercdn_spa_probe when -static-spa is enabled")
	staticVerifyResolver := fs.String("static-verify-resolver", "1.1.1.1:53", "DNS resolver for Cloudflare Static readiness probes")
	edgeKVNamespaceID := fs.String("edge-kv-namespace-id", "", "Cloudflare Workers KV namespace id for hybrid_edge edge manifests")
	edgeKVNamespace := fs.String("edge-kv-namespace", "supercdn-edge-manifest", "Cloudflare Workers KV namespace title for hybrid_edge edge manifests")
	edgeManifestMode := fs.String("edge-manifest-mode", "route", "hybrid_edge Worker manifest mode: route or enforce")
	edgeDefaultCacheControl := fs.String("edge-default-cache-control", "public, max-age=300", "default Cache-Control for hybrid_edge Worker fallback responses")
	edgeCandidateWait := fs.Bool("edge-candidate-wait", true, "wait for routing-policy/resource-failover candidates before publishing hybrid_edge edge manifest")
	edgeCandidateTimeout := fs.Duration("edge-candidate-timeout", 10*time.Minute, "maximum time to wait for hybrid_edge routing/failover candidates")
	_ = fs.Parse(args)
	if *site == "" {
		return errors.New("-site is required")
	}
	if *dir == "" && *bundle == "" {
		return errors.New("-dir or -bundle is required")
	}
	if *dir != "" && *bundle != "" {
		return errors.New("use either -dir or -bundle, not both")
	}
	if strings.EqualFold(*env, "production") && !flagWasSet(fs, "promote") {
		*promote = true
	}
	resolvedTarget := deploymentTargetAlias(*target)
	resolvedProfile := *profile
	resolvedDomains := splitCSV(*domains)
	var defaults siteDeploymentTargetDefaults
	defaultsResolved := false
	resolveDefaults := func() error {
		if defaultsResolved {
			return nil
		}
		next, err := c.resolveSiteDeploymentTarget(*site, *profile, *target)
		if err != nil {
			return err
		}
		defaults = next
		defaultsResolved = true
		return nil
	}
	if opts.RequireExistingSite {
		if err := resolveDefaults(); err != nil {
			return err
		}
		if !defaults.SiteExists {
			return fmt.Errorf("%s requires an existing site; use create-site or deploy-site for the first publish", command)
		}
	}
	if strings.TrimSpace(*target) == "" {
		if err := resolveDefaults(); err != nil {
			return err
		}
		resolvedTarget = deploymentTargetAlias(defaults.DeploymentTarget)
		if strings.TrimSpace(resolvedProfile) == "" {
			resolvedProfile = defaults.RouteProfile
		}
		if len(resolvedDomains) == 0 {
			resolvedDomains = defaults.Domains
		}
	} else if strings.TrimSpace(resolvedProfile) == "" || len(resolvedDomains) == 0 {
		if err := resolveDefaults(); err == nil {
			if strings.TrimSpace(resolvedProfile) == "" {
				resolvedProfile = defaults.RouteProfile
			}
			if len(resolvedDomains) == 0 {
				resolvedDomains = defaults.Domains
			}
		}
	}
	if *entryOriginFallback && resolvedTarget != "hybrid_edge" {
		return errors.New("entry origin fallback is only supported for hybrid_edge deployments")
	}
	if resolvedTarget == "cloudflare_static" {
		if *resourceFailover {
			return errors.New("resource failover is not supported for cloudflare_static deployments")
		}
		if *dir == "" {
			return fmt.Errorf("cloudflare_static %s requires -dir", command)
		}
		if *bundle != "" {
			return fmt.Errorf("cloudflare_static %s does not accept -bundle yet", command)
		}
		return deploySiteCloudflareStatic(c, cloudflareStaticDeploySiteOptions{
			Site:              *site,
			Dir:               *dir,
			Environment:       *env,
			RouteProfile:      resolvedProfile,
			DeploymentTarget:  resolvedTarget,
			RoutingPolicy:     strings.TrimSpace(*routingPolicy),
			ResourceFailover:  false,
			Domains:           resolvedDomains,
			WorkerName:        *staticName,
			CompatibilityDate: *compatDate,
			Message:           *staticMessage,
			CachePolicy:       *staticCachePolicy,
			NotFoundHandling:  cloudflareStaticNotFoundHandlingFlag(*staticNotFoundHandling, *staticSPA),
			VerifyMode:        *staticVerify,
			VerifyTimeout:     *staticVerifyTimeout,
			VerifyInterval:    *staticVerifyInterval,
			VerifySPAPath:     *staticVerifySPAPath,
			VerifyResolver:    *staticVerifyResolver,
			Promote:           *promote,
			Pinned:            *pinned,
		})
	}
	if resolvedTarget == "hybrid_edge" {
		if *dir == "" {
			return fmt.Errorf("hybrid_edge %s requires -dir", command)
		}
		if *bundle != "" {
			return fmt.Errorf("hybrid_edge %s does not accept -bundle yet", command)
		}
		return deploySiteHybridEdge(c, hybridEdgeDeploySiteOptions{
			Site:                *site,
			Dir:                 *dir,
			Environment:         *env,
			RouteProfile:        resolvedProfile,
			DeploymentTarget:    resolvedTarget,
			RoutingPolicy:       strings.TrimSpace(*routingPolicy),
			ResourceFailover:    *resourceFailover,
			EntryOriginFallback: *entryOriginFallback,
			Domains:             resolvedDomains,
			WorkerName:          *edgeName,
			CompatibilityDate:   *compatDate,
			Message:             *staticMessage,
			CachePolicy:         *staticCachePolicy,
			NotFoundHandling:    cloudflareStaticNotFoundHandlingFlag(*staticNotFoundHandling, *staticSPA),
			VerifyMode:          *staticVerify,
			VerifyTimeout:       *staticVerifyTimeout,
			VerifyInterval:      *staticVerifyInterval,
			VerifySPAPath:       *staticVerifySPAPath,
			VerifyResolver:      *staticVerifyResolver,
			Promote:             *promote,
			Pinned:              *pinned,
			Timeout:             *timeout,
			KVNamespaceID:       *edgeKVNamespaceID,
			KVNamespace:         *edgeKVNamespace,
			ManifestMode:        *edgeManifestMode,
			DefaultCacheControl: *edgeDefaultCacheControl,
			CandidateWait:       *edgeCandidateWait,
			CandidateTimeout:    *edgeCandidateTimeout,
		})
	}
	artifact := *bundle
	cleanup := ""
	if *dir != "" {
		zipPath, err := zipDirectory(*dir)
		if err != nil {
			return err
		}
		artifact = zipPath
		cleanup = zipPath
	}
	if cleanup != "" {
		defer os.Remove(cleanup)
	}
	fields := map[string]string{
		"route_profile":     resolvedProfile,
		"deployment_target": resolvedTarget,
		"routing_policy":    strings.TrimSpace(*routingPolicy),
		"resource_failover": fmt.Sprint(*resourceFailover),
		"environment":       *env,
		"promote":           fmt.Sprint(*promote),
		"pinned":            fmt.Sprint(*pinned),
	}
	raw, err := c.uploadFileRaw("/api/v1/sites/"+url.PathEscape(*site)+"/deployments", "artifact", artifact, fields)
	if err != nil {
		return err
	}
	if !*wait {
		return printJSON(raw)
	}
	var created struct {
		DeploymentID string `json:"deployment_id"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		return err
	}
	if created.DeploymentID == "" {
		return printJSON(raw)
	}
	return c.waitDeployment(*site, created.DeploymentID, *timeout)
}

type siteDeploymentTargetDefaults struct {
	SiteID           string   `json:"site_id"`
	SiteExists       bool     `json:"site_exists"`
	RouteProfile     string   `json:"route_profile"`
	DeploymentTarget string   `json:"deployment_target"`
	Source           string   `json:"source"`
	Domains          []string `json:"domains,omitempty"`
	DefaultDomain    string   `json:"default_domain,omitempty"`
}

func (c client) resolveSiteDeploymentTarget(site, routeProfile, target string) (siteDeploymentTargetDefaults, error) {
	q := url.Values{}
	if strings.TrimSpace(routeProfile) != "" {
		q.Set("route_profile", strings.TrimSpace(routeProfile))
	}
	if strings.TrimSpace(target) != "" {
		q.Set("deployment_target", strings.TrimSpace(target))
	}
	path := "/api/v1/sites/" + url.PathEscape(site) + "/deployment-target"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	raw, err := c.doRaw(http.MethodGet, path, nil, "")
	if err != nil {
		return siteDeploymentTargetDefaults{}, err
	}
	var defaults siteDeploymentTargetDefaults
	if err := json.Unmarshal(raw, &defaults); err != nil {
		return siteDeploymentTargetDefaults{}, err
	}
	return defaults, nil
}

type cloudflareStaticDeploySiteOptions struct {
	Site              string
	Dir               string
	Environment       string
	RouteProfile      string
	DeploymentTarget  string
	RoutingPolicy     string
	ResourceFailover  bool
	Domains           []string
	WorkerName        string
	CompatibilityDate string
	Message           string
	CachePolicy       string
	NotFoundHandling  string
	VerifyMode        string
	VerifyTimeout     time.Duration
	VerifyInterval    time.Duration
	VerifySPAPath     string
	VerifyResolver    string
	Promote           bool
	Pinned            bool
	Operation         string
	RollbackTarget    string
}

func deploySiteCloudflareStatic(c client, opts cloudflareStaticDeploySiteOptions) error {
	raw, err := deploySiteCloudflareStaticRaw(c, opts)
	if len(raw) > 0 {
		_ = printJSON(raw)
	}
	return err
}

func deploySiteCloudflareStaticRaw(c client, opts cloudflareStaticDeploySiteOptions) ([]byte, error) {
	stats, err := summarizeCloudflareStaticDirectory(opts.Dir)
	if err != nil {
		return nil, err
	}
	workerName := strings.TrimSpace(opts.WorkerName)
	if workerName == "" {
		workerName = "supercdn-" + cleanWorkerName(opts.Site) + "-static"
	}
	publish, err := runCloudflareStaticPublish(cloudflareStaticPublishOptions{
		Site:              opts.Site,
		WorkerName:        workerName,
		Dir:               opts.Dir,
		Domains:           opts.Domains,
		CompatibilityDate: opts.CompatibilityDate,
		Message:           firstNonEmpty(opts.Message, "SuperCDN cloudflare_static deploy "+opts.Site),
		CachePolicy:       opts.CachePolicy,
		NotFoundHandling:  opts.NotFoundHandling,
		DryRun:            false,
		EnvFile:           "configs/private/cloudflare.env",
		Wrangler:          "npx",
		WranglerPrefix:    "worker",
	})
	if err != nil {
		raw, _ := json.Marshal(publish)
		return raw, err
	}
	verify, err := verifyCloudflareStaticPublish(context.Background(), cloudflareStaticVerifyOptions{
		Mode:                        opts.VerifyMode,
		Domains:                     opts.Domains,
		Timeout:                     opts.VerifyTimeout,
		Interval:                    opts.VerifyInterval,
		SPAPath:                     opts.VerifySPAPath,
		Resolver:                    opts.VerifyResolver,
		NotFoundHandling:            publish.NotFoundHandling,
		RequireDirectAssets:         true,
		RequireEdgeStaticHTML:       false,
		RequireEdgeManifestAssets:   false,
		RequireGeneratedCachePolicy: publish.HeadersGenerated,
		RequireImmutableAssetCache:  publish.HeadersGenerated,
	})
	if err != nil {
		raw, _ := json.Marshal(cloudflareStaticProviderWriteFailure{
			Status: "verify_failed_after_provider_write",
			SiteID: opts.Site,
			Warnings: []string{
				"Cloudflare Worker/custom-domain write may have succeeded, but readiness verification timed out before Super CDN recorded the deployment.",
				"Rerun deploy-site after DNS/custom-domain propagation settles, or run the probe command below to verify the live domain.",
			},
			Worker:       publish,
			Verify:       verify,
			NextCommands: cloudflareVerifyFailureNextCommands(opts.Site, opts.Dir, "cloudflare_static", opts.RouteProfile, opts.Domains, false),
		})
		return raw, err
	}
	req := map[string]any{
		"environment":                opts.Environment,
		"route_profile":              opts.RouteProfile,
		"deployment_target":          "cloudflare_static",
		"routing_policy":             opts.RoutingPolicy,
		"resource_failover":          opts.ResourceFailover,
		"worker_name":                workerName,
		"version_id":                 extractCloudflareVersionID(publish.Output),
		"domains":                    opts.Domains,
		"compatibility_date":         opts.CompatibilityDate,
		"assets_sha256":              stats.SHA256,
		"file_count":                 stats.FileCount,
		"total_size":                 stats.TotalSize,
		"cache_policy":               publish.CachePolicy,
		"headers_generated":          publish.HeadersGenerated,
		"not_found_handling":         publish.NotFoundHandling,
		"verification_status":        verify.Status,
		"verified_at_utc":            time.Now().UTC().Format(time.RFC3339Nano),
		"published_at_utc":           time.Now().UTC().Format(time.RFC3339Nano),
		"promote":                    opts.Promote,
		"pinned":                     opts.Pinned,
		"operation":                  strings.TrimSpace(opts.Operation),
		"rollback_target_deployment": strings.TrimSpace(opts.RollbackTarget),
	}
	return c.doRaw(http.MethodPost, "/api/v1/sites/"+url.PathEscape(opts.Site)+"/cloudflare-static/deployments", bytes.NewReader(mustJSON(req)), "application/json")
}

type hybridEdgeDeploySiteOptions struct {
	Site                string
	Dir                 string
	Environment         string
	RouteProfile        string
	DeploymentTarget    string
	RoutingPolicy       string
	ResourceFailover    bool
	EntryOriginFallback bool
	Domains             []string
	WorkerName          string
	CompatibilityDate   string
	Message             string
	CachePolicy         string
	NotFoundHandling    string
	VerifyMode          string
	VerifyTimeout       time.Duration
	VerifyInterval      time.Duration
	VerifySPAPath       string
	VerifyResolver      string
	Promote             bool
	Pinned              bool
	Timeout             time.Duration
	KVNamespaceID       string
	KVNamespace         string
	ManifestMode        string
	DefaultCacheControl string
	CandidateWait       bool
	CandidateTimeout    time.Duration
	Operation           string
	RollbackTarget      string
}

type siteDeploymentResult struct {
	ID               string                        `json:"id"`
	SiteID           string                        `json:"site_id"`
	Environment      string                        `json:"environment,omitempty"`
	Status           string                        `json:"status"`
	RouteProfile     string                        `json:"route_profile"`
	DeploymentTarget string                        `json:"deployment_target"`
	RoutingPolicy    string                        `json:"routing_policy,omitempty"`
	ResourceFailover bool                          `json:"resource_failover"`
	Active           bool                          `json:"active"`
	ManifestKey      string                        `json:"manifest_key,omitempty"`
	FileCount        int                           `json:"file_count,omitempty"`
	TotalSize        int64                         `json:"total_size,omitempty"`
	ProductionURL    string                        `json:"production_url,omitempty"`
	ProductionURLs   []string                      `json:"production_urls,omitempty"`
	PreviewURL       string                        `json:"preview_url,omitempty"`
	HybridEdge       *hybridEdgeDeploymentEvidence `json:"hybrid_edge,omitempty"`
}

type hybridEdgeDeploymentEvidence struct {
	WorkerName          string   `json:"worker_name,omitempty"`
	VersionID           string   `json:"version_id,omitempty"`
	Domains             []string `json:"domains,omitempty"`
	URLs                []string `json:"urls,omitempty"`
	CompatibilityDate   string   `json:"compatibility_date,omitempty"`
	AssetsSHA256        string   `json:"assets_sha256,omitempty"`
	CachePolicy         string   `json:"cache_policy,omitempty"`
	HeadersGenerated    bool     `json:"headers_generated,omitempty"`
	NotFoundHandling    string   `json:"not_found_handling,omitempty"`
	VerificationStatus  string   `json:"verification_status,omitempty"`
	VerifiedAt          string   `json:"verified_at,omitempty"`
	PublishedAt         string   `json:"published_at,omitempty"`
	KVNamespaceID       string   `json:"kv_namespace_id,omitempty"`
	KVNamespace         string   `json:"kv_namespace,omitempty"`
	KeyPrefix           string   `json:"key_prefix,omitempty"`
	ManifestSHA256      string   `json:"manifest_sha256,omitempty"`
	ManifestSize        int      `json:"manifest_size,omitempty"`
	ManifestMode        string   `json:"manifest_mode,omitempty"`
	DefaultCacheControl string   `json:"default_cache_control,omitempty"`
	EntryOriginFallback bool     `json:"entry_origin_fallback,omitempty"`
	ActiveKey           bool     `json:"active_key,omitempty"`
	DeploymentKey       bool     `json:"deployment_key,omitempty"`
}

type edgeManifestPublishResponse struct {
	SiteID            string                `json:"site_id"`
	DeploymentID      string                `json:"deployment_id"`
	Active            bool                  `json:"active"`
	CloudflareAccount string                `json:"cloudflare_account,omitempty"`
	CloudflareLibrary string                `json:"cloudflare_library,omitempty"`
	KVNamespaceID     string                `json:"kv_namespace_id,omitempty"`
	KVNamespace       string                `json:"kv_namespace,omitempty"`
	KeyPrefix         string                `json:"key_prefix"`
	Domains           []string              `json:"domains,omitempty"`
	DryRun            bool                  `json:"dry_run"`
	Status            string                `json:"status"`
	ManifestSize      int                   `json:"manifest_size"`
	ManifestSHA256    string                `json:"manifest_sha256"`
	Writes            []edgeManifestKVWrite `json:"writes,omitempty"`
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
	Size   int    `json:"size,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	Error  string `json:"error,omitempty"`
}

type hybridEdgeDeployResponse struct {
	Status       string                          `json:"status"`
	SiteID       string                          `json:"site_id"`
	DeploymentID string                          `json:"deployment_id"`
	URL          string                          `json:"url,omitempty"`
	URLs         []string                        `json:"urls,omitempty"`
	Deployment   siteDeploymentResult            `json:"deployment"`
	EdgeManifest edgeManifestPublishResponse     `json:"edge_manifest"`
	Worker       cloudflareStaticPublishResponse `json:"worker"`
	Verify       cloudflareStaticVerifyReport    `json:"verify"`
	Warnings     []string                        `json:"warnings,omitempty"`
	NextCommands []string                        `json:"next_commands,omitempty"`
}

type cloudflareStaticProviderWriteFailure struct {
	Status       string                          `json:"status"`
	SiteID       string                          `json:"site_id"`
	Warnings     []string                        `json:"warnings,omitempty"`
	Worker       cloudflareStaticPublishResponse `json:"worker"`
	Verify       cloudflareStaticVerifyReport    `json:"verify"`
	NextCommands []string                        `json:"next_commands,omitempty"`
}

func cloudflareVerifyFailureNextCommands(site, dir, target, profile string, domains []string, hybrid bool) []string {
	parts := []string{
		"supercdnctl deploy-site",
		"-site " + cliHintArg(site),
		"-dir " + cliHintArg(dir),
		"-target " + cliHintArg(target),
	}
	if strings.TrimSpace(profile) != "" {
		parts = append(parts, "-profile "+cliHintArg(profile))
	}
	cleanedDomains := cleanDomains(domains)
	if len(cleanedDomains) > 0 {
		parts = append(parts, "-domains "+cliHintArg(strings.Join(cleanedDomains, ",")))
	}
	commands := []string{strings.Join(parts, " ")}
	if len(cleanedDomains) > 0 {
		probe := []string{
			"supercdnctl probe-site",
			"-url " + cliHintArg("https://"+cleanedDomains[0]+"/"),
			"-require-edge-static-html",
		}
		if hybrid {
			probe = append(probe, "-require-edge-manifest-assets")
		} else {
			probe = append(probe, "-require-html-revalidate", "-require-immutable-assets")
		}
		commands = append(commands, strings.Join(probe, " "))
	}
	return commands
}

func deploySiteHybridEdge(c client, opts hybridEdgeDeploySiteOptions) error {
	raw, err := deploySiteHybridEdgeRaw(c, opts)
	if len(raw) > 0 {
		_ = printJSON(raw)
	}
	return err
}

func deploySiteHybridEdgeRaw(c client, opts hybridEdgeDeploySiteOptions) ([]byte, error) {
	if len(cleanDomains(opts.Domains)) == 0 {
		return nil, errors.New("hybrid_edge deploy-site requires at least one domain")
	}
	stats, err := summarizeCloudflareStaticDirectory(opts.Dir)
	if err != nil {
		return nil, err
	}
	dep, err := createAndWaitSiteDeployment(c, opts.Site, siteDeploymentUploadOptions{
		Dir:              opts.Dir,
		Environment:      opts.Environment,
		RouteProfile:     opts.RouteProfile,
		DeploymentTarget: opts.DeploymentTarget,
		RoutingPolicy:    opts.RoutingPolicy,
		ResourceFailover: opts.ResourceFailover,
		Promote:          opts.Promote,
		Pinned:           opts.Pinned,
		Timeout:          opts.Timeout,
	})
	if err != nil {
		return nil, err
	}
	routingPolicy := firstNonEmpty(dep.RoutingPolicy, opts.RoutingPolicy)
	resourceFailover := dep.ResourceFailover || opts.ResourceFailover
	if opts.CandidateWait && (strings.TrimSpace(routingPolicy) != "" || resourceFailover) {
		mode := "resource_failover"
		if strings.TrimSpace(routingPolicy) != "" {
			mode = "routing_policy"
		}
		report, err := c.waitEdgeManifestCandidates(edgeManifestCandidateWaitOptions{
			Site:          opts.Site,
			Deployment:    dep.ID,
			Mode:          mode,
			MinCandidates: 2,
			Timeout:       opts.CandidateTimeout,
		})
		if err != nil {
			raw, _ := json.Marshal(report)
			return raw, err
		}
	}
	edgeManifest, err := c.publishEdgeManifestForDeployment(edgeManifestPublishOptions{
		Site:          opts.Site,
		Deployment:    dep.ID,
		Domains:       opts.Domains,
		KVNamespaceID: opts.KVNamespaceID,
		KVNamespace:   opts.KVNamespace,
		ActiveKey:     dep.Active,
		DeploymentKey: true,
		DryRun:        false,
	})
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(edgeManifest.KVNamespaceID) == "" {
		return nil, errors.New("hybrid_edge publish-edge-manifest did not return a kv_namespace_id")
	}
	workerName := strings.TrimSpace(opts.WorkerName)
	if workerName == "" {
		workerName = "supercdn-" + cleanWorkerName(opts.Site) + "-edge"
	}
	publish, err := runHybridEdgePublish(hybridEdgePublishOptions{
		Site:                opts.Site,
		WorkerName:          workerName,
		Dir:                 opts.Dir,
		Domains:             opts.Domains,
		CompatibilityDate:   opts.CompatibilityDate,
		Message:             firstNonEmpty(opts.Message, "SuperCDN hybrid_edge deploy "+opts.Site),
		CachePolicy:         opts.CachePolicy,
		NotFoundHandling:    opts.NotFoundHandling,
		KVNamespaceID:       edgeManifest.KVNamespaceID,
		ManifestMode:        firstNonEmpty(opts.ManifestMode, "route"),
		DefaultCacheControl: firstNonEmpty(opts.DefaultCacheControl, "public, max-age=300"),
		EntryOriginFallback: opts.EntryOriginFallback,
		OriginBaseURL:       c.baseURL,
		EnvFile:             "configs/private/cloudflare.env",
		Wrangler:            "npx",
		WranglerPrefix:      "worker",
	})
	if err != nil {
		raw, _ := json.Marshal(publish)
		return raw, err
	}
	verify, err := verifyCloudflareStaticPublish(context.Background(), cloudflareStaticVerifyOptions{
		Mode:                        opts.VerifyMode,
		Domains:                     opts.Domains,
		Timeout:                     opts.VerifyTimeout,
		Interval:                    opts.VerifyInterval,
		SPAPath:                     opts.VerifySPAPath,
		Resolver:                    opts.VerifyResolver,
		NotFoundHandling:            publish.NotFoundHandling,
		RequireDirectAssets:         false,
		RequireEdgeStaticHTML:       true,
		RequireEdgeManifestAssets:   true,
		RequireGeneratedCachePolicy: publish.HeadersGenerated,
		RequireImmutableAssetCache:  false,
	})
	if err != nil {
		raw, _ := json.Marshal(hybridEdgeDeployResponse{
			Status:       "verify_failed_after_provider_write",
			SiteID:       opts.Site,
			DeploymentID: dep.ID,
			URL:          firstNonEmpty(dep.ProductionURL, firstString(dep.ProductionURLs)),
			URLs:         dep.ProductionURLs,
			Deployment:   dep,
			EdgeManifest: edgeManifest,
			Worker:       publish,
			Verify:       verify,
			Warnings: []string{
				"Super CDN deployment metadata, active Workers KV manifest or Worker/custom-domain state may already point at this deployment, but readiness verification timed out.",
				"Rerun deploy-site after DNS/custom-domain propagation settles, or run the probe command below to verify the live domain before treating the deployment as healthy.",
			},
			NextCommands: cloudflareVerifyFailureNextCommands(opts.Site, opts.Dir, "hybrid_edge", opts.RouteProfile, opts.Domains, true),
		})
		return raw, err
	}
	var warnings []string
	if strings.EqualFold(strings.TrimSpace(verify.Status), "ok") {
		recordedAt := time.Now().UTC().Format(time.RFC3339Nano)
		evidenceReq := map[string]any{
			"worker_name":                workerName,
			"version_id":                 extractCloudflareVersionID(publish.Output),
			"domains":                    opts.Domains,
			"compatibility_date":         opts.CompatibilityDate,
			"assets_sha256":              stats.SHA256,
			"cache_policy":               publish.CachePolicy,
			"headers_generated":          publish.HeadersGenerated,
			"not_found_handling":         publish.NotFoundHandling,
			"verification_status":        verify.Status,
			"verified_at_utc":            recordedAt,
			"published_at_utc":           recordedAt,
			"kv_namespace_id":            edgeManifest.KVNamespaceID,
			"kv_namespace":               edgeManifest.KVNamespace,
			"key_prefix":                 edgeManifest.KeyPrefix,
			"manifest_sha256":            edgeManifest.ManifestSHA256,
			"manifest_size":              edgeManifest.ManifestSize,
			"manifest_mode":              firstNonEmpty(opts.ManifestMode, "route"),
			"default_cache_control":      firstNonEmpty(opts.DefaultCacheControl, "public, max-age=300"),
			"entry_origin_fallback":      opts.EntryOriginFallback,
			"active_key":                 dep.Active,
			"deployment_key":             true,
			"operation":                  strings.TrimSpace(opts.Operation),
			"rollback_target_deployment": strings.TrimSpace(opts.RollbackTarget),
		}
		recordedRaw, err := c.recordHybridEdgeEvidence(opts.Site, dep.ID, evidenceReq)
		if err != nil {
			raw, _ := json.Marshal(hybridEdgeDeployResponse{
				Status:       "evidence_record_failed_after_provider_write",
				SiteID:       opts.Site,
				DeploymentID: dep.ID,
				URL:          firstNonEmpty(dep.ProductionURL, firstString(dep.ProductionURLs)),
				URLs:         dep.ProductionURLs,
				Deployment:   dep,
				EdgeManifest: edgeManifest,
				Worker:       publish,
				Verify:       verify,
				Warnings: []string{
					"Hybrid edge provider write and readiness verification completed, but provider evidence could not be recorded in Super CDN metadata.",
					"Do not treat rollback evidence as complete until the deployment response contains hybrid_edge Worker/KV/manifest evidence.",
				},
				NextCommands: []string{
					"supercdnctl deployment -site " + cliHintArg(opts.Site) + " -deployment " + cliHintArg(dep.ID),
					"supercdnctl reconcile-deployment -site " + cliHintArg(opts.Site) + " -deployment " + cliHintArg(dep.ID),
				},
			})
			return raw, err
		}
		var recorded siteDeploymentResult
		if err := json.Unmarshal(recordedRaw, &recorded); err == nil && strings.TrimSpace(recorded.ID) != "" {
			dep = recorded
		}
	} else {
		warnings = append(warnings, "Hybrid edge provider evidence was not recorded because readiness verification status was "+strings.TrimSpace(verify.Status)+"; rollback-plan will report hybrid_edge evidence as incomplete.")
	}
	resp := hybridEdgeDeployResponse{
		Status:       "ok",
		SiteID:       opts.Site,
		DeploymentID: dep.ID,
		URL:          firstNonEmpty(dep.ProductionURL, firstString(dep.ProductionURLs)),
		URLs:         dep.ProductionURLs,
		Deployment:   dep,
		EdgeManifest: edgeManifest,
		Worker:       publish,
		Verify:       verify,
		Warnings:     warnings,
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func (c client) recordHybridEdgeEvidence(site, deployment string, req map[string]any) ([]byte, error) {
	return c.doRaw(http.MethodPost, "/api/v1/sites/"+url.PathEscape(site)+"/deployments/"+url.PathEscape(deployment)+"/hybrid-edge/evidence", bytes.NewReader(mustJSON(req)), "application/json")
}

type siteDeploymentUploadOptions struct {
	Dir              string
	Environment      string
	RouteProfile     string
	DeploymentTarget string
	RoutingPolicy    string
	ResourceFailover bool
	Promote          bool
	Pinned           bool
	Timeout          time.Duration
}

func createAndWaitSiteDeployment(c client, site string, opts siteDeploymentUploadOptions) (siteDeploymentResult, error) {
	zipPath, err := zipDirectory(opts.Dir)
	if err != nil {
		return siteDeploymentResult{}, err
	}
	defer os.Remove(zipPath)
	fields := map[string]string{
		"route_profile":     opts.RouteProfile,
		"deployment_target": opts.DeploymentTarget,
		"routing_policy":    opts.RoutingPolicy,
		"resource_failover": fmt.Sprint(opts.ResourceFailover),
		"environment":       opts.Environment,
		"promote":           fmt.Sprint(opts.Promote),
		"pinned":            fmt.Sprint(opts.Pinned),
	}
	raw, err := c.uploadFileRaw("/api/v1/sites/"+url.PathEscape(site)+"/deployments", "artifact", zipPath, fields)
	if err != nil {
		return siteDeploymentResult{}, err
	}
	var created struct {
		DeploymentID string `json:"deployment_id"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		return siteDeploymentResult{}, err
	}
	if created.DeploymentID == "" {
		return siteDeploymentResult{}, errors.New("deployment response did not include deployment_id")
	}
	readyRaw, err := c.waitDeploymentRaw(site, created.DeploymentID, opts.Timeout)
	if err != nil {
		_ = printJSON(readyRaw)
		return siteDeploymentResult{}, err
	}
	var dep siteDeploymentResult
	if err := json.Unmarshal(readyRaw, &dep); err != nil {
		return siteDeploymentResult{}, err
	}
	return dep, nil
}

type edgeManifestPublishOptions struct {
	Site          string
	Deployment    string
	Domains       []string
	KVNamespaceID string
	KVNamespace   string
	ActiveKey     bool
	DeploymentKey bool
	DryRun        bool
}

func (c client) publishEdgeManifestForDeployment(opts edgeManifestPublishOptions) (edgeManifestPublishResponse, error) {
	req := map[string]any{
		"domains":         opts.Domains,
		"kv_namespace_id": opts.KVNamespaceID,
		"kv_namespace":    opts.KVNamespace,
		"active_key":      opts.ActiveKey,
		"deployment_key":  opts.DeploymentKey,
		"dry_run":         opts.DryRun,
	}
	raw, err := c.doRaw(http.MethodPost, "/api/v1/sites/"+url.PathEscape(opts.Site)+"/deployments/"+url.PathEscape(opts.Deployment)+"/edge-manifest/publish", bytes.NewReader(mustJSON(req)), "application/json")
	if err != nil {
		return edgeManifestPublishResponse{}, err
	}
	var resp edgeManifestPublishResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return edgeManifestPublishResponse{}, err
	}
	if resp.Status != "ok" && resp.Status != "planned" {
		return resp, fmt.Errorf("publish edge manifest status %q", resp.Status)
	}
	return resp, nil
}

type edgeManifestCandidateWaitOptions struct {
	Site          string
	Deployment    string
	Mode          string
	MinCandidates int
	Timeout       time.Duration
}

type edgeManifestCandidateWaitReport struct {
	Status           string                             `json:"status"`
	SiteID           string                             `json:"site_id,omitempty"`
	DeploymentID     string                             `json:"deployment_id,omitempty"`
	Mode             string                             `json:"mode"`
	MinCandidates    int                                `json:"min_candidates"`
	Attempts         int                                `json:"attempts"`
	RequiredRoutes   int                                `json:"required_routes"`
	ReadyRoutes      int                                `json:"ready_routes"`
	LastCheckedAtUTC string                             `json:"last_checked_at_utc,omitempty"`
	Routes           []edgeManifestCandidateRouteStatus `json:"routes,omitempty"`
	ManifestWarnings []string                           `json:"manifest_warnings,omitempty"`
	Warnings         []string                           `json:"warnings,omitempty"`
}

type edgeManifestCandidateRouteStatus struct {
	Path               string `json:"path"`
	Type               string `json:"type,omitempty"`
	Delivery           string `json:"delivery,omitempty"`
	File               string `json:"file,omitempty"`
	Status             int    `json:"status,omitempty"`
	Candidates         int    `json:"candidates"`
	ReadyCandidates    int    `json:"ready_candidates"`
	RequiredCandidates int    `json:"required_candidates"`
	OK                 bool   `json:"ok"`
	Message            string `json:"message,omitempty"`
}

type edgeManifestCandidateManifest struct {
	SiteID       string                                `json:"site_id"`
	DeploymentID string                                `json:"deployment_id"`
	Routes       map[string]edgeManifestCandidateRoute `json:"routes"`
	Warnings     []string                              `json:"warnings,omitempty"`
}

type edgeManifestCandidateRoute struct {
	Type       string                       `json:"type"`
	Delivery   string                       `json:"delivery"`
	File       string                       `json:"file"`
	Status     int                          `json:"status"`
	Candidates []edgeManifestCandidateEntry `json:"candidates,omitempty"`
}

type edgeManifestCandidateEntry struct {
	Target string `json:"target"`
	Type   string `json:"type,omitempty"`
	Status string `json:"status,omitempty"`
	URL    string `json:"url,omitempty"`
}

func (c client) waitEdgeManifestCandidates(opts edgeManifestCandidateWaitOptions) (edgeManifestCandidateWaitReport, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Minute
	}
	if opts.MinCandidates <= 0 {
		opts.MinCandidates = 2
	}
	deadline := time.Now().Add(opts.Timeout)
	var report edgeManifestCandidateWaitReport
	attempts := 0
	for {
		attempts++
		raw, err := c.doRaw(http.MethodGet, "/api/v1/sites/"+url.PathEscape(opts.Site)+"/deployments/"+url.PathEscape(opts.Deployment)+"/edge-manifest", nil, "")
		if err != nil {
			return report, err
		}
		report, err = edgeManifestCandidateReadiness(raw, opts.Mode, opts.MinCandidates)
		if err != nil {
			return report, err
		}
		report.Attempts = attempts
		if report.Status == "ok" {
			return report, nil
		}
		if time.Now().After(deadline) {
			if report.Status == "" {
				report.Status = "timeout"
			}
			return report, fmt.Errorf("edge manifest %s candidates did not become ready before timeout", opts.Mode)
		}
		time.Sleep(2 * time.Second)
	}
}

func edgeManifestCandidateReadiness(raw []byte, mode string, minCandidates int) (edgeManifestCandidateWaitReport, error) {
	if minCandidates <= 0 {
		minCandidates = 2
	}
	mode = strings.TrimSpace(mode)
	var manifest edgeManifestCandidateManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return edgeManifestCandidateWaitReport{}, err
	}
	report := edgeManifestCandidateWaitReport{
		Status:           "ok",
		SiteID:           manifest.SiteID,
		DeploymentID:     manifest.DeploymentID,
		Mode:             mode,
		MinCandidates:    minCandidates,
		LastCheckedAtUTC: time.Now().UTC().Format(time.RFC3339Nano),
		ManifestWarnings: manifest.Warnings,
	}
	paths := make([]string, 0, len(manifest.Routes))
	for pathValue := range manifest.Routes {
		paths = append(paths, pathValue)
	}
	sort.Strings(paths)
	for _, pathValue := range paths {
		route := manifest.Routes[pathValue]
		if !edgeManifestRouteNeedsCandidates(route) {
			continue
		}
		status := edgeManifestCandidateRouteStatus{
			Path:               pathValue,
			Type:               route.Type,
			Delivery:           route.Delivery,
			File:               route.File,
			Status:             route.Status,
			Candidates:         len(route.Candidates),
			ReadyCandidates:    edgeManifestReadyCandidateCount(route.Candidates),
			RequiredCandidates: minCandidates,
		}
		switch mode {
		case "routing_policy":
			status.OK = route.Type == "smart" && status.ReadyCandidates >= minCandidates
			if !status.OK {
				status.Message = fmt.Sprintf("expected smart route with at least %d ready candidates", minCandidates)
			}
		case "resource_failover":
			status.OK = route.Type == "failover" && status.ReadyCandidates >= minCandidates
			if !status.OK {
				status.Message = fmt.Sprintf("expected failover route with at least %d ready candidates", minCandidates)
			}
		default:
			status.OK = status.ReadyCandidates >= minCandidates
			if !status.OK {
				status.Message = fmt.Sprintf("expected at least %d ready candidates", minCandidates)
			}
		}
		report.RequiredRoutes++
		if status.OK {
			report.ReadyRoutes++
		} else {
			report.Status = "pending"
		}
		report.Routes = append(report.Routes, status)
	}
	if report.RequiredRoutes == 0 {
		report.Warnings = append(report.Warnings, "edge manifest has no non-entry resource routes that require candidates")
	}
	return report, nil
}

func edgeManifestRouteNeedsCandidates(route edgeManifestCandidateRoute) bool {
	if route.Type == "smart" || route.Type == "failover" {
		return true
	}
	if strings.EqualFold(route.Delivery, "origin") {
		return false
	}
	if route.Delivery == "" {
		return false
	}
	return !strings.EqualFold(route.File, "index.html")
}

func edgeManifestReadyCandidateCount(candidates []edgeManifestCandidateEntry) int {
	count := 0
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.URL) == "" {
			continue
		}
		if status := strings.TrimSpace(candidate.Status); status != "" && !strings.EqualFold(status, "ready") {
			continue
		}
		count++
	}
	return count
}

type hybridEdgePublishOptions struct {
	Site                string
	WorkerName          string
	Dir                 string
	Domains             []string
	CompatibilityDate   string
	Message             string
	CachePolicy         string
	NotFoundHandling    string
	KVNamespaceID       string
	ManifestMode        string
	DefaultCacheControl string
	EntryOriginFallback bool
	OriginBaseURL       string
	EnvFile             string
	Wrangler            string
	WranglerPrefix      string
}

func runHybridEdgePublish(opts hybridEdgePublishOptions) (cloudflareStaticPublishResponse, error) {
	if strings.TrimSpace(opts.Dir) == "" {
		return cloudflareStaticPublishResponse{}, errors.New("-dir is required")
	}
	absDir, err := filepath.Abs(opts.Dir)
	if err != nil {
		return cloudflareStaticPublishResponse{}, err
	}
	info, err := os.Stat(absDir)
	if err != nil {
		return cloudflareStaticPublishResponse{}, err
	}
	if !info.IsDir() {
		return cloudflareStaticPublishResponse{}, fmt.Errorf("%s is not a directory", opts.Dir)
	}
	preparedDir, cleanup, headers, err := prepareCloudflareStaticAssetsDir(absDir, opts.CachePolicy)
	if err != nil {
		return cloudflareStaticPublishResponse{}, err
	}
	if cleanup != nil {
		defer cleanup()
	}
	notFoundHandling, err := normalizeCloudflareStaticNotFoundHandling(opts.NotFoundHandling)
	if err != nil {
		return cloudflareStaticPublishResponse{}, err
	}
	workerName := strings.TrimSpace(opts.WorkerName)
	if workerName == "" {
		if strings.TrimSpace(opts.Site) == "" {
			return cloudflareStaticPublishResponse{}, errors.New("-site or -edge-name is required")
		}
		workerName = "supercdn-" + cleanWorkerName(opts.Site) + "-edge"
	}
	workerMain, err := filepath.Abs(filepath.Join("worker", "src", "index.ts"))
	if err != nil {
		return cloudflareStaticPublishResponse{}, err
	}
	wranglerConfig, configCleanup, err := writeHybridEdgeWranglerConfig(hybridEdgeWranglerConfigOptions{
		WorkerName:          workerName,
		WorkerMain:          workerMain,
		AssetsDir:           preparedDir,
		CompatibilityDate:   opts.CompatibilityDate,
		NotFoundHandling:    notFoundHandling,
		KVNamespaceID:       opts.KVNamespaceID,
		ManifestMode:        opts.ManifestMode,
		DefaultCacheControl: opts.DefaultCacheControl,
		EntryOriginFallback: opts.EntryOriginFallback,
		OriginBaseURL:       opts.OriginBaseURL,
	})
	if err != nil {
		return cloudflareStaticPublishResponse{}, err
	}
	defer configCleanup()
	wrangler := firstNonEmpty(strings.TrimSpace(opts.Wrangler), "npx")
	cmdArgs := wranglerDeployArgs(wrangler, opts.WranglerPrefix, workerName, preparedDir, opts.Domains, opts.CompatibilityDate, opts.Message, false, wranglerConfig)
	resp := cloudflareStaticPublishResponse{
		Status:            "planned",
		DryRun:            false,
		Worker:            workerName,
		AssetsDir:         preparedDir,
		SourceDir:         absDir,
		Domains:           opts.Domains,
		CompatibilityDate: strings.TrimSpace(opts.CompatibilityDate),
		CachePolicy:       headers.Policy,
		NotFoundHandling:  notFoundHandling,
		WranglerConfig:    wranglerConfig,
		HeadersFile:       headers.Path,
		HeadersSource:     headers.Source,
		HeadersGenerated:  headers.Generated,
		Command:           append([]string{wrangler}, cmdArgs...),
	}
	env, err := cloudflareStaticEnv(opts.EnvFile)
	if err != nil {
		return resp, err
	}
	out, exitCode, err := runCommand(wrangler, cmdArgs, env)
	resp.Output = strings.TrimSpace(out)
	resp.ExitCode = exitCode
	if err != nil {
		resp.Status = "failed"
		return resp, err
	}
	resp.Status = "ok"
	return resp, nil
}

type hybridEdgeWranglerConfigOptions struct {
	WorkerName          string
	WorkerMain          string
	AssetsDir           string
	CompatibilityDate   string
	NotFoundHandling    string
	KVNamespaceID       string
	ManifestMode        string
	DefaultCacheControl string
	EntryOriginFallback bool
	OriginBaseURL       string
}

func writeHybridEdgeWranglerConfig(opts hybridEdgeWranglerConfigOptions) (string, func(), error) {
	if strings.TrimSpace(opts.KVNamespaceID) == "" {
		return "", nil, errors.New("kv namespace id is required")
	}
	tmp, err := os.MkdirTemp("", "supercdn-hybrid-edge-wrangler-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	configPath := filepath.Join(tmp, "wrangler.toml")
	var b strings.Builder
	b.WriteString("name = " + tomlString(opts.WorkerName) + "\n")
	b.WriteString("main = " + tomlPathString(opts.WorkerMain) + "\n")
	b.WriteString("compatibility_date = " + tomlString(strings.TrimSpace(opts.CompatibilityDate)) + "\n\n")
	b.WriteString("[vars]\n")
	b.WriteString("ORIGIN_BASE_URL = " + tomlString(firstNonEmpty(opts.OriginBaseURL, "https://origin.example.com")) + "\n")
	b.WriteString("EDGE_DEFAULT_CACHE_CONTROL = " + tomlString(firstNonEmpty(opts.DefaultCacheControl, "public, max-age=300")) + "\n")
	b.WriteString("EDGE_ENTRY_ORIGIN_FALLBACK = " + tomlString(strconv.FormatBool(opts.EntryOriginFallback)) + "\n")
	b.WriteString("EDGE_MANIFEST_DRY_RUN = \"true\"\n")
	b.WriteString("EDGE_MANIFEST_KEY_PREFIX = \"sites/\"\n")
	b.WriteString("EDGE_MANIFEST_MODE = " + tomlString(firstNonEmpty(opts.ManifestMode, "route")) + "\n")
	b.WriteString("EDGE_ORIGIN_FALLBACK = \"false\"\n")
	b.WriteString("EDGE_STATIC_ASSETS = \"true\"\n\n")
	b.WriteString("[assets]\n")
	b.WriteString("directory = " + tomlPathString(opts.AssetsDir) + "\n")
	b.WriteString("binding = \"ASSETS\"\n")
	b.WriteString("run_worker_first = true\n")
	if strings.TrimSpace(opts.NotFoundHandling) != "" {
		b.WriteString("not_found_handling = " + tomlString(strings.TrimSpace(opts.NotFoundHandling)) + "\n")
	}
	b.WriteString("\n[[kv_namespaces]]\n")
	b.WriteString("binding = \"EDGE_MANIFEST\"\n")
	b.WriteString("id = " + tomlString(strings.TrimSpace(opts.KVNamespaceID)) + "\n")
	if err := os.WriteFile(configPath, []byte(b.String()), 0600); err != nil {
		cleanup()
		return "", nil, err
	}
	return configPath, cleanup, nil
}

type cloudflareStaticVerifyOptions struct {
	Mode                        string
	Domains                     []string
	Timeout                     time.Duration
	Interval                    time.Duration
	SPAPath                     string
	Resolver                    string
	NotFoundHandling            string
	RequireDirectAssets         bool
	RequireEdgeStaticHTML       bool
	RequireEdgeManifestAssets   bool
	RequireGeneratedCachePolicy bool
	RequireImmutableAssetCache  bool
}

type cloudflareStaticVerifyReport struct {
	Status   string             `json:"status"`
	Mode     string             `json:"mode"`
	Domains  []string           `json:"domains,omitempty"`
	Attempts int                `json:"attempts,omitempty"`
	Reports  []siteprobe.Report `json:"reports,omitempty"`
	Warnings []string           `json:"warnings,omitempty"`
	Errors   []string           `json:"errors,omitempty"`
}

func verifyCloudflareStaticPublish(ctx context.Context, opts cloudflareStaticVerifyOptions) (cloudflareStaticVerifyReport, error) {
	mode, err := normalizeCloudflareStaticVerifyMode(opts.Mode)
	if err != nil {
		return cloudflareStaticVerifyReport{}, err
	}
	domains := cleanDomains(opts.Domains)
	report := cloudflareStaticVerifyReport{Status: "planned", Mode: mode, Domains: domains}
	if mode == cloudflareStaticVerifyNone {
		report.Status = "skipped"
		return report, nil
	}
	if len(domains) == 0 {
		report.Status = "skipped"
		report.Warnings = append(report.Warnings, "no custom domains to verify")
		return report, nil
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	interval := opts.Interval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	spaPath := strings.TrimSpace(opts.SPAPath)
	if spaPath == "" && opts.NotFoundHandling == cloudflareStaticNotFoundSPA {
		spaPath = "/__supercdn_spa_probe"
	}
	httpClient, err := httpClientWithDNSResolver(opts.Resolver)
	if err != nil {
		return cloudflareStaticVerifyReport{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var last []siteprobe.Report
	for {
		report.Attempts++
		last = probeCloudflareStaticDomains(ctx, domains, siteprobe.Options{
			SPAPath:                    spaPath,
			MaxAssets:                  20,
			Timeout:                    30 * time.Second,
			Client:                     httpClient,
			RequireDirectAssets:        opts.RequireDirectAssets,
			RequireEdgeStaticHTML:      opts.RequireEdgeStaticHTML,
			RequireEdgeManifestAssets:  opts.RequireEdgeManifestAssets,
			RequireHTMLRevalidate:      opts.RequireGeneratedCachePolicy,
			RequireImmutableAssetCache: opts.RequireImmutableAssetCache,
		})
		if cloudflareStaticReportsOK(last) {
			report.Status = "ok"
			report.Reports = last
			return report, nil
		}
		if mode == cloudflareStaticVerifyWarn {
			report.Status = "warning"
			report.Reports = last
			report.Warnings = append(report.Warnings, "Cloudflare Static readiness probe failed; deployment will still be recorded")
			_, _ = fmt.Fprintln(os.Stderr, "warning: Cloudflare Static readiness probe failed; continuing because -static-verify=warn")
			return report, nil
		}
		select {
		case <-ctx.Done():
			report.Status = "failed"
			report.Reports = last
			report.Errors = append(report.Errors, "Cloudflare Static readiness probe did not pass before timeout")
			return report, errors.New("cloudflare static readiness probe failed")
		case <-time.After(interval):
		}
	}
}

func probeCloudflareStaticDomains(ctx context.Context, domains []string, opts siteprobe.Options) []siteprobe.Report {
	reports := make([]siteprobe.Report, 0, len(domains))
	for _, domain := range domains {
		probeOpts := opts
		probeOpts.URL = "https://" + domain + "/"
		report, err := siteprobe.Run(ctx, probeOpts)
		if err != nil {
			report = siteprobe.Report{
				OK:      false,
				Status:  "failed",
				URL:     probeOpts.URL,
				Errors:  []string{err.Error()},
				Summary: map[string]int{},
			}
		}
		reports = append(reports, redactSignedProbeReport(report))
	}
	return reports
}

func cloudflareStaticReportsOK(reports []siteprobe.Report) bool {
	if len(reports) == 0 {
		return false
	}
	for _, report := range reports {
		if !report.OK {
			return false
		}
	}
	return true
}

func normalizeCloudflareStaticVerifyMode(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", cloudflareStaticVerifyWait:
		return cloudflareStaticVerifyWait, nil
	case cloudflareStaticVerifyWarn:
		return cloudflareStaticVerifyWarn, nil
	case cloudflareStaticVerifyNone:
		return cloudflareStaticVerifyNone, nil
	default:
		return "", fmt.Errorf("static-verify must be wait, warn, or none")
	}
}

func cleanDomains(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		host := strings.ToLower(strings.TrimSpace(value))
		host = strings.TrimPrefix(host, "https://")
		host = strings.TrimPrefix(host, "http://")
		host = strings.Trim(host, "/")
		if host == "" || seen[host] {
			continue
		}
		seen[host] = true
		out = append(out, host)
	}
	return out
}

func promoteDeployment(c client, args []string) error {
	fs := flag.NewFlagSet("promote-deployment", flag.ExitOnError)
	site := fs.String("site", "", "site id")
	deployment := fs.String("deployment", "", "deployment id")
	_ = fs.Parse(args)
	if *site == "" || *deployment == "" {
		return errors.New("-site and -deployment are required")
	}
	return c.doJSON(http.MethodPost, "/api/v1/sites/"+url.PathEscape(*site)+"/deployments/"+url.PathEscape(*deployment)+"/promote", map[string]any{})
}

func deleteDeployment(c client, args []string) error {
	fs := flag.NewFlagSet("delete-deployment", flag.ExitOnError)
	site := fs.String("site", "", "site id")
	deployment := fs.String("deployment", "", "deployment id")
	deleteObjects := fs.Bool("delete-objects", false, "delete tracked deployment objects before deleting deployment metadata")
	deleteRemote := fs.Bool("delete-remote", true, "delete remote object replicas when -delete-objects is set")
	dryRun := fs.Bool("dry-run", false, "plan deletion without modifying metadata or remote objects")
	_ = fs.Parse(args)
	if *site == "" || *deployment == "" {
		return errors.New("-site and -deployment are required")
	}
	if *dryRun {
		return deleteDeploymentDryRun(c, *site, *deployment, *deleteObjects, *deleteRemote)
	}
	q := url.Values{}
	q.Set("delete_objects", fmt.Sprint(*deleteObjects))
	q.Set("delete_remote", fmt.Sprint(*deleteRemote))
	return c.do(http.MethodDelete, "/api/v1/sites/"+url.PathEscape(*site)+"/deployments/"+url.PathEscape(*deployment)+"?"+q.Encode(), nil, "")
}

type deleteDeploymentPlanDeployment struct {
	ID               string                                `json:"id"`
	SiteID           string                                `json:"site_id"`
	Status           string                                `json:"status"`
	DeploymentTarget string                                `json:"deployment_target"`
	Active           bool                                  `json:"active"`
	Pinned           bool                                  `json:"pinned"`
	FileCount        int                                   `json:"file_count,omitempty"`
	ArtifactSHA256   string                                `json:"artifact_sha256,omitempty"`
	ManifestKey      string                                `json:"manifest_key,omitempty"`
	CloudflareStatic *rollbackPlanCloudflareStaticEvidence `json:"cloudflare_static,omitempty"`
	HybridEdge       *hybridEdgeDeploymentEvidence         `json:"hybrid_edge,omitempty"`
}

type deleteDeploymentPlanOutput struct {
	SiteID                 string   `json:"site_id"`
	DeploymentID           string   `json:"deployment_id"`
	Status                 string   `json:"status,omitempty"`
	Target                 string   `json:"deployment_target,omitempty"`
	Active                 bool     `json:"active"`
	Pinned                 bool     `json:"pinned"`
	DeleteObjects          bool     `json:"delete_objects"`
	DeleteRemote           bool     `json:"delete_remote"`
	SafeToRun              bool     `json:"safe_to_run"`
	RemoteCleanupSupported bool     `json:"remote_cleanup_supported"`
	RemoteCleanupBlockers  []string `json:"remote_cleanup_blockers,omitempty"`
	Warnings               []string `json:"warnings,omitempty"`
	NextCommands           []string `json:"next_commands,omitempty"`
	Evidence               struct {
		FileCount        int                                   `json:"file_count,omitempty"`
		ArtifactSHA256   string                                `json:"artifact_sha256,omitempty"`
		ManifestKey      string                                `json:"manifest_key,omitempty"`
		CloudflareStatic *rollbackPlanCloudflareStaticEvidence `json:"cloudflare_static,omitempty"`
		HybridEdge       *hybridEdgeDeploymentEvidence         `json:"hybrid_edge,omitempty"`
	} `json:"evidence"`
}

func deleteDeploymentDryRun(c client, site, deployment string, deleteObjects, deleteRemote bool) error {
	raw, err := c.doRaw(http.MethodGet, "/api/v1/sites/"+url.PathEscape(site)+"/deployments/"+url.PathEscape(deployment), nil, "")
	if err != nil {
		return err
	}
	var dep deleteDeploymentPlanDeployment
	if err := json.Unmarshal(raw, &dep); err != nil {
		return fmt.Errorf("parse deployment: %w", err)
	}
	if strings.TrimSpace(dep.SiteID) == "" {
		dep.SiteID = site
	}
	if strings.TrimSpace(dep.ID) == "" {
		dep.ID = deployment
	}
	plan := buildDeleteDeploymentPlan(dep, deleteObjects, deleteRemote)
	return printJSON(mustJSON(plan))
}

func buildDeleteDeploymentPlan(dep deleteDeploymentPlanDeployment, deleteObjects, deleteRemote bool) deleteDeploymentPlanOutput {
	target := deploymentTargetAlias(dep.DeploymentTarget)
	if target == "" {
		target = dep.DeploymentTarget
	}
	out := deleteDeploymentPlanOutput{
		SiteID:        dep.SiteID,
		DeploymentID:  dep.ID,
		Status:        dep.Status,
		Target:        target,
		Active:        dep.Active,
		Pinned:        dep.Pinned,
		DeleteObjects: deleteObjects,
		DeleteRemote:  deleteRemote,
		SafeToRun:     true,
	}
	out.Evidence.FileCount = dep.FileCount
	out.Evidence.ArtifactSHA256 = dep.ArtifactSHA256
	out.Evidence.ManifestKey = dep.ManifestKey
	out.Evidence.CloudflareStatic = dep.CloudflareStatic
	out.Evidence.HybridEdge = dep.HybridEdge
	out.RemoteCleanupSupported = deleteObjects && deleteRemote
	if dep.Active {
		out.SafeToRun = false
		out.Warnings = append(out.Warnings, "active production deployment cannot be deleted")
	}
	if dep.Pinned {
		out.SafeToRun = false
		out.Warnings = append(out.Warnings, "pinned deployment cannot be deleted")
	}
	if target == "cloudflare_static" || target == "hybrid_edge" {
		out.RemoteCleanupSupported = false
		out.RemoteCleanupBlockers = cloudflareDeleteRemoteCleanupBlockers(target)
		out.Warnings = append(out.Warnings, "delete-deployment removes Super CDN metadata only; Cloudflare Worker versions, custom domains and KV entries are not deleted")
	}
	if out.SafeToRun {
		out.NextCommands = append(out.NextCommands, deleteDeploymentApplyCommand(dep.SiteID, dep.ID, deleteObjects, deleteRemote))
	}
	return out
}

func cloudflareDeleteRemoteCleanupBlockers(target string) []string {
	blockers := []string{
		"delete-deployment does not delete Cloudflare Worker versions, custom domains or KV entries",
		"remote Cloudflare cleanup requires an operator to verify no active deployment, Worker route, custom domain or KV key still references the resources",
	}
	if target == "hybrid_edge" {
		blockers = append(blockers, "hybrid_edge cleanup must verify both deployment and active KV manifest keys before deleting KV entries")
	}
	return blockers
}

func deleteDeploymentApplyCommand(site, deployment string, deleteObjects, deleteRemote bool) string {
	parts := []string{
		"supercdnctl delete-deployment",
		"-site " + cliHintArg(site),
		"-deployment " + cliHintArg(deployment),
	}
	if deleteObjects {
		parts = append(parts, "-delete-objects")
	}
	if !deleteRemote {
		parts = append(parts, "-delete-remote=false")
	}
	return strings.Join(parts, " ")
}
