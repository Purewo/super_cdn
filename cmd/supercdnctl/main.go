package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	urlpath "path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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

func syncSiteDNS(c client, args []string) error {
	fs := flag.NewFlagSet("sync-site-dns", flag.ExitOnError)
	site := fs.String("site", "", "site id")
	domains := fs.String("domains", "", "comma-separated bound domains to sync; empty means all site domains")
	cfAccount := fs.String("cloudflare-account", "", "Cloudflare account name; defaults by domain match")
	cfLibrary := fs.String("cloudflare-library", "", "Cloudflare library name")
	target := fs.String("target", "", "DNS record target; defaults to server config")
	recordType := fs.String("type", "", "DNS record type: A, AAAA or CNAME; defaults by target")
	proxied := fs.Bool("proxied", true, "create/update as proxied Cloudflare DNS record")
	ttl := fs.Int("ttl", 1, "DNS TTL; 1 means automatic")
	dryRun := fs.Bool("dry-run", false, "plan DNS changes without calling create/update")
	force := fs.Bool("force", false, "update an existing same-type DNS record with different content/proxy status")
	_ = fs.Parse(args)
	if *site == "" {
		return errors.New("-site is required")
	}
	return c.doJSON(http.MethodPost, "/api/v1/sites/"+url.PathEscape(*site)+"/dns", map[string]any{
		"domains":            splitCSV(*domains),
		"cloudflare_account": *cfAccount,
		"cloudflare_library": *cfLibrary,
		"target":             *target,
		"type":               *recordType,
		"proxied":            *proxied,
		"ttl":                *ttl,
		"dry_run":            *dryRun,
		"force":              *force,
	})
}

func syncWorkerRoutes(c client, args []string) error {
	fs := flag.NewFlagSet("sync-worker-routes", flag.ExitOnError)
	site := fs.String("site", "", "site id")
	domains := fs.String("domains", "", "comma-separated bound domains to sync; empty means all site domains")
	cfAccount := fs.String("cloudflare-account", "", "Cloudflare account name; defaults by domain match")
	cfLibrary := fs.String("cloudflare-library", "", "Cloudflare library name")
	script := fs.String("script", "", "Cloudflare Worker script name; defaults to server config")
	dryRun := fs.Bool("dry-run", false, "plan route changes without calling create/update")
	force := fs.Bool("force", false, "update an existing route that points to another worker script")
	_ = fs.Parse(args)
	if *site == "" {
		return errors.New("-site is required")
	}
	return c.doJSON(http.MethodPost, "/api/v1/sites/"+url.PathEscape(*site)+"/worker-routes", map[string]any{
		"domains":            splitCSV(*domains),
		"cloudflare_account": *cfAccount,
		"cloudflare_library": *cfLibrary,
		"script":             *script,
		"dry_run":            *dryRun,
		"force":              *force,
	})
}

func syncCloudflareR2(c client, args []string) error {
	fs := flag.NewFlagSet("sync-cloudflare-r2", flag.ExitOnError)
	cfAccount := fs.String("cloudflare-account", "", "Cloudflare account name")
	cfLibrary := fs.String("cloudflare-library", "", "Cloudflare library name")
	all := fs.Bool("all", false, "sync all Cloudflare accounts with R2 configured")
	dryRun := fs.Bool("dry-run", true, "plan R2 changes without modifying Cloudflare; pass -dry-run=false to execute")
	force := fs.Bool("force", false, "replace differing CORS policy or update inactive custom domain")
	syncCORS := fs.Bool("cors", true, "sync bucket CORS policy")
	syncDomain := fs.Bool("domain", true, "sync bucket public custom/r2.dev domain")
	corsOrigins := fs.String("cors-origins", "", "comma-separated CORS allowed origins; defaults to *")
	corsMethods := fs.String("cors-methods", "GET,HEAD", "comma-separated CORS methods")
	corsHeaders := fs.String("cors-headers", "*", "comma-separated CORS allowed headers")
	corsExpose := fs.String("cors-expose", "ETag,Content-Length,Content-Type,Cache-Control", "comma-separated CORS exposed headers")
	corsMaxAge := fs.Int("cors-max-age", 86400, "CORS max-age seconds")
	_ = fs.Parse(args)
	return c.doJSON(http.MethodPost, "/api/v1/cloudflare/r2/sync", map[string]any{
		"cloudflare_account":   *cfAccount,
		"cloudflare_library":   *cfLibrary,
		"all":                  *all,
		"dry_run":              *dryRun,
		"force":                *force,
		"sync_cors":            *syncCORS,
		"sync_domain":          *syncDomain,
		"cors_origins":         splitCSV(*corsOrigins),
		"cors_methods":         splitCSV(*corsMethods),
		"cors_headers":         splitCSV(*corsHeaders),
		"cors_expose_headers":  splitCSV(*corsExpose),
		"cors_max_age_seconds": *corsMaxAge,
	})
}

func provisionCloudflareR2(c client, args []string) error {
	fs := flag.NewFlagSet("provision-cloudflare-r2", flag.ExitOnError)
	cfAccount := fs.String("cloudflare-account", "", "Cloudflare account name")
	cfLibrary := fs.String("cloudflare-library", "", "Cloudflare library name")
	all := fs.Bool("all", false, "provision all Cloudflare accounts")
	bucket := fs.String("bucket", "", "R2 bucket name; defaults to supercdn-{library}")
	publicBaseURL := fs.String("public-base-url", "", "R2 public base URL; defaults to https://{library}.r2.{root_domain}")
	locationHint := fs.String("location-hint", "", "R2 location hint")
	jurisdiction := fs.String("jurisdiction", "", "R2 jurisdiction header value")
	storageClass := fs.String("storage-class", "", "R2 storage class")
	dryRun := fs.Bool("dry-run", true, "plan R2 provisioning without modifying Cloudflare; pass -dry-run=false to execute")
	force := fs.Bool("force", false, "replace differing CORS policy or update inactive custom domain")
	syncCORS := fs.Bool("cors", true, "sync bucket CORS policy after create/existence check")
	syncDomain := fs.Bool("domain", true, "sync bucket public custom/r2.dev domain after create/existence check")
	corsOrigins := fs.String("cors-origins", "", "comma-separated CORS allowed origins; defaults to *")
	corsMethods := fs.String("cors-methods", "GET,HEAD", "comma-separated CORS methods")
	corsHeaders := fs.String("cors-headers", "*", "comma-separated CORS allowed headers")
	corsExpose := fs.String("cors-expose", "ETag,Content-Length,Content-Type,Cache-Control", "comma-separated CORS exposed headers")
	corsMaxAge := fs.Int("cors-max-age", 86400, "CORS max-age seconds")
	_ = fs.Parse(args)
	return c.doJSON(http.MethodPost, "/api/v1/cloudflare/r2/provision", map[string]any{
		"cloudflare_account":   *cfAccount,
		"cloudflare_library":   *cfLibrary,
		"all":                  *all,
		"bucket":               *bucket,
		"public_base_url":      *publicBaseURL,
		"location_hint":        *locationHint,
		"jurisdiction":         *jurisdiction,
		"storage_class":        *storageClass,
		"dry_run":              *dryRun,
		"force":                *force,
		"sync_cors":            *syncCORS,
		"sync_domain":          *syncDomain,
		"cors_origins":         splitCSV(*corsOrigins),
		"cors_methods":         splitCSV(*corsMethods),
		"cors_headers":         splitCSV(*corsHeaders),
		"cors_expose_headers":  splitCSV(*corsExpose),
		"cors_max_age_seconds": *corsMaxAge,
	})
}

type r2CredentialsCLIResponse struct {
	DryRun         bool                            `json:"dry_run"`
	Force          bool                            `json:"force"`
	Status         string                          `json:"status"`
	Accounts       []r2CredentialsCLIAccountResult `json:"accounts"`
	Warnings       []string                        `json:"warnings,omitempty"`
	Errors         []string                        `json:"errors,omitempty"`
	ConfigPath     string                          `json:"config_path,omitempty"`
	ConfigWritten  bool                            `json:"config_written,omitempty"`
	ConfigAccounts []string                        `json:"config_accounts,omitempty"`
}

type r2CredentialsCLIAccountResult struct {
	Account       string                 `json:"account"`
	Default       bool                   `json:"default"`
	Library       string                 `json:"library,omitempty"`
	Bucket        string                 `json:"bucket,omitempty"`
	PublicBaseURL string                 `json:"public_base_url,omitempty"`
	Result        r2CredentialsCLIResult `json:"result"`
}

type r2CredentialsCLIResult struct {
	Bucket              string `json:"bucket"`
	Jurisdiction        string `json:"jurisdiction"`
	TokenName           string `json:"token_name"`
	PermissionGroupName string `json:"permission_group_name"`
	PermissionGroupID   string `json:"permission_group_id,omitempty"`
	Resource            string `json:"resource"`
	AccessKeyID         string `json:"access_key_id,omitempty"`
	SecretAccessKey     string `json:"secret_access_key,omitempty"`
	DryRun              bool   `json:"dry_run"`
	Action              string `json:"action"`
	Status              string `json:"status"`
	Error               string `json:"error,omitempty"`
}

func createR2Credentials(c client, args []string) error {
	fs := flag.NewFlagSet("create-r2-credentials", flag.ExitOnError)
	cfAccount := fs.String("cloudflare-account", "", "Cloudflare account name")
	cfLibrary := fs.String("cloudflare-library", "", "Cloudflare library name")
	all := fs.Bool("all", false, "create credentials for all Cloudflare accounts")
	bucket := fs.String("bucket", "", "R2 bucket name; defaults to configured/provisioned bucket")
	jurisdiction := fs.String("jurisdiction", "", "R2 jurisdiction for the scoped bucket resource")
	tokenName := fs.String("token-name", "", "Cloudflare account token name; supports {account}, {library}, {root}")
	permissionGroup := fs.String("permission-group", "", "Cloudflare permission group name; defaults to Workers R2 Storage Bucket Item Write")
	dryRun := fs.Bool("dry-run", true, "plan R2 credential creation without modifying Cloudflare; pass -dry-run=false to execute")
	force := fs.Bool("force", false, "create replacement credentials even if this account already has R2 credentials configured")
	writeConfig := fs.String("write-config", "", "local config file to update with the one-time generated credentials")
	_ = fs.Parse(args)
	if !*dryRun && *writeConfig == "" {
		return errors.New("-write-config is required with -dry-run=false so the one-time R2 secret is not lost")
	}
	req := map[string]any{
		"cloudflare_account":    *cfAccount,
		"cloudflare_library":    *cfLibrary,
		"all":                   *all,
		"bucket":                *bucket,
		"jurisdiction":          *jurisdiction,
		"token_name":            *tokenName,
		"permission_group_name": *permissionGroup,
		"dry_run":               *dryRun,
		"force":                 *force,
	}
	raw, err := c.doJSONRaw(http.MethodPost, "/api/v1/cloudflare/r2/credentials", req)
	if err != nil {
		return err
	}
	var resp r2CredentialsCLIResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return err
	}
	if !*dryRun && *writeConfig != "" {
		if resp.Status != "ok" {
			sanitizeR2CredentialsResponse(&resp)
			out, _ := json.Marshal(resp)
			_ = printJSON(out)
			return errors.New("r2 credential creation failed; config was not updated")
		}
		updated, err := writeR2CredentialsToConfig(*writeConfig, resp)
		if err != nil {
			return err
		}
		resp.ConfigPath = *writeConfig
		resp.ConfigWritten = len(updated) > 0
		resp.ConfigAccounts = updated
	}
	sanitizeR2CredentialsResponse(&resp)
	out, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	return printJSON(out)
}

func setR2Credentials(args []string) error {
	fs := flag.NewFlagSet("set-r2-credentials", flag.ExitOnError)
	configPath := fs.String("config", "", "local config file to update")
	cfAccount := fs.String("cloudflare-account", "", "Cloudflare account name")
	bucket := fs.String("bucket", "", "R2 bucket name")
	publicBaseURL := fs.String("public-base-url", "", "R2 public base URL")
	accessKeyID := fs.String("access-key-id", "", "R2 S3 access key id")
	secretAccessKey := fs.String("secret-access-key", "", "R2 S3 secret access key")
	_ = fs.Parse(args)
	if *configPath == "" || *cfAccount == "" || *accessKeyID == "" || *secretAccessKey == "" {
		return errors.New("-config, -cloudflare-account, -access-key-id and -secret-access-key are required")
	}
	resp := r2CredentialsCLIResponse{
		Status: "ok",
		Accounts: []r2CredentialsCLIAccountResult{{
			Account:       *cfAccount,
			Bucket:        *bucket,
			PublicBaseURL: *publicBaseURL,
			Result: r2CredentialsCLIResult{
				Bucket:          *bucket,
				AccessKeyID:     *accessKeyID,
				SecretAccessKey: *secretAccessKey,
				Action:          "import",
				Status:          "ok",
			},
		}},
	}
	updated, err := writeR2CredentialsToConfig(*configPath, resp)
	if err != nil {
		return err
	}
	resp.ConfigPath = *configPath
	resp.ConfigWritten = true
	resp.ConfigAccounts = updated
	sanitizeR2CredentialsResponse(&resp)
	out, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	return printJSON(out)
}

func publishCloudflareStatic(args []string) error {
	fs := flag.NewFlagSet("publish-cloudflare-static", flag.ExitOnError)
	site := fs.String("site", "", "site id used to derive the default Worker name")
	worker := fs.String("name", "", "Cloudflare Worker name; defaults to supercdn-{site}-static")
	dir := fs.String("dir", "", "static asset directory")
	domains := fs.String("domains", "", "comma-separated custom domains")
	compatDate := fs.String("compatibility-date", time.Now().UTC().Format("2006-01-02"), "Workers compatibility date")
	envFile := fs.String("env-file", "configs/private/cloudflare.env", "local env file containing CF_API_TOKEN and CF_ACCOUNT_ID; empty to skip")
	wrangler := fs.String("wrangler", "npx", "wrangler executable; default uses npx --prefix worker wrangler")
	wranglerPrefix := fs.String("wrangler-prefix", "worker", "npm package directory when -wrangler is npx")
	message := fs.String("message", "", "deployment message")
	cachePolicy := fs.String("static-cache-policy", cloudflareStaticCachePolicyAuto, "Cloudflare Static cache policy: auto, force, or none")
	notFoundHandling := fs.String("static-not-found-handling", "", "Cloudflare Static not_found_handling: none, 404-page, or single-page-application")
	spa := fs.Bool("static-spa", false, "enable Cloudflare Static single-page-application fallback")
	dryRun := fs.Bool("dry-run", true, "plan deployment without modifying Cloudflare; pass -dry-run=false to deploy")
	_ = fs.Parse(args)
	resp, err := runCloudflareStaticPublish(cloudflareStaticPublishOptions{
		Site:              *site,
		WorkerName:        *worker,
		Dir:               *dir,
		Domains:           splitCSV(*domains),
		CompatibilityDate: *compatDate,
		EnvFile:           *envFile,
		Wrangler:          *wrangler,
		WranglerPrefix:    *wranglerPrefix,
		Message:           *message,
		CachePolicy:       *cachePolicy,
		NotFoundHandling:  cloudflareStaticNotFoundHandlingFlag(*notFoundHandling, *spa),
		DryRun:            *dryRun,
	})
	if err != nil {
		raw, _ := json.Marshal(resp)
		_ = printJSON(raw)
		return err
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	return printJSON(raw)
}

func runCloudflareStaticPublish(opts cloudflareStaticPublishOptions) (cloudflareStaticPublishResponse, error) {
	if strings.TrimSpace(opts.Dir) == "" {
		return cloudflareStaticPublishResponse{}, errors.New("-dir is required")
	}
	absDir, err := filepath.Abs(opts.Dir)
	if err != nil {
		return cloudflareStaticPublishResponse{}, err
	}
	info, err := os.Stat(absDir)
	if err != nil {
		return cloudflareStaticPublishResponse{}, err
	}
	if !info.IsDir() {
		return cloudflareStaticPublishResponse{}, fmt.Errorf("%s is not a directory", opts.Dir)
	}
	preparedDir, cleanup, headers, err := prepareCloudflareStaticAssetsDir(absDir, opts.CachePolicy)
	if err != nil {
		return cloudflareStaticPublishResponse{}, err
	}
	if cleanup != nil {
		defer cleanup()
	}
	workerName := strings.TrimSpace(opts.WorkerName)
	if workerName == "" {
		if strings.TrimSpace(opts.Site) == "" {
			return cloudflareStaticPublishResponse{}, errors.New("-site or -name is required")
		}
		workerName = "supercdn-" + cleanWorkerName(opts.Site) + "-static"
	}
	notFoundHandling, err := normalizeCloudflareStaticNotFoundHandling(opts.NotFoundHandling)
	if err != nil {
		return cloudflareStaticPublishResponse{}, err
	}
	wranglerConfig := ""
	var configCleanup func()
	if notFoundHandling != "" {
		wranglerConfig, configCleanup, err = writeCloudflareStaticWranglerConfig(workerName, preparedDir, opts.CompatibilityDate, notFoundHandling)
		if err != nil {
			return cloudflareStaticPublishResponse{}, err
		}
		defer configCleanup()
	}
	wrangler := firstNonEmpty(strings.TrimSpace(opts.Wrangler), "npx")
	cmdArgs := wranglerDeployArgs(wrangler, opts.WranglerPrefix, workerName, preparedDir, opts.Domains, opts.CompatibilityDate, opts.Message, opts.DryRun, wranglerConfig)
	resp := cloudflareStaticPublishResponse{
		Status:            "planned",
		DryRun:            opts.DryRun,
		Worker:            workerName,
		AssetsDir:         preparedDir,
		SourceDir:         absDir,
		Domains:           opts.Domains,
		CompatibilityDate: strings.TrimSpace(opts.CompatibilityDate),
		CachePolicy:       headers.Policy,
		NotFoundHandling:  notFoundHandling,
		WranglerConfig:    wranglerConfig,
		HeadersFile:       headers.Path,
		HeadersSource:     headers.Source,
		HeadersGenerated:  headers.Generated,
		Command:           append([]string{wrangler}, cmdArgs...),
	}
	env, err := cloudflareStaticEnv(opts.EnvFile)
	if err != nil {
		return resp, err
	}
	out, exitCode, err := runCommand(wrangler, cmdArgs, env)
	resp.Output = strings.TrimSpace(out)
	resp.ExitCode = exitCode
	if err != nil {
		resp.Status = "failed"
		return resp, err
	}
	if opts.DryRun {
		resp.Status = "planned"
	} else {
		resp.Status = "ok"
	}
	return resp, nil
}

func wranglerDeployArgs(wrangler, wranglerPrefix, workerName, assetsDir string, domains []string, compatDate, message string, dryRun bool, configPath string) []string {
	var args []string
	if filepath.Base(strings.ToLower(strings.TrimSpace(wrangler))) == "npx" && strings.TrimSpace(wranglerPrefix) != "" {
		args = append(args, "--prefix", wranglerPrefix, "wrangler")
	}
	args = append(args, "deploy")
	if strings.TrimSpace(configPath) != "" {
		args = append(args, "--config", configPath)
	} else {
		args = append(args, "--name", workerName, "--compatibility-date", strings.TrimSpace(compatDate), "--assets", assetsDir)
	}
	for _, domain := range domains {
		args = append(args, "--domain", domain)
	}
	if strings.TrimSpace(message) != "" {
		args = append(args, "--message", strings.TrimSpace(message))
	}
	if dryRun {
		args = append(args, "--dry-run")
	}
	return args
}

type cloudflareStaticHeadersMeta struct {
	Policy    string
	Path      string
	Source    string
	Generated bool
}

func prepareCloudflareStaticAssetsDir(sourceDir, policy string) (string, func(), cloudflareStaticHeadersMeta, error) {
	policy, err := normalizeCloudflareStaticCachePolicy(policy)
	if err != nil {
		return "", nil, cloudflareStaticHeadersMeta{}, err
	}
	existingHeaders := filepath.Join(sourceDir, "_headers")
	if policy == cloudflareStaticCachePolicyNone {
		return sourceDir, nil, cloudflareStaticHeadersMeta{Policy: policy, Path: existingHeaders, Source: "disabled"}, nil
	}
	if policy == cloudflareStaticCachePolicyAuto {
		if info, err := os.Stat(existingHeaders); err == nil && !info.IsDir() {
			return sourceDir, nil, cloudflareStaticHeadersMeta{Policy: policy, Path: existingHeaders, Source: "existing"}, nil
		} else if err != nil && !os.IsNotExist(err) {
			return "", nil, cloudflareStaticHeadersMeta{}, err
		}
	}
	tmp, err := os.MkdirTemp("", "supercdn-cloudflare-static-*")
	if err != nil {
		return "", nil, cloudflareStaticHeadersMeta{}, err
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	if err := copyDirectory(sourceDir, tmp); err != nil {
		cleanup()
		return "", nil, cloudflareStaticHeadersMeta{}, err
	}
	headersPath := filepath.Join(tmp, "_headers")
	if err := os.WriteFile(headersPath, []byte(generatedCloudflareStaticHeaders(sourceDir)), 0644); err != nil {
		cleanup()
		return "", nil, cloudflareStaticHeadersMeta{}, err
	}
	return tmp, cleanup, cloudflareStaticHeadersMeta{Policy: policy, Path: headersPath, Source: "generated", Generated: true}, nil
}

func normalizeCloudflareStaticCachePolicy(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return cloudflareStaticCachePolicyAuto, nil
	}
	switch value {
	case cloudflareStaticCachePolicyAuto, cloudflareStaticCachePolicyForce, cloudflareStaticCachePolicyNone:
		return value, nil
	default:
		return "", fmt.Errorf("static cache policy must be auto, force or none")
	}
}

func cloudflareStaticNotFoundHandlingFlag(value string, spa bool) string {
	if spa {
		return cloudflareStaticNotFoundSPA
	}
	return value
}

func normalizeCloudflareStaticNotFoundHandling(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || value == cloudflareStaticNotFoundNone {
		return "", nil
	}
	switch value {
	case cloudflareStaticNotFound404, cloudflareStaticNotFoundSPA:
		return value, nil
	default:
		return "", fmt.Errorf("static not found handling must be none, 404-page or single-page-application")
	}
}

func writeCloudflareStaticWranglerConfig(workerName, assetsDir, compatDate, notFoundHandling string) (string, func(), error) {
	tmp, err := os.MkdirTemp("", "supercdn-wrangler-config-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	configPath := filepath.Join(tmp, "wrangler.toml")
	var b strings.Builder
	b.WriteString("name = " + strconv.Quote(workerName) + "\n")
	b.WriteString("compatibility_date = " + strconv.Quote(strings.TrimSpace(compatDate)) + "\n\n")
	b.WriteString("[assets]\n")
	b.WriteString("directory = " + strconv.Quote(filepath.ToSlash(assetsDir)) + "\n")
	b.WriteString("not_found_handling = " + strconv.Quote(notFoundHandling) + "\n")
	if err := os.WriteFile(configPath, []byte(b.String()), 0644); err != nil {
		cleanup()
		return "", nil, err
	}
	return configPath, cleanup, nil
}

func copyDirectory(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("cloudflare_static assets do not support symlink: %s", p)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			_ = in.Close()
			return err
		}
		_, copyErr := io.Copy(out, in)
		inErr := in.Close()
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if inErr != nil {
			return inErr
		}
		return closeErr
	})
}

func generatedCloudflareStaticHeaders(root string) string {
	files := listCloudflareStaticHeaderFiles(root)
	versionedRefs := versionedAssetReferences(root)
	var b strings.Builder
	b.WriteString("# Generated by SuperCDN. Do not edit in-place; change the deploy command or provide your own _headers file.\n")
	b.WriteString("# HTML stays revalidating. Versioned/build assets get immutable browser caching.\n\n")
	b.WriteString("/\n")
	b.WriteString("  Cache-Control: " + cloudflareStaticHTMLCacheControl + "\n\n")
	for _, rel := range files {
		publicPath := "/" + filepath.ToSlash(rel)
		if publicPath == "/_headers" || publicPath == "/_redirects" {
			continue
		}
		b.WriteString(publicPath + "\n")
		b.WriteString("  Cache-Control: " + cloudflareStaticCacheControlForPath(publicPath, versionedRefs) + "\n\n")
	}
	return b.String()
}

func listCloudflareStaticHeaderFiles(root string) []string {
	var files []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files = append(files, rel)
		return nil
	})
	sort.Slice(files, func(i, j int) bool {
		return filepath.ToSlash(files[i]) < filepath.ToSlash(files[j])
	})
	return files
}

func cloudflareStaticCacheControlForPath(publicPath string, versionedRefs map[string]bool) string {
	ext := strings.ToLower(urlpath.Ext(publicPath))
	base := strings.ToLower(urlpath.Base(publicPath))
	switch {
	case ext == ".html" || ext == ".htm" || base == "sw.js" || base == "service-worker.js":
		return cloudflareStaticHTMLCacheControl
	case isCloudflareStaticAssetExtension(ext) && (versionedRefs[publicPath] || isKnownBuildAssetPath(publicPath) || filenameLooksVersioned(base)):
		return cloudflareStaticImmutableCacheControl
	default:
		return cloudflareStaticShortCacheControl
	}
}

func isCloudflareStaticAssetExtension(ext string) bool {
	switch strings.ToLower(ext) {
	case ".js", ".mjs", ".css", ".json", ".wasm", ".map",
		".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".svg", ".ico",
		".woff", ".woff2", ".ttf", ".otf", ".eot",
		".mp4", ".webm", ".mp3", ".ogg", ".wav",
		".zip", ".gz", ".br", ".pdf", ".csv":
		return true
	default:
		return false
	}
}

func isKnownBuildAssetPath(publicPath string) bool {
	publicPath = strings.ToLower(publicPath)
	for _, prefix := range []string{"/assets/", "/static/", "/build/", "/_next/static/"} {
		if strings.HasPrefix(publicPath, prefix) {
			return true
		}
	}
	return false
}

func filenameLooksVersioned(base string) bool {
	name := strings.TrimSuffix(base, urlpath.Ext(base))
	for _, part := range filenameVersionSeparatorsRE.Split(name, -1) {
		if len(part) >= 8 && filenameVersionTokenRE.MatchString(strings.ToLower(part)) {
			hasLetter, hasDigit := false, false
			for _, r := range part {
				if r >= '0' && r <= '9' {
					hasDigit = true
				}
				if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
					hasLetter = true
				}
			}
			if hasLetter && hasDigit {
				return true
			}
		}
	}
	return false
}

var (
	assetRefWithQueryRE         = regexp.MustCompile(`(?i)(?:src|href)\s*=\s*["']([^"']+\?[^"']*)["']`)
	filenameVersionSeparatorsRE = regexp.MustCompile(`[._-]+`)
	filenameVersionTokenRE      = regexp.MustCompile(`^[a-z0-9]+$`)
)

func versionedAssetReferences(root string) map[string]bool {
	refs := map[string]bool{}
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.EqualFold(filepath.Ext(p), ".html") {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		htmlDir := "/" + strings.Trim(strings.TrimSuffix(filepath.ToSlash(rel), urlpath.Base(filepath.ToSlash(rel))), "/")
		if htmlDir == "/" {
			htmlDir = ""
		}
		for _, match := range assetRefWithQueryRE.FindAllStringSubmatch(string(raw), -1) {
			ref := strings.TrimSpace(match[1])
			u, err := url.Parse(ref)
			if err != nil || u.IsAbs() || u.Path == "" || strings.HasPrefix(u.Path, "//") {
				continue
			}
			if !isCloudflareStaticAssetExtension(strings.ToLower(urlpath.Ext(u.Path))) {
				continue
			}
			var publicPath string
			if strings.HasPrefix(u.Path, "/") {
				publicPath = urlpath.Clean(u.Path)
			} else {
				publicPath = urlpath.Clean(urlpath.Join("/", htmlDir, u.Path))
			}
			if !strings.HasPrefix(publicPath, "/") {
				publicPath = "/" + publicPath
			}
			refs[publicPath] = true
		}
		return nil
	})
	return refs
}

func cloudflareStaticEnv(path string) ([]string, error) {
	env := os.Environ()
	values, err := readSimpleEnvFile(path)
	if err != nil {
		return nil, err
	}
	if token := firstNonEmpty(os.Getenv("CLOUDFLARE_API_TOKEN"), values["CLOUDFLARE_API_TOKEN"], values["CF_API_TOKEN"]); token != "" {
		env = append(env, "CLOUDFLARE_API_TOKEN="+token)
	}
	if accountID := firstNonEmpty(os.Getenv("CLOUDFLARE_ACCOUNT_ID"), values["CLOUDFLARE_ACCOUNT_ID"], values["CF_ACCOUNT_ID"]); accountID != "" {
		env = append(env, "CLOUDFLARE_ACCOUNT_ID="+accountID)
	}
	for key, value := range values {
		if strings.HasPrefix(key, "CF_") || strings.HasPrefix(key, "CLOUDFLARE_") {
			env = append(env, key+"="+value)
		}
	}
	return env, nil
}

func readSimpleEnvFile(path string) (map[string]string, error) {
	values := map[string]string{}
	path = strings.TrimSpace(path)
	if path == "" {
		return values, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return values, nil
		}
		return nil, err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key != "" {
			values[key] = value
		}
	}
	return values, nil
}

func runCommand(name string, args, env []string) (string, int, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = env
	raw, err := cmd.CombinedOutput()
	if err == nil {
		return string(raw), 0, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return string(raw), exitErr.ExitCode(), err
	}
	return string(raw), -1, err
}

func httpClientWithDNSResolver(resolverAddress string) (*http.Client, error) {
	resolverAddress = strings.TrimSpace(resolverAddress)
	if resolverAddress == "" {
		return nil, nil
	}
	if !strings.Contains(resolverAddress, ":") {
		resolverAddress += ":53"
	}
	if _, _, err := net.SplitHostPort(resolverAddress); err != nil {
		return nil, fmt.Errorf("invalid resolver %q: %w", resolverAddress, err)
	}
	resolverDialer := &net.Dialer{Timeout: 5 * time.Second}
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return resolverDialer.DialContext(ctx, network, resolverAddress)
		},
	}
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Resolver:  resolver,
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = dialer.DialContext
	return &http.Client{Transport: transport}, nil
}

func cleanWorkerName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func deploymentTargetAlias(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "cloudflare", "cloudflare_static", "workers_static", "workers_assets", "pages":
		return "cloudflare_static"
	case "hybrid", "hybrid_edge", "edge":
		return "hybrid_edge"
	case "origin", "go_origin", "origin_assisted":
		return "origin_assisted"
	default:
		return value
	}
}

func extractCloudflareVersionID(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if value, ok := strings.CutPrefix(line, "Current Version ID:"); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func writeR2CredentialsToConfig(configPath string, resp r2CredentialsCLIResponse) ([]string, error) {
	configPath = strings.TrimSpace(configPath)
	if configPath == "" {
		return nil, errors.New("config path is required")
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	accounts, ok := root["cloudflare_accounts"].([]any)
	if !ok {
		return nil, errors.New("config has no cloudflare_accounts array")
	}
	updated := []string{}
	for _, item := range resp.Accounts {
		if item.Result.Status != "ok" || item.Result.AccessKeyID == "" || item.Result.SecretAccessKey == "" {
			continue
		}
		accountConfig := findConfigAccount(accounts, item.Account)
		if accountConfig == nil {
			return updated, fmt.Errorf("cloudflare account %q not found in config", item.Account)
		}
		r2, ok := accountConfig["r2"].(map[string]any)
		if !ok || r2 == nil {
			r2 = map[string]any{}
			accountConfig["r2"] = r2
		}
		if item.Bucket != "" {
			r2["bucket"] = item.Bucket
		}
		if item.PublicBaseURL != "" {
			r2["public_base_url"] = item.PublicBaseURL
		}
		r2["access_key_id"] = item.Result.AccessKeyID
		r2["secret_access_key"] = item.Result.SecretAccessKey
		updated = append(updated, item.Account)
	}
	if len(updated) == 0 {
		return nil, errors.New("no generated credentials were available to write")
	}
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	out = append(out, '\n')
	if err := os.WriteFile(configPath, out, 0o600); err != nil {
		return nil, err
	}
	return updated, nil
}

func findConfigAccount(accounts []any, name string) map[string]any {
	for _, raw := range accounts {
		account, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if strings.TrimSpace(fmt.Sprint(account["name"])) == name {
			return account
		}
	}
	return nil
}

func sanitizeR2CredentialsResponse(resp *r2CredentialsCLIResponse) {
	for i := range resp.Accounts {
		if resp.Accounts[i].Result.SecretAccessKey != "" {
			resp.Accounts[i].Result.SecretAccessKey = "<redacted>"
		}
	}
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
  supercdnctl [global flags] create-bucket -slug movie-posters -name 影视海报桶 -profile china_all -types image
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
