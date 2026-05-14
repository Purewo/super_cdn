package main

import (
	"errors"
	"flag"
	"net/http"
	"net/url"
	"strings"
)

func initLibraries(c client, args []string) error {
	fs := flag.NewFlagSet("init-libraries", flag.ExitOnError)
	libraries := fs.String("libraries", "", "comma-separated resource library names; empty means all")
	dirs := fs.String("dirs", "", "comma-separated directories; empty means Super CDN defaults")
	dryRun := fs.Bool("dry-run", false, "return the initialization plan without creating directories")
	_ = fs.Parse(args)
	req := map[string]any{
		"libraries":   splitCSV(*libraries),
		"directories": splitCSV(*dirs),
		"dry_run":     *dryRun,
	}
	return c.doJSON(http.MethodPost, "/api/v1/init/resource-libraries", req)
}

func getInitJob(c client, args []string) error {
	fs := flag.NewFlagSet("init-job", flag.ExitOnError)
	id := fs.String("id", "", "init job id")
	_ = fs.Parse(args)
	if *id == "" {
		return errors.New("-id is required")
	}
	return c.do(http.MethodGet, "/api/v1/init/jobs/"+*id, nil, "")
}

func resourceStatus(c client, args []string) error {
	fs := flag.NewFlagSet("resource-status", flag.ExitOnError)
	library := fs.String("library", "", "resource library name")
	_ = fs.Parse(args)
	path := "/api/v1/resource-libraries/status"
	if *library != "" {
		path += "?library=" + url.QueryEscape(*library)
	}
	return c.do(http.MethodGet, path, nil, "")
}

func routingPolicyStatus(c client, args []string) error {
	fs := flag.NewFlagSet("routing-policy-status", flag.ExitOnError)
	policy := fs.String("policy", "", "routing policy name; empty shows all policies")
	_ = fs.Parse(args)
	path := "/api/v1/routing-policies/status"
	if strings.TrimSpace(*policy) != "" {
		path += "?policy=" + url.QueryEscape(strings.TrimSpace(*policy))
	}
	return c.do(http.MethodGet, path, nil, "")
}

func healthCheck(c client, args []string) error {
	fs := flag.NewFlagSet("health-check", flag.ExitOnError)
	libraries := fs.String("libraries", "", "comma-separated resource library names; empty means all")
	writeProbe := fs.Bool("write-probe", false, "explicitly upload/read/delete a small temporary probe")
	force := fs.Bool("force", false, "bypass local health check cooldown")
	minInterval := fs.Int("min-interval", 0, "minimum seconds between remote checks; 0 uses server default")
	_ = fs.Parse(args)
	req := map[string]any{
		"libraries":            splitCSV(*libraries),
		"write_probe":          *writeProbe,
		"force":                *force,
		"min_interval_seconds": *minInterval,
	}
	return c.doJSON(http.MethodPost, "/api/v1/resource-libraries/health-check", req)
}

func e2eProbe(c client, args []string) error {
	fs := flag.NewFlagSet("e2e-probe", flag.ExitOnError)
	profile := fs.String("profile", "china_all", "route profile to probe")
	keep := fs.Bool("keep", false, "keep remote file and local object records")
	_ = fs.Parse(args)
	req := map[string]any{
		"route_profile": *profile,
		"keep":          *keep,
	}
	return c.doJSON(http.MethodPost, "/api/v1/resource-libraries/e2e-probe", req)
}
