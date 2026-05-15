package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"supercdn/internal/siteinspect"
	"supercdn/internal/siteprobe"
	"supercdn/internal/urlredact"
)

func inspectSite(args []string) error {
	fs := flag.NewFlagSet("inspect-site", flag.ExitOnError)
	dir := fs.String("dir", "", "dist directory to inspect")
	bundle := fs.String("bundle", "", "zip artifact to inspect")
	_ = fs.Parse(args)
	if *dir == "" && *bundle == "" {
		return errors.New("-dir or -bundle is required")
	}
	if *dir != "" && *bundle != "" {
		return errors.New("use either -dir or -bundle, not both")
	}
	var (
		report siteinspect.Report
		err    error
	)
	if *dir != "" {
		report, err = siteinspect.InspectDirectory(*dir)
	} else {
		report, err = siteinspect.InspectZip(*bundle)
	}
	if err != nil {
		return err
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return err
	}
	return printJSON(raw)
}

func probeSite(c client, args []string) error {
	fs := flag.NewFlagSet("probe-site", flag.ExitOnError)
	site := fs.String("site", "", "site id")
	deployment := fs.String("deployment", "", "deployment id; defaults to active production deployment")
	probeURL := fs.String("url", "", "absolute public site URL to probe")
	preview := fs.Bool("preview", false, "probe the deployment preview URL")
	production := fs.Bool("production", false, "probe the production URL when -deployment is set")
	spaPath := fs.String("spa-path", "", "optional SPA route path to verify as HTML")
	origin := fs.String("origin", "", "Origin header for redirected asset checks; defaults to the probe URL origin")
	resolver := fs.String("resolver", "", "DNS resolver for HTTP probes, for example 1.1.1.1:53")
	maxAssets := fs.Int("max-assets", 20, "maximum JS/CSS assets to probe from index HTML")
	timeout := fs.Duration("timeout", 30*time.Second, "overall probe timeout")
	requireDirectAssets := fs.Bool("require-direct-assets", false, "fail if JS/CSS assets redirect away from the probed site")
	requireEdgeStaticHTML := fs.Bool("require-edge-static-html", false, "fail if root HTML or SPA fallback is not served by Cloudflare Static Assets")
	requireEdgeManifestAssets := fs.Bool("require-edge-manifest-assets", false, "fail if JS/CSS asset first hops are not routed by the edge manifest")
	requireHTMLRevalidate := fs.Bool("require-html-revalidate", false, "fail if root HTML is not served with a revalidating cache policy")
	requireImmutableAssets := fs.Bool("require-immutable-assets", false, "fail if JS/CSS assets are not served with immutable long-term cache policy")
	browserRender := fs.Bool("browser-render", false, "run an installed Chrome/Edge headless screenshot check for blank-page detection")
	browserPath := fs.String("browser-path", "", "Chrome/Edge executable path for -browser-render")
	browserTimeout := fs.Duration("browser-timeout", defaultBrowserRenderTimeout, "maximum time for -browser-render")
	browserVirtualTime := fs.Duration("browser-virtual-time", defaultBrowserVirtualTimeBudget, "virtual time budget for browser JavaScript rendering")
	browserWidth := fs.Int("browser-width", defaultBrowserRenderWidth, "browser screenshot viewport width")
	browserHeight := fs.Int("browser-height", defaultBrowserRenderHeight, "browser screenshot viewport height")
	browserNonWhiteThreshold := fs.Float64("browser-non-white-threshold", defaultBrowserNonWhiteThreshold, "minimum non-white pixel ratio for -browser-render")
	browserScreenshot := fs.String("browser-screenshot", "", "optional path to keep the browser screenshot")
	redactURLs := fs.Bool("redact-urls", true, "redact query values for signed URLs from JSON output")
	_ = fs.Parse(args)
	if *preview && *production {
		return errors.New("use either -preview or -production, not both")
	}
	targetURL := strings.TrimSpace(*probeURL)
	if targetURL == "" {
		if *site == "" {
			return errors.New("-site or -url is required")
		}
		if c.token == "" {
			return errors.New("token is required when resolving a site deployment; pass -token, SUPERCDN_TOKEN, or use -url")
		}
		dep, err := loadProbeDeployment(c, *site, *deployment)
		if err != nil {
			return err
		}
		preferPreview := *preview || (*deployment != "" && !*production)
		targetURL, err = deploymentProbeURL(dep, preferPreview)
		if err != nil {
			return err
		}
	}
	report, err := runSiteProbe(*resolver, siteprobe.Options{
		URL:                        targetURL,
		Origin:                     *origin,
		SPAPath:                    *spaPath,
		MaxAssets:                  *maxAssets,
		Timeout:                    *timeout,
		RequireDirectAssets:        *requireDirectAssets,
		RequireEdgeStaticHTML:      *requireEdgeStaticHTML,
		RequireEdgeManifestAssets:  *requireEdgeManifestAssets,
		RequireHTMLRevalidate:      *requireHTMLRevalidate,
		RequireImmutableAssetCache: *requireImmutableAssets,
	})
	if err != nil {
		return err
	}
	if *browserRender {
		check := runBrowserRenderCheck(context.Background(), browserRenderOptions{
			URL:                targetURL,
			BrowserPath:        *browserPath,
			Timeout:            *browserTimeout,
			VirtualTimeBudget:  *browserVirtualTime,
			Width:              *browserWidth,
			Height:             *browserHeight,
			NonWhiteThreshold:  *browserNonWhiteThreshold,
			KeepScreenshotPath: *browserScreenshot,
		})
		report.Browser = &check
		report.Duration += check.Duration
		if check.OK {
			report.Summary["browser_ok"] = 1
		} else {
			report.Summary["browser_failed"] = 1
			report.OK = false
			report.Errors = append(report.Errors, "browser render failed: "+firstNonEmpty(check.Error, "blank-page check did not pass"))
		}
	}
	if *redactURLs {
		report = redactSignedProbeReport(report)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return err
	}
	if err := printJSON(raw); err != nil {
		return err
	}
	if !report.OK {
		return errors.New("site probe failed")
	}
	return nil
}

type probeDeployment struct {
	ID             string   `json:"id"`
	Environment    string   `json:"environment"`
	Status         string   `json:"status"`
	Active         bool     `json:"active"`
	ProductionURL  string   `json:"production_url"`
	ProductionURLs []string `json:"production_urls"`
	PreviewURL     string   `json:"preview_url"`
}

func loadProbeDeployment(c client, site, deployment string) (probeDeployment, error) {
	if deployment != "" {
		raw, err := c.doRaw(http.MethodGet, "/api/v1/sites/"+url.PathEscape(site)+"/deployments/"+url.PathEscape(deployment), nil, "")
		if err != nil {
			return probeDeployment{}, err
		}
		var dep probeDeployment
		if err := json.Unmarshal(raw, &dep); err != nil {
			return probeDeployment{}, err
		}
		return dep, nil
	}
	raw, err := c.doRaw(http.MethodGet, "/api/v1/sites/"+url.PathEscape(site)+"/deployments?limit=100", nil, "")
	if err != nil {
		return probeDeployment{}, err
	}
	var resp struct {
		Deployments []probeDeployment `json:"deployments"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return probeDeployment{}, err
	}
	for _, dep := range resp.Deployments {
		if dep.Active && strings.EqualFold(dep.Environment, "production") {
			return dep, nil
		}
	}
	for _, dep := range resp.Deployments {
		if dep.Active {
			return dep, nil
		}
	}
	return probeDeployment{}, errors.New("active deployment not found")
}

func deploymentProbeURL(dep probeDeployment, preferPreview bool) (string, error) {
	if preferPreview && dep.PreviewURL != "" {
		return dep.PreviewURL, nil
	}
	if dep.ProductionURL != "" {
		return dep.ProductionURL, nil
	}
	if len(dep.ProductionURLs) > 0 && dep.ProductionURLs[0] != "" {
		return dep.ProductionURLs[0], nil
	}
	if dep.PreviewURL != "" {
		return dep.PreviewURL, nil
	}
	return "", fmt.Errorf("deployment %q has no probeable URL", dep.ID)
}

func runSiteProbe(resolver string, opts siteprobe.Options) (siteprobe.Report, error) {
	httpClient, err := httpClientWithDNSResolver(resolver)
	if err != nil {
		return siteprobe.Report{}, err
	}
	opts.Client = httpClient
	return siteprobe.Run(context.Background(), opts)
}

func redactSignedProbeReport(report siteprobe.Report) siteprobe.Report {
	report.URL = redactSignedURL(report.URL)
	report.FinalURL = redactSignedURL(report.FinalURL)
	report.HTML.URL = redactSignedURL(report.HTML.URL)
	report.HTML.FinalURL = redactSignedURL(report.HTML.FinalURL)
	if report.SPA != nil {
		spa := *report.SPA
		spa.URL = redactSignedURL(spa.URL)
		spa.FinalURL = redactSignedURL(spa.FinalURL)
		report.SPA = &spa
	}
	if report.Browser != nil {
		browser := *report.Browser
		browser.URL = redactSignedURL(browser.URL)
		report.Browser = &browser
	}
	for i := range report.Assets {
		report.Assets[i].URL = redactSignedURL(report.Assets[i].URL)
		report.Assets[i].FinalURL = redactSignedURL(report.Assets[i].FinalURL)
		for j := range report.Assets[i].Chain {
			report.Assets[i].Chain[j].URL = redactSignedURL(report.Assets[i].Chain[j].URL)
			report.Assets[i].Chain[j].Location = redactSignedURL(report.Assets[i].Chain[j].Location)
		}
	}
	return report
}

func redactSignedURL(raw string) string {
	return urlredact.RedactSignedQueryValues(raw)
}
