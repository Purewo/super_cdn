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

func main() {
	cfg, err := loadCLIConfig()
	if err != nil {
		fatal(err)
	}
	defaultProfile := firstNonEmpty(os.Getenv("SUPERCDN_PROFILE"), cfg.CurrentProfile, "default")
	profile := flag.String("profile", defaultProfile, "local CLI profile")
	serverURL := flag.String("server", os.Getenv("SUPERCDN_URL"), "Super CDN server URL")
	token := flag.String("token", os.Getenv("SUPERCDN_TOKEN"), "admin or user API token")
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	if stored, ok := cfg.Profiles[*profile]; ok {
		if *serverURL == "" && os.Getenv("SUPERCDN_URL") == "" {
			*serverURL = stored.Server
		}
		if *token == "" && os.Getenv("SUPERCDN_TOKEN") == "" {
			*token = stored.Token
		}
	}
	*serverURL = firstNonEmpty(*serverURL, "http://127.0.0.1:8080")
	if *token == "" && commandNeedsToken(args[0]) {
		fatal(errors.New("token is required; run login, pass -token, or set SUPERCDN_TOKEN"))
	}
	c := client{baseURL: strings.TrimRight(*serverURL, "/"), token: *token, http: http.DefaultClient}
	switch args[0] {
	case "login":
		err = login(c, *profile, args[1:])
	case "logout":
		err = logout(*profile, args[1:])
	case "whoami":
		err = whoami(c, args[1:])
	case "doctor":
		err = doctor(c, args[1:])
	case "invite-user":
		err = inviteUser(c, args[1:])
	case "list-users":
		err = listUsers(c, args[1:])
	case "revoke-token":
		err = revokeToken(c, args[1:])
	case "create-project":
		err = createProject(c, args[1:])
	case "upload":
		err = uploadAsset(c, args[1:])
	case "create-site":
		err = createSite(c, args[1:])
	case "list-sites":
		err = listSites(c, args[1:])
	case "offline-site":
		err = offlineSite(c, args[1:])
	case "online-site":
		err = onlineSite(c, args[1:])
	case "delete-site":
		err = deleteSite(c, args[1:])
	case "bind-domain":
		err = bindDomain(c, args[1:])
	case "domain-status":
		err = domainStatus(c, args[1:])
	case "cloudflare-status":
		err = cloudflareStatus(c, args[1:])
	case "ipfs-status":
		err = ipfsStatus(c, args[1:])
	case "ipfs-smoke":
		err = ipfsSmoke(c, args[1:])
	case "ipfs-web-smoke":
		err = ipfsWebSmoke(c, args[1:])
	case "refresh-ipfs-pins":
		err = refreshIPFSPins(c, args[1:])
	case "sync-site-dns":
		err = syncSiteDNS(c, args[1:])
	case "sync-worker-routes":
		err = syncWorkerRoutes(c, args[1:])
	case "sync-cloudflare-r2":
		err = syncCloudflareR2(c, args[1:])
	case "provision-cloudflare-r2":
		err = provisionCloudflareR2(c, args[1:])
	case "create-r2-credentials":
		err = createR2Credentials(c, args[1:])
	case "set-r2-credentials":
		err = setR2Credentials(args[1:])
	case "deploy-site":
		err = deploySite(c, args[1:])
	case "update-site":
		err = updateSite(c, args[1:])
	case "inspect-site":
		err = inspectSite(args[1:])
	case "probe-site":
		err = probeSite(c, args[1:])
	case "list-deployments":
		err = listDeployments(c, args[1:])
	case "deployment":
		err = getDeployment(c, args[1:])
	case "export-edge-manifest":
		err = exportEdgeManifest(c, args[1:])
	case "publish-edge-manifest":
		err = publishEdgeManifest(c, args[1:])
	case "refresh-edge-manifest":
		err = refreshEdgeManifest(c, args[1:])
	case "publish-cloudflare-static":
		err = publishCloudflareStatic(args[1:])
	case "promote-deployment":
		err = promoteDeployment(c, args[1:])
	case "delete-deployment":
		err = deleteDeployment(c, args[1:])
	case "gc":
		err = gc(c, args[1:])
	case "gc-site":
		err = gcSite(c, args[1:])
	case "init-libraries":
		err = initLibraries(c, args[1:])
	case "init-job":
		err = getInitJob(c, args[1:])
	case "resource-status":
		err = resourceStatus(c, args[1:])
	case "routing-policy-status":
		err = routingPolicyStatus(c, args[1:])
	case "route-explain":
		err = routeExplain(c, args[1:])
	case "cdn-doctor":
		err = cdnDoctor(c, args[1:])
	case "site-doctor":
		err = siteDoctor(c, args[1:])
	case "health-check":
		err = healthCheck(c, args[1:])
	case "e2e-probe":
		err = e2eProbe(c, args[1:])
	case "create-bucket":
		err = createBucket(c, args[1:])
	case "create-cdn-bucket":
		err = createCDNBucket(c, args[1:])
	case "create-domestic-cdn-bucket":
		err = createDomesticCDNBucket(c, args[1:])
	case "create-mobile-cdn-bucket":
		err = createMobileCDNBucket(c, args[1:])
	case "create-ipfs-bucket":
		err = createIPFSBucket(c, args[1:])
	case "init-bucket":
		err = initBucket(c, args[1:])
	case "upload-bucket":
		err = uploadBucket(c, args[1:])
	case "upload-bucket-dir":
		err = uploadBucketDir(c, args[1:])
	case "list-bucket":
		err = listBucket(c, args[1:])
	case "purge-bucket":
		err = purgeBucket(c, args[1:])
	case "warmup-bucket":
		err = warmupBucket(c, args[1:])
	case "delete-bucket-object":
		err = deleteBucketObject(c, args[1:])
	case "delete-bucket":
		err = deleteBucket(c, args[1:])
	case "job":
		err = getJob(c, args[1:])
	case "replicas":
		err = replicas(c, args[1:])
	case "refresh-replicas":
		err = refreshReplicas(c, args[1:])
	case "repair-replicas":
		err = repairReplicas(c, args[1:])
	case "purge":
		err = purge(c, args[1:])
	case "purge-site":
		err = purgeSite(c, args[1:])
	default:
		err = fmt.Errorf("unknown command %q", args[0])
	}
	if err != nil {
		fatal(err)
	}
}

func commandNeedsToken(command string) bool {
	switch command {
	case "inspect-site", "probe-site", "set-r2-credentials", "publish-cloudflare-static", "login", "logout":
		return false
	default:
		return true
	}
}

func login(c client, profileName string, args []string) error {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	inviteToken := fs.String("invite-token", "", "invite token")
	tokenName := fs.String("token-name", "", "local token name")
	_ = fs.Parse(args)
	if *inviteToken == "" {
		return errors.New("-invite-token is required")
	}
	raw, err := c.doJSONRaw(http.MethodPost, "/api/v1/auth/accept-invite", map[string]string{
		"invite_token": *inviteToken,
		"token_name":   firstNonEmpty(*tokenName, profileName),
	})
	if err != nil {
		return err
	}
	var resp struct {
		User     any             `json:"user"`
		APIToken string          `json:"api_token"`
		Token    json.RawMessage `json:"token"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return err
	}
	if resp.APIToken == "" {
		return errors.New("login response did not include an api token")
	}
	if err := saveCLIProfile(profileName, c.baseURL, resp.APIToken); err != nil {
		return err
	}
	return printJSON(mustJSON(map[string]any{
		"status":  "ok",
		"profile": profileName,
		"server":  c.baseURL,
		"user":    resp.User,
		"token":   json.RawMessage(resp.Token),
	}))
}

func logout(profileName string, args []string) error {
	fs := flag.NewFlagSet("logout", flag.ExitOnError)
	_ = fs.Parse(args)
	cfg, err := loadCLIConfig()
	if err != nil {
		return err
	}
	delete(cfg.Profiles, profileName)
	if cfg.CurrentProfile == profileName {
		cfg.CurrentProfile = ""
	}
	if err := saveCLIConfig(cfg); err != nil {
		return err
	}
	return printJSON(mustJSON(map[string]any{"status": "ok", "profile": profileName}))
}

func whoami(c client, args []string) error {
	fs := flag.NewFlagSet("whoami", flag.ExitOnError)
	_ = fs.Parse(args)
	return c.do(http.MethodGet, "/api/v1/auth/me", nil, "")
}

func inviteUser(c client, args []string) error {
	fs := flag.NewFlagSet("invite-user", flag.ExitOnError)
	name := fs.String("name", "", "user name")
	role := fs.String("role", "maintainer", "owner, maintainer, or viewer")
	expires := fs.Duration("expires", 7*24*time.Hour, "invite expiration")
	_ = fs.Parse(args)
	if *name == "" {
		return errors.New("-name is required")
	}
	return c.doJSON(http.MethodPost, "/api/v1/auth/invites", map[string]any{
		"name":               *name,
		"role":               *role,
		"expires_in_seconds": int64(expires.Seconds()),
	})
}

func listUsers(c client, args []string) error {
	fs := flag.NewFlagSet("list-users", flag.ExitOnError)
	_ = fs.Parse(args)
	return c.do(http.MethodGet, "/api/v1/users", nil, "")
}

func revokeToken(c client, args []string) error {
	fs := flag.NewFlagSet("revoke-token", flag.ExitOnError)
	id := fs.String("id", "", "token id")
	_ = fs.Parse(args)
	if *id == "" {
		return errors.New("-id is required")
	}
	return c.do(http.MethodDelete, "/api/v1/tokens/"+url.PathEscape(*id), nil, "")
}

func createProject(c client, args []string) error {
	fs := flag.NewFlagSet("create-project", flag.ExitOnError)
	id := fs.String("id", "", "project id")
	_ = fs.Parse(args)
	if *id == "" {
		return errors.New("-id is required")
	}
	return c.doJSON(http.MethodPost, "/api/v1/projects", map[string]string{"id": *id})
}

func uploadAsset(c client, args []string) error {
	fs := flag.NewFlagSet("upload", flag.ExitOnError)
	file := fs.String("file", "", "file to upload")
	project := fs.String("project", "", "project id")
	dst := fs.String("path", "", "object path")
	profile := fs.String("profile", "overseas", "route profile")
	cacheControl := fs.String("cache-control", "", "Cache-Control value")
	_ = fs.Parse(args)
	if *file == "" || *project == "" || *dst == "" {
		return errors.New("-file, -project and -path are required")
	}
	info, err := os.Stat(*file)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory", *file)
	}
	if err := c.doJSONQuiet(http.MethodPost, "/api/v1/preflight/upload", map[string]any{
		"route_profile":     *profile,
		"total_size":        info.Size(),
		"largest_file_size": info.Size(),
		"batch_file_count":  1,
	}); err != nil {
		return fmt.Errorf("preflight failed: %w", err)
	}
	fields := map[string]string{
		"project_id":    *project,
		"path":          *dst,
		"route_profile": *profile,
		"cache_control": *cacheControl,
	}
	return c.uploadFile("/api/v1/assets", "file", *file, fields)
}

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

func usage() {
	fmt.Println(`Usage:
  supercdnctl [global flags] login -invite-token sci_xxx
  supercdnctl [global flags] whoami
  supercdnctl [global flags] doctor
  supercdnctl [global flags] invite-user -name alice -role maintainer
  supercdnctl [global flags] list-users
  supercdnctl [global flags] revoke-token -id tok_xxx
  supercdnctl [global flags] create-project -id assets
  supercdnctl [global flags] list-sites
  supercdnctl [global flags] offline-site -site blog
  supercdnctl [global flags] online-site -site blog
  supercdnctl [global flags] delete-site -site blog -force
  supercdnctl [global flags] upload -file ./logo.png -project assets -path /img/logo.png -profile overseas
  supercdnctl [global flags] create-site -site blog -name "AI学习星图" -profile china_all -domains example.com,www.example.com
  supercdnctl [global flags] bind-domain -site blog -domain-id blog
  supercdnctl [global flags] domain-status -domain blog.sites.qwk.ccwu.cc
  supercdnctl [global flags] cloudflare-status
  supercdnctl [global flags] ipfs-status
  supercdnctl [global flags] ipfs-smoke -file ./poster.jpg
  supercdnctl [global flags] ipfs-web-smoke -file ./poster.jpg
  supercdnctl [global flags] refresh-ipfs-pins -object-id 1
  supercdnctl [global flags] sync-site-dns -site blog -dry-run
  supercdnctl [global flags] sync-worker-routes -site blog -dry-run
  supercdnctl [global flags] sync-cloudflare-r2 -cloudflare-account cf_business_main -dry-run
  supercdnctl [global flags] provision-cloudflare-r2 -cloudflare-library overseas_accel -dry-run
  supercdnctl [global flags] create-r2-credentials -cloudflare-account cf_business_main -write-config .\configs\config.local.json -dry-run=false
  supercdnctl set-r2-credentials -config .\configs\config.local.json -cloudflare-account cf_business_main -access-key-id <id> -secret-access-key <secret>
  supercdnctl [global flags] deploy-site -site blog -dir ./dist -profile china_all -target hybrid_edge -domains blog.qwk.ccwu.cc -static-spa
  supercdnctl [global flags] deploy-site -site blog -dir ./dist -profile overseas -static-spa
  supercdnctl [global flags] deploy-site -site blog -bundle ./dist.zip -env preview
  supercdnctl [global flags] update-site -site blog -dir ./dist -static-spa
  supercdnctl inspect-site -dir ./dist
  supercdnctl [global flags] probe-site -site blog -spa-path /movie/123
  supercdnctl probe-site -url https://blog.example.com/ -max-assets 20 -require-direct-assets
  supercdnctl [global flags] list-deployments -site blog
  supercdnctl [global flags] deployment -site blog -deployment dpl-abc
  supercdnctl [global flags] export-edge-manifest -site blog -deployment dpl-abc -out .\edge-manifest.json
  supercdnctl [global flags] publish-edge-manifest -site blog -deployment dpl-abc -kv-namespace supercdn-edge-manifest -dry-run
  supercdnctl [global flags] refresh-edge-manifest -site blog -kv-namespace supercdn-edge-manifest -spa-path /movie/123
  supercdnctl publish-cloudflare-static -site blog -dir ./dist -domains blog-static-test.example.com -dry-run=false
  supercdnctl [global flags] promote-deployment -site blog -deployment dpl-abc
  supercdnctl [global flags] delete-deployment -site blog -deployment dpl-abc
  supercdnctl [global flags] gc -dry-run -older-than 1h
  supercdnctl [global flags] gc -dry-run=false -older-than 1h
  supercdnctl [global flags] gc-site -site blog
  supercdnctl [global flags] init-libraries -dry-run
  supercdnctl [global flags] init-job -id 1
  supercdnctl [global flags] doctor -resources=false
  supercdnctl [global flags] resource-status -library repo_china_all
  supercdnctl [global flags] routing-policy-status -policy global_smart
  supercdnctl [global flags] route-explain -site cyberstream -path /assets/app.js -country CN
  supercdnctl [global flags] cdn-doctor -bucket movie-posters -path posters/poster.jpg
  supercdnctl [global flags] site-doctor -site cyberstream -path /assets/app.js
  supercdnctl [global flags] health-check -libraries repo_china_all
  supercdnctl [global flags] e2e-probe -profile china_all
  supercdnctl [global flags] create-bucket -slug movie-posters -name 影视海报�?-profile china_all -types image
  supercdnctl [global flags] create-cdn-bucket -slug movie-posters -name movie-posters -types image
  supercdnctl [global flags] create-domestic-cdn-bucket -slug mobile-posters -line mobile -types image
  supercdnctl [global flags] create-ipfs-bucket -slug durable-assets -types image,archive
  supercdnctl [global flags] init-bucket -bucket movie-posters
  supercdnctl [global flags] upload-bucket -bucket movie-posters -file poster.jpg -path posters/poster.jpg -warmup
  supercdnctl [global flags] upload-bucket-dir -bucket movie-posters -dir ./posters -prefix posters -concurrency 10
  supercdnctl [global flags] upload-bucket-dir -bucket movie-posters -dir ./posters -prefix posters -skip-existing -retry 2 -report-file ./upload-report.json
  supercdnctl [global flags] list-bucket -bucket movie-posters
  supercdnctl [global flags] purge-bucket -bucket movie-posters -prefix posters/ -dry-run
  supercdnctl [global flags] warmup-bucket -bucket movie-posters -path posters/poster.jpg -dry-run
  supercdnctl [global flags] delete-bucket-object -bucket movie-posters -path posters/poster.jpg
  supercdnctl [global flags] delete-bucket-object -bucket movie-posters -paths posters/a.jpg,posters/b.jpg
  supercdnctl [global flags] delete-bucket-object -bucket movie-posters -prefix posters/tmp/ -force
  supercdnctl [global flags] delete-bucket-object -bucket movie-posters -all -force
  supercdnctl [global flags] delete-bucket -bucket movie-posters -force
  supercdnctl [global flags] job -id 1
  supercdnctl [global flags] replicas -object-id 1
  supercdnctl [global flags] refresh-replicas -object-id 1 -target repo_backup
  supercdnctl [global flags] refresh-replicas -bucket movie-posters -prefix posters/
  supercdnctl [global flags] repair-replicas -object-id 1 -target repo_backup
  supercdnctl [global flags] purge -urls https://example.com/a.css
  supercdnctl [global flags] purge-site -site blog -dry-run

Global flags:
  -server   Super CDN server URL; saved by login when omitted later
  -token    Admin or user API token; overrides saved profile
  -profile  Local profile name; defaults to SUPERCDN_PROFILE or current saved profile`)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
