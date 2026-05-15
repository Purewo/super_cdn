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
	HybridEdge       *hybridEdgeDeploymentEvidence         `json:"hybrid_edge,omitempty"`
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
	HybridEdge       *hybridEdgeDeploymentEvidence         `json:"hybrid_edge,omitempty"`
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

type rollbackApplyReport struct {
	Status             string                         `json:"status"`
	DryRun             bool                           `json:"dry_run"`
	WriteSupported     bool                           `json:"write_supported"`
	WriteReady         bool                           `json:"write_ready"`
	SiteID             string                         `json:"site_id"`
	TargetDeploymentID string                         `json:"target_deployment_id"`
	DeploymentTarget   string                         `json:"deployment_target,omitempty"`
	Action             string                         `json:"action,omitempty"`
	Source             cloudflareStaticRecoverySource `json:"source,omitempty"`
	Evidence           rollbackPlanEvidence           `json:"evidence"`
	Deployment         json.RawMessage                `json:"deployment,omitempty"`
	ProviderWrite      json.RawMessage                `json:"provider_write,omitempty"`
	MissingEvidence    []string                       `json:"missing_evidence,omitempty"`
	Warnings           []string                       `json:"warnings,omitempty"`
	NextCommands       []string                       `json:"next_commands,omitempty"`
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
	dep, err := fetchRollbackPlanDeployment(c, *site, *deployment)
	if err != nil {
		return err
	}
	return printJSON(mustJSON(buildRollbackPlan(dep, *dir)))
}

func rollbackApply(c client, args []string) error {
	fs := flag.NewFlagSet("rollback-apply", flag.ExitOnError)
	site := fs.String("site", "", "site id")
	deployment := fs.String("deployment", "", "deployment id to roll back to")
	dir := fs.String("dir", "", "source dist directory for Cloudflare-backed rollback")
	staticVerify := fs.String("static-verify", cloudflareStaticVerifyWait, "Cloudflare Static readiness check: wait, warn, or none")
	staticVerifyTimeout := fs.Duration("static-verify-timeout", 2*time.Minute, "maximum time to wait for Cloudflare Static custom domains")
	staticVerifyInterval := fs.Duration("static-verify-interval", 5*time.Second, "delay between Cloudflare Static readiness probes")
	staticVerifySPAPath := fs.String("static-verify-spa-path", "", "SPA path to verify after Cloudflare Static publish")
	staticVerifyResolver := fs.String("static-verify-resolver", "1.1.1.1:53", "DNS resolver for Cloudflare Static readiness probes")
	message := fs.String("message", "", "Cloudflare deployment message for the rollback write")
	dryRun := fs.Bool("dry-run", true, "validate rollback evidence without writing provider or metadata state")
	confirm := fs.String("confirm", "", "must be rollback for writes")
	_ = fs.Parse(args)

	report := rollbackApplyReport{
		Status:             "planned",
		DryRun:             *dryRun,
		WriteSupported:     true,
		SiteID:             strings.TrimSpace(*site),
		TargetDeploymentID: strings.TrimSpace(*deployment),
	}
	if report.SiteID == "" || report.TargetDeploymentID == "" {
		report.Status = "blocked"
		report.MissingEvidence = rollbackApplyMissingArgs(report)
		_ = printJSON(mustJSON(report))
		return errors.New("rollback apply target is incomplete")
	}
	dep, err := fetchRollbackPlanDeployment(c, report.SiteID, report.TargetDeploymentID)
	if err != nil {
		report.Status = "blocked"
		report.Warnings = append(report.Warnings, err.Error())
		_ = printJSON(mustJSON(report))
		return err
	}
	plan := buildRollbackPlan(dep, *dir)
	report.DeploymentTarget = plan.DeploymentTarget
	report.Action = plan.Action
	report.Evidence = plan.Evidence
	report.MissingEvidence = append(report.MissingEvidence, plan.MissingEvidence...)
	report.Warnings = append(report.Warnings, plan.Warnings...)
	report.NextCommands = append(report.NextCommands, plan.NextCommands...)
	if dep.Active {
		report.Status = "noop"
		report.WriteReady = true
		return printJSON(mustJSON(report))
	}
	switch plan.DeploymentTarget {
	case "origin_assisted":
		return rollbackApplyOriginAssisted(c, dep, report, *dryRun, *confirm)
	case "cloudflare_static":
		return rollbackApplyCloudflareStatic(c, dep, report, rollbackApplyCloudflareStaticOptions{
			Dir:            strings.TrimSpace(*dir),
			Message:        strings.TrimSpace(*message),
			VerifyMode:     *staticVerify,
			VerifyTimeout:  *staticVerifyTimeout,
			VerifyInterval: *staticVerifyInterval,
			VerifySPAPath:  *staticVerifySPAPath,
			VerifyResolver: *staticVerifyResolver,
			DryRun:         *dryRun,
			Confirm:        *confirm,
		})
	case "hybrid_edge":
		return rollbackApplyHybridEdge(c, dep, report, rollbackApplyHybridEdgeOptions{
			Dir:              strings.TrimSpace(*dir),
			Message:          strings.TrimSpace(*message),
			VerifyMode:       *staticVerify,
			VerifyTimeout:    *staticVerifyTimeout,
			VerifyInterval:   *staticVerifyInterval,
			VerifySPAPath:    *staticVerifySPAPath,
			VerifyResolver:   *staticVerifyResolver,
			DryRun:           *dryRun,
			Confirm:          *confirm,
			Timeout:          30 * time.Minute,
			CandidateTimeout: 10 * time.Minute,
		})
	default:
		report.Status = "blocked"
		report.WriteSupported = false
		if len(report.Warnings) == 0 {
			report.Warnings = append(report.Warnings, "rollback apply is not supported for deployment_target "+plan.DeploymentTarget)
		}
		_ = printJSON(mustJSON(report))
		return errors.New("rollback apply is not supported for deployment target")
	}
}

func fetchRollbackPlanDeployment(c client, site, deployment string) (rollbackPlanDeployment, error) {
	raw, err := c.doRaw(http.MethodGet, "/api/v1/sites/"+url.PathEscape(site)+"/deployments/"+url.PathEscape(deployment), nil, "")
	if err != nil {
		return rollbackPlanDeployment{}, err
	}
	var dep rollbackPlanDeployment
	if err := json.Unmarshal(raw, &dep); err != nil {
		return rollbackPlanDeployment{}, fmt.Errorf("parse deployment: %w", err)
	}
	if strings.TrimSpace(dep.SiteID) == "" {
		dep.SiteID = strings.TrimSpace(site)
	}
	if strings.TrimSpace(dep.ID) == "" {
		dep.ID = strings.TrimSpace(deployment)
	}
	return dep, nil
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
		out.MissingEvidence = cloudflareRollbackMissingEvidence(dep, target, sourceDir)
		if len(out.MissingEvidence) == 0 {
			out.SafeToRun = true
			out.RollbackWriteReady = true
			out.NextCommands = append(out.NextCommands, rollbackApplyCommand(dep, sourceDir))
		} else {
			out.WriteBlockers = cloudflareStaticRollbackWriteBlockers()
		}
		out.Warnings = append(out.Warnings, "cloudflare_static rollback must republish the desired asset version; promote-deployment is intentionally blocked")
		out.NextCommands = append(out.NextCommands, rollbackRedeployCommand(dep, target, sourceDir))
		out.NextCommands = append(out.NextCommands, rollbackPostRedeployProbeCommand(dep, target))
	case "hybrid_edge":
		out.Action = "redeploy_hybrid_edge"
		out.RedeployRequired = true
		out.MissingEvidence = cloudflareRollbackMissingEvidence(dep, target, sourceDir)
		if len(out.MissingEvidence) == 0 {
			out.SafeToRun = true
			out.RollbackWriteReady = true
			out.NextCommands = append(out.NextCommands, rollbackApplyCommand(dep, sourceDir))
		} else {
			out.WriteBlockers = hybridEdgeRollbackWriteBlockers()
		}
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

func rollbackApplyMissingArgs(report rollbackApplyReport) []string {
	var missing []string
	if strings.TrimSpace(report.SiteID) == "" {
		missing = append(missing, "site")
	}
	if strings.TrimSpace(report.TargetDeploymentID) == "" {
		missing = append(missing, "deployment")
	}
	return missing
}

func rollbackApplyOriginAssisted(c client, dep rollbackPlanDeployment, report rollbackApplyReport, dryRun bool, confirm string) error {
	report.WriteReady = true
	if dryRun {
		report.Status = "verified"
		return printJSON(mustJSON(report))
	}
	if strings.TrimSpace(confirm) != "rollback" {
		report.Status = "blocked"
		report.Warnings = append(report.Warnings, "origin_assisted rollback writes require -confirm rollback")
		_ = printJSON(mustJSON(report))
		return errors.New("rollback apply requires confirmation")
	}
	if strings.TrimSpace(c.token) == "" {
		report.Status = "blocked"
		report.Warnings = append(report.Warnings, "token is required for rollback writes; pass -token, SUPERCDN_TOKEN, or use a saved profile")
		_ = printJSON(mustJSON(report))
		return errors.New("token is required for rollback writes")
	}
	raw, err := c.doRaw(http.MethodPost, "/api/v1/sites/"+url.PathEscape(dep.SiteID)+"/deployments/"+url.PathEscape(dep.ID)+"/promote", nil, "")
	if err != nil {
		report.Status = "blocked"
		report.Warnings = append(report.Warnings, err.Error())
		_ = printJSON(mustJSON(report))
		return err
	}
	report.Status = "promoted"
	report.Deployment = json.RawMessage(raw)
	return printJSON(mustJSON(report))
}

type rollbackApplyCloudflareStaticOptions struct {
	Dir            string
	Message        string
	VerifyMode     string
	VerifyTimeout  time.Duration
	VerifyInterval time.Duration
	VerifySPAPath  string
	VerifyResolver string
	DryRun         bool
	Confirm        string
}

var rollbackDeployCloudflareStatic = deploySiteCloudflareStaticRaw
var rollbackDeployHybridEdge = deploySiteHybridEdgeRaw

func rollbackApplyCloudflareStatic(c client, dep rollbackPlanDeployment, report rollbackApplyReport, opts rollbackApplyCloudflareStaticOptions) error {
	if dep.CloudflareStatic != nil {
		report.Source.HeadersPolicy = firstNonEmpty(strings.TrimSpace(dep.CloudflareStatic.CachePolicy), cloudflareStaticCachePolicyAuto)
	}
	if err := populateRollbackApplyCloudflareStaticSource(&report, opts.Dir); err != nil {
		report.Status = "blocked"
		report.Warnings = append(report.Warnings, err.Error())
		_ = printJSON(mustJSON(report))
		return err
	}
	report.MissingEvidence = cloudflareRollbackMissingEvidence(dep, "cloudflare_static", opts.Dir)
	if len(report.MissingEvidence) > 0 {
		report.Status = "blocked"
		_ = printJSON(mustJSON(report))
		return errors.New("cloudflare static rollback evidence is incomplete")
	}
	if err := validateRollbackApplyCloudflareStaticSource(report, dep); err != nil {
		report.Status = "blocked"
		report.Warnings = append(report.Warnings, err.Error())
		_ = printJSON(mustJSON(report))
		return err
	}
	report.WriteReady = true
	if opts.DryRun {
		report.Status = "verified"
		return printJSON(mustJSON(report))
	}
	if strings.TrimSpace(opts.Confirm) != "rollback" {
		report.Status = "blocked"
		report.Warnings = append(report.Warnings, "Cloudflare Static rollback writes require -confirm rollback")
		_ = printJSON(mustJSON(report))
		return errors.New("cloudflare static rollback requires confirmation")
	}
	if strings.TrimSpace(c.token) == "" {
		report.Status = "blocked"
		report.Warnings = append(report.Warnings, "token is required for rollback writes; pass -token, SUPERCDN_TOKEN, or use a saved profile")
		_ = printJSON(mustJSON(report))
		return errors.New("token is required for rollback writes")
	}
	domains := rollbackCloudflareStaticDomains(dep)
	raw, err := rollbackDeployCloudflareStatic(c, cloudflareStaticDeploySiteOptions{
		Site:              dep.SiteID,
		Dir:               opts.Dir,
		Environment:       firstNonEmpty(strings.TrimSpace(dep.Environment), "production"),
		RouteProfile:      dep.RouteProfile,
		DeploymentTarget:  "cloudflare_static",
		RoutingPolicy:     strings.TrimSpace(dep.RoutingPolicy),
		ResourceFailover:  false,
		Domains:           domains,
		WorkerName:        rollbackCloudflareStaticWorkerName(dep),
		CompatibilityDate: rollbackCloudflareStaticCompatibilityDate(dep),
		Message:           firstNonEmpty(opts.Message, "SuperCDN cloudflare_static rollback "+dep.SiteID+" to "+dep.ID),
		CachePolicy:       rollbackCloudflareStaticCachePolicy(dep),
		NotFoundHandling:  rollbackCloudflareStaticNotFoundHandling(dep),
		VerifyMode:        opts.VerifyMode,
		VerifyTimeout:     opts.VerifyTimeout,
		VerifyInterval:    opts.VerifyInterval,
		VerifySPAPath:     opts.VerifySPAPath,
		VerifyResolver:    opts.VerifyResolver,
		Promote:           true,
		Pinned:            false,
		Operation:         "rollback_apply",
		RollbackTarget:    dep.ID,
	})
	if len(raw) > 0 {
		if err != nil {
			report.ProviderWrite = json.RawMessage(raw)
		} else {
			report.Deployment = json.RawMessage(raw)
		}
	}
	if err != nil {
		report.Status = "blocked"
		report.Warnings = append(report.Warnings, err.Error())
		_ = printJSON(mustJSON(report))
		return err
	}
	report.Status = "rolled_back"
	return printJSON(mustJSON(report))
}

type rollbackApplyHybridEdgeOptions struct {
	Dir              string
	Message          string
	VerifyMode       string
	VerifyTimeout    time.Duration
	VerifyInterval   time.Duration
	VerifySPAPath    string
	VerifyResolver   string
	DryRun           bool
	Confirm          string
	Timeout          time.Duration
	CandidateTimeout time.Duration
}

func rollbackApplyHybridEdge(c client, dep rollbackPlanDeployment, report rollbackApplyReport, opts rollbackApplyHybridEdgeOptions) error {
	if dep.HybridEdge != nil {
		report.Source.HeadersPolicy = firstNonEmpty(strings.TrimSpace(dep.HybridEdge.CachePolicy), cloudflareStaticCachePolicyAuto)
	}
	if err := populateRollbackApplyCloudflareStaticSource(&report, opts.Dir); err != nil {
		report.Status = "blocked"
		report.Warnings = append(report.Warnings, err.Error())
		_ = printJSON(mustJSON(report))
		return err
	}
	report.MissingEvidence = cloudflareRollbackMissingEvidence(dep, "hybrid_edge", opts.Dir)
	if len(report.MissingEvidence) > 0 {
		report.Status = "blocked"
		_ = printJSON(mustJSON(report))
		return errors.New("hybrid_edge rollback evidence is incomplete")
	}
	if err := validateRollbackApplyHybridEdgeSource(report, dep); err != nil {
		report.Status = "blocked"
		report.Warnings = append(report.Warnings, err.Error())
		_ = printJSON(mustJSON(report))
		return err
	}
	report.WriteReady = true
	if opts.DryRun {
		report.Status = "verified"
		return printJSON(mustJSON(report))
	}
	if strings.TrimSpace(opts.Confirm) != "rollback" {
		report.Status = "blocked"
		report.Warnings = append(report.Warnings, "hybrid_edge rollback writes require -confirm rollback")
		_ = printJSON(mustJSON(report))
		return errors.New("hybrid_edge rollback requires confirmation")
	}
	if strings.TrimSpace(c.token) == "" {
		report.Status = "blocked"
		report.Warnings = append(report.Warnings, "token is required for rollback writes; pass -token, SUPERCDN_TOKEN, or use a saved profile")
		_ = printJSON(mustJSON(report))
		return errors.New("token is required for rollback writes")
	}
	raw, err := rollbackDeployHybridEdge(c, hybridEdgeDeploySiteOptions{
		Site:                dep.SiteID,
		Dir:                 opts.Dir,
		Environment:         firstNonEmpty(strings.TrimSpace(dep.Environment), "production"),
		RouteProfile:        dep.RouteProfile,
		DeploymentTarget:    "hybrid_edge",
		RoutingPolicy:       strings.TrimSpace(dep.RoutingPolicy),
		ResourceFailover:    dep.ResourceFailover,
		EntryOriginFallback: dep.HybridEdge.EntryOriginFallback,
		Domains:             rollbackCloudflareStaticDomains(dep),
		WorkerName:          rollbackHybridEdgeWorkerName(dep),
		CompatibilityDate:   rollbackHybridEdgeCompatibilityDate(dep),
		Message:             firstNonEmpty(opts.Message, "SuperCDN hybrid_edge rollback "+dep.SiteID+" to "+dep.ID),
		CachePolicy:         rollbackHybridEdgeCachePolicy(dep),
		NotFoundHandling:    rollbackHybridEdgeNotFoundHandling(dep),
		VerifyMode:          opts.VerifyMode,
		VerifyTimeout:       opts.VerifyTimeout,
		VerifyInterval:      opts.VerifyInterval,
		VerifySPAPath:       opts.VerifySPAPath,
		VerifyResolver:      opts.VerifyResolver,
		Promote:             true,
		Pinned:              false,
		Timeout:             opts.Timeout,
		KVNamespaceID:       rollbackHybridEdgeKVNamespaceID(dep),
		KVNamespace:         rollbackHybridEdgeKVNamespace(dep),
		ManifestMode:        rollbackHybridEdgeManifestMode(dep),
		DefaultCacheControl: rollbackHybridEdgeDefaultCacheControl(dep),
		CandidateWait:       true,
		CandidateTimeout:    opts.CandidateTimeout,
		Operation:           "rollback_apply",
		RollbackTarget:      dep.ID,
	})
	if len(raw) > 0 {
		if err != nil {
			report.ProviderWrite = json.RawMessage(raw)
		} else {
			report.Deployment = json.RawMessage(raw)
		}
	}
	if err != nil {
		report.Status = "blocked"
		report.Warnings = append(report.Warnings, err.Error())
		_ = printJSON(mustJSON(report))
		return err
	}
	report.Status = "rolled_back"
	return printJSON(mustJSON(report))
}

func populateRollbackApplyCloudflareStaticSource(report *rollbackApplyReport, dir string) error {
	report.Source.Dir = strings.TrimSpace(dir)
	if report.Source.Dir == "" {
		return nil
	}
	summary, err := summarizeCloudflareStaticDirectory(report.Source.Dir)
	if err != nil {
		return err
	}
	_, cleanup, headers, err := prepareCloudflareStaticAssetsDir(report.Source.Dir, report.Source.HeadersPolicy)
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

func validateRollbackApplyCloudflareStaticSource(report rollbackApplyReport, dep rollbackPlanDeployment) error {
	if dep.CloudflareStatic == nil {
		return errors.New("cloudflare_static evidence is missing")
	}
	if dep.Status != "" && dep.Status != "ready" && dep.Status != "active" {
		return errors.New("target deployment is not ready")
	}
	if strings.TrimSpace(dep.Environment) != "" && dep.Environment != "production" {
		return errors.New("rollback-apply only supports production Cloudflare Static deployments")
	}
	if strings.TrimSpace(dep.CloudflareStatic.AssetsSHA256) == "" || report.Source.AssetsSHA256 != dep.CloudflareStatic.AssetsSHA256 {
		return errors.New("source assets_sha256 does not match target deployment evidence")
	}
	if dep.FileCount > 0 && report.Source.FileCount != dep.FileCount {
		return errors.New("source file_count does not match target deployment evidence")
	}
	if dep.TotalSize > 0 && report.Source.TotalSize != dep.TotalSize {
		return errors.New("source total_size does not match target deployment evidence")
	}
	return nil
}

func validateRollbackApplyHybridEdgeSource(report rollbackApplyReport, dep rollbackPlanDeployment) error {
	if dep.HybridEdge == nil {
		return errors.New("hybrid_edge evidence is missing")
	}
	if dep.Status != "" && dep.Status != "ready" && dep.Status != "active" {
		return errors.New("target deployment is not ready")
	}
	if strings.TrimSpace(dep.Environment) != "" && dep.Environment != "production" {
		return errors.New("rollback-apply only supports production hybrid_edge deployments")
	}
	if strings.TrimSpace(dep.HybridEdge.AssetsSHA256) == "" || report.Source.AssetsSHA256 != dep.HybridEdge.AssetsSHA256 {
		return errors.New("source assets_sha256 does not match target deployment evidence")
	}
	return nil
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
		HybridEdge:       dep.HybridEdge,
	}
}

func cloudflareStaticRollbackWriteBlockers() []string {
	return []string{
		"cloudflare_static rollback apply requires the historical source_dist_dir plus recorded Worker/domain/assets evidence",
		"Cloudflare Worker assets must move before Super CDN metadata is allowed to claim rollback",
	}
}

func hybridEdgeRollbackWriteBlockers() []string {
	return []string{
		"hybrid_edge rollback apply requires the historical source_dist_dir plus recorded Worker/KV/manifest evidence",
		"Worker assets, deployment KV manifest and active KV manifest must be republished together",
		"Cloudflare custom-domain traffic must pass strict edge-static and edge-manifest probes before metadata is trusted",
	}
}

func cloudflareRollbackMissingEvidence(dep rollbackPlanDeployment, target, sourceDir string) []string {
	var missing []string
	if strings.TrimSpace(sourceDir) == "" {
		missing = append(missing, "source_dist_dir")
	}
	if strings.TrimSpace(dep.RouteProfile) == "" {
		missing = append(missing, "route_profile")
	}
	if target != "cloudflare_static" && strings.TrimSpace(dep.ArtifactSHA256) == "" {
		missing = append(missing, "artifact_sha256")
	}
	if dep.FileCount <= 0 {
		missing = append(missing, "file_count")
	}
	if target == "hybrid_edge" {
		hybrid := dep.HybridEdge
		if hybrid == nil {
			missing = append(missing, "hybrid_edge")
		} else {
			if strings.TrimSpace(hybrid.WorkerName) == "" {
				missing = append(missing, "hybrid_edge.worker_name")
			}
			if strings.TrimSpace(hybrid.AssetsSHA256) == "" {
				missing = append(missing, "hybrid_edge.assets_sha256")
			}
			if strings.TrimSpace(hybrid.KVNamespaceID) == "" {
				missing = append(missing, "hybrid_edge.kv_namespace_id")
			}
			if strings.TrimSpace(hybrid.ManifestSHA256) == "" {
				missing = append(missing, "hybrid_edge.manifest_sha256")
			}
			if hybrid.ManifestSize <= 0 {
				missing = append(missing, "hybrid_edge.manifest_size")
			}
			if !hasNonEmpty(hybrid.Domains) && !hasNonEmpty(hybrid.URLs) && !hasNonEmpty(dep.ProductionURLs) && !hasNonEmpty(dep.SiteDomains) {
				missing = append(missing, "hybrid_edge.domains")
			}
			if strings.TrimSpace(hybrid.VerificationStatus) == "" {
				missing = append(missing, "hybrid_edge.verification_status")
			} else if !strings.EqualFold(strings.TrimSpace(hybrid.VerificationStatus), "ok") {
				missing = append(missing, "hybrid_edge.verification_status=ok")
			}
			if strings.TrimSpace(hybrid.VerifiedAt) == "" {
				missing = append(missing, "hybrid_edge.verified_at")
			}
			if strings.TrimSpace(hybrid.PublishedAt) == "" {
				missing = append(missing, "hybrid_edge.published_at")
			}
		}
		if strings.TrimSpace(dep.ManifestKey) == "" {
			missing = append(missing, "edge_manifest_key")
		}
	} else {
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
	if target == "cloudflare_static" || target == "hybrid_edge" {
		if domains := rollbackCloudflareStaticDomains(dep); len(domains) > 0 {
			parts = append(parts, "-domains "+cliHintArg(strings.Join(domains, ",")))
		}
	}
	if target == "cloudflare_static" {
		if worker := rollbackCloudflareStaticWorkerName(dep); worker != "" {
			parts = append(parts, "-static-name "+cliHintArg(worker))
		}
		if cachePolicy := rollbackCloudflareStaticCachePolicy(dep); cachePolicy != "" {
			parts = append(parts, "-static-cache-policy "+cliHintArg(cachePolicy))
		}
		if notFound := rollbackCloudflareStaticNotFoundHandling(dep); notFound == cloudflareStaticNotFoundSPA {
			parts = append(parts, "-static-spa")
		} else if notFound != "" {
			parts = append(parts, "-static-not-found-handling "+cliHintArg(notFound))
		}
	}
	if target == "hybrid_edge" {
		if worker := rollbackHybridEdgeWorkerName(dep); worker != "" {
			parts = append(parts, "-edge-name "+cliHintArg(worker))
		}
		if kvNamespaceID := rollbackHybridEdgeKVNamespaceID(dep); kvNamespaceID != "" {
			parts = append(parts, "-edge-kv-namespace-id "+cliHintArg(kvNamespaceID))
		}
		if kvNamespace := rollbackHybridEdgeKVNamespace(dep); kvNamespace != "" {
			parts = append(parts, "-edge-kv-namespace "+cliHintArg(kvNamespace))
		}
		if manifestMode := rollbackHybridEdgeManifestMode(dep); manifestMode != "" {
			parts = append(parts, "-edge-manifest-mode "+cliHintArg(manifestMode))
		}
		if defaultCacheControl := rollbackHybridEdgeDefaultCacheControl(dep); defaultCacheControl != "" {
			parts = append(parts, "-edge-default-cache-control "+cliHintArg(defaultCacheControl))
		}
		if dep.HybridEdge != nil && dep.HybridEdge.EntryOriginFallback {
			parts = append(parts, "-entry-origin-fallback")
		}
	}
	return strings.Join(parts, " ")
}

func rollbackApplyCommand(dep rollbackPlanDeployment, sourceDir string) string {
	parts := []string{
		"supercdnctl rollback-apply",
		"-site " + cliHintArg(dep.SiteID),
		"-deployment " + cliHintArg(dep.ID),
	}
	if strings.TrimSpace(sourceDir) != "" {
		parts = append(parts, "-dir "+cliHintArg(sourceDir))
	}
	parts = append(parts, "-dry-run=false", "-confirm rollback")
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

func rollbackCloudflareStaticWorkerName(dep rollbackPlanDeployment) string {
	if dep.CloudflareStatic != nil && strings.TrimSpace(dep.CloudflareStatic.WorkerName) != "" {
		return strings.TrimSpace(dep.CloudflareStatic.WorkerName)
	}
	return "supercdn-" + cleanWorkerName(dep.SiteID) + "-static"
}

func rollbackCloudflareStaticCompatibilityDate(dep rollbackPlanDeployment) string {
	if dep.CloudflareStatic != nil && strings.TrimSpace(dep.CloudflareStatic.CompatibilityDate) != "" {
		return strings.TrimSpace(dep.CloudflareStatic.CompatibilityDate)
	}
	return time.Now().UTC().Format("2006-01-02")
}

func rollbackCloudflareStaticCachePolicy(dep rollbackPlanDeployment) string {
	if dep.CloudflareStatic != nil && strings.TrimSpace(dep.CloudflareStatic.CachePolicy) != "" {
		return strings.TrimSpace(dep.CloudflareStatic.CachePolicy)
	}
	return cloudflareStaticCachePolicyAuto
}

func rollbackCloudflareStaticNotFoundHandling(dep rollbackPlanDeployment) string {
	if dep.CloudflareStatic != nil {
		return strings.TrimSpace(dep.CloudflareStatic.NotFoundHandling)
	}
	return ""
}

func rollbackCloudflareStaticDomains(dep rollbackPlanDeployment) []string {
	var values []string
	if dep.CloudflareStatic != nil {
		values = append(values, dep.CloudflareStatic.Domains...)
		for _, raw := range dep.CloudflareStatic.URLs {
			if host := rollbackHostFromURL(raw); host != "" {
				values = append(values, host)
			}
		}
	}
	if dep.HybridEdge != nil {
		values = append(values, dep.HybridEdge.Domains...)
		for _, raw := range dep.HybridEdge.URLs {
			if host := rollbackHostFromURL(raw); host != "" {
				values = append(values, host)
			}
		}
	}
	values = append(values, dep.SiteDomains...)
	for _, raw := range dep.ProductionURLs {
		if host := rollbackHostFromURL(raw); host != "" {
			values = append(values, host)
		}
	}
	return cleanDomains(values)
}

func rollbackHybridEdgeWorkerName(dep rollbackPlanDeployment) string {
	if dep.HybridEdge != nil && strings.TrimSpace(dep.HybridEdge.WorkerName) != "" {
		return strings.TrimSpace(dep.HybridEdge.WorkerName)
	}
	return "supercdn-" + cleanWorkerName(dep.SiteID) + "-edge"
}

func rollbackHybridEdgeKVNamespaceID(dep rollbackPlanDeployment) string {
	if dep.HybridEdge != nil {
		return strings.TrimSpace(dep.HybridEdge.KVNamespaceID)
	}
	return ""
}

func rollbackHybridEdgeKVNamespace(dep rollbackPlanDeployment) string {
	if dep.HybridEdge != nil {
		return strings.TrimSpace(dep.HybridEdge.KVNamespace)
	}
	return ""
}

func rollbackHybridEdgeManifestMode(dep rollbackPlanDeployment) string {
	if dep.HybridEdge != nil {
		return strings.TrimSpace(dep.HybridEdge.ManifestMode)
	}
	return ""
}

func rollbackHybridEdgeDefaultCacheControl(dep rollbackPlanDeployment) string {
	if dep.HybridEdge != nil {
		return strings.TrimSpace(dep.HybridEdge.DefaultCacheControl)
	}
	return ""
}

func rollbackHybridEdgeCompatibilityDate(dep rollbackPlanDeployment) string {
	if dep.HybridEdge != nil && strings.TrimSpace(dep.HybridEdge.CompatibilityDate) != "" {
		return strings.TrimSpace(dep.HybridEdge.CompatibilityDate)
	}
	return time.Now().UTC().Format("2006-01-02")
}

func rollbackHybridEdgeCachePolicy(dep rollbackPlanDeployment) string {
	if dep.HybridEdge != nil && strings.TrimSpace(dep.HybridEdge.CachePolicy) != "" {
		return strings.TrimSpace(dep.HybridEdge.CachePolicy)
	}
	return cloudflareStaticCachePolicyAuto
}

func rollbackHybridEdgeNotFoundHandling(dep rollbackPlanDeployment) string {
	if dep.HybridEdge != nil {
		return strings.TrimSpace(dep.HybridEdge.NotFoundHandling)
	}
	return ""
}

func rollbackHostFromURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	return parsed.Hostname()
}
