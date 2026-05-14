package main

import (
	"errors"
	"flag"
	"net/http"
	"net/url"
	"strings"
)

func getJob(c client, args []string) error {
	fs := flag.NewFlagSet("job", flag.ExitOnError)
	id := fs.String("id", "", "job id")
	_ = fs.Parse(args)
	if *id == "" {
		return errors.New("-id is required")
	}
	return c.do(http.MethodGet, "/api/v1/jobs/"+*id, nil, "")
}

func replicas(c client, args []string) error {
	fs := flag.NewFlagSet("replicas", flag.ExitOnError)
	id := fs.String("object-id", "", "object id")
	_ = fs.Parse(args)
	if *id == "" {
		return errors.New("-object-id is required")
	}
	return c.do(http.MethodGet, "/api/v1/objects/"+*id+"/replicas", nil, "")
}

func refreshReplicas(c client, args []string) error {
	fs := flag.NewFlagSet("refresh-replicas", flag.ExitOnError)
	id := fs.String("object-id", "", "object id")
	bucket := fs.String("bucket", "", "asset bucket slug")
	target := fs.String("target", "", "optional replica target to refresh")
	dst := fs.String("path", "", "logical path inside the bucket")
	paths := fs.String("paths", "", "comma-separated logical paths inside the bucket")
	prefix := fs.String("prefix", "", "refresh objects whose logical path is under this prefix")
	all := fs.Bool("all", false, "refresh all tracked objects in the bucket")
	limit := fs.Int("limit", 0, "maximum objects for prefix selection")
	_ = fs.Parse(args)
	if strings.TrimSpace(*id) == "" && strings.TrimSpace(*bucket) == "" {
		return errors.New("one of -object-id or -bucket is required")
	}
	if strings.TrimSpace(*id) != "" && strings.TrimSpace(*bucket) != "" {
		return errors.New("choose only one of -object-id or -bucket")
	}
	if strings.TrimSpace(*id) != "" {
		return c.doJSON(http.MethodPost, "/api/v1/objects/"+url.PathEscape(*id)+"/replicas/refresh", map[string]any{
			"target": strings.TrimSpace(*target),
		})
	}
	exactPaths := splitCSV(*paths)
	if strings.TrimSpace(*dst) != "" {
		exactPaths = append([]string{strings.TrimSpace(*dst)}, exactPaths...)
	}
	modes := 0
	if len(exactPaths) > 0 {
		modes++
	}
	if strings.TrimSpace(*prefix) != "" {
		modes++
	}
	if *all {
		modes++
	}
	if modes > 1 {
		return errors.New("choose only one of -path/-paths, -prefix, or -all")
	}
	req := map[string]any{
		"target": strings.TrimSpace(*target),
	}
	if len(exactPaths) == 1 {
		req["path"] = exactPaths[0]
	} else if len(exactPaths) > 1 {
		req["paths"] = exactPaths
	} else if strings.TrimSpace(*prefix) != "" {
		req["prefix"] = strings.TrimSpace(*prefix)
		if *limit > 0 {
			req["limit"] = *limit
		}
	} else {
		req["all"] = true
	}
	if *all {
		req["all"] = true
	}
	return c.doJSON(http.MethodPost, "/api/v1/asset-buckets/"+url.PathEscape(*bucket)+"/replicas/refresh", req)
}

func repairReplicas(c client, args []string) error {
	fs := flag.NewFlagSet("repair-replicas", flag.ExitOnError)
	id := fs.String("object-id", "", "object id")
	target := fs.String("target", "", "optional replica target to repair")
	force := fs.Bool("force", false, "requeue ready or pending replicas too")
	_ = fs.Parse(args)
	if *id == "" {
		return errors.New("-object-id is required")
	}
	return c.doJSON(http.MethodPost, "/api/v1/objects/"+*id+"/replicas/repair", map[string]any{
		"target": strings.TrimSpace(*target),
		"force":  *force,
	})
}

func purge(c client, args []string) error {
	fs := flag.NewFlagSet("purge", flag.ExitOnError)
	urls := fs.String("urls", "", "comma-separated URLs")
	cfAccount := fs.String("cloudflare-account", "", "Cloudflare account name")
	_ = fs.Parse(args)
	if *urls == "" {
		return errors.New("-urls is required")
	}
	return c.doJSON(http.MethodPost, "/api/v1/cache/purge", map[string]any{
		"urls":               splitCSV(*urls),
		"cloudflare_account": *cfAccount,
	})
}

func purgeSite(c client, args []string) error {
	fs := flag.NewFlagSet("purge-site", flag.ExitOnError)
	site := fs.String("site", "", "site id")
	deployment := fs.String("deployment", "", "deployment id; empty means active production deployment")
	cfAccount := fs.String("cloudflare-account", "", "Cloudflare account name; defaults by domain match")
	cfLibrary := fs.String("cloudflare-library", "", "Cloudflare library name")
	dryRun := fs.Bool("dry-run", false, "generate purge URLs without calling Cloudflare")
	_ = fs.Parse(args)
	if *site == "" {
		return errors.New("-site is required")
	}
	apiPath := "/api/v1/sites/" + url.PathEscape(*site) + "/purge"
	if *deployment != "" {
		apiPath = "/api/v1/sites/" + url.PathEscape(*site) + "/deployments/" + url.PathEscape(*deployment) + "/purge"
	}
	return c.doJSON(http.MethodPost, apiPath, map[string]any{
		"cloudflare_account": *cfAccount,
		"cloudflare_library": *cfLibrary,
		"dry_run":            *dryRun,
	})
}
