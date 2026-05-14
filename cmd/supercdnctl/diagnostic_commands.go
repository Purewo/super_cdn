package main

import (
	"errors"
	"flag"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func doctor(c client, args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	resources := fs.Bool("resources", true, "include resource library status; root token required for details")
	routing := fs.Bool("routing", true, "include routing policy status")
	_ = fs.Parse(args)
	q := url.Values{}
	q.Set("resources", strconv.FormatBool(*resources))
	q.Set("routing", strconv.FormatBool(*routing))
	return c.do(http.MethodGet, "/api/v1/doctor?"+q.Encode(), nil, "")
}

func routeExplain(c client, args []string) error {
	fs := flag.NewFlagSet("route-explain", flag.ExitOnError)
	site := fs.String("site", "", "site id")
	routePath := fs.String("path", "", "site request path, for example /assets/app.js")
	deployment := fs.String("deployment", "", "deployment id; empty uses active production deployment")
	country := fs.String("country", "", "simulated Cloudflare country code, for example CN")
	clientIP := fs.String("client-ip", "", "simulated client IP for deterministic load-balance hashing")
	_ = fs.Parse(args)
	if strings.TrimSpace(*site) == "" || strings.TrimSpace(*routePath) == "" {
		return errors.New("-site and -path are required")
	}
	q := url.Values{}
	q.Set("path", strings.TrimSpace(*routePath))
	if strings.TrimSpace(*deployment) != "" {
		q.Set("deployment", strings.TrimSpace(*deployment))
	}
	if strings.TrimSpace(*country) != "" {
		q.Set("country", strings.TrimSpace(*country))
	}
	if strings.TrimSpace(*clientIP) != "" {
		q.Set("client_ip", strings.TrimSpace(*clientIP))
	}
	return c.do(http.MethodGet, "/api/v1/sites/"+url.PathEscape(strings.TrimSpace(*site))+"/route-explain?"+q.Encode(), nil, "")
}

func cdnDoctor(c client, args []string) error {
	fs := flag.NewFlagSet("cdn-doctor", flag.ExitOnError)
	bucket := fs.String("bucket", "", "asset bucket slug")
	objectPath := fs.String("path", "", "optional bucket logical path")
	country := fs.String("country", "", "simulated Cloudflare country code for routing selection, for example CN")
	clientIP := fs.String("client-ip", "", "simulated client IP for deterministic load-balance hashing")
	_ = fs.Parse(args)
	if strings.TrimSpace(*bucket) == "" {
		return errors.New("-bucket is required")
	}
	q := url.Values{}
	if strings.TrimSpace(*objectPath) != "" {
		q.Set("path", strings.TrimSpace(*objectPath))
	}
	if strings.TrimSpace(*country) != "" {
		q.Set("country", strings.TrimSpace(*country))
	}
	if strings.TrimSpace(*clientIP) != "" {
		q.Set("client_ip", strings.TrimSpace(*clientIP))
	}
	apiPath := "/api/v1/asset-buckets/" + url.PathEscape(strings.TrimSpace(*bucket)) + "/doctor"
	if encoded := q.Encode(); encoded != "" {
		apiPath += "?" + encoded
	}
	return c.do(http.MethodGet, apiPath, nil, "")
}

func siteDoctor(c client, args []string) error {
	fs := flag.NewFlagSet("site-doctor", flag.ExitOnError)
	site := fs.String("site", "", "site id")
	routePath := fs.String("path", "", "optional site request path, for example /assets/app.js")
	deployment := fs.String("deployment", "", "deployment id; empty uses active production deployment")
	country := fs.String("country", "", "simulated Cloudflare country code for routing selection, for example CN")
	clientIP := fs.String("client-ip", "", "simulated client IP for deterministic load-balance hashing")
	_ = fs.Parse(args)
	if strings.TrimSpace(*site) == "" {
		return errors.New("-site is required")
	}
	q := url.Values{}
	if strings.TrimSpace(*routePath) != "" {
		q.Set("path", strings.TrimSpace(*routePath))
	}
	if strings.TrimSpace(*deployment) != "" {
		q.Set("deployment", strings.TrimSpace(*deployment))
	}
	if strings.TrimSpace(*country) != "" {
		q.Set("country", strings.TrimSpace(*country))
	}
	if strings.TrimSpace(*clientIP) != "" {
		q.Set("client_ip", strings.TrimSpace(*clientIP))
	}
	apiPath := "/api/v1/sites/" + url.PathEscape(strings.TrimSpace(*site)) + "/doctor"
	if encoded := q.Encode(); encoded != "" {
		apiPath += "?" + encoded
	}
	return c.do(http.MethodGet, apiPath, nil, "")
}
