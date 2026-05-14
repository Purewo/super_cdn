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

type switchPlanDoctorResponse struct {
	Status          string                     `json:"status"`
	Path            string                     `json:"path,omitempty"`
	RouteProfile    *switchPlanRouteProfile    `json:"route_profile,omitempty"`
	Deployment      *switchPlanDeployment      `json:"deployment,omitempty"`
	Warnings        []string                   `json:"warnings,omitempty"`
	Recommendations []switchPlanRecommendation `json:"recommendations,omitempty"`
	NextCommands    []string                   `json:"next_commands,omitempty"`
	Checks          []switchPlanCheck          `json:"checks,omitempty"`
	Candidates      []switchPlanCandidate      `json:"candidates,omitempty"`
	Selection       *switchPlanSelection       `json:"selection,omitempty"`
	Route           *switchPlanRoute           `json:"route,omitempty"`
}

type switchPlanRouteProfile struct {
	Name          string `json:"name,omitempty"`
	RoutingPolicy string `json:"routing_policy,omitempty"`
}

type switchPlanDeployment struct {
	ID               string `json:"id,omitempty"`
	DeploymentTarget string `json:"deployment_target,omitempty"`
	RoutingPolicy    string `json:"routing_policy,omitempty"`
	ResourceFailover bool   `json:"resource_failover,omitempty"`
}

type switchPlanRoute struct {
	Path             string                `json:"path,omitempty"`
	RoutingPolicy    string                `json:"routing_policy,omitempty"`
	ResourceFailover bool                  `json:"resource_failover,omitempty"`
	Warnings         []string              `json:"warnings,omitempty"`
	Candidates       []switchPlanCandidate `json:"candidates,omitempty"`
	Selection        *switchPlanSelection  `json:"selection,omitempty"`
}

type switchPlanCandidate struct {
	Target        string `json:"target"`
	TargetType    string `json:"target_type,omitempty"`
	RegionGroup   string `json:"region_group,omitempty"`
	Status        string `json:"status"`
	ReplicaStatus string `json:"replica_status,omitempty"`
	Reason        string `json:"reason,omitempty"`
	Selected      bool   `json:"selected,omitempty"`
}

type switchPlanSelection struct {
	Target      string `json:"target,omitempty"`
	TargetType  string `json:"target_type,omitempty"`
	RegionGroup string `json:"region_group,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

type switchPlanRecommendation struct {
	Action  string `json:"action"`
	Level   string `json:"level"`
	Summary string `json:"summary"`
	Reason  string `json:"reason,omitempty"`
	Command string `json:"command,omitempty"`
}

type switchPlanCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

type switchPlanOutput struct {
	Status          string                     `json:"status"`
	Mode            string                     `json:"mode"`
	Resource        string                     `json:"resource"`
	Path            string                     `json:"path"`
	SafeToSwitch    bool                       `json:"safe_to_switch"`
	CandidateReady  bool                       `json:"candidate_ready"`
	ApplySupported  bool                       `json:"apply_supported"`
	ApplyReason     string                     `json:"apply_reason,omitempty"`
	CandidateCount  int                        `json:"candidate_count"`
	ReadyCandidates int                        `json:"ready_candidates"`
	SelectedTarget  string                     `json:"selected_target,omitempty"`
	Warnings        []string                   `json:"warnings,omitempty"`
	Risks           []string                   `json:"risks,omitempty"`
	Candidates      []switchPlanCandidate      `json:"candidates,omitempty"`
	Recommendations []switchPlanRecommendation `json:"recommendations,omitempty"`
	NextCommands    []string                   `json:"next_commands,omitempty"`
}

func switchPlan(c client, args []string) error {
	fs := flag.NewFlagSet("switch-plan", flag.ExitOnError)
	bucket := fs.String("bucket", "", "asset bucket slug to inspect")
	site := fs.String("site", "", "site id to inspect")
	routePath := fs.String("path", "", "bucket logical path or site request path")
	deployment := fs.String("deployment", "", "site deployment id; empty uses active production deployment")
	country := fs.String("country", "", "simulated Cloudflare country code, for example CN")
	clientIP := fs.String("client-ip", "", "simulated client IP for deterministic load-balance hashing")
	_ = fs.Parse(args)

	bucketValue := strings.TrimSpace(*bucket)
	siteValue := strings.TrimSpace(*site)
	pathValue := strings.TrimSpace(*routePath)
	if (bucketValue == "") == (siteValue == "") {
		return errors.New("exactly one of -bucket or -site is required")
	}
	if pathValue == "" {
		return errors.New("-path is required")
	}
	if bucketValue != "" {
		raw, err := c.doRaw(http.MethodGet, "/api/v1/asset-buckets/"+url.PathEscape(bucketValue)+"/doctor?"+switchPlanQuery(pathValue, "", *country, *clientIP), nil, "")
		if err != nil {
			return err
		}
		return printSwitchPlan("bucket", bucketValue, pathValue, raw)
	}
	raw, err := c.doRaw(http.MethodGet, "/api/v1/sites/"+url.PathEscape(siteValue)+"/doctor?"+switchPlanQuery(pathValue, *deployment, *country, *clientIP), nil, "")
	if err != nil {
		return err
	}
	return printSwitchPlan("site", siteValue, pathValue, raw)
}

func switchApply(c client, args []string) error {
	fs := flag.NewFlagSet("switch-apply", flag.ExitOnError)
	bucket := fs.String("bucket", "", "asset bucket slug to switch")
	site := fs.String("site", "", "site id to switch")
	routePath := fs.String("path", "", "bucket logical path or site request path")
	target := fs.String("target", "", "ready replica target to make primary")
	deployment := fs.String("deployment", "", "site deployment id; empty uses active production deployment")
	expectedCurrent := fs.String("expected-current", "", "optional current primary target guard")
	dryRun := fs.Bool("dry-run", true, "plan only; pass -dry-run=false with -confirm switch to apply")
	confirm := fs.String("confirm", "", "must be switch when dry-run=false")
	_ = fs.Parse(args)

	bucketValue := strings.TrimSpace(*bucket)
	siteValue := strings.TrimSpace(*site)
	pathValue := strings.TrimSpace(*routePath)
	targetValue := strings.TrimSpace(*target)
	if (bucketValue == "") == (siteValue == "") {
		return errors.New("exactly one of -bucket or -site is required")
	}
	if pathValue == "" {
		return errors.New("-path is required")
	}
	if targetValue == "" {
		return errors.New("-target is required")
	}
	body := map[string]any{
		"path":    pathValue,
		"target":  targetValue,
		"dry_run": *dryRun,
	}
	if strings.TrimSpace(*expectedCurrent) != "" {
		body["expected_current_target"] = strings.TrimSpace(*expectedCurrent)
	}
	if strings.TrimSpace(*confirm) != "" {
		body["confirm"] = strings.TrimSpace(*confirm)
	}
	if bucketValue != "" {
		return c.doJSON(http.MethodPost, "/api/v1/asset-buckets/"+url.PathEscape(bucketValue)+"/objects/primary-target", body)
	}
	endpoint := "/api/v1/sites/" + url.PathEscape(siteValue) + "/files/primary-target"
	if strings.TrimSpace(*deployment) != "" {
		endpoint = "/api/v1/sites/" + url.PathEscape(siteValue) + "/deployments/" + url.PathEscape(strings.TrimSpace(*deployment)) + "/files/primary-target"
	}
	return c.doJSON(http.MethodPost, endpoint, body)
}

func switchPlanQuery(pathValue, deployment, country, clientIP string) string {
	q := url.Values{}
	q.Set("path", strings.TrimSpace(pathValue))
	if strings.TrimSpace(deployment) != "" {
		q.Set("deployment", strings.TrimSpace(deployment))
	}
	if strings.TrimSpace(country) != "" {
		q.Set("country", strings.TrimSpace(country))
	}
	if strings.TrimSpace(clientIP) != "" {
		q.Set("client_ip", strings.TrimSpace(clientIP))
	}
	return q.Encode()
}

func printSwitchPlan(mode, resource, pathValue string, raw []byte) error {
	var doctor switchPlanDoctorResponse
	if err := json.Unmarshal(raw, &doctor); err != nil {
		return fmt.Errorf("parse doctor report: %w", err)
	}
	plan := buildSwitchPlan(mode, resource, pathValue, doctor)
	return printJSON(mustJSON(plan))
}

func buildSwitchPlan(mode, resource, pathValue string, doctor switchPlanDoctorResponse) switchPlanOutput {
	candidates := doctor.Candidates
	selection := doctor.Selection
	warnings := append([]string{}, doctor.Warnings...)
	if doctor.Route != nil {
		if len(candidates) == 0 {
			candidates = doctor.Route.Candidates
		}
		if selection == nil {
			selection = doctor.Route.Selection
		}
		warnings = append(warnings, doctor.Route.Warnings...)
	}
	ready := 0
	for _, candidate := range candidates {
		if candidate.Status == "ready" {
			ready++
		}
	}
	candidateRisks := switchPlanRisks(doctor, len(candidates), ready)
	applySupported, applyReason := switchPlanApplySupport(mode, doctor)
	risks := append([]string{}, candidateRisks...)
	if !applySupported {
		risks = append(risks, applyReason)
	}
	candidateReady := len(candidateRisks) == 0 && ready >= 2
	nextCommands := append([]string{}, doctor.NextCommands...)
	if !applySupported {
		nextCommands = append(nextCommands, switchPlanUnsupportedApplyCommands(mode, resource, firstNonEmpty(doctor.Path, pathValue), doctor)...)
	}
	out := switchPlanOutput{
		Status:          doctor.Status,
		Mode:            mode,
		Resource:        resource,
		Path:            firstNonEmpty(doctor.Path, pathValue),
		SafeToSwitch:    candidateReady && applySupported,
		CandidateReady:  candidateReady,
		ApplySupported:  applySupported,
		ApplyReason:     applyReason,
		CandidateCount:  len(candidates),
		ReadyCandidates: ready,
		Warnings:        compactStrings(warnings),
		Risks:           compactStrings(risks),
		Candidates:      candidates,
		Recommendations: doctor.Recommendations,
		NextCommands:    compactStrings(nextCommands),
	}
	if selection != nil {
		out.SelectedTarget = selection.Target
	}
	return out
}

func switchPlanApplySupport(mode string, doctor switchPlanDoctorResponse) (bool, string) {
	switch mode {
	case "bucket":
		if doctor.RouteProfile != nil && strings.TrimSpace(doctor.RouteProfile.RoutingPolicy) != "" {
			return false, "bucket uses routing_policy " + strings.TrimSpace(doctor.RouteProfile.RoutingPolicy) + "; switch-apply cannot control policy-based live routing"
		}
	case "site":
		if doctor.Route != nil {
			if strings.TrimSpace(doctor.Route.RoutingPolicy) != "" {
				return false, "site route uses routing_policy " + strings.TrimSpace(doctor.Route.RoutingPolicy) + "; switch-apply cannot control policy-based live routing"
			}
			if doctor.Route.ResourceFailover {
				return false, "site route uses resource_failover; switch-apply cannot control failover candidate order"
			}
		}
		if doctor.Deployment != nil {
			if strings.TrimSpace(doctor.Deployment.RoutingPolicy) != "" {
				return false, "deployment uses routing_policy " + strings.TrimSpace(doctor.Deployment.RoutingPolicy) + "; switch-apply cannot control policy-based live routing"
			}
			if doctor.Deployment.ResourceFailover {
				return false, "deployment uses resource_failover; switch-apply cannot control failover candidate order"
			}
		}
	}
	return true, ""
}

func switchPlanUnsupportedApplyCommands(mode, resource, pathValue string, doctor switchPlanDoctorResponse) []string {
	var commands []string
	switch mode {
	case "bucket":
		if doctor.RouteProfile != nil && strings.TrimSpace(doctor.RouteProfile.RoutingPolicy) != "" {
			commands = append(commands, "supercdnctl routing-policy-status -policy "+cliHintArg(doctor.RouteProfile.RoutingPolicy))
		}
	case "site":
		routePolicy := ""
		resourceFailover := false
		if doctor.Route != nil {
			routePolicy = strings.TrimSpace(doctor.Route.RoutingPolicy)
			resourceFailover = doctor.Route.ResourceFailover
		}
		if routePolicy == "" && doctor.Deployment != nil {
			routePolicy = strings.TrimSpace(doctor.Deployment.RoutingPolicy)
			resourceFailover = resourceFailover || doctor.Deployment.ResourceFailover
		}
		if routePolicy != "" {
			commands = append(commands, "supercdnctl routing-policy-status -policy "+cliHintArg(routePolicy))
		}
		if routePolicy != "" || resourceFailover {
			commands = append(commands, "supercdnctl route-explain -site "+cliHintArg(resource)+" -path "+cliHintArg(pathValue))
		}
	}
	return commands
}

func switchPlanRisks(doctor switchPlanDoctorResponse, candidateCount, ready int) []string {
	var risks []string
	if strings.EqualFold(doctor.Status, "error") {
		risks = append(risks, "doctor report status is error")
	}
	for _, check := range doctor.Checks {
		if strings.EqualFold(check.Status, "error") {
			risks = append(risks, "check "+check.Name+" is error")
		}
	}
	for _, recommendation := range doctor.Recommendations {
		if strings.EqualFold(recommendation.Level, "error") {
			risks = append(risks, recommendation.Summary)
		}
	}
	if candidateCount == 0 {
		risks = append(risks, "no delivery candidates were reported for this path")
	} else if ready < 2 {
		risks = append(risks, "fewer than two ready candidates are available")
	}
	return risks
}

func compactStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
