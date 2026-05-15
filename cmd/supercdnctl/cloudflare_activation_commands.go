package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"net/http"
	"net/url"
	"strings"
	"time"

	"supercdn/internal/cloudflarestatic"
	"supercdn/internal/model"
	"supercdn/internal/siteprobe"
)

type cloudflareStaticActivationReport struct {
	Status          string                           `json:"status"`
	DryRun          bool                             `json:"dry_run"`
	WriteReady      bool                             `json:"write_ready"`
	WriteSupported  bool                             `json:"write_supported"`
	SiteID          string                           `json:"site_id"`
	DeploymentID    string                           `json:"deployment_id"`
	Source          cloudflareStaticRecoverySource   `json:"source"`
	Provider        cloudflareStaticRecoveryProvider `json:"provider"`
	ProbeURL        string                           `json:"probe_url,omitempty"`
	Probe           *siteprobe.Report                `json:"probe,omitempty"`
	Deployment      json.RawMessage                  `json:"deployment,omitempty"`
	MissingEvidence []string                         `json:"missing_evidence,omitempty"`
	Warnings        []string                         `json:"warnings,omitempty"`
	NextCommands    []string                         `json:"next_commands,omitempty"`
}

func activateCloudflareStatic(c client, args []string) error {
	fs := flag.NewFlagSet("activate-cloudflare-static", flag.ExitOnError)
	site := fs.String("site", "", "site id")
	deployment := fs.String("deployment", "", "recovered Cloudflare Static deployment id")
	dir := fs.String("dir", "", "source dist directory used for the provider write")
	probeURL := fs.String("url", "", "explicit public URL to probe; defaults to recorded Cloudflare Static URL")
	resolver := fs.String("resolver", "", "DNS resolver for HTTP probes, for example 1.1.1.1:53")
	maxAssets := fs.Int("max-assets", 20, "maximum JS/CSS assets to probe from index HTML")
	timeout := fs.Duration("timeout", 30*time.Second, "overall probe timeout")
	dryRun := fs.Bool("dry-run", true, "validate activation evidence without changing active metadata")
	confirm := fs.String("confirm", "", "must be activate for writes")
	_ = fs.Parse(args)

	report := cloudflareStaticActivationReport{
		Status:         "planned",
		DryRun:         *dryRun,
		WriteSupported: true,
		SiteID:         strings.TrimSpace(*site),
		DeploymentID:   strings.TrimSpace(*deployment),
	}
	if report.SiteID == "" || report.DeploymentID == "" {
		report.Status = "blocked"
		report.MissingEvidence = cloudflareStaticActivationMissingEvidence(report)
		_ = printJSON(mustJSON(report))
		return errors.New("cloudflare static activation evidence is incomplete")
	}

	dep, err := fetchCloudflareStaticActivationDeployment(c, report.SiteID, report.DeploymentID)
	if err != nil {
		report.Status = "blocked"
		report.Warnings = append(report.Warnings, err.Error())
		_ = printJSON(mustJSON(report))
		return err
	}
	if dep.CloudflareStatic != nil {
		report.Provider = cloudflareStaticRecoveryProvider{
			WorkerName:        strings.TrimSpace(dep.CloudflareStatic.WorkerName),
			VersionID:         strings.TrimSpace(dep.CloudflareStatic.VersionID),
			Domains:           cleanDomains(dep.CloudflareStatic.Domains),
			CompatibilityDate: strings.TrimSpace(dep.CloudflareStatic.CompatibilityDate),
			CachePolicy:       strings.TrimSpace(dep.CloudflareStatic.CachePolicy),
			NotFoundHandling:  strings.TrimSpace(dep.CloudflareStatic.NotFoundHandling),
		}
	}
	if report.Provider.CachePolicy == "" {
		report.Provider.CachePolicy = cloudflarestatic.CachePolicyAuto
	}
	if err := populateCloudflareStaticActivationSource(&report, strings.TrimSpace(*dir)); err != nil {
		report.Status = "blocked"
		report.Warnings = append(report.Warnings, err.Error())
	}
	report.ProbeURL = strings.TrimSpace(*probeURL)
	if report.ProbeURL == "" {
		report.ProbeURL = cloudflareStaticActivationDefaultProbeURL(dep)
	}
	if report.Provider.Domains == nil && dep.CloudflareStatic != nil {
		report.Provider.Domains = cleanDomains(dep.CloudflareStatic.Domains)
	}
	report.MissingEvidence = cloudflareStaticActivationMissingEvidence(report)
	if len(report.MissingEvidence) > 0 {
		report.Status = "blocked"
		_ = printJSON(mustJSON(report))
		return errors.New("cloudflare static activation evidence is incomplete")
	}
	if err := validateCloudflareStaticActivationSource(report, dep); err != nil {
		report.Status = "blocked"
		report.Warnings = append(report.Warnings, err.Error())
		_ = printJSON(mustJSON(report))
		return err
	}
	if !*dryRun {
		if strings.TrimSpace(*confirm) != "activate" {
			report.Status = "blocked"
			report.Warnings = append(report.Warnings, "real activation writes require -confirm activate")
			_ = printJSON(mustJSON(report))
			return errors.New("cloudflare static activation requires confirmation")
		}
		if strings.TrimSpace(c.token) == "" {
			report.Status = "blocked"
			report.Warnings = append(report.Warnings, "token is required for activation writes; pass -token, SUPERCDN_TOKEN, or use a saved profile")
			_ = printJSON(mustJSON(report))
			return errors.New("token is required for cloudflare static activation writes")
		}
	}
	probe, err := runSiteProbe(*resolver, siteprobe.Options{
		URL:                        report.ProbeURL,
		MaxAssets:                  *maxAssets,
		Timeout:                    *timeout,
		RequireDirectAssets:        true,
		RequireEdgeStaticHTML:      true,
		RequireHTMLRevalidate:      dep.CloudflareStatic.HeadersGenerated,
		RequireImmutableAssetCache: dep.CloudflareStatic.HeadersGenerated,
	})
	if err != nil {
		report.Status = "blocked"
		report.Warnings = append(report.Warnings, err.Error())
		_ = printJSON(mustJSON(report))
		return err
	}
	probe = redactSignedProbeReport(probe)
	report.Probe = &probe
	report.NextCommands = cloudflareStaticActivationNextCommands(report)
	if !probe.OK {
		report.Status = "blocked"
		report.Warnings = append(report.Warnings, "strict Cloudflare Static probe did not pass; activation must not write")
		_ = printJSON(mustJSON(report))
		return errors.New("cloudflare static activation probe failed")
	}
	report.WriteReady = true
	if *dryRun {
		report.Status = "verified"
		return printJSON(mustJSON(report))
	}
	req := map[string]any{
		"confirm":             "activate",
		"probe_url":           report.ProbeURL,
		"worker_name":         report.Provider.WorkerName,
		"version_id":          report.Provider.VersionID,
		"domains":             report.Provider.Domains,
		"assets_sha256":       report.Source.AssetsSHA256,
		"file_count":          report.Source.FileCount,
		"total_size":          report.Source.TotalSize,
		"verification_status": "ok",
		"verified_at_utc":     time.Now().UTC().Format(time.RFC3339Nano),
	}
	raw, err := c.doRaw(http.MethodPost, "/api/v1/sites/"+url.PathEscape(report.SiteID)+"/deployments/"+url.PathEscape(report.DeploymentID)+"/cloudflare-static/activate", bytes.NewReader(mustJSON(req)), "application/json")
	if err != nil {
		report.Status = "blocked"
		report.Warnings = append(report.Warnings, err.Error())
		_ = printJSON(mustJSON(report))
		return err
	}
	report.Status = "activated"
	report.Deployment = json.RawMessage(raw)
	return printJSON(mustJSON(report))
}

func fetchCloudflareStaticActivationDeployment(c client, siteID, deploymentID string) (model.SiteDeployment, error) {
	raw, err := c.doRaw(http.MethodGet, "/api/v1/sites/"+url.PathEscape(siteID)+"/deployments/"+url.PathEscape(deploymentID), nil, "")
	if err != nil {
		return model.SiteDeployment{}, err
	}
	var dep model.SiteDeployment
	if err := json.Unmarshal(raw, &dep); err != nil {
		return model.SiteDeployment{}, err
	}
	return dep, nil
}

func populateCloudflareStaticActivationSource(report *cloudflareStaticActivationReport, dir string) error {
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

func cloudflareStaticActivationDefaultProbeURL(dep model.SiteDeployment) string {
	if dep.CloudflareStatic != nil {
		for _, raw := range dep.CloudflareStatic.URLs {
			if strings.TrimSpace(raw) != "" {
				return strings.TrimSpace(raw)
			}
		}
		for _, domain := range dep.CloudflareStatic.Domains {
			if strings.TrimSpace(domain) != "" {
				return "https://" + strings.TrimSpace(domain) + "/"
			}
		}
	}
	return strings.TrimSpace(dep.ProductionURL)
}

func cloudflareStaticActivationMissingEvidence(report cloudflareStaticActivationReport) []string {
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
	if strings.TrimSpace(report.Provider.VersionID) == "" {
		missing = append(missing, "version_id")
	}
	if len(report.Provider.Domains) == 0 {
		missing = append(missing, "domains")
	}
	if strings.TrimSpace(report.ProbeURL) == "" {
		missing = append(missing, "probe_url")
	}
	return missing
}

func validateCloudflareStaticActivationSource(report cloudflareStaticActivationReport, dep model.SiteDeployment) error {
	if dep.DeploymentTarget != model.SiteDeploymentTargetCloudflareStatic {
		return errors.New("deployment target must be cloudflare_static")
	}
	if dep.Status != model.SiteDeploymentReady && dep.Status != model.SiteDeploymentActive {
		return errors.New("deployment is not ready")
	}
	if dep.CloudflareStatic == nil {
		return errors.New("cloudflare_static evidence is missing")
	}
	if strings.TrimSpace(dep.CloudflareStatic.AssetsSHA256) == "" || report.Source.AssetsSHA256 != dep.CloudflareStatic.AssetsSHA256 {
		return errors.New("source assets_sha256 does not match deployment evidence")
	}
	if report.Source.FileCount != dep.FileCount {
		return errors.New("source file_count does not match deployment evidence")
	}
	if report.Source.TotalSize != dep.TotalSize {
		return errors.New("source total_size does not match deployment evidence")
	}
	return nil
}

func cloudflareStaticActivationNextCommands(report cloudflareStaticActivationReport) []string {
	probe := []string{
		"supercdnctl probe-site",
		"-url " + cliHintArg(report.ProbeURL),
		"-require-edge-static-html",
		"-require-direct-assets",
	}
	if report.Source.HeadersGenerated {
		probe = append(probe, "-require-html-revalidate", "-require-immutable-assets")
	}
	activate := []string{
		"supercdnctl activate-cloudflare-static",
		"-site " + cliHintArg(report.SiteID),
		"-deployment " + cliHintArg(report.DeploymentID),
		"-dir " + cliHintArg(report.Source.Dir),
		"-url " + cliHintArg(report.ProbeURL),
		"-dry-run=false",
		"-confirm activate",
	}
	return []string{
		strings.Join(probe, " "),
		strings.Join(activate, " "),
	}
}
