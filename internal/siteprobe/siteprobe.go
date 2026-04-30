package siteprobe

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	defaultMaxAssets = 20
	maxHTMLBytes     = 2 << 20
	maxAssetBytes    = 64 << 10
	maxRedirects     = 10
)

type Options struct {
	URL                        string
	Origin                     string
	SPAPath                    string
	MaxAssets                  int
	Timeout                    time.Duration
	Client                     *http.Client
	RequireDirectAssets        bool
	RequireEdgeStaticHTML      bool
	RequireEdgeManifestAssets  bool
	RequireHTMLRevalidate      bool
	RequireImmutableAssetCache bool
}

type Report struct {
	OK       bool           `json:"ok"`
	Status   string         `json:"status"`
	URL      string         `json:"url"`
	FinalURL string         `json:"final_url,omitempty"`
	Origin   string         `json:"origin"`
	HTML     Check          `json:"html"`
	Assets   []AssetCheck   `json:"assets,omitempty"`
	SPA      *Check         `json:"spa,omitempty"`
	Warnings []string       `json:"warnings,omitempty"`
	Errors   []string       `json:"errors,omitempty"`
	Summary  map[string]int `json:"summary"`
	Duration int64          `json:"duration_ms"`
}

type Check struct {
	URL          string `json:"url"`
	FinalURL     string `json:"final_url,omitempty"`
	StatusCode   int    `json:"status_code"`
	ContentType  string `json:"content_type,omitempty"`
	CacheControl string `json:"cache_control,omitempty"`
	EdgeSource   string `json:"x_supercdn_edge_source,omitempty"`
	LatencyMS    int64  `json:"latency_ms"`
	OK           bool   `json:"ok"`
	Error        string `json:"error,omitempty"`
}

type AssetCheck struct {
	Type             string         `json:"type"`
	URL              string         `json:"url"`
	FinalURL         string         `json:"final_url,omitempty"`
	StatusCode       int            `json:"status_code"`
	ContentType      string         `json:"content_type,omitempty"`
	CacheControl     string         `json:"cache_control,omitempty"`
	EdgeSource       string         `json:"x_supercdn_edge_source,omitempty"`
	EdgeManifest     string         `json:"x_supercdn_edge_manifest,omitempty"`
	EdgeAction       string         `json:"x_supercdn_edge_action,omitempty"`
	SignatureSuspect bool           `json:"signature_suspect,omitempty"`
	Redirected       bool           `json:"redirected"`
	CORS             string         `json:"cors"`
	LatencyMS        int64          `json:"latency_ms"`
	OK               bool           `json:"ok"`
	Chain            []ResponseStep `json:"chain,omitempty"`
	Warnings         []string       `json:"warnings,omitempty"`
	Errors           []string       `json:"errors,omitempty"`
}

type ResponseStep struct {
	URL                      string `json:"url"`
	StatusCode               int    `json:"status_code"`
	Location                 string `json:"location,omitempty"`
	ContentType              string `json:"content_type,omitempty"`
	CacheControl             string `json:"cache_control,omitempty"`
	SuperCDNRedirect         string `json:"x_supercdn_redirect,omitempty"`
	SuperCDNEdgeSource       string `json:"x_supercdn_edge_source,omitempty"`
	SuperCDNEdgeManifest     string `json:"x_supercdn_edge_manifest,omitempty"`
	SuperCDNEdgeAction       string `json:"x_supercdn_edge_action,omitempty"`
	AccessControlAllowOrigin string `json:"access_control_allow_origin,omitempty"`
	LatencyMS                int64  `json:"latency_ms"`
}

type assetRef struct {
	typ string
	raw string
}

type fetchResult struct {
	finalURL string
	body     []byte
	steps    []ResponseStep
	err      error
}

var (
	scriptSrcRE = regexp.MustCompile(`(?is)<script\b[^>]*\bsrc\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)[^>]*>`)
	linkTagRE   = regexp.MustCompile(`(?is)<link\b[^>]*>`)
	hrefRE      = regexp.MustCompile(`(?is)\bhref\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)`)
	relRE       = regexp.MustCompile(`(?is)\brel\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)`)
	asRE        = regexp.MustCompile(`(?is)\bas\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)`)
)

func Run(ctx context.Context, opts Options) (Report, error) {
	if strings.TrimSpace(opts.URL) == "" {
		return Report{}, fmt.Errorf("url is required")
	}
	start := time.Now()
	maxAssets := opts.MaxAssets
	if maxAssets <= 0 {
		maxAssets = defaultMaxAssets
	}
	client := opts.Client
	if client == nil {
		client = http.DefaultClient
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	baseURL, err := url.Parse(opts.URL)
	if err != nil {
		return Report{}, err
	}
	if baseURL.Scheme == "" || baseURL.Host == "" {
		return Report{}, fmt.Errorf("url must be absolute")
	}
	origin := strings.TrimSpace(opts.Origin)
	if origin == "" {
		origin = baseURL.Scheme + "://" + baseURL.Host
	}

	report := Report{
		URL:     baseURL.String(),
		Origin:  origin,
		Summary: map[string]int{},
	}
	htmlFetch := fetch(ctx, client, baseURL.String(), "", maxHTMLBytes)
	report.HTML = checkFromFetch(baseURL.String(), htmlFetch)
	report.FinalURL = htmlFetch.finalURL
	if htmlFetch.err != nil {
		report.Errors = append(report.Errors, "html fetch failed: "+htmlFetch.err.Error())
	} else {
		validateHTML(&report, opts)
	}

	if len(htmlFetch.body) > 0 {
		assets := internalAssetRefs(baseURL, htmlFetch.body, maxAssets)
		report.Summary["assets_found"] = len(assets)
		for _, asset := range assets {
			report.Assets = append(report.Assets, probeAsset(ctx, client, asset, origin, opts))
		}
		if len(assets) == maxAssets {
			report.Warnings = append(report.Warnings, fmt.Sprintf("asset probe capped at %d resources", maxAssets))
		}
	}

	if strings.TrimSpace(opts.SPAPath) != "" {
		spaURL := resolvePath(baseURL, opts.SPAPath)
		spaFetch := fetch(ctx, client, spaURL, "", maxHTMLBytes)
		check := checkFromFetch(spaURL, spaFetch)
		if check.OK && !isHTMLContentType(check.ContentType) {
			check.OK = false
			check.Error = "SPA fallback did not return HTML"
		}
		if check.OK && opts.RequireEdgeStaticHTML && !strings.EqualFold(check.EdgeSource, "cloudflare_static") {
			check.OK = false
			check.Error = "SPA fallback was not served by Cloudflare Static Assets"
		}
		report.SPA = &check
		if !check.OK {
			report.Errors = append(report.Errors, "spa fallback failed: "+firstNonEmpty(check.Error, fmt.Sprintf("status %d", check.StatusCode)))
		}
	}

	failures := len(report.Errors)
	for _, asset := range report.Assets {
		if asset.OK {
			report.Summary["assets_ok"]++
		} else {
			failures++
			report.Summary["assets_failed"]++
		}
		if asset.Redirected {
			report.Summary["assets_redirected"]++
		}
		if asset.SignatureSuspect {
			report.Summary["signature_suspect"]++
		}
	}
	if report.Summary["signature_suspect"] > 0 {
		report.Warnings = append(report.Warnings, "one or more assets look like expired storage signatures; refresh the edge manifest and retry")
	}
	if report.HTML.OK {
		report.Summary["html_ok"] = 1
	}
	if report.SPA != nil && report.SPA.OK {
		report.Summary["spa_ok"] = 1
	}
	report.OK = failures == 0
	if report.OK {
		report.Status = "ok"
	} else {
		report.Status = "failed"
	}
	report.Duration = time.Since(start).Milliseconds()
	return report, nil
}

func checkFromFetch(requestURL string, result fetchResult) Check {
	check := Check{URL: requestURL, FinalURL: result.finalURL}
	if result.err != nil {
		check.Error = result.err.Error()
		return check
	}
	if len(result.steps) > 0 {
		last := result.steps[len(result.steps)-1]
		check.StatusCode = last.StatusCode
		check.ContentType = last.ContentType
		check.CacheControl = last.CacheControl
		check.EdgeSource = last.SuperCDNEdgeSource
		check.LatencyMS = sumLatency(result.steps)
	}
	check.OK = check.StatusCode >= 200 && check.StatusCode <= 299
	if !check.OK && check.Error == "" {
		check.Error = fmt.Sprintf("unexpected status %d", check.StatusCode)
	}
	return check
}

func validateHTML(report *Report, opts Options) {
	if !report.HTML.OK {
		report.Errors = append(report.Errors, "html fetch failed: "+firstNonEmpty(report.HTML.Error, fmt.Sprintf("status %d", report.HTML.StatusCode)))
		return
	}
	if !isHTMLContentType(report.HTML.ContentType) {
		report.Errors = append(report.Errors, "root URL did not return HTML")
		report.HTML.OK = false
		report.HTML.Error = "root URL did not return HTML"
	}
	if opts.RequireHTMLRevalidate && !htmlCacheLooksRevalidating(report.HTML.CacheControl) {
		report.Errors = append(report.Errors, "root HTML cache policy is not revalidating")
		report.HTML.OK = false
		report.HTML.Error = "root HTML cache policy is not revalidating"
	}
	if opts.RequireEdgeStaticHTML && !strings.EqualFold(report.HTML.EdgeSource, "cloudflare_static") {
		report.Errors = append(report.Errors, "root HTML was not served by Cloudflare Static Assets")
		report.HTML.OK = false
		report.HTML.Error = "root HTML was not served by Cloudflare Static Assets"
	}
}

func probeAsset(ctx context.Context, client *http.Client, ref assetRef, origin string, opts Options) AssetCheck {
	result := fetch(ctx, client, ref.raw, origin, maxAssetBytes)
	check := AssetCheck{
		Type:     ref.typ,
		URL:      ref.raw,
		FinalURL: result.finalURL,
		Chain:    result.steps,
	}
	if result.err != nil {
		check.Errors = append(check.Errors, result.err.Error())
		return check
	}
	if len(result.steps) == 0 {
		check.Errors = append(check.Errors, "empty response")
		return check
	}
	last := result.steps[len(result.steps)-1]
	check.StatusCode = last.StatusCode
	check.ContentType = last.ContentType
	check.CacheControl = last.CacheControl
	check.EdgeSource = result.steps[0].SuperCDNEdgeSource
	check.EdgeManifest = result.steps[0].SuperCDNEdgeManifest
	check.EdgeAction = result.steps[0].SuperCDNEdgeAction
	check.Redirected = len(result.steps) > 1
	check.LatencyMS = sumLatency(result.steps)
	if last.StatusCode < 200 || last.StatusCode > 299 {
		check.Errors = append(check.Errors, fmt.Sprintf("unexpected status %d", last.StatusCode))
	}
	if storageSignatureFailureLikely(result.steps) {
		check.SignatureSuspect = true
		check.Warnings = append(check.Warnings, "storage signature may be expired; refresh the edge manifest and retry")
	}
	if !assetMIMEOK(ref.typ, last.ContentType) {
		check.Errors = append(check.Errors, fmt.Sprintf("unexpected %s content type %q", ref.typ, last.ContentType))
	}
	if opts.RequireDirectAssets && check.Redirected {
		check.Errors = append(check.Errors, "asset was redirected instead of being served directly")
	}
	if opts.RequireEdgeManifestAssets && !firstStepLooksEdgeManifestRoute(result.steps) {
		check.Errors = append(check.Errors, "asset first hop was not routed by the edge manifest")
	}
	if opts.RequireImmutableAssetCache && !cacheLooksImmutable(last.CacheControl) {
		check.Errors = append(check.Errors, "asset cache policy is not immutable")
	}
	check.CORS = corsStatus(origin, result.steps)
	if check.CORS == "missing" {
		check.Errors = append(check.Errors, "cross-origin final response is missing Access-Control-Allow-Origin")
	} else if check.CORS == "mismatch" {
		check.Errors = append(check.Errors, "cross-origin final response has a mismatched Access-Control-Allow-Origin")
	}
	check.OK = len(check.Errors) == 0
	return check
}

func fetch(ctx context.Context, client *http.Client, rawURL, origin string, maxBody int64) fetchResult {
	current := rawURL
	var steps []ResponseStep
	for i := 0; i <= maxRedirects; i++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, current, nil)
		if err != nil {
			return fetchResult{finalURL: current, steps: steps, err: err}
		}
		req.Header.Set("User-Agent", "supercdnctl-site-probe/1")
		req.Header.Set("Accept", "*/*")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		noRedirectClient := *client
		noRedirectClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
		start := time.Now()
		resp, err := noRedirectClient.Do(req)
		latency := time.Since(start).Milliseconds()
		if err != nil {
			return fetchResult{finalURL: current, steps: steps, err: err}
		}
		step := ResponseStep{
			URL:                      current,
			StatusCode:               resp.StatusCode,
			Location:                 resp.Header.Get("Location"),
			ContentType:              resp.Header.Get("Content-Type"),
			CacheControl:             resp.Header.Get("Cache-Control"),
			SuperCDNRedirect:         resp.Header.Get("X-Supercdn-Redirect"),
			SuperCDNEdgeSource:       resp.Header.Get("X-SuperCDN-Edge-Source"),
			SuperCDNEdgeManifest:     resp.Header.Get("X-SuperCDN-Edge-Manifest"),
			SuperCDNEdgeAction:       resp.Header.Get("X-SuperCDN-Edge-Action"),
			AccessControlAllowOrigin: resp.Header.Get("Access-Control-Allow-Origin"),
			LatencyMS:                latency,
		}
		steps = append(steps, step)
		if isRedirect(resp.StatusCode) && step.Location != "" {
			_ = resp.Body.Close()
			next, err := resolveURL(current, step.Location)
			if err != nil {
				return fetchResult{finalURL: current, steps: steps, err: err}
			}
			current = next
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBody))
		closeErr := resp.Body.Close()
		if readErr != nil {
			return fetchResult{finalURL: current, body: body, steps: steps, err: readErr}
		}
		if closeErr != nil {
			return fetchResult{finalURL: current, body: body, steps: steps, err: closeErr}
		}
		return fetchResult{finalURL: current, body: body, steps: steps}
	}
	return fetchResult{finalURL: current, steps: steps, err: fmt.Errorf("too many redirects")}
}

func internalAssetRefs(base *url.URL, html []byte, limit int) []assetRef {
	seen := map[string]bool{}
	var refs []assetRef
	add := func(typ, raw string) {
		if len(refs) >= limit {
			return
		}
		resolved, ok := resolveInternalAsset(base, raw, typ)
		if !ok || seen[resolved] {
			return
		}
		seen[resolved] = true
		refs = append(refs, assetRef{typ: typ, raw: resolved})
	}
	for _, match := range scriptSrcRE.FindAllSubmatch(html, -1) {
		add("script", attrValue(match[1]))
	}
	for _, match := range linkTagRE.FindAll(html, -1) {
		tag := bytes.ToLower(match)
		hrefMatch := hrefRE.FindSubmatch(match)
		if len(hrefMatch) < 2 {
			continue
		}
		href := attrValue(hrefMatch[1])
		rel := attrFrom(tag, relRE)
		asValue := attrFrom(tag, asRE)
		ext := strings.ToLower(path.Ext(stripQueryPath(href)))
		switch {
		case strings.Contains(rel, "stylesheet") || ext == ".css" || asValue == "style":
			add("style", href)
		case strings.Contains(rel, "modulepreload") || asValue == "script" || ext == ".js" || ext == ".mjs":
			add("script", href)
		}
	}
	sort.SliceStable(refs, func(i, j int) bool {
		if refs[i].typ == refs[j].typ {
			return refs[i].raw < refs[j].raw
		}
		return refs[i].typ < refs[j].typ
	})
	return refs
}

func resolveInternalAsset(base *url.URL, raw, typ string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "#") {
		return "", false
	}
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "data:") || strings.HasPrefix(lower, "blob:") || strings.HasPrefix(lower, "javascript:") || strings.HasPrefix(lower, "mailto:") {
		return "", false
	}
	ref, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	resolved := base.ResolveReference(ref)
	if resolved.Host != base.Host || resolved.Scheme != base.Scheme {
		return "", false
	}
	ext := strings.ToLower(path.Ext(resolved.Path))
	if typ == "script" && ext != ".js" && ext != ".mjs" && ext != "" {
		return "", false
	}
	if typ == "style" && ext != ".css" && ext != "" {
		return "", false
	}
	return resolved.String(), true
}

func attrValue(raw []byte) string {
	out := strings.TrimSpace(string(raw))
	if len(out) >= 2 {
		if (out[0] == '"' && out[len(out)-1] == '"') || (out[0] == '\'' && out[len(out)-1] == '\'') {
			return out[1 : len(out)-1]
		}
	}
	return out
}

func attrFrom(tag []byte, re *regexp.Regexp) string {
	match := re.FindSubmatch(tag)
	if len(match) < 2 {
		return ""
	}
	return strings.ToLower(attrValue(match[1]))
}

func resolvePath(base *url.URL, p string) string {
	ref, err := url.Parse(p)
	if err != nil {
		return base.String()
	}
	return base.ResolveReference(ref).String()
}

func resolveURL(current, location string) (string, error) {
	base, err := url.Parse(current)
	if err != nil {
		return "", err
	}
	ref, err := url.Parse(location)
	if err != nil {
		return "", err
	}
	return base.ResolveReference(ref).String(), nil
}

func corsStatus(origin string, steps []ResponseStep) string {
	if len(steps) == 0 {
		return "unknown"
	}
	firstURL, errFirst := url.Parse(steps[0].URL)
	lastURL, errLast := url.Parse(steps[len(steps)-1].URL)
	if errFirst != nil || errLast != nil {
		return "unknown"
	}
	if firstURL.Scheme == lastURL.Scheme && firstURL.Host == lastURL.Host {
		return "not_required"
	}
	allowed := strings.TrimSpace(steps[len(steps)-1].AccessControlAllowOrigin)
	if allowed == "*" || allowed == origin {
		return "ok"
	}
	if allowed != "" {
		return "mismatch"
	}
	return "missing"
}

func assetMIMEOK(typ, contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	}
	switch typ {
	case "script":
		switch strings.ToLower(mediaType) {
		case "text/javascript", "application/javascript", "application/ecmascript", "text/ecmascript", "application/x-javascript":
			return true
		default:
			return false
		}
	case "style":
		return strings.EqualFold(mediaType, "text/css")
	default:
		return contentType != ""
	}
}

func isHTMLContentType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	}
	return strings.EqualFold(mediaType, "text/html")
}

func htmlCacheLooksRevalidating(value string) bool {
	value = strings.ToLower(value)
	return strings.Contains(value, "no-cache") ||
		strings.Contains(value, "no-store") ||
		(strings.Contains(value, "max-age=0") && strings.Contains(value, "must-revalidate"))
}

func cacheLooksImmutable(value string) bool {
	value = strings.ToLower(value)
	return strings.Contains(value, "immutable") && strings.Contains(value, "max-age=31536000")
}

func firstStepLooksEdgeManifestRoute(steps []ResponseStep) bool {
	if len(steps) == 0 {
		return false
	}
	first := steps[0]
	return strings.EqualFold(first.SuperCDNEdgeSource, "manifest") &&
		strings.EqualFold(first.SuperCDNEdgeManifest, "route") &&
		strings.EqualFold(first.SuperCDNEdgeAction, "route")
}

func storageSignatureFailureLikely(steps []ResponseStep) bool {
	if len(steps) == 0 {
		return false
	}
	last := steps[len(steps)-1]
	switch last.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusGone:
	default:
		return false
	}
	if firstStepLooksEdgeManifestRoute(steps) {
		return true
	}
	for _, step := range steps {
		if signedURLLike(step.URL) || signedURLLike(step.Location) {
			return true
		}
	}
	return false
}

func signedURLLike(raw string) bool {
	if strings.TrimSpace(raw) == "" {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	for key := range parsed.Query() {
		if signedQueryParam(key) {
			return true
		}
	}
	return false
}

func signedQueryParam(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "sign",
		"signature",
		"expires",
		"policy",
		"key-pair-id",
		"awsaccesskeyid",
		"x-amz-algorithm",
		"x-amz-credential",
		"x-amz-date",
		"x-amz-expires",
		"x-amz-security-token",
		"x-amz-signature",
		"x-amz-signedheaders":
		return true
	default:
		return false
	}
}

func isRedirect(status int) bool {
	return status == http.StatusMovedPermanently ||
		status == http.StatusFound ||
		status == http.StatusSeeOther ||
		status == http.StatusTemporaryRedirect ||
		status == http.StatusPermanentRedirect
}

func stripQueryPath(raw string) string {
	ref, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return ref.Path
}

func sumLatency(steps []ResponseStep) int64 {
	var total int64
	for _, step := range steps {
		total += step.LatencyMS
	}
	return total
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
