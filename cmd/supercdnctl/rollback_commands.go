package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type rollbackPlanDeployment struct {
	ID               string                                `json:"id"`
	SiteID           string                                `json:"site_id"`
	Status           string                                `json:"status"`
	Environment      string                                `json:"environment"`
	RouteProfile     string                                `json:"route_profile"`
	DeploymentTarget string                                `json:"deployment_target"`
	RoutingPolicy    string                                `json:"routing_policy,omitempty"`
	ResourceFailover bool                                  `json:"resource_failover,omitempty"`
	Active           bool                                  `json:"active"`
	ArtifactSHA256   string                                `json:"artifact_sha256,omitempty"`
	ArtifactSize     int64                                 `json:"artifact_size,omitempty"`
	ManifestKey      string                                `json:"manifest_key,omitempty"`
	FileCount        int                                   `json:"file_count,omitempty"`
	TotalSize        int64                                 `json:"total_size,omitempty"`
	CloudflareStatic *rollbackPlanCloudflareStaticEvidence `json:"cloudflare_static,omitempty"`
	ProductionURLs   []string                              `json:"production_urls,omitempty"`
	SiteDomains      []string                              `json:"site_domains,omitempty"`
}

type rollbackPlanEvidence struct {
	RouteProfile     string                                `json:"route_profile,omitempty"`
	RoutingPolicy    string                                `json:"routing_policy,omitempty"`
	ResourceFailover bool                                  `json:"resource_failover,omitempty"`
	ArtifactSHA256   string                                `json:"artifact_sha256,omitempty"`
	ArtifactSize     int64                                 `json:"artifact_size,omitempty"`
	ManifestKey      string                                `json:"manifest_key,omitempty"`
	FileCount        int                                   `json:"file_count,omitempty"`
	TotalSize        int64                                 `json:"total_size,omitempty"`
	ProductionURLs   []string                              `json:"production_urls,omitempty"`
	SiteDomains      []string                              `json:"site_domains,omitempty"`
	CloudflareStatic *rollbackPlanCloudflareStaticEvidence `json:"cloudflare_static,omitempty"`
}

type rollbackPlanCloudflareStaticEvidence struct {
	WorkerName         string   `json:"worker_name,omitempty"`
	VersionID          string   `json:"version_id,omitempty"`
	Domains            []string `json:"domains,omitempty"`
	URLs               []string `json:"urls,omitempty"`
	CompatibilityDate  string   `json:"compatibility_date,omitempty"`
	AssetsSHA256       string   `json:"assets_sha256,omitempty"`
	CachePolicy        string   `json:"cache_policy,omitempty"`
	HeadersGenerated   bool     `json:"headers_generated,omitempty"`
	NotFoundHandling   string   `json:"not_found_handling,omitempty"`
	VerificationStatus string   `json:"verification_status,omitempty"`
	VerifiedAt         string   `json:"verified_at,omitempty"`
	PublishedAt        string   `json:"published_at,omitempty"`
}

type rollbackPlanOutput struct {
	SiteID                   string               `json:"site_id"`
	DeploymentID             string               `json:"deployment_id"`
	DeploymentTarget         string               `json:"deployment_target"`
	Environment              string               `json:"environment,omitempty"`
	Status                   string               `json:"status,omitempty"`
	Active                   bool                 `json:"active"`
	Action                   string               `json:"action"`
	MetadataPromoteSupported bool                 `json:"metadata_promote_supported"`
	RedeployRequired         bool                 `json:"redeploy_required"`
	SafeToRun                bool                 `json:"safe_to_run"`
	RollbackWriteReady       bool                 `json:"rollback_write_ready"`
	Evidence                 rollbackPlanEvidence `json:"evidence"`
	Warnings                 []string             `json:"warnings,omitempty"`
	WriteBlockers            []string             `json:"write_blockers,omitempty"`
	MissingEvidence          []string             `json:"missing_evidence,omitempty"`
	NextCommands             []string             `json:"next_commands,omitempty"`
}

func rollbackPlan(c client, args []string) error {
	fs := flag.NewFlagSet("rollback-plan", flag.ExitOnError)
	site := fs.String("site", "", "site id")
	deployment := fs.String("deployment", "", "deployment id to roll back to")
	dir := fs.String("dir", "", "source dist directory for redeploy-required targets")
	_ = fs.Parse(args)
	if *site == "" || *deployment == "" {
		return errors.New("-site and -deployment are required")
	}
	raw, err := c.doRaw(http.MethodGet, "/api/v1/sites/"+url.PathEscape(*site)+"/deployments/"+url.PathEscape(*deployment), nil, "")
	if err != nil {
		return err
	}
	var dep rollbackPlanDeployment
	if err := json.Unmarshal(raw, &dep); err != nil {
		return fmt.Errorf("parse deployment: %w", err)
	}
	if strings.TrimSpace(dep.SiteID) == "" {
		dep.SiteID = strings.TrimSpace(*site)
	}
	if strings.TrimSpace(dep.ID) == "" {
		dep.ID = strings.TrimSpace(*deployment)
	}
	return printJSON(mustJSON(buildRollbackPlan(dep, *dir)))
}

func buildRollbackPlan(dep rollbackPlanDeployment, sourceDir string) rollbackPlanOutput {
	target := deploymentTargetAlias(dep.DeploymentTarget)
	if target == "" {
		target = "origin_assisted"
	}
	out := rollbackPlanOutput{
		SiteID:           dep.SiteID,
		DeploymentID:     dep.ID,
		DeploymentTarget: target,
		Environment:      dep.Environment,
		Status:           dep.Status,
		Active:           dep.Active,
		Evidence:         rollbackPlanEvidenceFromDeployment(dep),
	}
	if dep.Active {
		out.Action = "noop"
		out.SafeToRun = true
		out.RollbackWriteReady = true
		out.Warnings = append(out.Warnings, "deployment is already active")
		return out
	}
	if dep.Status != "" && dep.Status != "ready" && dep.Status != "active" {
		out.Action = "not_ready"
		out.WriteBlockers = append(out.WriteBlockers, "target deployment is not ready")
		out.Warnings = append(out.Warnings, "deployment status is "+dep.Status+"; do not roll back until it is ready")
		out.NextCommands = append(out.NextCommands, "supercdnctl deployment -site "+cliHintArg(dep.SiteID)+" -deployment "+cliHintArg(dep.ID))
		return out
	}
	switch target {
	case "origin_assisted":
		out.Action = "metadata_promote"
		out.MetadataPromoteSupported = true
		out.SafeToRun = true
		out.RollbackWriteReady = true
		out.NextCommands = append(out.NextCommands, "supercdnctl promote-deployment -site "+cliHintArg(dep.SiteID)+" -deployment "+cliHintArg(dep.ID))
	case "cloudflare_static":
		out.Action = "redeploy_cloudflare_static"
		out.RedeployRequired = true
		out.WriteBlockers = cloudflareRollbackWriteBlockers(target)
		out.MissingEvidence = cloudflareRollbackMissingEvidence(dep, target, sourceDir)
		out.Warnings = append(out.Warnings, "cloudflare_static rollback must republish the desired asset version; promote-deployment is intentionally blocked")
		out.NextCommands = append(out.NextCommands, rollbackRedeployCommand(dep, target, sourceDir))
		out.NextCommands = append(out.NextCommands, rollbackPostRedeployProbeCommand(dep, target))
	case "hybrid_edge":
		out.Action = "redeploy_hybrid_edge"
		out.RedeployRequired = true
		out.WriteBlockers = cloudflareRollbackWriteBlockers(target)
		out.MissingEvidence = cloudflareRollbackMissingEvidence(dep, target, sourceDir)
		out.Warnings = append(out.Warnings, "hybrid_edge rollback must republish Worker assets and the active KV manifest together; promote-deployment is intentionally blocked")
		out.NextCommands = append(out.NextCommands, rollbackRedeployCommand(dep, target, sourceDir))
		out.NextCommands = append(out.NextCommands, rollbackPostRedeployProbeCommand(dep, target))
	default:
		out.Action = "manual_review"
		out.WriteBlockers = append(out.WriteBlockers, "unknown deployment_target")
		out.Warnings = append(out.Warnings, "unknown deployment_target "+target+"; inspect the deployment before attempting rollback")
		out.NextCommands = append(out.NextCommands, "supercdnctl deployment -site "+cliHintArg(dep.SiteID)+" -deployment "+cliHintArg(dep.ID))
	}
	return out
}

func rollbackPlanEvidenceFromDeployment(dep rollbackPlanDeployment) rollbackPlanEvidence {
	return rollbackPlanEvidence{
		RouteProfile:     dep.RouteProfile,
		RoutingPolicy:    dep.RoutingPolicy,
		ResourceFailover: dep.ResourceFailover,
		ArtifactSHA256:   dep.ArtifactSHA256,
		ArtifactSize:     dep.ArtifactSize,
		ManifestKey:      dep.ManifestKey,
		FileCount:        dep.FileCount,
		TotalSize:        dep.TotalSize,
		ProductionURLs:   append([]string(nil), dep.ProductionURLs...),
		SiteDomains:      append([]string(nil), dep.SiteDomains...),
		CloudflareStatic: dep.CloudflareStatic,
	}
}

func cloudflareRollbackWriteBlockers(target string) []string {
	blockers := []string{
		target + " rollback write command is not implemented; rerun deploy-site with the intended historical artifact",
		"rollback-plan has not verified real custom-domain traffic after a Cloudflare write",
		"Cloudflare Worker assets must move before Super CDN metadata is allowed to claim rollback",
	}
	if target == "hybrid_edge" {
		blockers = append(blockers, "active Workers KV manifest must be published together with Worker assets")
	}
	return blockers
}

func cloudflareRollbackMissingEvidence(dep rollbackPlanDeployment, target, sourceDir string) []string {
	var missing []string
	if strings.TrimSpace(sourceDir) == "" {
		missing = append(missing, "source_dist_dir")
	}
	if strings.TrimSpace(dep.RouteProfile) == "" {
		missing = append(missing, "route_profile")
	}
	if strings.TrimSpace(dep.ArtifactSHA256) == "" {
		missing = append(missing, "artifact_sha256")
	}
	if dep.FileCount <= 0 {
		missing = append(missing, "file_count")
	}
	static := dep.CloudflareStatic
	if static == nil {
		missing = append(missing, "cloudflare_static")
	} else {
		if strings.TrimSpace(static.WorkerName) == "" {
			missing = append(missing, "cloudflare_static.worker_name")
		}
		if strings.TrimSpace(static.VersionID) == "" {
			missing = append(missing, "cloudflare_static.version_id")
		}
		if strings.TrimSpace(static.AssetsSHA256) == "" {
			missing = append(missing, "cloudflare_static.assets_sha256")
		}
		if !hasNonEmpty(static.Domains) && !hasNonEmpty(static.URLs) && !hasNonEmpty(dep.ProductionURLs) && !hasNonEmpty(dep.SiteDomains) {
			missing = append(missing, "cloudflare_static.domains")
		}
		if strings.TrimSpace(static.VerificationStatus) == "" {
			missing = append(missing, "cloudflare_static.verification_status")
		} else if !strings.EqualFold(strings.TrimSpace(static.VerificationStatus), "ok") {
			missing = append(missing, "cloudflare_static.verification_status=ok")
		}
		if strings.TrimSpace(static.VerifiedAt) == "" {
			missing = append(missing, "cloudflare_static.verified_at")
		}
		if strings.TrimSpace(static.PublishedAt) == "" {
			missing = append(missing, "cloudflare_static.published_at")
		}
	}
	if target == "hybrid_edge" && strings.TrimSpace(dep.ManifestKey) == "" {
		missing = append(missing, "edge_manifest_key")
	}
	return missing
}

func hasNonEmpty(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func rollbackRedeployCommand(dep rollbackPlanDeployment, target, sourceDir string) string {
	dirArg := "<dist>"
	if strings.TrimSpace(sourceDir) != "" {
		dirArg = cliHintArg(sourceDir)
	}
	parts := []string{
		"supercdnctl deploy-site",
		"-site " + cliHintArg(dep.SiteID),
		"-dir " + dirArg,
		"-target " + cliHintArg(target),
	}
	if strings.TrimSpace(dep.RouteProfile) != "" {
		parts = append(parts, "-profile "+cliHintArg(dep.RouteProfile))
	}
	if strings.TrimSpace(dep.RoutingPolicy) != "" {
		parts = append(parts, "-routing-policy "+cliHintArg(dep.RoutingPolicy))
	}
	if dep.ResourceFailover {
		parts = append(parts, "-resource-failover")
	}
	return strings.Join(parts, " ")
}

func rollbackPostRedeployProbeCommand(dep rollbackPlanDeployment, target string) string {
	parts := []string{
		"supercdnctl probe-site",
		"-site " + cliHintArg(dep.SiteID),
		"-production",
		"-require-edge-static-html",
	}
	if target == "hybrid_edge" {
		parts = append(parts, "-require-edge-manifest-assets")
	} else {
		parts = append(parts, "-require-html-revalidate", "-require-immutable-assets")
	}
	return strings.Join(parts, " ")
}
