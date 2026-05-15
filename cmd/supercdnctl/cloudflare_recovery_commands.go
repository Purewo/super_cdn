package main

import (
	"errors"
	"flag"
	"strings"
	"time"

	"supercdn/internal/siteprobe"
)

type cloudflareStaticRecoveryReport struct {
	Status          string                           `json:"status"`
	DryRun          bool                             `json:"dry_run"`
	WriteReady      bool                             `json:"write_ready"`
	WriteSupported  bool                             `json:"write_supported"`
	SiteID          string                           `json:"site_id"`
	Source          cloudflareStaticRecoverySource   `json:"source"`
	Provider        cloudflareStaticRecoveryProvider `json:"provider"`
	ProbeURL        string                           `json:"probe_url,omitempty"`
	Probe           *siteprobe.Report                `json:"probe,omitempty"`
	MissingEvidence []string                         `json:"missing_evidence,omitempty"`
	Warnings        []string                         `json:"warnings,omitempty"`
	NextCommands    []string                         `json:"next_commands,omitempty"`
}

type cloudflareStaticRecoverySource struct {
	Dir              string `json:"dir,omitempty"`
	FileCount        int    `json:"file_count,omitempty"`
	TotalSize        int64  `json:"total_size,omitempty"`
	AssetsSHA256     string `json:"assets_sha256,omitempty"`
	HeadersPolicy    string `json:"headers_policy,omitempty"`
	HeadersSource    string `json:"headers_source,omitempty"`
	HeadersGenerated bool   `json:"headers_generated,omitempty"`
}

type cloudflareStaticRecoveryProvider struct {
	WorkerName        string   `json:"worker_name,omitempty"`
	VersionID         string   `json:"version_id,omitempty"`
	Domains           []string `json:"domains,omitempty"`
	CompatibilityDate string   `json:"compatibility_date,omitempty"`
	CachePolicy       string   `json:"cache_policy,omitempty"`
	NotFoundHandling  string   `json:"not_found_handling,omitempty"`
}

func recoverCloudflareStatic(c client, args []string) error {
	_ = c
	fs := flag.NewFlagSet("recover-cloudflare-static", flag.ExitOnError)
	site := fs.String("site", "", "site id")
	dir := fs.String("dir", "", "source dist directory")
	worker := fs.String("worker-name", "", "Cloudflare Worker name from the provider write")
	versionID := fs.String("version-id", "", "Cloudflare Worker version id from the provider write, when available")
	domains := fs.String("domains", "", "comma-separated custom domains from the provider write")
	probeURL := fs.String("url", "", "explicit public URL to probe; defaults to the first domain")
	compatDate := fs.String("compatibility-date", time.Now().UTC().Format("2006-01-02"), "Workers compatibility date from the provider write")
	cachePolicy := fs.String("static-cache-policy", cloudflareStaticCachePolicyAuto, "Cloudflare Static cache policy: auto, force, or none")
	notFoundHandling := fs.String("static-not-found-handling", "", "Cloudflare Static not_found_handling: none, 404-page, or single-page-application")
	spa := fs.Bool("static-spa", false, "enable Cloudflare Static single-page-application fallback")
	spaPath := fs.String("spa-path", "", "optional SPA route path to verify as HTML")
	resolver := fs.String("resolver", "", "DNS resolver for HTTP probes, for example 1.1.1.1:53")
	maxAssets := fs.Int("max-assets", 20, "maximum JS/CSS assets to probe from index HTML")
	timeout := fs.Duration("timeout", 30*time.Second, "overall probe timeout")
	dryRun := fs.Bool("dry-run", true, "validate recovery evidence without writing metadata")
	confirm := fs.String("confirm", "", "must be recover when future write support is enabled")
	_ = fs.Parse(args)

	report := cloudflareStaticRecoveryReport{
		Status:         "planned",
		DryRun:         *dryRun,
		WriteSupported: false,
		SiteID:         strings.TrimSpace(*site),
		Provider: cloudflareStaticRecoveryProvider{
			WorkerName:        strings.TrimSpace(*worker),
			VersionID:         strings.TrimSpace(*versionID),
			Domains:           cleanDomains(splitCSV(*domains)),
			CompatibilityDate: strings.TrimSpace(*compatDate),
			CachePolicy:       strings.TrimSpace(*cachePolicy),
			NotFoundHandling:  cloudflareStaticNotFoundHandlingFlag(*notFoundHandling, *spa),
		},
	}
	if report.Provider.WorkerName == "" && report.SiteID != "" {
		report.Provider.WorkerName = "supercdn-" + cleanWorkerName(report.SiteID) + "-static"
	}
	if err := populateCloudflareStaticRecoverySource(&report, strings.TrimSpace(*dir)); err != nil {
		report.Status = "blocked"
		report.Warnings = append(report.Warnings, err.Error())
	}
	report.ProbeURL = strings.TrimSpace(*probeURL)
	if report.ProbeURL == "" && len(report.Provider.Domains) > 0 {
		report.ProbeURL = "https://" + report.Provider.Domains[0] + "/"
	}
	report.MissingEvidence = cloudflareStaticRecoveryMissingEvidence(report)
	if len(report.MissingEvidence) > 0 {
		report.Status = "blocked"
		_ = printJSON(mustJSON(report))
		return errors.New("cloudflare static recovery evidence is incomplete")
	}
	if !*dryRun {
		report.Status = "blocked"
		if strings.TrimSpace(*confirm) != "recover" {
			report.Warnings = append(report.Warnings, "real recovery writes require -confirm recover")
		}
		report.Warnings = append(report.Warnings, "cloudflare static recovery write is not implemented yet; dry-run evidence validation is the supported boundary")
		report.NextCommands = cloudflareStaticRecoveryNextCommands(report)
		_ = printJSON(mustJSON(report))
		return errors.New("cloudflare static recovery write is not implemented")
	}
	probe, err := runSiteProbe(*resolver, siteprobe.Options{
		URL:                        report.ProbeURL,
		SPAPath:                    *spaPath,
		MaxAssets:                  *maxAssets,
		Timeout:                    *timeout,
		RequireDirectAssets:        true,
		RequireEdgeStaticHTML:      true,
		RequireHTMLRevalidate:      report.Source.HeadersGenerated,
		RequireImmutableAssetCache: report.Source.HeadersGenerated,
	})
	if err != nil {
		report.Status = "blocked"
		report.Warnings = append(report.Warnings, err.Error())
		_ = printJSON(mustJSON(report))
		return err
	}
	probe = redactSignedProbeReport(probe)
	report.Probe = &probe
	report.NextCommands = cloudflareStaticRecoveryNextCommands(report)
	if !probe.OK {
		report.Status = "blocked"
		report.Warnings = append(report.Warnings, "strict Cloudflare Static probe did not pass; metadata recovery must not write")
		_ = printJSON(mustJSON(report))
		return errors.New("cloudflare static recovery probe failed")
	}
	report.Status = "verified"
	report.WriteReady = true
	return printJSON(mustJSON(report))
}

func populateCloudflareStaticRecoverySource(report *cloudflareStaticRecoveryReport, dir string) error {
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

func cloudflareStaticRecoveryMissingEvidence(report cloudflareStaticRecoveryReport) []string {
	var missing []string
	if strings.TrimSpace(report.SiteID) == "" {
		missing = append(missing, "site")
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
	if strings.TrimSpace(report.Provider.VersionID) == "" {
		missing = append(missing, "version_id")
	}
	if len(report.Provider.Domains) == 0 && strings.TrimSpace(report.ProbeURL) == "" {
		missing = append(missing, "domain_or_url")
	}
	return missing
}

func cloudflareStaticRecoveryNextCommands(report cloudflareStaticRecoveryReport) []string {
	probe := []string{
		"supercdnctl probe-site",
		"-url " + cliHintArg(report.ProbeURL),
		"-require-edge-static-html",
		"-require-direct-assets",
	}
	if report.Source.HeadersGenerated {
		probe = append(probe, "-require-html-revalidate", "-require-immutable-assets")
	}
	recover := []string{
		"supercdnctl recover-cloudflare-static",
		"-site " + cliHintArg(report.SiteID),
		"-dir " + cliHintArg(report.Source.Dir),
		"-worker-name " + cliHintArg(report.Provider.WorkerName),
		"-version-id " + cliHintArg(report.Provider.VersionID),
	}
	if len(report.Provider.Domains) > 0 {
		recover = append(recover, "-domains "+cliHintArg(strings.Join(report.Provider.Domains, ",")))
	}
	if strings.TrimSpace(report.ProbeURL) != "" {
		recover = append(recover, "-url "+cliHintArg(report.ProbeURL))
	}
	recover = append(recover, "-dry-run=false", "-confirm recover")
	return []string{
		strings.Join(probe, " "),
		strings.Join(recover, " "),
	}
}
