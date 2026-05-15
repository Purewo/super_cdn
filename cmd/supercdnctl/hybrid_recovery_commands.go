package main

import (
	"encoding/json"
	"errors"
	"flag"
	"strings"
	"time"

	"supercdn/internal/deploymentevidence"
	"supercdn/internal/siteprobe"
)

type hybridEdgeRecoveryReport struct {
	Status          string                         `json:"status"`
	DryRun          bool                           `json:"dry_run"`
	WriteReady      bool                           `json:"write_ready"`
	WriteSupported  bool                           `json:"write_supported"`
	SiteID          string                         `json:"site_id"`
	DeploymentID    string                         `json:"deployment_id"`
	Source          cloudflareStaticRecoverySource `json:"source"`
	Provider        hybridEdgeRecoveryProvider     `json:"provider"`
	EdgeManifest    *edgeManifestPublishResponse   `json:"edge_manifest,omitempty"`
	ProbeURL        string                         `json:"probe_url,omitempty"`
	Probe           *siteprobe.Report              `json:"probe,omitempty"`
	Deployment      json.RawMessage                `json:"deployment,omitempty"`
	MissingEvidence []string                       `json:"missing_evidence,omitempty"`
	Warnings        []string                       `json:"warnings,omitempty"`
	NextCommands    []string                       `json:"next_commands,omitempty"`
}

type hybridEdgeRecoveryProvider struct {
	WorkerName          string   `json:"worker_name,omitempty"`
	VersionID           string   `json:"version_id,omitempty"`
	Domains             []string `json:"domains,omitempty"`
	CompatibilityDate   string   `json:"compatibility_date,omitempty"`
	CachePolicy         string   `json:"cache_policy,omitempty"`
	NotFoundHandling    string   `json:"not_found_handling,omitempty"`
	KVNamespaceID       string   `json:"kv_namespace_id,omitempty"`
	KVNamespace         string   `json:"kv_namespace,omitempty"`
	KeyPrefix           string   `json:"key_prefix,omitempty"`
	ManifestSHA256      string   `json:"manifest_sha256,omitempty"`
	ManifestSize        int      `json:"manifest_size,omitempty"`
	ManifestMode        string   `json:"manifest_mode,omitempty"`
	DefaultCacheControl string   `json:"default_cache_control,omitempty"`
	EntryOriginFallback bool     `json:"entry_origin_fallback,omitempty"`
	ActiveKey           bool     `json:"active_key"`
	DeploymentKey       bool     `json:"deployment_key"`
}

func recoverHybridEdge(c client, args []string) error {
	fs := flag.NewFlagSet("recover-hybrid-edge", flag.ExitOnError)
	site := fs.String("site", "", "site id")
	deployment := fs.String("deployment", "", "deployment id whose hybrid_edge evidence should be recorded")
	dir := fs.String("dir", "", "source dist directory used by the provider write")
	worker := fs.String("worker-name", "", "Cloudflare Worker name from the provider write")
	versionID := fs.String("version-id", "", "Cloudflare Worker version id from the provider write, when available")
	domains := fs.String("domains", "", "comma-separated custom domains from the provider write")
	probeURL := fs.String("url", "", "explicit public URL to probe; defaults to the first domain")
	compatDate := fs.String("compatibility-date", time.Now().UTC().Format("2006-01-02"), "Workers compatibility date from the provider write")
	cachePolicy := fs.String("static-cache-policy", cloudflareStaticCachePolicyAuto, "Cloudflare Static cache policy: auto, force, or none")
	notFoundHandling := fs.String("static-not-found-handling", "", "Cloudflare Static not_found_handling: none, 404-page, or single-page-application")
	spa := fs.Bool("static-spa", false, "record Cloudflare Static single-page-application fallback")
	entryOriginFallback := fs.Bool("entry-origin-fallback", false, "record explicit hybrid_edge entry origin fallback")
	kvNamespaceID := fs.String("edge-kv-namespace-id", "", "Cloudflare Workers KV namespace id for hybrid_edge edge manifests")
	kvNamespace := fs.String("edge-kv-namespace", "supercdn-edge-manifest", "Cloudflare Workers KV namespace title for hybrid_edge edge manifests")
	manifestMode := fs.String("edge-manifest-mode", "route", "hybrid_edge Worker manifest mode: route or enforce")
	defaultCacheControl := fs.String("edge-default-cache-control", "public, max-age=300", "default Cache-Control for hybrid_edge Worker fallback responses")
	spaPath := fs.String("spa-path", "", "optional SPA route path to verify as HTML")
	resolver := fs.String("resolver", "", "DNS resolver for HTTP probes, for example 1.1.1.1:53")
	maxAssets := fs.Int("max-assets", 20, "maximum JS/CSS assets to probe from index HTML")
	timeout := fs.Duration("timeout", 30*time.Second, "overall probe timeout")
	dryRun := fs.Bool("dry-run", true, "validate hybrid evidence without writing metadata")
	confirm := fs.String("confirm", "", "must be recover for writes")
	_ = fs.Parse(args)

	report := hybridEdgeRecoveryReport{
		Status:         "planned",
		DryRun:         *dryRun,
		WriteSupported: true,
		SiteID:         strings.TrimSpace(*site),
		DeploymentID:   strings.TrimSpace(*deployment),
		Provider: hybridEdgeRecoveryProvider{
			WorkerName:          strings.TrimSpace(*worker),
			VersionID:           strings.TrimSpace(*versionID),
			Domains:             cleanDomains(splitCSV(*domains)),
			CompatibilityDate:   strings.TrimSpace(*compatDate),
			CachePolicy:         strings.TrimSpace(*cachePolicy),
			NotFoundHandling:    cloudflareStaticNotFoundHandlingFlag(*notFoundHandling, *spa),
			KVNamespaceID:       strings.TrimSpace(*kvNamespaceID),
			KVNamespace:         strings.TrimSpace(*kvNamespace),
			ManifestMode:        strings.TrimSpace(*manifestMode),
			DefaultCacheControl: strings.TrimSpace(*defaultCacheControl),
			EntryOriginFallback: *entryOriginFallback,
			DeploymentKey:       true,
		},
	}
	if report.SiteID == "" || report.DeploymentID == "" {
		report.Status = "blocked"
		report.MissingEvidence = hybridEdgeRecoveryMissingEvidence(report)
		_ = printJSON(mustJSON(report))
		return errors.New("hybrid edge recovery target is incomplete")
	}
	dep, err := fetchRollbackPlanDeployment(c, report.SiteID, report.DeploymentID)
	if err != nil {
		report.Status = "blocked"
		report.Warnings = append(report.Warnings, err.Error())
		_ = printJSON(mustJSON(report))
		return err
	}
	if deploymentTargetAlias(dep.DeploymentTarget) != "hybrid_edge" {
		report.Status = "blocked"
		report.Warnings = append(report.Warnings, "deployment target must be hybrid_edge")
		_ = printJSON(mustJSON(report))
		return errors.New("deployment target must be hybrid_edge")
	}
	if dep.Status != "" && dep.Status != "ready" && dep.Status != "active" {
		report.Status = "blocked"
		report.Warnings = append(report.Warnings, "deployment status is "+dep.Status)
		_ = printJSON(mustJSON(report))
		return errors.New("deployment is not ready")
	}
	if report.Provider.WorkerName == "" {
		report.Provider.WorkerName = rollbackHybridEdgeWorkerName(dep)
	}
	if len(report.Provider.Domains) == 0 {
		report.Provider.Domains = rollbackCloudflareStaticDomains(dep)
	}
	report.Provider.ActiveKey = dep.Active
	if err := populateHybridEdgeRecoverySource(&report, strings.TrimSpace(*dir)); err != nil {
		report.Status = "blocked"
		report.Warnings = append(report.Warnings, err.Error())
	}
	report.ProbeURL = strings.TrimSpace(*probeURL)
	if report.ProbeURL == "" && len(report.Provider.Domains) > 0 {
		report.ProbeURL = "https://" + report.Provider.Domains[0] + "/"
	}
	edgeManifest, err := c.publishEdgeManifestForDeployment(edgeManifestPublishOptions{
		Site:          report.SiteID,
		Deployment:    report.DeploymentID,
		Domains:       report.Provider.Domains,
		KVNamespaceID: report.Provider.KVNamespaceID,
		KVNamespace:   report.Provider.KVNamespace,
		ActiveKey:     dep.Active,
		DeploymentKey: true,
		DryRun:        true,
	})
	if err != nil {
		report.Status = "blocked"
		report.Warnings = append(report.Warnings, err.Error())
		_ = printJSON(mustJSON(report))
		return err
	}
	report.EdgeManifest = &edgeManifest
	report.Provider.KVNamespaceID = strings.TrimSpace(edgeManifest.KVNamespaceID)
	report.Provider.KVNamespace = strings.TrimSpace(edgeManifest.KVNamespace)
	report.Provider.KeyPrefix = strings.TrimSpace(edgeManifest.KeyPrefix)
	report.Provider.ManifestSHA256 = strings.TrimSpace(edgeManifest.ManifestSHA256)
	report.Provider.ManifestSize = edgeManifest.ManifestSize
	report.MissingEvidence = hybridEdgeRecoveryMissingEvidence(report)
	if len(report.MissingEvidence) > 0 {
		report.Status = "blocked"
		_ = printJSON(mustJSON(report))
		return errors.New("hybrid edge recovery evidence is incomplete")
	}
	if !*dryRun {
		if strings.TrimSpace(*confirm) != "recover" {
			report.Status = "blocked"
			report.Warnings = append(report.Warnings, "hybrid edge recovery writes require -confirm recover")
			_ = printJSON(mustJSON(report))
			return errors.New("hybrid edge recovery requires confirmation")
		}
		if strings.TrimSpace(c.token) == "" {
			report.Status = "blocked"
			report.Warnings = append(report.Warnings, "token is required for recovery writes; pass -token, SUPERCDN_TOKEN, or use a saved profile")
			_ = printJSON(mustJSON(report))
			return errors.New("token is required for hybrid edge recovery writes")
		}
	}
	probe, err := runSiteProbe(*resolver, siteprobe.Options{
		URL:                       report.ProbeURL,
		SPAPath:                   *spaPath,
		MaxAssets:                 *maxAssets,
		Timeout:                   *timeout,
		RequireEdgeStaticHTML:     true,
		RequireEdgeManifestAssets: true,
	})
	if err != nil {
		report.Status = "blocked"
		report.Warnings = append(report.Warnings, err.Error())
		_ = printJSON(mustJSON(report))
		return err
	}
	probe = redactSignedProbeReport(probe)
	report.Probe = &probe
	report.NextCommands = hybridEdgeRecoveryNextCommands(report)
	if !probe.OK {
		report.Status = "blocked"
		report.Warnings = append(report.Warnings, "strict hybrid_edge probe did not pass; metadata recovery must not write")
		_ = printJSON(mustJSON(report))
		return errors.New("hybrid edge recovery probe failed")
	}
	report.WriteReady = true
	if *dryRun {
		report.Status = "verified"
		return printJSON(mustJSON(report))
	}
	recordedAt := time.Now().UTC().Format(time.RFC3339Nano)
	req := map[string]any{
		"worker_name":                report.Provider.WorkerName,
		"version_id":                 report.Provider.VersionID,
		"domains":                    report.Provider.Domains,
		"compatibility_date":         report.Provider.CompatibilityDate,
		"assets_sha256":              report.Source.AssetsSHA256,
		"cache_policy":               report.Source.HeadersPolicy,
		"headers_generated":          report.Source.HeadersGenerated,
		"not_found_handling":         report.Provider.NotFoundHandling,
		"verification_status":        "ok",
		"verified_at_utc":            recordedAt,
		"published_at_utc":           recordedAt,
		"kv_namespace_id":            report.Provider.KVNamespaceID,
		"kv_namespace":               report.Provider.KVNamespace,
		"key_prefix":                 report.Provider.KeyPrefix,
		"manifest_sha256":            report.Provider.ManifestSHA256,
		"manifest_size":              report.Provider.ManifestSize,
		"manifest_mode":              report.Provider.ManifestMode,
		"default_cache_control":      report.Provider.DefaultCacheControl,
		"entry_origin_fallback":      report.Provider.EntryOriginFallback,
		"active_key":                 report.Provider.ActiveKey,
		"deployment_key":             report.Provider.DeploymentKey,
		"operation":                  deploymentevidence.OperationWriteback,
		"rollback_target_deployment": "",
	}
	raw, err := c.recordHybridEdgeEvidence(report.SiteID, report.DeploymentID, req)
	if err != nil {
		report.Status = "blocked"
		report.Warnings = append(report.Warnings, err.Error())
		_ = printJSON(mustJSON(report))
		return err
	}
	report.Status = "recorded"
	report.Deployment = json.RawMessage(raw)
	return printJSON(mustJSON(report))
}

func populateHybridEdgeRecoverySource(report *hybridEdgeRecoveryReport, dir string) error {
	report.Source.Dir = dir
	if dir == "" {
		return nil
	}
	summary, err := summarizeCloudflareStaticDirectory(dir)
	if err != nil {
		return err
	}
	_, cleanup, headers, err := prepareCloudflareStaticAssetsDir(dir, report.Provider.CachePolicy)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return err
	}
	report.Source.FileCount = summary.FileCount
	report.Source.TotalSize = summary.TotalSize
	report.Source.AssetsSHA256 = summary.SHA256
	report.Source.HeadersPolicy = headers.Policy
	report.Source.HeadersSource = headers.Source
	report.Source.HeadersGenerated = headers.Generated
	return nil
}

func hybridEdgeRecoveryMissingEvidence(report hybridEdgeRecoveryReport) []string {
	var missing []string
	if strings.TrimSpace(report.SiteID) == "" {
		missing = append(missing, "site")
	}
	if strings.TrimSpace(report.DeploymentID) == "" {
		missing = append(missing, "deployment")
	}
	if strings.TrimSpace(report.Source.Dir) == "" {
		missing = append(missing, "source_dir")
	}
	if report.Source.FileCount <= 0 {
		missing = append(missing, "source_summary")
	}
	if strings.TrimSpace(report.Provider.WorkerName) == "" {
		missing = append(missing, "worker_name")
	}
	if len(report.Provider.Domains) == 0 {
		missing = append(missing, "domains")
	}
	if strings.TrimSpace(report.ProbeURL) == "" {
		missing = append(missing, "probe_url")
	}
	if strings.TrimSpace(report.Provider.KVNamespaceID) == "" {
		missing = append(missing, "kv_namespace_id")
	}
	if strings.TrimSpace(report.Provider.ManifestSHA256) == "" {
		missing = append(missing, "manifest_sha256")
	}
	if report.Provider.ManifestSize <= 0 {
		missing = append(missing, "manifest_size")
	}
	return missing
}

func hybridEdgeRecoveryNextCommands(report hybridEdgeRecoveryReport) []string {
	probe := []string{
		"supercdnctl probe-site",
		"-url " + cliHintArg(report.ProbeURL),
		"-require-edge-static-html",
		"-require-edge-manifest-assets",
	}
	recover := []string{
		"supercdnctl recover-hybrid-edge",
		"-site " + cliHintArg(report.SiteID),
		"-deployment " + cliHintArg(report.DeploymentID),
		"-dir " + cliHintArg(report.Source.Dir),
		"-worker-name " + cliHintArg(report.Provider.WorkerName),
	}
	if report.Provider.VersionID != "" {
		recover = append(recover, "-version-id "+cliHintArg(report.Provider.VersionID))
	}
	if len(report.Provider.Domains) > 0 {
		recover = append(recover, "-domains "+cliHintArg(strings.Join(report.Provider.Domains, ",")))
	}
	if strings.TrimSpace(report.ProbeURL) != "" {
		recover = append(recover, "-url "+cliHintArg(report.ProbeURL))
	}
	if report.Provider.KVNamespaceID != "" {
		recover = append(recover, "-edge-kv-namespace-id "+cliHintArg(report.Provider.KVNamespaceID))
	}
	recover = append(recover, "-dry-run=false", "-confirm recover")
	return []string{
		strings.Join(probe, " "),
		strings.Join(recover, " "),
	}
}
