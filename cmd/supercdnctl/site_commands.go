package main

import (
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
)

func createSite(c client, args []string) error {
	fs := flag.NewFlagSet("create-site", flag.ExitOnError)
	site := fs.String("site", "", "site id")
	name := fs.String("name", "", "site display name")
	profile := fs.String("profile", "overseas", "route profile")
	target := fs.String("target", "", "deployment target: origin_assisted, cloudflare_static, or hybrid_edge")
	routingPolicy := fs.String("routing-policy", "", "routing policy name; requires matching multi-source route profile")
	mode := fs.String("mode", "standard", "standard or spa")
	domains := fs.String("domains", "", "comma-separated domains")
	defaultDomainID := fs.String("domain-id", "", "default allocated subdomain id")
	randomDomain := fs.Bool("random-domain", false, "append random suffix to the default allocated domain")
	noDefaultDomain := fs.Bool("no-default-domain", false, "do not allocate the configured default site domain")
	_ = fs.Parse(args)
	if *site == "" {
		return errors.New("-site is required")
	}
	req := map[string]any{
		"id":                    *site,
		"name":                  *name,
		"route_profile":         *profile,
		"deployment_target":     *target,
		"routing_policy":        *routingPolicy,
		"mode":                  *mode,
		"domains":               splitCSV(*domains),
		"default_domain_id":     *defaultDomainID,
		"random_default_domain": *randomDomain,
		"skip_default_domain":   *noDefaultDomain,
	}
	return c.doJSON(http.MethodPost, "/api/v1/sites", req)
}

func listSites(c client, args []string) error {
	fs := flag.NewFlagSet("list-sites", flag.ExitOnError)
	_ = fs.Parse(args)
	return c.do(http.MethodGet, "/api/v1/sites", nil, "")
}

func offlineSite(c client, args []string) error {
	fs := flag.NewFlagSet("offline-site", flag.ExitOnError)
	site := fs.String("site", "", "site id")
	_ = fs.Parse(args)
	if *site == "" {
		return errors.New("-site is required")
	}
	return c.doJSON(http.MethodPost, "/api/v1/sites/"+url.PathEscape(*site)+"/offline", map[string]any{})
}

func onlineSite(c client, args []string) error {
	fs := flag.NewFlagSet("online-site", flag.ExitOnError)
	site := fs.String("site", "", "site id")
	_ = fs.Parse(args)
	if *site == "" {
		return errors.New("-site is required")
	}
	return c.doJSON(http.MethodPost, "/api/v1/sites/"+url.PathEscape(*site)+"/online", map[string]any{})
}

func deleteSite(c client, args []string) error {
	fs := flag.NewFlagSet("delete-site", flag.ExitOnError)
	site := fs.String("site", "", "site id")
	force := fs.Bool("force", false, "required; delete the site and all tracked resource objects")
	deleteRemote := fs.Bool("delete-remote", true, "delete remote object replicas while deleting tracked site resources")
	_ = fs.Parse(args)
	if *site == "" {
		return errors.New("-site is required")
	}
	if !*force {
		return errors.New("-force is required")
	}
	q := url.Values{}
	q.Set("force", "true")
	q.Set("delete_remote", fmt.Sprint(*deleteRemote))
	return c.do(http.MethodDelete, "/api/v1/sites/"+url.PathEscape(*site)+"?"+q.Encode(), nil, "")
}

func bindDomain(c client, args []string) error {
	fs := flag.NewFlagSet("bind-domain", flag.ExitOnError)
	site := fs.String("site", "", "site id")
	domains := fs.String("domains", "", "comma-separated domains")
	defaultDomainID := fs.String("domain-id", "", "default allocated subdomain id")
	randomDomain := fs.Bool("random-domain", false, "append random suffix to the default allocated domain")
	noDefaultDomain := fs.Bool("no-default-domain", false, "do not allocate the configured default site domain")
	replace := fs.Bool("replace", false, "replace existing domain bindings instead of appending")
	_ = fs.Parse(args)
	if *site == "" {
		return errors.New("-site is required")
	}
	if *domains == "" && *defaultDomainID == "" && !*randomDomain && *noDefaultDomain {
		return errors.New("-domains, -domain-id or -random-domain is required")
	}
	req := map[string]any{
		"domains":               splitCSV(*domains),
		"default_domain_id":     *defaultDomainID,
		"random_default_domain": *randomDomain,
		"skip_default_domain":   *noDefaultDomain,
		"append":                !*replace,
	}
	return c.doJSON(http.MethodPost, "/api/v1/sites/"+url.PathEscape(*site)+"/domains", req)
}

func domainStatus(c client, args []string) error {
	fs := flag.NewFlagSet("domain-status", flag.ExitOnError)
	domain := fs.String("domain", "", "domain to check")
	_ = fs.Parse(args)
	if *domain == "" {
		return errors.New("-domain is required")
	}
	return c.do(http.MethodGet, "/api/v1/domains/"+url.PathEscape(*domain)+"/status", nil, "")
}
