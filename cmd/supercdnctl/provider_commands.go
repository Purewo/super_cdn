package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func cloudflareStatus(c client, args []string) error {
	fs := flag.NewFlagSet("cloudflare-status", flag.ExitOnError)
	account := fs.String("account", "", "Cloudflare account name")
	all := fs.Bool("all", false, "show all configured Cloudflare accounts")
	_ = fs.Parse(args)
	q := url.Values{}
	if *account != "" {
		q.Set("account", *account)
	}
	if *all {
		q.Set("all", "true")
	}
	pathValue := "/api/v1/cloudflare/status"
	if len(q) > 0 {
		pathValue += "?" + q.Encode()
	}
	return c.do(http.MethodGet, pathValue, nil, "")
}

func ipfsStatus(c client, args []string) error {
	fs := flag.NewFlagSet("ipfs-status", flag.ExitOnError)
	target := fs.String("target", "", "IPFS storage target name; empty checks all Pinata/IPFS targets")
	_ = fs.Parse(args)
	q := url.Values{}
	if strings.TrimSpace(*target) != "" {
		q.Set("target", strings.TrimSpace(*target))
	}
	pathValue := "/api/v1/ipfs/status"
	if len(q) > 0 {
		pathValue += "?" + q.Encode()
	}
	return c.do(http.MethodGet, pathValue, nil, "")
}

func refreshIPFSPins(c client, args []string) error {
	fs := flag.NewFlagSet("refresh-ipfs-pins", flag.ExitOnError)
	objectID := fs.Int64("object-id", 0, "object id whose known IPFS pins should be refreshed")
	target := fs.String("target", "", "optional IPFS storage target name")
	_ = fs.Parse(args)
	if *objectID <= 0 {
		return errors.New("-object-id is required")
	}
	return c.doJSON(http.MethodPost, "/api/v1/ipfs/pins/refresh", map[string]any{
		"object_id": *objectID,
		"target":    strings.TrimSpace(*target),
	})
}

type ipfsSmokePin struct {
	ObjectID      int64  `json:"object_id,omitempty"`
	Target        string `json:"target,omitempty"`
	Provider      string `json:"provider,omitempty"`
	CID           string `json:"cid,omitempty"`
	GatewayURL    string `json:"gateway_url,omitempty"`
	Locator       string `json:"locator,omitempty"`
	PinStatus     string `json:"pin_status,omitempty"`
	ProviderPinID string `json:"provider_pin_id,omitempty"`
}

type ipfsSmokeUploadObject struct {
	ID   int64          `json:"id"`
	IPFS []ipfsSmokePin `json:"ipfs,omitempty"`
}

type ipfsSmokeUploadResponse struct {
	Object    ipfsSmokeUploadObject `json:"object"`
	PublicURL string                `json:"public_url,omitempty"`
	CDNURL    string                `json:"cdn_url,omitempty"`
	IPFS      []ipfsSmokePin        `json:"ipfs,omitempty"`
}

type ipfsSmokeRefreshResponse struct {
	Status   string         `json:"status"`
	ObjectID int64          `json:"object_id"`
	Target   string         `json:"target,omitempty"`
	Pins     []ipfsSmokePin `json:"pins,omitempty"`
	Errors   []string       `json:"errors,omitempty"`
}

type ipfsSmokeProbeResult struct {
	Name         string `json:"name"`
	Method       string `json:"method"`
	URL          string `json:"url"`
	Range        string `json:"range,omitempty"`
	HTTPStatus   int    `json:"http_status,omitempty"`
	Location     string `json:"location,omitempty"`
	Bytes        int64  `json:"bytes,omitempty"`
	LatencyMS    int64  `json:"latency_ms,omitempty"`
	SpeedBytesPS int64  `json:"speed_bytes_per_second,omitempty"`
	CacheControl string `json:"cache_control,omitempty"`
	ContentType  string `json:"content_type,omitempty"`
	AcceptRanges string `json:"accept_ranges,omitempty"`
	Error        string `json:"error,omitempty"`
}

type ipfsSmokeResult struct {
	Status         string                    `json:"status"`
	Bucket         string                    `json:"bucket"`
	CreatedBucket  bool                      `json:"created_bucket,omitempty"`
	Cleanup        bool                      `json:"cleanup"`
	DeletedBucket  json.RawMessage           `json:"deleted_bucket,omitempty"`
	File           string                    `json:"file"`
	SizeBytes      int64                     `json:"size_bytes"`
	LogicalPath    string                    `json:"logical_path"`
	RouteProfile   string                    `json:"route_profile"`
	Target         string                    `json:"target,omitempty"`
	PublicURL      string                    `json:"public_url,omitempty"`
	GatewayURL     string                    `json:"gateway_url,omitempty"`
	CID            string                    `json:"cid,omitempty"`
	Provider       string                    `json:"provider,omitempty"`
	ObjectID       int64                     `json:"object_id,omitempty"`
	Upload         ipfsSmokeUploadResponse   `json:"upload"`
	ProviderStatus json.RawMessage           `json:"provider_status,omitempty"`
	Refresh        *ipfsSmokeRefreshResponse `json:"refresh,omitempty"`
	Probes         []ipfsSmokeProbeResult    `json:"probes,omitempty"`
	Warnings       []string                  `json:"warnings,omitempty"`
}

type ipfsWebSmokeRoute struct {
	Type             string         `json:"type"`
	Location         string         `json:"location,omitempty"`
	IPFS             []ipfsSmokePin `json:"ipfs,omitempty"`
	GatewayFallbacks []string       `json:"gateway_fallbacks,omitempty"`
}

type ipfsWebSmokeManifest struct {
	SiteID       string                       `json:"site_id"`
	DeploymentID string                       `json:"deployment_id"`
	RouteProfile string                       `json:"route_profile"`
	Routes       map[string]ipfsWebSmokeRoute `json:"routes"`
	Warnings     []string                     `json:"warnings,omitempty"`
}

type ipfsWebSmokeResult struct {
	Status         string                 `json:"status"`
	Site           string                 `json:"site"`
	DeploymentID   string                 `json:"deployment_id,omitempty"`
	Cleanup        bool                   `json:"cleanup"`
	Deleted        json.RawMessage        `json:"deleted_deployment,omitempty"`
	File           string                 `json:"file,omitempty"`
	SizeBytes      int64                  `json:"size_bytes"`
	AssetPath      string                 `json:"asset_path"`
	RouteProfile   string                 `json:"route_profile"`
	Target         string                 `json:"target,omitempty"`
	PreviewURL     string                 `json:"preview_url,omitempty"`
	AssetURL       string                 `json:"asset_url,omitempty"`
	GatewayURL     string                 `json:"gateway_url,omitempty"`
	CID            string                 `json:"cid,omitempty"`
	Provider       string                 `json:"provider,omitempty"`
	Deployment     siteDeploymentResult   `json:"deployment"`
	ManifestRoute  ipfsWebSmokeRoute      `json:"manifest_route"`
	ProviderStatus json.RawMessage        `json:"provider_status,omitempty"`
	Probes         []ipfsSmokeProbeResult `json:"probes,omitempty"`
	Warnings       []string               `json:"warnings,omitempty"`
}

func ipfsSmoke(c client, args []string) error {
	fs := flag.NewFlagSet("ipfs-smoke", flag.ExitOnError)
	file := fs.String("file", "", "file to upload through the IPFS bucket workflow")
	bucket := fs.String("bucket", "", "bucket slug; defaults to ipfs-smoke-<timestamp>")
	dst := fs.String("path", "", "logical path inside the bucket; defaults to smoke/<timestamp>-<file>")
	profile := fs.String("profile", "ipfs_archive", "IPFS route profile")
	target := fs.String("target", "ipfs_pinata", "IPFS storage target for status and pin refresh")
	assetType := fs.String("asset-type", "", "optional asset type override")
	types := fs.String("types", "image,video,document,archive,other", "bucket allowed asset types")
	cacheControl := fs.String("cache-control", "public, max-age=31536000, immutable", "bucket default Cache-Control")
	createBucket := fs.Bool("create-bucket", true, "create the bucket before uploading")
	cleanup := fs.Bool("cleanup", false, "delete the smoke bucket after probes")
	skipStatus := fs.Bool("skip-status", false, "skip IPFS provider status check")
	skipRefresh := fs.Bool("skip-refresh", false, "skip pin status refresh after upload")
	skipRange := fs.Bool("skip-range", false, "skip Range GET probe")
	downloadRuns := fs.Int("download-runs", 1, "number of full GET probes against the gateway URL")
	proxyURL := fs.String("proxy-url", "", "optional HTTP proxy URL for gateway probes")
	timeout := fs.Duration("timeout", 120*time.Second, "gateway probe timeout")
	_ = fs.Parse(args)
	if *file == "" {
		return errors.New("-file is required")
	}
	info, err := os.Stat(*file)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory", *file)
	}
	now := time.Now().UTC()
	if strings.TrimSpace(*bucket) == "" {
		*bucket = "ipfs-smoke-" + now.Format("20060102150405")
	}
	if strings.TrimSpace(*dst) == "" {
		*dst = defaultIPFSSmokePath(*file, now)
	}
	result := ipfsSmokeResult{
		Status:       "running",
		Bucket:       strings.TrimSpace(*bucket),
		Cleanup:      *cleanup,
		File:         *file,
		SizeBytes:    info.Size(),
		LogicalPath:  strings.TrimSpace(*dst),
		RouteProfile: strings.TrimSpace(*profile),
		Target:       strings.TrimSpace(*target),
	}
	if !*skipStatus {
		statusPath := "/api/v1/ipfs/status"
		if result.Target != "" {
			statusPath += "?target=" + url.QueryEscape(result.Target)
		}
		raw, err := c.doRaw(http.MethodGet, statusPath, nil, "")
		if err != nil {
			return err
		}
		result.ProviderStatus = json.RawMessage(raw)
	}
	if *createBucket {
		req := map[string]any{
			"slug":                  result.Bucket,
			"name":                  result.Bucket,
			"route_profile":         result.RouteProfile,
			"allowed_types":         splitCSV(*types),
			"default_cache_control": strings.TrimSpace(*cacheControl),
		}
		if _, err := c.doJSONRaw(http.MethodPost, "/api/v1/asset-buckets", req); err != nil {
			return err
		}
		result.CreatedBucket = true
	}
	fields := map[string]string{
		"path":          result.LogicalPath,
		"asset_type":    strings.TrimSpace(*assetType),
		"cache_control": strings.TrimSpace(*cacheControl),
	}
	uploadRaw, err := c.uploadFileRaw("/api/v1/asset-buckets/"+url.PathEscape(result.Bucket)+"/objects", "file", *file, fields)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(uploadRaw, &result.Upload); err != nil {
		return err
	}
	result.PublicURL = result.Upload.PublicURL
	result.GatewayURL = result.Upload.CDNURL
	result.ObjectID = result.Upload.Object.ID
	pin := firstIPFSSmokePin(result.Upload.IPFS, result.Upload.Object.IPFS)
	result.CID = pin.CID
	result.Provider = pin.Provider
	if pin.Target != "" {
		result.Target = pin.Target
	}
	if pin.GatewayURL != "" {
		result.GatewayURL = pin.GatewayURL
	}
	if result.CID == "" {
		result.Warnings = append(result.Warnings, "upload response did not include IPFS CID metadata")
	}
	if result.GatewayURL == "" {
		result.Warnings = append(result.Warnings, "upload response did not include a gateway URL")
	}
	if !*skipRefresh && result.ObjectID > 0 {
		refreshReq := map[string]any{"object_id": result.ObjectID, "target": result.Target}
		refreshRaw, err := c.doJSONRaw(http.MethodPost, "/api/v1/ipfs/pins/refresh", refreshReq)
		if err != nil {
			result.Warnings = append(result.Warnings, "pin refresh failed: "+err.Error())
		} else {
			var refresh ipfsSmokeRefreshResponse
			if err := json.Unmarshal(refreshRaw, &refresh); err != nil {
				return err
			}
			result.Refresh = &refresh
		}
	}
	if result.GatewayURL != "" {
		probeClient, err := gatewayProbeClient(*proxyURL)
		if err != nil {
			return err
		}
		result.Probes = append(result.Probes, probeURL(probeClient, "head", http.MethodHead, result.GatewayURL, "", *timeout))
		if !*skipRange {
			result.Probes = append(result.Probes, probeURL(probeClient, "range", http.MethodGet, result.GatewayURL, "bytes=0-1048575", *timeout))
		}
		for i := 1; i <= *downloadRuns; i++ {
			name := "get"
			if *downloadRuns > 1 {
				name = fmt.Sprintf("get_%d", i)
			}
			result.Probes = append(result.Probes, probeURL(probeClient, name, http.MethodGet, result.GatewayURL, "", *timeout))
		}
	}
	result.Status = "ok"
	for _, probe := range result.Probes {
		if probe.Error != "" || probe.HTTPStatus >= 400 {
			result.Status = "partial"
			break
		}
	}
	if *cleanup {
		deletePath := "/api/v1/asset-buckets/" + url.PathEscape(result.Bucket) + "?force=true&delete_objects=true&delete_remote=true"
		deleteRaw, err := c.doRaw(http.MethodDelete, deletePath, nil, "")
		if err != nil {
			result.Status = "partial"
			result.Warnings = append(result.Warnings, "cleanup failed: "+err.Error())
		} else {
			result.DeletedBucket = json.RawMessage(deleteRaw)
		}
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return printJSON(raw)
}

func ipfsWebSmoke(c client, args []string) error {
	fs := flag.NewFlagSet("ipfs-web-smoke", flag.ExitOnError)
	file := fs.String("file", "", "optional asset file to include in the test site")
	site := fs.String("site", "", "site id; defaults to ipfs-web-smoke-<timestamp>")
	assetPath := fs.String("asset-path", "", "asset path inside the site; defaults to assets/ipfs-smoke-<timestamp>.<ext>")
	profile := fs.String("profile", "ipfs_archive", "IPFS route profile")
	target := fs.String("target", "ipfs_pinata", "IPFS storage target for status display")
	cleanup := fs.Bool("cleanup", false, "delete the preview deployment after probes")
	skipStatus := fs.Bool("skip-status", false, "skip IPFS provider status check")
	skipRange := fs.Bool("skip-range", false, "skip Range GET probe")
	downloadRuns := fs.Int("download-runs", 1, "number of full GET probes against the gateway URL")
	proxyURL := fs.String("proxy-url", "", "optional HTTP proxy URL for gateway probes")
	timeout := fs.Duration("timeout", 30*time.Minute, "maximum time to wait for deployment and gateway probes")
	_ = fs.Parse(args)

	now := time.Now().UTC()
	if strings.TrimSpace(*site) == "" {
		*site = "ipfs-web-smoke-" + now.Format("20060102150405")
	}
	dir, cleanupDir, resolvedAssetPath, size, err := prepareIPFSWebSmokeDir(*file, *assetPath, now)
	if err != nil {
		return err
	}
	defer cleanupDir()

	result := ipfsWebSmokeResult{
		Status:       "running",
		Site:         cleanWorkerName(*site),
		Cleanup:      *cleanup,
		File:         strings.TrimSpace(*file),
		SizeBytes:    size,
		AssetPath:    resolvedAssetPath,
		RouteProfile: strings.TrimSpace(*profile),
		Target:       strings.TrimSpace(*target),
	}
	if result.Site == "" {
		return errors.New("-site must contain at least one alphanumeric character")
	}
	if !*skipStatus {
		statusPath := "/api/v1/ipfs/status"
		if result.Target != "" {
			statusPath += "?target=" + url.QueryEscape(result.Target)
		}
		raw, err := c.doRaw(http.MethodGet, statusPath, nil, "")
		if err != nil {
			return err
		}
		result.ProviderStatus = json.RawMessage(raw)
	}
	if _, err := c.doJSONRaw(http.MethodPost, "/api/v1/sites", map[string]any{
		"id":                  result.Site,
		"route_profile":       result.RouteProfile,
		"deployment_target":   "origin_assisted",
		"mode":                "standard",
		"skip_default_domain": true,
	}); err != nil {
		return err
	}
	dep, err := createAndWaitSiteDeployment(c, result.Site, siteDeploymentUploadOptions{
		Dir:              dir,
		Environment:      "preview",
		RouteProfile:     result.RouteProfile,
		DeploymentTarget: "origin_assisted",
		Promote:          false,
		Pinned:           !*cleanup,
		Timeout:          *timeout,
	})
	if err != nil {
		return err
	}
	result.Deployment = dep
	result.DeploymentID = dep.ID
	result.PreviewURL = absoluteCLIURL(c.baseURL, dep.PreviewURL)
	result.AssetURL = absoluteCLIURL(c.baseURL, "/p/"+result.Site+"/"+dep.ID+"/"+result.AssetPath)

	manifestRaw, err := c.doRaw(http.MethodGet, "/api/v1/sites/"+url.PathEscape(result.Site)+"/deployments/"+url.PathEscape(dep.ID)+"/edge-manifest", nil, "")
	if err != nil {
		return err
	}
	var manifest ipfsWebSmokeManifest
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		return err
	}
	route := manifest.Routes["/"+result.AssetPath]
	result.ManifestRoute = route
	if route.Type != "ipfs" {
		result.Warnings = append(result.Warnings, fmt.Sprintf("edge manifest asset route type is %q, expected ipfs", route.Type))
	}
	pin := firstIPFSSmokePin(route.IPFS)
	result.CID = pin.CID
	result.Provider = pin.Provider
	if pin.Target != "" {
		result.Target = pin.Target
	}
	result.GatewayURL = firstNonEmpty(route.Location, firstString(route.GatewayFallbacks), pin.GatewayURL)
	if result.CID == "" {
		result.Warnings = append(result.Warnings, "edge manifest route did not include IPFS CID metadata")
	}
	if result.GatewayURL == "" {
		result.Warnings = append(result.Warnings, "edge manifest route did not include a gateway URL")
	}

	probeClient, err := gatewayProbeClient(*proxyURL)
	if err != nil {
		return err
	}
	noRedirect := noRedirectClient(probeClient)
	if result.PreviewURL != "" {
		result.Probes = append(result.Probes, probeURL(noRedirect, "site_root", http.MethodGet, result.PreviewURL, "", *timeout))
	}
	if result.AssetURL != "" {
		result.Probes = append(result.Probes, probeURL(noRedirect, "site_asset_first_hop", http.MethodHead, result.AssetURL, "", *timeout))
	}
	if result.GatewayURL != "" {
		result.Probes = append(result.Probes, probeURL(probeClient, "gateway_head", http.MethodHead, result.GatewayURL, "", *timeout))
		if !*skipRange {
			result.Probes = append(result.Probes, probeURL(probeClient, "gateway_range", http.MethodGet, result.GatewayURL, "bytes=0-1048575", *timeout))
		}
		for i := 1; i <= *downloadRuns; i++ {
			name := "gateway_get"
			if *downloadRuns > 1 {
				name = fmt.Sprintf("gateway_get_%d", i)
			}
			result.Probes = append(result.Probes, probeURL(probeClient, name, http.MethodGet, result.GatewayURL, "", *timeout))
		}
	}

	result.Status = ipfsSmokeStatus(result.Probes, result.Warnings)
	if *cleanup && dep.ID != "" {
		deleteRaw, err := c.doRaw(http.MethodDelete, "/api/v1/sites/"+url.PathEscape(result.Site)+"/deployments/"+url.PathEscape(dep.ID)+"?delete_objects=true&delete_remote=true", nil, "")
		if err != nil {
			result.Status = "partial"
			result.Warnings = append(result.Warnings, "cleanup failed: "+err.Error())
		} else {
			result.Deleted = json.RawMessage(deleteRaw)
		}
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return printJSON(raw)
}

func firstIPFSSmokePin(groups ...[]ipfsSmokePin) ipfsSmokePin {
	for _, pins := range groups {
		for _, pin := range pins {
			if pin.CID != "" || pin.GatewayURL != "" {
				return pin
			}
		}
	}
	return ipfsSmokePin{}
}

func defaultIPFSSmokePath(file string, now time.Time) string {
	ext := strings.ToLower(filepath.Ext(file))
	base := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
	base = strings.Trim(regexp.MustCompile(`[^A-Za-z0-9._-]+`).ReplaceAllString(base, "-"), "-._")
	if base == "" {
		base = "file"
	}
	return "smoke/" + now.Format("20060102T150405Z") + "-" + base + ext
}

func prepareIPFSWebSmokeDir(file, assetPath string, now time.Time) (string, func(), string, int64, error) {
	dir, err := os.MkdirTemp("", "supercdn-ipfs-web-smoke-*")
	if err != nil {
		return "", nil, "", 0, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	assetPath = strings.Trim(strings.ReplaceAll(strings.TrimSpace(assetPath), "\\", "/"), "/")
	if assetPath == "" {
		assetPath = defaultIPFSWebSmokeAssetPath(file, now)
	}
	if strings.Contains(assetPath, "..") {
		cleanup()
		return "", nil, "", 0, fmt.Errorf("asset path must not contain ..")
	}
	targetPath := filepath.Join(dir, filepath.FromSlash(assetPath))
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		cleanup()
		return "", nil, "", 0, err
	}
	var size int64
	if strings.TrimSpace(file) != "" {
		info, err := os.Stat(file)
		if err != nil {
			cleanup()
			return "", nil, "", 0, err
		}
		if info.IsDir() {
			cleanup()
			return "", nil, "", 0, fmt.Errorf("%s is a directory", file)
		}
		src, err := os.Open(file)
		if err != nil {
			cleanup()
			return "", nil, "", 0, err
		}
		defer src.Close()
		dst, err := os.Create(targetPath)
		if err != nil {
			cleanup()
			return "", nil, "", 0, err
		}
		n, copyErr := io.Copy(dst, src)
		closeErr := dst.Close()
		if copyErr != nil {
			cleanup()
			return "", nil, "", 0, copyErr
		}
		if closeErr != nil {
			cleanup()
			return "", nil, "", 0, closeErr
		}
		size = n
	} else {
		payload := []byte("supercdn ipfs web smoke " + now.Format(time.RFC3339Nano) + "\n")
		if err := os.WriteFile(targetPath, payload, 0o644); err != nil {
			cleanup()
			return "", nil, "", 0, err
		}
		size = int64(len(payload))
	}
	index := "<!doctype html><html><head><meta charset=\"utf-8\"><title>IPFS Web Smoke</title></head><body><a href=\"" + assetPath + "\">asset</a></body></html>"
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(index), 0o644); err != nil {
		cleanup()
		return "", nil, "", 0, err
	}
	return dir, cleanup, assetPath, size, nil
}

func defaultIPFSWebSmokeAssetPath(file string, now time.Time) string {
	ext := strings.ToLower(filepath.Ext(file))
	if ext == "" {
		ext = ".txt"
	}
	return "assets/ipfs-smoke-" + now.Format("20060102T150405Z") + ext
}

func gatewayProbeClient(proxyRaw string) (*http.Client, error) {
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment}
	proxyRaw = strings.TrimSpace(proxyRaw)
	if proxyRaw != "" {
		u, err := url.Parse(proxyRaw)
		if err != nil {
			return nil, err
		}
		transport.Proxy = http.ProxyURL(u)
	}
	return &http.Client{Transport: transport}, nil
}

func noRedirectClient(base *http.Client) *http.Client {
	if base == nil {
		base = http.DefaultClient
	}
	copyClient := *base
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &copyClient
}

func probeURL(client *http.Client, name, method, targetURL, rangeHeader string, timeout time.Duration) ipfsSmokeProbeResult {
	result := ipfsSmokeProbeResult{Name: name, Method: method, URL: targetURL, Range: rangeHeader}
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, targetURL, nil)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	start := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start)
	result.LatencyMS = latency.Milliseconds()
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer resp.Body.Close()
	result.HTTPStatus = resp.StatusCode
	result.Location = resp.Header.Get("Location")
	result.CacheControl = resp.Header.Get("Cache-Control")
	result.ContentType = resp.Header.Get("Content-Type")
	result.AcceptRanges = resp.Header.Get("Accept-Ranges")
	if method == http.MethodHead {
		result.Bytes = resp.ContentLength
		return result
	}
	n, err := io.Copy(io.Discard, resp.Body)
	if err != nil {
		result.Error = err.Error()
	}
	result.Bytes = n
	if latency > 0 {
		result.SpeedBytesPS = int64(float64(n) / latency.Seconds())
	}
	return result
}

func ipfsSmokeStatus(probes []ipfsSmokeProbeResult, warnings []string) string {
	if len(warnings) > 0 {
		return "partial"
	}
	for _, probe := range probes {
		if probe.Error != "" || probe.HTTPStatus >= 400 {
			return "partial"
		}
	}
	return "ok"
}

func absoluteCLIURL(baseURL, value string) string {
	value = strings.TrimSpace(value)
	if value == "" || isHTTPURL(value) {
		return value
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return value
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	return baseURL + value
}

func isHTTPURL(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://")
}
