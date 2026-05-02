package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
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

	"supercdn/internal/siteinspect"
	"supercdn/internal/siteprobe"
)

type client struct {
	baseURL string
	token   string
	http    *http.Client
}

type cliConfig struct {
	CurrentProfile string                `json:"current_profile,omitempty"`
	Profiles       map[string]cliProfile `json:"profiles,omitempty"`
}

type cliProfile struct {
	Server string `json:"server"`
	Token  string `json:"token"`
}

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

func loadCLIConfig() (cliConfig, error) {
	path, err := cliConfigPath()
	if err != nil {
		return cliConfig{}, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cliConfig{Profiles: map[string]cliProfile{}}, nil
	}
	if err != nil {
		return cliConfig{}, err
	}
	var cfg cliConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cliConfig{}, fmt.Errorf("read CLI config %s: %w", path, err)
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]cliProfile{}
	}
	return cfg, nil
}

func saveCLIConfig(cfg cliConfig) error {
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]cliProfile{}
	}
	path, err := cliConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(path, raw, 0o600)
}

func cliConfigPath() (string, error) {
	if path := strings.TrimSpace(os.Getenv("SUPERCDN_CONFIG")); path != "" {
		return path, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "supercdn", "cli.json"), nil
}

func saveCLIProfile(profileName, serverURL, token string) error {
	cfg, err := loadCLIConfig()
	if err != nil {
		return err
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]cliProfile{}
	}
	cfg.CurrentProfile = profileName
	cfg.Profiles[profileName] = cliProfile{Server: strings.TrimRight(serverURL, "/"), Token: token}
	return saveCLIConfig(cfg)
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

func deploySite(c client, args []string) error {
	fs := flag.NewFlagSet("deploy-site", flag.ExitOnError)
	site := fs.String("site", "", "site id")
	dir := fs.String("dir", "", "dist directory")
	bundle := fs.String("bundle", "", "zip artifact to upload")
	env := fs.String("env", "production", "deployment environment: production or preview")
	promote := fs.Bool("promote", false, "promote the deployment to production after processing")
	pinned := fs.Bool("pinned", false, "prevent automatic deployment retention cleanup")
	wait := fs.Bool("wait", true, "wait for asynchronous deployment completion")
	timeout := fs.Duration("timeout", 30*time.Minute, "maximum time to wait")
	profile := fs.String("profile", "", "route profile override")
	target := fs.String("target", "", "deployment target override: origin_assisted, cloudflare_static, or hybrid_edge")
	routingPolicy := fs.String("routing-policy", "", "routing policy override; requires matching multi-source route profile")
	resourceFailover := fs.Bool("resource-failover", false, "enable explicit Web resource failover between route-profile storage targets")
	entryOriginFallback := fs.Bool("entry-origin-fallback", false, "temporarily fall back entry HTML/SPAs to Go origin when Cloudflare entry fails")
	domains := fs.String("domains", "", "comma-separated custom domains when -target cloudflare_static or hybrid_edge")
	staticName := fs.String("static-name", "", "Worker name when -target cloudflare_static; defaults to supercdn-{site}-static")
	edgeName := fs.String("edge-name", "", "Worker name when -target hybrid_edge; defaults to supercdn-{site}-edge")
	compatDate := fs.String("compatibility-date", time.Now().UTC().Format("2006-01-02"), "Workers compatibility date when -target cloudflare_static or hybrid_edge")
	staticMessage := fs.String("message", "", "Cloudflare deployment message when -target cloudflare_static or hybrid_edge")
	staticCachePolicy := fs.String("static-cache-policy", cloudflareStaticCachePolicyAuto, "Cloudflare Static cache policy: auto, force, or none")
	staticNotFoundHandling := fs.String("static-not-found-handling", "", "Cloudflare Static not_found_handling: none, 404-page, or single-page-application")
	staticSPA := fs.Bool("static-spa", false, "enable Cloudflare Static single-page-application fallback")
	staticVerify := fs.String("static-verify", cloudflareStaticVerifyWait, "Cloudflare Static readiness check: wait, warn, or none")
	staticVerifyTimeout := fs.Duration("static-verify-timeout", 2*time.Minute, "maximum time to wait for Cloudflare Static custom domains")
	staticVerifyInterval := fs.Duration("static-verify-interval", 5*time.Second, "delay between Cloudflare Static readiness probes")
	staticVerifySPAPath := fs.String("static-verify-spa-path", "", "SPA path to verify after Cloudflare Static publish; defaults to /__supercdn_spa_probe when -static-spa is enabled")
	staticVerifyResolver := fs.String("static-verify-resolver", "1.1.1.1:53", "DNS resolver for Cloudflare Static readiness probes")
	edgeKVNamespaceID := fs.String("edge-kv-namespace-id", "", "Cloudflare Workers KV namespace id for hybrid_edge edge manifests")
	edgeKVNamespace := fs.String("edge-kv-namespace", "supercdn-edge-manifest", "Cloudflare Workers KV namespace title for hybrid_edge edge manifests")
	edgeManifestMode := fs.String("edge-manifest-mode", "route", "hybrid_edge Worker manifest mode: route or enforce")
	edgeDefaultCacheControl := fs.String("edge-default-cache-control", "public, max-age=300", "default Cache-Control for hybrid_edge Worker fallback responses")
	edgeCandidateWait := fs.Bool("edge-candidate-wait", true, "wait for routing-policy/resource-failover candidates before publishing hybrid_edge edge manifest")
	edgeCandidateTimeout := fs.Duration("edge-candidate-timeout", 10*time.Minute, "maximum time to wait for hybrid_edge routing/failover candidates")
	_ = fs.Parse(args)
	if *site == "" {
		return errors.New("-site is required")
	}
	if *dir == "" && *bundle == "" {
		return errors.New("-dir or -bundle is required")
	}
	if *dir != "" && *bundle != "" {
		return errors.New("use either -dir or -bundle, not both")
	}
	if strings.EqualFold(*env, "production") && !flagWasSet(fs, "promote") {
		*promote = true
	}
	resolvedTarget := deploymentTargetAlias(*target)
	resolvedProfile := *profile
	resolvedDomains := splitCSV(*domains)
	if strings.TrimSpace(*target) == "" {
		defaults, err := c.resolveSiteDeploymentTarget(*site, *profile, "")
		if err != nil {
			return err
		}
		resolvedTarget = deploymentTargetAlias(defaults.DeploymentTarget)
		if strings.TrimSpace(resolvedProfile) == "" {
			resolvedProfile = defaults.RouteProfile
		}
		if len(resolvedDomains) == 0 {
			resolvedDomains = defaults.Domains
		}
	} else if strings.TrimSpace(resolvedProfile) == "" || len(resolvedDomains) == 0 {
		defaults, err := c.resolveSiteDeploymentTarget(*site, *profile, *target)
		if err == nil {
			if strings.TrimSpace(resolvedProfile) == "" {
				resolvedProfile = defaults.RouteProfile
			}
			if len(resolvedDomains) == 0 {
				resolvedDomains = defaults.Domains
			}
		}
	}
	if *entryOriginFallback && resolvedTarget != "hybrid_edge" {
		return errors.New("entry origin fallback is only supported for hybrid_edge deployments")
	}
	if resolvedTarget == "cloudflare_static" {
		if *resourceFailover {
			return errors.New("resource failover is not supported for cloudflare_static deployments")
		}
		if *dir == "" {
			return errors.New("cloudflare_static deploy-site requires -dir")
		}
		if *bundle != "" {
			return errors.New("cloudflare_static deploy-site does not accept -bundle yet")
		}
		return deploySiteCloudflareStatic(c, cloudflareStaticDeploySiteOptions{
			Site:              *site,
			Dir:               *dir,
			Environment:       *env,
			RouteProfile:      resolvedProfile,
			DeploymentTarget:  resolvedTarget,
			RoutingPolicy:     strings.TrimSpace(*routingPolicy),
			ResourceFailover:  false,
			Domains:           resolvedDomains,
			WorkerName:        *staticName,
			CompatibilityDate: *compatDate,
			Message:           *staticMessage,
			CachePolicy:       *staticCachePolicy,
			NotFoundHandling:  cloudflareStaticNotFoundHandlingFlag(*staticNotFoundHandling, *staticSPA),
			VerifyMode:        *staticVerify,
			VerifyTimeout:     *staticVerifyTimeout,
			VerifyInterval:    *staticVerifyInterval,
			VerifySPAPath:     *staticVerifySPAPath,
			VerifyResolver:    *staticVerifyResolver,
			Promote:           *promote,
			Pinned:            *pinned,
		})
	}
	if resolvedTarget == "hybrid_edge" {
		if *dir == "" {
			return errors.New("hybrid_edge deploy-site requires -dir")
		}
		if *bundle != "" {
			return errors.New("hybrid_edge deploy-site does not accept -bundle yet")
		}
		return deploySiteHybridEdge(c, hybridEdgeDeploySiteOptions{
			Site:                *site,
			Dir:                 *dir,
			Environment:         *env,
			RouteProfile:        resolvedProfile,
			DeploymentTarget:    resolvedTarget,
			RoutingPolicy:       strings.TrimSpace(*routingPolicy),
			ResourceFailover:    *resourceFailover,
			EntryOriginFallback: *entryOriginFallback,
			Domains:             resolvedDomains,
			WorkerName:          *edgeName,
			CompatibilityDate:   *compatDate,
			Message:             *staticMessage,
			CachePolicy:         *staticCachePolicy,
			NotFoundHandling:    cloudflareStaticNotFoundHandlingFlag(*staticNotFoundHandling, *staticSPA),
			VerifyMode:          *staticVerify,
			VerifyTimeout:       *staticVerifyTimeout,
			VerifyInterval:      *staticVerifyInterval,
			VerifySPAPath:       *staticVerifySPAPath,
			VerifyResolver:      *staticVerifyResolver,
			Promote:             *promote,
			Pinned:              *pinned,
			Timeout:             *timeout,
			KVNamespaceID:       *edgeKVNamespaceID,
			KVNamespace:         *edgeKVNamespace,
			ManifestMode:        *edgeManifestMode,
			DefaultCacheControl: *edgeDefaultCacheControl,
			CandidateWait:       *edgeCandidateWait,
			CandidateTimeout:    *edgeCandidateTimeout,
		})
	}
	artifact := *bundle
	cleanup := ""
	if *dir != "" {
		zipPath, err := zipDirectory(*dir)
		if err != nil {
			return err
		}
		artifact = zipPath
		cleanup = zipPath
	}
	if cleanup != "" {
		defer os.Remove(cleanup)
	}
	fields := map[string]string{
		"route_profile":     resolvedProfile,
		"deployment_target": resolvedTarget,
		"routing_policy":    strings.TrimSpace(*routingPolicy),
		"resource_failover": fmt.Sprint(*resourceFailover),
		"environment":       *env,
		"promote":           fmt.Sprint(*promote),
		"pinned":            fmt.Sprint(*pinned),
	}
	raw, err := c.uploadFileRaw("/api/v1/sites/"+url.PathEscape(*site)+"/deployments", "artifact", artifact, fields)
	if err != nil {
		return err
	}
	if !*wait {
		return printJSON(raw)
	}
	var created struct {
		DeploymentID string `json:"deployment_id"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		return err
	}
	if created.DeploymentID == "" {
		return printJSON(raw)
	}
	return c.waitDeployment(*site, created.DeploymentID, *timeout)
}

type siteDeploymentTargetDefaults struct {
	SiteID           string   `json:"site_id"`
	SiteExists       bool     `json:"site_exists"`
	RouteProfile     string   `json:"route_profile"`
	DeploymentTarget string   `json:"deployment_target"`
	Source           string   `json:"source"`
	Domains          []string `json:"domains,omitempty"`
	DefaultDomain    string   `json:"default_domain,omitempty"`
}

func (c client) resolveSiteDeploymentTarget(site, routeProfile, target string) (siteDeploymentTargetDefaults, error) {
	q := url.Values{}
	if strings.TrimSpace(routeProfile) != "" {
		q.Set("route_profile", strings.TrimSpace(routeProfile))
	}
	if strings.TrimSpace(target) != "" {
		q.Set("deployment_target", strings.TrimSpace(target))
	}
	path := "/api/v1/sites/" + url.PathEscape(site) + "/deployment-target"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	raw, err := c.doRaw(http.MethodGet, path, nil, "")
	if err != nil {
		return siteDeploymentTargetDefaults{}, err
	}
	var defaults siteDeploymentTargetDefaults
	if err := json.Unmarshal(raw, &defaults); err != nil {
		return siteDeploymentTargetDefaults{}, err
	}
	return defaults, nil
}

type cloudflareStaticDeploySiteOptions struct {
	Site              string
	Dir               string
	Environment       string
	RouteProfile      string
	DeploymentTarget  string
	RoutingPolicy     string
	ResourceFailover  bool
	Domains           []string
	WorkerName        string
	CompatibilityDate string
	Message           string
	CachePolicy       string
	NotFoundHandling  string
	VerifyMode        string
	VerifyTimeout     time.Duration
	VerifyInterval    time.Duration
	VerifySPAPath     string
	VerifyResolver    string
	Promote           bool
	Pinned            bool
}

func deploySiteCloudflareStatic(c client, opts cloudflareStaticDeploySiteOptions) error {
	stats, err := summarizeCloudflareStaticDirectory(opts.Dir)
	if err != nil {
		return err
	}
	workerName := strings.TrimSpace(opts.WorkerName)
	if workerName == "" {
		workerName = "supercdn-" + cleanWorkerName(opts.Site) + "-static"
	}
	publish, err := runCloudflareStaticPublish(cloudflareStaticPublishOptions{
		Site:              opts.Site,
		WorkerName:        workerName,
		Dir:               opts.Dir,
		Domains:           opts.Domains,
		CompatibilityDate: opts.CompatibilityDate,
		Message:           firstNonEmpty(opts.Message, "SuperCDN cloudflare_static deploy "+opts.Site),
		CachePolicy:       opts.CachePolicy,
		NotFoundHandling:  opts.NotFoundHandling,
		DryRun:            false,
		EnvFile:           "configs/private/cloudflare.env",
		Wrangler:          "npx",
		WranglerPrefix:    "worker",
	})
	if err != nil {
		raw, _ := json.Marshal(publish)
		_ = printJSON(raw)
		return err
	}
	verify, err := verifyCloudflareStaticPublish(context.Background(), cloudflareStaticVerifyOptions{
		Mode:                        opts.VerifyMode,
		Domains:                     opts.Domains,
		Timeout:                     opts.VerifyTimeout,
		Interval:                    opts.VerifyInterval,
		SPAPath:                     opts.VerifySPAPath,
		Resolver:                    opts.VerifyResolver,
		NotFoundHandling:            publish.NotFoundHandling,
		RequireDirectAssets:         true,
		RequireEdgeStaticHTML:       false,
		RequireEdgeManifestAssets:   false,
		RequireGeneratedCachePolicy: publish.HeadersGenerated,
		RequireImmutableAssetCache:  publish.HeadersGenerated,
	})
	if err != nil {
		raw, _ := json.Marshal(verify)
		_ = printJSON(raw)
		return err
	}
	req := map[string]any{
		"environment":         opts.Environment,
		"route_profile":       opts.RouteProfile,
		"deployment_target":   "cloudflare_static",
		"routing_policy":      opts.RoutingPolicy,
		"resource_failover":   opts.ResourceFailover,
		"worker_name":         workerName,
		"version_id":          extractCloudflareVersionID(publish.Output),
		"domains":             opts.Domains,
		"compatibility_date":  opts.CompatibilityDate,
		"assets_sha256":       stats.SHA256,
		"file_count":          stats.FileCount,
		"total_size":          stats.TotalSize,
		"cache_policy":        publish.CachePolicy,
		"headers_generated":   publish.HeadersGenerated,
		"not_found_handling":  publish.NotFoundHandling,
		"verification_status": verify.Status,
		"verified_at_utc":     time.Now().UTC().Format(time.RFC3339Nano),
		"published_at_utc":    time.Now().UTC().Format(time.RFC3339Nano),
		"promote":             opts.Promote,
		"pinned":              opts.Pinned,
	}
	return c.doJSON(http.MethodPost, "/api/v1/sites/"+url.PathEscape(opts.Site)+"/cloudflare-static/deployments", req)
}

type hybridEdgeDeploySiteOptions struct {
	Site                string
	Dir                 string
	Environment         string
	RouteProfile        string
	DeploymentTarget    string
	RoutingPolicy       string
	ResourceFailover    bool
	EntryOriginFallback bool
	Domains             []string
	WorkerName          string
	CompatibilityDate   string
	Message             string
	CachePolicy         string
	NotFoundHandling    string
	VerifyMode          string
	VerifyTimeout       time.Duration
	VerifyInterval      time.Duration
	VerifySPAPath       string
	VerifyResolver      string
	Promote             bool
	Pinned              bool
	Timeout             time.Duration
	KVNamespaceID       string
	KVNamespace         string
	ManifestMode        string
	DefaultCacheControl string
	CandidateWait       bool
	CandidateTimeout    time.Duration
}

type siteDeploymentResult struct {
	ID               string   `json:"id"`
	SiteID           string   `json:"site_id"`
	Status           string   `json:"status"`
	RouteProfile     string   `json:"route_profile"`
	DeploymentTarget string   `json:"deployment_target"`
	RoutingPolicy    string   `json:"routing_policy,omitempty"`
	ResourceFailover bool     `json:"resource_failover"`
	Active           bool     `json:"active"`
	ProductionURL    string   `json:"production_url,omitempty"`
	ProductionURLs   []string `json:"production_urls,omitempty"`
	PreviewURL       string   `json:"preview_url,omitempty"`
}

type edgeManifestPublishResponse struct {
	SiteID            string                `json:"site_id"`
	DeploymentID      string                `json:"deployment_id"`
	Active            bool                  `json:"active"`
	CloudflareAccount string                `json:"cloudflare_account,omitempty"`
	CloudflareLibrary string                `json:"cloudflare_library,omitempty"`
	KVNamespaceID     string                `json:"kv_namespace_id,omitempty"`
	KVNamespace       string                `json:"kv_namespace,omitempty"`
	KeyPrefix         string                `json:"key_prefix"`
	Domains           []string              `json:"domains,omitempty"`
	DryRun            bool                  `json:"dry_run"`
	Status            string                `json:"status"`
	ManifestSize      int                   `json:"manifest_size"`
	ManifestSHA256    string                `json:"manifest_sha256"`
	Writes            []edgeManifestKVWrite `json:"writes,omitempty"`
	ManifestWarnings  []string              `json:"manifest_warnings,omitempty"`
	Warnings          []string              `json:"warnings,omitempty"`
	Errors            []string              `json:"errors,omitempty"`
}

type edgeManifestKVWrite struct {
	Domain string `json:"domain"`
	Key    string `json:"key"`
	Kind   string `json:"kind"`
	Action string `json:"action"`
	DryRun bool   `json:"dry_run,omitempty"`
	Size   int    `json:"size,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	Error  string `json:"error,omitempty"`
}

type hybridEdgeDeployResponse struct {
	Status       string                          `json:"status"`
	SiteID       string                          `json:"site_id"`
	DeploymentID string                          `json:"deployment_id"`
	URL          string                          `json:"url,omitempty"`
	URLs         []string                        `json:"urls,omitempty"`
	Deployment   siteDeploymentResult            `json:"deployment"`
	EdgeManifest edgeManifestPublishResponse     `json:"edge_manifest"`
	Worker       cloudflareStaticPublishResponse `json:"worker"`
	Verify       cloudflareStaticVerifyReport    `json:"verify"`
}

func deploySiteHybridEdge(c client, opts hybridEdgeDeploySiteOptions) error {
	if len(cleanDomains(opts.Domains)) == 0 {
		return errors.New("hybrid_edge deploy-site requires at least one domain")
	}
	dep, err := createAndWaitSiteDeployment(c, opts.Site, siteDeploymentUploadOptions{
		Dir:              opts.Dir,
		Environment:      opts.Environment,
		RouteProfile:     opts.RouteProfile,
		DeploymentTarget: opts.DeploymentTarget,
		RoutingPolicy:    opts.RoutingPolicy,
		ResourceFailover: opts.ResourceFailover,
		Promote:          opts.Promote,
		Pinned:           opts.Pinned,
		Timeout:          opts.Timeout,
	})
	if err != nil {
		return err
	}
	routingPolicy := firstNonEmpty(dep.RoutingPolicy, opts.RoutingPolicy)
	resourceFailover := dep.ResourceFailover || opts.ResourceFailover
	if opts.CandidateWait && (strings.TrimSpace(routingPolicy) != "" || resourceFailover) {
		mode := "resource_failover"
		if strings.TrimSpace(routingPolicy) != "" {
			mode = "routing_policy"
		}
		report, err := c.waitEdgeManifestCandidates(edgeManifestCandidateWaitOptions{
			Site:          opts.Site,
			Deployment:    dep.ID,
			Mode:          mode,
			MinCandidates: 2,
			Timeout:       opts.CandidateTimeout,
		})
		if err != nil {
			raw, _ := json.Marshal(report)
			_ = printJSON(raw)
			return err
		}
	}
	edgeManifest, err := c.publishEdgeManifestForDeployment(edgeManifestPublishOptions{
		Site:          opts.Site,
		Deployment:    dep.ID,
		Domains:       opts.Domains,
		KVNamespaceID: opts.KVNamespaceID,
		KVNamespace:   opts.KVNamespace,
		ActiveKey:     dep.Active,
		DeploymentKey: true,
		DryRun:        false,
	})
	if err != nil {
		return err
	}
	if strings.TrimSpace(edgeManifest.KVNamespaceID) == "" {
		return errors.New("hybrid_edge publish-edge-manifest did not return a kv_namespace_id")
	}
	workerName := strings.TrimSpace(opts.WorkerName)
	if workerName == "" {
		workerName = "supercdn-" + cleanWorkerName(opts.Site) + "-edge"
	}
	publish, err := runHybridEdgePublish(hybridEdgePublishOptions{
		Site:                opts.Site,
		WorkerName:          workerName,
		Dir:                 opts.Dir,
		Domains:             opts.Domains,
		CompatibilityDate:   opts.CompatibilityDate,
		Message:             firstNonEmpty(opts.Message, "SuperCDN hybrid_edge deploy "+opts.Site),
		CachePolicy:         opts.CachePolicy,
		NotFoundHandling:    opts.NotFoundHandling,
		KVNamespaceID:       edgeManifest.KVNamespaceID,
		ManifestMode:        firstNonEmpty(opts.ManifestMode, "route"),
		DefaultCacheControl: firstNonEmpty(opts.DefaultCacheControl, "public, max-age=300"),
		EntryOriginFallback: opts.EntryOriginFallback,
		OriginBaseURL:       c.baseURL,
		EnvFile:             "configs/private/cloudflare.env",
		Wrangler:            "npx",
		WranglerPrefix:      "worker",
	})
	if err != nil {
		raw, _ := json.Marshal(publish)
		_ = printJSON(raw)
		return err
	}
	verify, err := verifyCloudflareStaticPublish(context.Background(), cloudflareStaticVerifyOptions{
		Mode:                        opts.VerifyMode,
		Domains:                     opts.Domains,
		Timeout:                     opts.VerifyTimeout,
		Interval:                    opts.VerifyInterval,
		SPAPath:                     opts.VerifySPAPath,
		Resolver:                    opts.VerifyResolver,
		NotFoundHandling:            publish.NotFoundHandling,
		RequireDirectAssets:         false,
		RequireEdgeStaticHTML:       true,
		RequireEdgeManifestAssets:   true,
		RequireGeneratedCachePolicy: publish.HeadersGenerated,
		RequireImmutableAssetCache:  false,
	})
	if err != nil {
		raw, _ := json.Marshal(verify)
		_ = printJSON(raw)
		return err
	}
	resp := hybridEdgeDeployResponse{
		Status:       "ok",
		SiteID:       opts.Site,
		DeploymentID: dep.ID,
		URL:          firstNonEmpty(dep.ProductionURL, firstString(dep.ProductionURLs)),
		URLs:         dep.ProductionURLs,
		Deployment:   dep,
		EdgeManifest: edgeManifest,
		Worker:       publish,
		Verify:       verify,
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	return printJSON(raw)
}

type siteDeploymentUploadOptions struct {
	Dir              string
	Environment      string
	RouteProfile     string
	DeploymentTarget string
	RoutingPolicy    string
	ResourceFailover bool
	Promote          bool
	Pinned           bool
	Timeout          time.Duration
}

func createAndWaitSiteDeployment(c client, site string, opts siteDeploymentUploadOptions) (siteDeploymentResult, error) {
	zipPath, err := zipDirectory(opts.Dir)
	if err != nil {
		return siteDeploymentResult{}, err
	}
	defer os.Remove(zipPath)
	fields := map[string]string{
		"route_profile":     opts.RouteProfile,
		"deployment_target": opts.DeploymentTarget,
		"routing_policy":    opts.RoutingPolicy,
		"resource_failover": fmt.Sprint(opts.ResourceFailover),
		"environment":       opts.Environment,
		"promote":           fmt.Sprint(opts.Promote),
		"pinned":            fmt.Sprint(opts.Pinned),
	}
	raw, err := c.uploadFileRaw("/api/v1/sites/"+url.PathEscape(site)+"/deployments", "artifact", zipPath, fields)
	if err != nil {
		return siteDeploymentResult{}, err
	}
	var created struct {
		DeploymentID string `json:"deployment_id"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		return siteDeploymentResult{}, err
	}
	if created.DeploymentID == "" {
		return siteDeploymentResult{}, errors.New("deployment response did not include deployment_id")
	}
	readyRaw, err := c.waitDeploymentRaw(site, created.DeploymentID, opts.Timeout)
	if err != nil {
		_ = printJSON(readyRaw)
		return siteDeploymentResult{}, err
	}
	var dep siteDeploymentResult
	if err := json.Unmarshal(readyRaw, &dep); err != nil {
		return siteDeploymentResult{}, err
	}
	return dep, nil
}

type edgeManifestPublishOptions struct {
	Site          string
	Deployment    string
	Domains       []string
	KVNamespaceID string
	KVNamespace   string
	ActiveKey     bool
	DeploymentKey bool
	DryRun        bool
}

func (c client) publishEdgeManifestForDeployment(opts edgeManifestPublishOptions) (edgeManifestPublishResponse, error) {
	req := map[string]any{
		"domains":         opts.Domains,
		"kv_namespace_id": opts.KVNamespaceID,
		"kv_namespace":    opts.KVNamespace,
		"active_key":      opts.ActiveKey,
		"deployment_key":  opts.DeploymentKey,
		"dry_run":         opts.DryRun,
	}
	raw, err := c.doRaw(http.MethodPost, "/api/v1/sites/"+url.PathEscape(opts.Site)+"/deployments/"+url.PathEscape(opts.Deployment)+"/edge-manifest/publish", bytes.NewReader(mustJSON(req)), "application/json")
	if err != nil {
		return edgeManifestPublishResponse{}, err
	}
	var resp edgeManifestPublishResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return edgeManifestPublishResponse{}, err
	}
	if resp.Status != "ok" && resp.Status != "planned" {
		return resp, fmt.Errorf("publish edge manifest status %q", resp.Status)
	}
	return resp, nil
}

type edgeManifestCandidateWaitOptions struct {
	Site          string
	Deployment    string
	Mode          string
	MinCandidates int
	Timeout       time.Duration
}

type edgeManifestCandidateWaitReport struct {
	Status           string                             `json:"status"`
	SiteID           string                             `json:"site_id,omitempty"`
	DeploymentID     string                             `json:"deployment_id,omitempty"`
	Mode             string                             `json:"mode"`
	MinCandidates    int                                `json:"min_candidates"`
	Attempts         int                                `json:"attempts"`
	RequiredRoutes   int                                `json:"required_routes"`
	ReadyRoutes      int                                `json:"ready_routes"`
	LastCheckedAtUTC string                             `json:"last_checked_at_utc,omitempty"`
	Routes           []edgeManifestCandidateRouteStatus `json:"routes,omitempty"`
	ManifestWarnings []string                           `json:"manifest_warnings,omitempty"`
	Warnings         []string                           `json:"warnings,omitempty"`
}

type edgeManifestCandidateRouteStatus struct {
	Path               string `json:"path"`
	Type               string `json:"type,omitempty"`
	Delivery           string `json:"delivery,omitempty"`
	File               string `json:"file,omitempty"`
	Status             int    `json:"status,omitempty"`
	Candidates         int    `json:"candidates"`
	ReadyCandidates    int    `json:"ready_candidates"`
	RequiredCandidates int    `json:"required_candidates"`
	OK                 bool   `json:"ok"`
	Message            string `json:"message,omitempty"`
}

type edgeManifestCandidateManifest struct {
	SiteID       string                                `json:"site_id"`
	DeploymentID string                                `json:"deployment_id"`
	Routes       map[string]edgeManifestCandidateRoute `json:"routes"`
	Warnings     []string                              `json:"warnings,omitempty"`
}

type edgeManifestCandidateRoute struct {
	Type       string                       `json:"type"`
	Delivery   string                       `json:"delivery"`
	File       string                       `json:"file"`
	Status     int                          `json:"status"`
	Candidates []edgeManifestCandidateEntry `json:"candidates,omitempty"`
}

type edgeManifestCandidateEntry struct {
	Target string `json:"target"`
	Type   string `json:"type,omitempty"`
	Status string `json:"status,omitempty"`
	URL    string `json:"url,omitempty"`
}

func (c client) waitEdgeManifestCandidates(opts edgeManifestCandidateWaitOptions) (edgeManifestCandidateWaitReport, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Minute
	}
	if opts.MinCandidates <= 0 {
		opts.MinCandidates = 2
	}
	deadline := time.Now().Add(opts.Timeout)
	var report edgeManifestCandidateWaitReport
	attempts := 0
	for {
		attempts++
		raw, err := c.doRaw(http.MethodGet, "/api/v1/sites/"+url.PathEscape(opts.Site)+"/deployments/"+url.PathEscape(opts.Deployment)+"/edge-manifest", nil, "")
		if err != nil {
			return report, err
		}
		report, err = edgeManifestCandidateReadiness(raw, opts.Mode, opts.MinCandidates)
		if err != nil {
			return report, err
		}
		report.Attempts = attempts
		if report.Status == "ok" {
			return report, nil
		}
		if time.Now().After(deadline) {
			if report.Status == "" {
				report.Status = "timeout"
			}
			return report, fmt.Errorf("edge manifest %s candidates did not become ready before timeout", opts.Mode)
		}
		time.Sleep(2 * time.Second)
	}
}

func edgeManifestCandidateReadiness(raw []byte, mode string, minCandidates int) (edgeManifestCandidateWaitReport, error) {
	if minCandidates <= 0 {
		minCandidates = 2
	}
	mode = strings.TrimSpace(mode)
	var manifest edgeManifestCandidateManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return edgeManifestCandidateWaitReport{}, err
	}
	report := edgeManifestCandidateWaitReport{
		Status:           "ok",
		SiteID:           manifest.SiteID,
		DeploymentID:     manifest.DeploymentID,
		Mode:             mode,
		MinCandidates:    minCandidates,
		LastCheckedAtUTC: time.Now().UTC().Format(time.RFC3339Nano),
		ManifestWarnings: manifest.Warnings,
	}
	paths := make([]string, 0, len(manifest.Routes))
	for pathValue := range manifest.Routes {
		paths = append(paths, pathValue)
	}
	sort.Strings(paths)
	for _, pathValue := range paths {
		route := manifest.Routes[pathValue]
		if !edgeManifestRouteNeedsCandidates(route) {
			continue
		}
		status := edgeManifestCandidateRouteStatus{
			Path:               pathValue,
			Type:               route.Type,
			Delivery:           route.Delivery,
			File:               route.File,
			Status:             route.Status,
			Candidates:         len(route.Candidates),
			ReadyCandidates:    edgeManifestReadyCandidateCount(route.Candidates),
			RequiredCandidates: minCandidates,
		}
		switch mode {
		case "routing_policy":
			status.OK = route.Type == "smart" && status.ReadyCandidates >= minCandidates
			if !status.OK {
				status.Message = fmt.Sprintf("expected smart route with at least %d ready candidates", minCandidates)
			}
		case "resource_failover":
			status.OK = route.Type == "failover" && status.ReadyCandidates >= minCandidates
			if !status.OK {
				status.Message = fmt.Sprintf("expected failover route with at least %d ready candidates", minCandidates)
			}
		default:
			status.OK = status.ReadyCandidates >= minCandidates
			if !status.OK {
				status.Message = fmt.Sprintf("expected at least %d ready candidates", minCandidates)
			}
		}
		report.RequiredRoutes++
		if status.OK {
			report.ReadyRoutes++
		} else {
			report.Status = "pending"
		}
		report.Routes = append(report.Routes, status)
	}
	if report.RequiredRoutes == 0 {
		report.Warnings = append(report.Warnings, "edge manifest has no non-entry resource routes that require candidates")
	}
	return report, nil
}

func edgeManifestRouteNeedsCandidates(route edgeManifestCandidateRoute) bool {
	if route.Type == "smart" || route.Type == "failover" {
		return true
	}
	if strings.EqualFold(route.Delivery, "origin") {
		return false
	}
	if route.Delivery == "" {
		return false
	}
	return !strings.EqualFold(route.File, "index.html")
}

func edgeManifestReadyCandidateCount(candidates []edgeManifestCandidateEntry) int {
	count := 0
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.URL) == "" {
			continue
		}
		if status := strings.TrimSpace(candidate.Status); status != "" && !strings.EqualFold(status, "ready") {
			continue
		}
		count++
	}
	return count
}

type hybridEdgePublishOptions struct {
	Site                string
	WorkerName          string
	Dir                 string
	Domains             []string
	CompatibilityDate   string
	Message             string
	CachePolicy         string
	NotFoundHandling    string
	KVNamespaceID       string
	ManifestMode        string
	DefaultCacheControl string
	EntryOriginFallback bool
	OriginBaseURL       string
	EnvFile             string
	Wrangler            string
	WranglerPrefix      string
}

func runHybridEdgePublish(opts hybridEdgePublishOptions) (cloudflareStaticPublishResponse, error) {
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
	notFoundHandling, err := normalizeCloudflareStaticNotFoundHandling(opts.NotFoundHandling)
	if err != nil {
		return cloudflareStaticPublishResponse{}, err
	}
	workerName := strings.TrimSpace(opts.WorkerName)
	if workerName == "" {
		if strings.TrimSpace(opts.Site) == "" {
			return cloudflareStaticPublishResponse{}, errors.New("-site or -edge-name is required")
		}
		workerName = "supercdn-" + cleanWorkerName(opts.Site) + "-edge"
	}
	workerMain, err := filepath.Abs(filepath.Join("worker", "src", "index.ts"))
	if err != nil {
		return cloudflareStaticPublishResponse{}, err
	}
	wranglerConfig, configCleanup, err := writeHybridEdgeWranglerConfig(hybridEdgeWranglerConfigOptions{
		WorkerName:          workerName,
		WorkerMain:          workerMain,
		AssetsDir:           preparedDir,
		CompatibilityDate:   opts.CompatibilityDate,
		NotFoundHandling:    notFoundHandling,
		KVNamespaceID:       opts.KVNamespaceID,
		ManifestMode:        opts.ManifestMode,
		DefaultCacheControl: opts.DefaultCacheControl,
		EntryOriginFallback: opts.EntryOriginFallback,
		OriginBaseURL:       opts.OriginBaseURL,
	})
	if err != nil {
		return cloudflareStaticPublishResponse{}, err
	}
	defer configCleanup()
	wrangler := firstNonEmpty(strings.TrimSpace(opts.Wrangler), "npx")
	cmdArgs := wranglerDeployArgs(wrangler, opts.WranglerPrefix, workerName, preparedDir, opts.Domains, opts.CompatibilityDate, opts.Message, false, wranglerConfig)
	resp := cloudflareStaticPublishResponse{
		Status:            "planned",
		DryRun:            false,
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
	resp.Status = "ok"
	return resp, nil
}

type hybridEdgeWranglerConfigOptions struct {
	WorkerName          string
	WorkerMain          string
	AssetsDir           string
	CompatibilityDate   string
	NotFoundHandling    string
	KVNamespaceID       string
	ManifestMode        string
	DefaultCacheControl string
	EntryOriginFallback bool
	OriginBaseURL       string
}

func writeHybridEdgeWranglerConfig(opts hybridEdgeWranglerConfigOptions) (string, func(), error) {
	if strings.TrimSpace(opts.KVNamespaceID) == "" {
		return "", nil, errors.New("kv namespace id is required")
	}
	tmp, err := os.MkdirTemp("", "supercdn-hybrid-edge-wrangler-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	configPath := filepath.Join(tmp, "wrangler.toml")
	var b strings.Builder
	b.WriteString("name = " + tomlString(opts.WorkerName) + "\n")
	b.WriteString("main = " + tomlPathString(opts.WorkerMain) + "\n")
	b.WriteString("compatibility_date = " + tomlString(strings.TrimSpace(opts.CompatibilityDate)) + "\n\n")
	b.WriteString("[vars]\n")
	b.WriteString("ORIGIN_BASE_URL = " + tomlString(firstNonEmpty(opts.OriginBaseURL, "https://origin.example.com")) + "\n")
	b.WriteString("EDGE_DEFAULT_CACHE_CONTROL = " + tomlString(firstNonEmpty(opts.DefaultCacheControl, "public, max-age=300")) + "\n")
	b.WriteString("EDGE_ENTRY_ORIGIN_FALLBACK = " + tomlString(strconv.FormatBool(opts.EntryOriginFallback)) + "\n")
	b.WriteString("EDGE_MANIFEST_DRY_RUN = \"true\"\n")
	b.WriteString("EDGE_MANIFEST_KEY_PREFIX = \"sites/\"\n")
	b.WriteString("EDGE_MANIFEST_MODE = " + tomlString(firstNonEmpty(opts.ManifestMode, "route")) + "\n")
	b.WriteString("EDGE_ORIGIN_FALLBACK = \"false\"\n")
	b.WriteString("EDGE_STATIC_ASSETS = \"true\"\n\n")
	b.WriteString("[assets]\n")
	b.WriteString("directory = " + tomlPathString(opts.AssetsDir) + "\n")
	b.WriteString("binding = \"ASSETS\"\n")
	b.WriteString("run_worker_first = true\n")
	if strings.TrimSpace(opts.NotFoundHandling) != "" {
		b.WriteString("not_found_handling = " + tomlString(strings.TrimSpace(opts.NotFoundHandling)) + "\n")
	}
	b.WriteString("\n[[kv_namespaces]]\n")
	b.WriteString("binding = \"EDGE_MANIFEST\"\n")
	b.WriteString("id = " + tomlString(strings.TrimSpace(opts.KVNamespaceID)) + "\n")
	if err := os.WriteFile(configPath, []byte(b.String()), 0600); err != nil {
		cleanup()
		return "", nil, err
	}
	return configPath, cleanup, nil
}

type cloudflareStaticVerifyOptions struct {
	Mode                        string
	Domains                     []string
	Timeout                     time.Duration
	Interval                    time.Duration
	SPAPath                     string
	Resolver                    string
	NotFoundHandling            string
	RequireDirectAssets         bool
	RequireEdgeStaticHTML       bool
	RequireEdgeManifestAssets   bool
	RequireGeneratedCachePolicy bool
	RequireImmutableAssetCache  bool
}

type cloudflareStaticVerifyReport struct {
	Status   string             `json:"status"`
	Mode     string             `json:"mode"`
	Domains  []string           `json:"domains,omitempty"`
	Attempts int                `json:"attempts,omitempty"`
	Reports  []siteprobe.Report `json:"reports,omitempty"`
	Warnings []string           `json:"warnings,omitempty"`
	Errors   []string           `json:"errors,omitempty"`
}

func verifyCloudflareStaticPublish(ctx context.Context, opts cloudflareStaticVerifyOptions) (cloudflareStaticVerifyReport, error) {
	mode, err := normalizeCloudflareStaticVerifyMode(opts.Mode)
	if err != nil {
		return cloudflareStaticVerifyReport{}, err
	}
	domains := cleanDomains(opts.Domains)
	report := cloudflareStaticVerifyReport{Status: "planned", Mode: mode, Domains: domains}
	if mode == cloudflareStaticVerifyNone {
		report.Status = "skipped"
		return report, nil
	}
	if len(domains) == 0 {
		report.Status = "skipped"
		report.Warnings = append(report.Warnings, "no custom domains to verify")
		return report, nil
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	interval := opts.Interval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	spaPath := strings.TrimSpace(opts.SPAPath)
	if spaPath == "" && opts.NotFoundHandling == cloudflareStaticNotFoundSPA {
		spaPath = "/__supercdn_spa_probe"
	}
	httpClient, err := httpClientWithDNSResolver(opts.Resolver)
	if err != nil {
		return cloudflareStaticVerifyReport{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var last []siteprobe.Report
	for {
		report.Attempts++
		last = probeCloudflareStaticDomains(ctx, domains, siteprobe.Options{
			SPAPath:                    spaPath,
			MaxAssets:                  20,
			Timeout:                    30 * time.Second,
			Client:                     httpClient,
			RequireDirectAssets:        opts.RequireDirectAssets,
			RequireEdgeStaticHTML:      opts.RequireEdgeStaticHTML,
			RequireEdgeManifestAssets:  opts.RequireEdgeManifestAssets,
			RequireHTMLRevalidate:      opts.RequireGeneratedCachePolicy,
			RequireImmutableAssetCache: opts.RequireImmutableAssetCache,
		})
		if cloudflareStaticReportsOK(last) {
			report.Status = "ok"
			report.Reports = last
			return report, nil
		}
		if mode == cloudflareStaticVerifyWarn {
			report.Status = "warning"
			report.Reports = last
			report.Warnings = append(report.Warnings, "Cloudflare Static readiness probe failed; deployment will still be recorded")
			_, _ = fmt.Fprintln(os.Stderr, "warning: Cloudflare Static readiness probe failed; continuing because -static-verify=warn")
			return report, nil
		}
		select {
		case <-ctx.Done():
			report.Status = "failed"
			report.Reports = last
			report.Errors = append(report.Errors, "Cloudflare Static readiness probe did not pass before timeout")
			return report, errors.New("cloudflare static readiness probe failed")
		case <-time.After(interval):
		}
	}
}

func probeCloudflareStaticDomains(ctx context.Context, domains []string, opts siteprobe.Options) []siteprobe.Report {
	reports := make([]siteprobe.Report, 0, len(domains))
	for _, domain := range domains {
		probeOpts := opts
		probeOpts.URL = "https://" + domain + "/"
		report, err := siteprobe.Run(ctx, probeOpts)
		if err != nil {
			report = siteprobe.Report{
				OK:      false,
				Status:  "failed",
				URL:     probeOpts.URL,
				Errors:  []string{err.Error()},
				Summary: map[string]int{},
			}
		}
		reports = append(reports, redactSignedProbeReport(report))
	}
	return reports
}

func cloudflareStaticReportsOK(reports []siteprobe.Report) bool {
	if len(reports) == 0 {
		return false
	}
	for _, report := range reports {
		if !report.OK {
			return false
		}
	}
	return true
}

func normalizeCloudflareStaticVerifyMode(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", cloudflareStaticVerifyWait:
		return cloudflareStaticVerifyWait, nil
	case cloudflareStaticVerifyWarn:
		return cloudflareStaticVerifyWarn, nil
	case cloudflareStaticVerifyNone:
		return cloudflareStaticVerifyNone, nil
	default:
		return "", fmt.Errorf("static-verify must be wait, warn, or none")
	}
}

func cleanDomains(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		host := strings.ToLower(strings.TrimSpace(value))
		host = strings.TrimPrefix(host, "https://")
		host = strings.TrimPrefix(host, "http://")
		host = strings.Trim(host, "/")
		if host == "" || seen[host] {
			continue
		}
		seen[host] = true
		out = append(out, host)
	}
	return out
}

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

func listDeployments(c client, args []string) error {
	fs := flag.NewFlagSet("list-deployments", flag.ExitOnError)
	site := fs.String("site", "", "site id")
	limit := fs.Int("limit", 100, "max deployments")
	_ = fs.Parse(args)
	if *site == "" {
		return errors.New("-site is required")
	}
	return c.do(http.MethodGet, "/api/v1/sites/"+url.PathEscape(*site)+"/deployments?limit="+url.QueryEscape(fmt.Sprint(*limit)), nil, "")
}

func getDeployment(c client, args []string) error {
	fs := flag.NewFlagSet("deployment", flag.ExitOnError)
	site := fs.String("site", "", "site id")
	deployment := fs.String("deployment", "", "deployment id")
	_ = fs.Parse(args)
	if *site == "" || *deployment == "" {
		return errors.New("-site and -deployment are required")
	}
	return c.do(http.MethodGet, "/api/v1/sites/"+url.PathEscape(*site)+"/deployments/"+url.PathEscape(*deployment), nil, "")
}

func exportEdgeManifest(c client, args []string) error {
	fs := flag.NewFlagSet("export-edge-manifest", flag.ExitOnError)
	site := fs.String("site", "", "site id")
	deployment := fs.String("deployment", "", "deployment id")
	out := fs.String("out", "", "optional output file; stdout when empty")
	_ = fs.Parse(args)
	if *site == "" || *deployment == "" {
		return errors.New("-site and -deployment are required")
	}
	raw, err := c.doRaw(http.MethodGet, "/api/v1/sites/"+url.PathEscape(*site)+"/deployments/"+url.PathEscape(*deployment)+"/edge-manifest", nil, "")
	if err != nil {
		return err
	}
	if *out == "" {
		return printJSON(raw)
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, raw, "", "  ") == nil {
		raw = append(pretty.Bytes(), '\n')
	}
	return os.WriteFile(*out, raw, 0o644)
}

func publishEdgeManifest(c client, args []string) error {
	fs := flag.NewFlagSet("publish-edge-manifest", flag.ExitOnError)
	site := fs.String("site", "", "site id")
	deployment := fs.String("deployment", "", "deployment id")
	domains := fs.String("domains", "", "comma-separated bound domains to publish keys for; empty means all site domains")
	cfAccount := fs.String("cloudflare-account", "", "Cloudflare account name; defaults by domain match")
	cfLibrary := fs.String("cloudflare-library", "", "Cloudflare library name")
	kvNamespaceID := fs.String("kv-namespace-id", "", "Cloudflare Workers KV namespace id")
	kvNamespace := fs.String("kv-namespace", "", "Cloudflare Workers KV namespace title; resolved to id by account")
	keyPrefix := fs.String("key-prefix", "", "KV key prefix; defaults to sites/")
	activeKey := fs.Bool("active-key", false, "publish sites/{host}/active/edge-manifest; defaults to true only for active deployments")
	deploymentKey := fs.Bool("deployment-key", true, "publish sites/{host}/deployments/{deployment}/edge-manifest")
	dryRun := fs.Bool("dry-run", true, "plan KV writes without modifying Cloudflare; pass -dry-run=false to publish")
	_ = fs.Parse(args)
	if *site == "" || *deployment == "" {
		return errors.New("-site and -deployment are required")
	}
	req := map[string]any{
		"domains":            splitCSV(*domains),
		"cloudflare_account": *cfAccount,
		"cloudflare_library": *cfLibrary,
		"kv_namespace_id":    *kvNamespaceID,
		"kv_namespace":       *kvNamespace,
		"key_prefix":         *keyPrefix,
		"deployment_key":     *deploymentKey,
		"dry_run":            *dryRun,
	}
	if flagWasSet(fs, "active-key") {
		req["active_key"] = *activeKey
	}
	return c.doJSON(http.MethodPost, "/api/v1/sites/"+url.PathEscape(*site)+"/deployments/"+url.PathEscape(*deployment)+"/edge-manifest/publish", req)
}

type edgeManifestRefreshResponse struct {
	Status        string                      `json:"status"`
	SiteID        string                      `json:"site_id"`
	DeploymentID  string                      `json:"deployment_id"`
	URL           string                      `json:"url,omitempty"`
	Deployment    probeDeployment             `json:"deployment"`
	EdgeManifest  edgeManifestPublishResponse `json:"edge_manifest"`
	Probe         *siteprobe.Report           `json:"probe,omitempty"`
	ProbeRedacted bool                        `json:"probe_redacted,omitempty"`
}

func refreshEdgeManifest(c client, args []string) error {
	fs := flag.NewFlagSet("refresh-edge-manifest", flag.ExitOnError)
	site := fs.String("site", "", "site id")
	deployment := fs.String("deployment", "", "deployment id; defaults to active production deployment")
	domains := fs.String("domains", "", "comma-separated bound domains to refresh; empty means all site domains")
	kvNamespaceID := fs.String("kv-namespace-id", "", "Cloudflare Workers KV namespace id")
	kvNamespace := fs.String("kv-namespace", "supercdn-edge-manifest", "Cloudflare Workers KV namespace title")
	deploymentKey := fs.Bool("deployment-key", true, "publish sites/{host}/deployments/{deployment}/edge-manifest")
	dryRun := fs.Bool("dry-run", false, "plan KV writes without modifying Cloudflare")
	probe := fs.Bool("probe", true, "run a site probe after refreshing the manifest")
	probeURL := fs.String("probe-url", "", "absolute public site URL to probe; defaults to the deployment production URL")
	spaPath := fs.String("spa-path", "", "optional SPA route path to verify as HTML")
	origin := fs.String("origin", "", "Origin header for redirected asset checks; defaults to the probe URL origin")
	resolver := fs.String("resolver", "", "DNS resolver for HTTP probes, for example 1.1.1.1:53")
	maxAssets := fs.Int("max-assets", 20, "maximum JS/CSS assets to probe from index HTML")
	timeout := fs.Duration("timeout", 30*time.Second, "overall probe timeout")
	hybridChecks := fs.Bool("hybrid-checks", true, "require Cloudflare Static HTML and edge-manifest asset first hops during probe")
	redactProbeURLs := fs.Bool("redact-probe-urls", true, "redact query values for signed URLs from the embedded probe report")
	_ = fs.Parse(args)
	if *site == "" {
		return errors.New("-site is required")
	}
	dep, err := loadProbeDeployment(c, *site, *deployment)
	if err != nil {
		return err
	}
	if dep.ID == "" {
		return errors.New("deployment response did not include id")
	}
	edgeManifest, err := c.publishEdgeManifestForDeployment(edgeManifestPublishOptions{
		Site:          *site,
		Deployment:    dep.ID,
		Domains:       cleanDomains(splitCSV(*domains)),
		KVNamespaceID: *kvNamespaceID,
		KVNamespace:   *kvNamespace,
		ActiveKey:     dep.Active,
		DeploymentKey: *deploymentKey,
		DryRun:        *dryRun,
	})
	if err != nil {
		return err
	}
	status := edgeManifest.Status
	if status == "" {
		status = "ok"
	}
	resp := edgeManifestRefreshResponse{
		Status:       status,
		SiteID:       *site,
		DeploymentID: dep.ID,
		Deployment:   dep,
		EdgeManifest: edgeManifest,
	}
	if *probe {
		targetURL := strings.TrimSpace(*probeURL)
		if targetURL == "" {
			targetURL, err = deploymentProbeURL(dep, false)
			if err != nil {
				return err
			}
		}
		resp.URL = targetURL
		report, err := runSiteProbe(*resolver, siteprobe.Options{
			URL:                       targetURL,
			Origin:                    *origin,
			SPAPath:                   *spaPath,
			MaxAssets:                 *maxAssets,
			Timeout:                   *timeout,
			RequireEdgeStaticHTML:     *hybridChecks,
			RequireEdgeManifestAssets: *hybridChecks,
		})
		if err != nil {
			return err
		}
		if *redactProbeURLs {
			report = redactSignedProbeReport(report)
			resp.URL = redactSignedURL(resp.URL)
			resp.ProbeRedacted = true
		}
		resp.Probe = &report
		if !report.OK {
			raw, _ := json.Marshal(resp)
			_ = printJSON(raw)
			return errors.New("site probe failed")
		}
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	return printJSON(raw)
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
	if strings.TrimSpace(raw) == "" {
		return raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.RawQuery == "" {
		return raw
	}
	query := parsed.Query()
	hasSignature := false
	for key := range query {
		if signedQueryParam(key) {
			hasSignature = true
			break
		}
	}
	if !hasSignature {
		return raw
	}
	for key, values := range query {
		for i := range values {
			values[i] = "<redacted>"
		}
		query[key] = values
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
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

type cloudflareStaticPublishResponse struct {
	Status            string   `json:"status"`
	DryRun            bool     `json:"dry_run"`
	Worker            string   `json:"worker"`
	AssetsDir         string   `json:"assets_dir"`
	SourceDir         string   `json:"source_dir,omitempty"`
	Domains           []string `json:"domains,omitempty"`
	CompatibilityDate string   `json:"compatibility_date"`
	CachePolicy       string   `json:"cache_policy,omitempty"`
	NotFoundHandling  string   `json:"not_found_handling,omitempty"`
	WranglerConfig    string   `json:"wrangler_config,omitempty"`
	HeadersFile       string   `json:"headers_file,omitempty"`
	HeadersSource     string   `json:"headers_source,omitempty"`
	HeadersGenerated  bool     `json:"headers_generated,omitempty"`
	Command           []string `json:"command"`
	Output            string   `json:"output,omitempty"`
	ExitCode          int      `json:"exit_code,omitempty"`
}

type cloudflareStaticPublishOptions struct {
	Site              string
	WorkerName        string
	Dir               string
	Domains           []string
	CompatibilityDate string
	EnvFile           string
	Wrangler          string
	WranglerPrefix    string
	Message           string
	CachePolicy       string
	NotFoundHandling  string
	DryRun            bool
}

const (
	cloudflareStaticCachePolicyAuto  = "auto"
	cloudflareStaticCachePolicyForce = "force"
	cloudflareStaticCachePolicyNone  = "none"

	cloudflareStaticNotFoundNone = "none"
	cloudflareStaticNotFound404  = "404-page"
	cloudflareStaticNotFoundSPA  = "single-page-application"

	cloudflareStaticHTMLCacheControl      = "public, max-age=0, must-revalidate"
	cloudflareStaticShortCacheControl     = "public, max-age=300, must-revalidate"
	cloudflareStaticImmutableCacheControl = "public, max-age=31536000, immutable"

	cloudflareStaticVerifyWait = "wait"
	cloudflareStaticVerifyWarn = "warn"
	cloudflareStaticVerifyNone = "none"
)

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

func promoteDeployment(c client, args []string) error {
	fs := flag.NewFlagSet("promote-deployment", flag.ExitOnError)
	site := fs.String("site", "", "site id")
	deployment := fs.String("deployment", "", "deployment id")
	_ = fs.Parse(args)
	if *site == "" || *deployment == "" {
		return errors.New("-site and -deployment are required")
	}
	return c.doJSON(http.MethodPost, "/api/v1/sites/"+url.PathEscape(*site)+"/deployments/"+url.PathEscape(*deployment)+"/promote", map[string]any{})
}

func deleteDeployment(c client, args []string) error {
	fs := flag.NewFlagSet("delete-deployment", flag.ExitOnError)
	site := fs.String("site", "", "site id")
	deployment := fs.String("deployment", "", "deployment id")
	deleteObjects := fs.Bool("delete-objects", false, "delete tracked deployment objects before deleting deployment metadata")
	deleteRemote := fs.Bool("delete-remote", true, "delete remote object replicas when -delete-objects is set")
	_ = fs.Parse(args)
	if *site == "" || *deployment == "" {
		return errors.New("-site and -deployment are required")
	}
	q := url.Values{}
	q.Set("delete_objects", fmt.Sprint(*deleteObjects))
	q.Set("delete_remote", fmt.Sprint(*deleteRemote))
	return c.do(http.MethodDelete, "/api/v1/sites/"+url.PathEscape(*site)+"/deployments/"+url.PathEscape(*deployment)+"?"+q.Encode(), nil, "")
}

func gcSite(c client, args []string) error {
	fs := flag.NewFlagSet("gc-site", flag.ExitOnError)
	site := fs.String("site", "", "site id")
	_ = fs.Parse(args)
	if *site == "" {
		return errors.New("-site is required")
	}
	return c.doJSON(http.MethodPost, "/api/v1/sites/"+url.PathEscape(*site)+"/gc", map[string]any{})
}

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

func createBucket(c client, args []string) error {
	return createBucketWithDefaults(c, args, "create-bucket", "china_all", "")
}

func createCDNBucket(c client, args []string) error {
	return createBucketWithDefaults(c, args, "create-cdn-bucket", "overseas_r2", "public, max-age=31536000, immutable")
}

func createMobileCDNBucket(c client, args []string) error {
	return createBucketWithDefaults(c, args, "create-mobile-cdn-bucket", "china_mobile", "public, max-age=86400")
}

func createIPFSBucket(c client, args []string) error {
	return createBucketWithDefaults(c, args, "create-ipfs-bucket", "ipfs_archive", "public, max-age=31536000, immutable")
}

func createDomesticCDNBucket(c client, args []string) error {
	fs := flag.NewFlagSet("create-domestic-cdn-bucket", flag.ExitOnError)
	slug := fs.String("slug", "", "bucket slug")
	name := fs.String("name", "", "bucket display name")
	description := fs.String("description", "", "bucket description")
	line := fs.String("line", "mobile", "domestic line: mobile, telecom, unicom, or all")
	profile := fs.String("profile", "", "explicit route profile; overrides -line")
	routingPolicy := fs.String("routing-policy", "", "routing policy name; requires matching multi-source route profile")
	types := fs.String("types", "", "comma-separated asset types: image,video,document,archive,other")
	maxCapacity := fs.Int64("max-capacity", 0, "bucket capacity limit in bytes; 0 means unlimited")
	maxFileSize := fs.Int64("max-file-size", 0, "single file limit in bytes; 0 means unlimited")
	cacheControl := fs.String("cache-control", "public, max-age=86400", "default Cache-Control value")
	_ = fs.Parse(args)
	if *slug == "" {
		return errors.New("-slug is required")
	}
	routeProfile := strings.TrimSpace(*profile)
	if routeProfile == "" {
		var err error
		routeProfile, err = domesticLineProfile(*line)
		if err != nil {
			return err
		}
	}
	req := map[string]any{
		"slug":                  *slug,
		"name":                  *name,
		"description":           *description,
		"route_profile":         routeProfile,
		"routing_policy":        strings.TrimSpace(*routingPolicy),
		"allowed_types":         splitCSV(*types),
		"max_capacity_bytes":    *maxCapacity,
		"max_file_size_bytes":   *maxFileSize,
		"default_cache_control": *cacheControl,
	}
	return c.doJSON(http.MethodPost, "/api/v1/asset-buckets", req)
}

func domesticLineProfile(line string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "mobile", "cmcc", "china_mobile":
		return "china_mobile", nil
	case "telecom", "ctcc", "china_telecom":
		return "china_telecom", nil
	case "unicom", "cucc", "china_unicom":
		return "china_unicom", nil
	case "all", "china_all":
		return "china_all", nil
	default:
		return "", fmt.Errorf("line must be mobile, telecom, unicom, or all")
	}
}

func createBucketWithDefaults(c client, args []string, commandName, defaultProfile, defaultCacheControl string) error {
	fs := flag.NewFlagSet(commandName, flag.ExitOnError)
	slug := fs.String("slug", "", "bucket slug")
	name := fs.String("name", "", "bucket display name")
	description := fs.String("description", "", "bucket description")
	profile := fs.String("profile", defaultProfile, "default route profile")
	routingPolicy := fs.String("routing-policy", "", "routing policy name; requires matching multi-source route profile")
	types := fs.String("types", "", "comma-separated asset types: image,video,document,archive,other")
	maxCapacity := fs.Int64("max-capacity", 0, "bucket capacity limit in bytes; 0 means unlimited")
	maxFileSize := fs.Int64("max-file-size", 0, "single file limit in bytes; 0 means unlimited")
	cacheControl := fs.String("cache-control", defaultCacheControl, "default Cache-Control value")
	_ = fs.Parse(args)
	if *slug == "" {
		return errors.New("-slug is required")
	}
	req := map[string]any{
		"slug":                  *slug,
		"name":                  *name,
		"description":           *description,
		"route_profile":         *profile,
		"routing_policy":        strings.TrimSpace(*routingPolicy),
		"allowed_types":         splitCSV(*types),
		"max_capacity_bytes":    *maxCapacity,
		"max_file_size_bytes":   *maxFileSize,
		"default_cache_control": *cacheControl,
	}
	return c.doJSON(http.MethodPost, "/api/v1/asset-buckets", req)
}

func initBucket(c client, args []string) error {
	fs := flag.NewFlagSet("init-bucket", flag.ExitOnError)
	bucket := fs.String("bucket", "", "bucket slug")
	dryRun := fs.Bool("dry-run", false, "return the initialization plan without creating directories")
	_ = fs.Parse(args)
	if *bucket == "" {
		return errors.New("-bucket is required")
	}
	return c.doJSON(http.MethodPost, "/api/v1/asset-buckets/"+url.PathEscape(*bucket)+"/init", map[string]any{"dry_run": *dryRun})
}

func uploadBucket(c client, args []string) error {
	fs := flag.NewFlagSet("upload-bucket", flag.ExitOnError)
	bucket := fs.String("bucket", "", "bucket slug")
	file := fs.String("file", "", "file to upload")
	dst := fs.String("path", "", "logical path inside the bucket")
	assetType := fs.String("asset-type", "", "optional asset type override")
	cacheControl := fs.String("cache-control", "", "Cache-Control value override")
	warmup := fs.Bool("warmup", false, "warm the uploaded public URL after upload")
	warmupMethod := fs.String("warmup-method", http.MethodHead, "warmup method: HEAD or GET")
	warmupBaseURL := fs.String("warmup-base-url", "", "public base URL override for warmup")
	_ = fs.Parse(args)
	if *bucket == "" || *file == "" || *dst == "" {
		return errors.New("-bucket, -file and -path are required")
	}
	fields := map[string]string{
		"path":          *dst,
		"asset_type":    *assetType,
		"cache_control": *cacheControl,
	}
	apiPath := "/api/v1/asset-buckets/" + url.PathEscape(*bucket) + "/objects"
	if !*warmup {
		return c.uploadFile(apiPath, "file", *file, fields)
	}
	uploadRaw, err := c.uploadFileRaw(apiPath, "file", *file, fields)
	if err != nil {
		return err
	}
	warmupRaw, err := c.doJSONRaw(http.MethodPost, "/api/v1/asset-buckets/"+url.PathEscape(*bucket)+"/warmup", map[string]any{
		"path":     *dst,
		"method":   *warmupMethod,
		"base_url": *warmupBaseURL,
	})
	if err != nil {
		return fmt.Errorf("upload succeeded but warmup failed: %w", err)
	}
	out, err := json.Marshal(struct {
		Upload json.RawMessage `json:"upload"`
		Warmup json.RawMessage `json:"warmup"`
	}{
		Upload: json.RawMessage(uploadRaw),
		Warmup: json.RawMessage(warmupRaw),
	})
	if err != nil {
		return err
	}
	return printJSON(out)
}

func listBucket(c client, args []string) error {
	fs := flag.NewFlagSet("list-bucket", flag.ExitOnError)
	bucket := fs.String("bucket", "", "bucket slug")
	prefix := fs.String("prefix", "", "logical path prefix")
	limit := fs.Int("limit", 100, "max objects to return")
	_ = fs.Parse(args)
	if *bucket == "" {
		return errors.New("-bucket is required")
	}
	path := "/api/v1/asset-buckets/" + url.PathEscape(*bucket) + "/objects?limit=" + url.QueryEscape(fmt.Sprint(*limit))
	if *prefix != "" {
		path += "&prefix=" + url.QueryEscape(*prefix)
	}
	return c.do(http.MethodGet, path, nil, "")
}

func purgeBucket(c client, args []string) error {
	req, bucket, err := parseBucketCacheFlags("purge-bucket", args)
	if err != nil {
		return err
	}
	return c.doJSON(http.MethodPost, "/api/v1/asset-buckets/"+url.PathEscape(bucket)+"/purge", req)
}

func warmupBucket(c client, args []string) error {
	req, bucket, err := parseBucketCacheFlags("warmup-bucket", args)
	if err != nil {
		return err
	}
	return c.doJSON(http.MethodPost, "/api/v1/asset-buckets/"+url.PathEscape(bucket)+"/warmup", req)
}

func parseBucketCacheFlags(name string, args []string) (map[string]any, string, error) {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	bucket := fs.String("bucket", "", "bucket slug")
	pathValue := fs.String("path", "", "single logical object path")
	paths := fs.String("paths", "", "comma-separated logical object paths")
	prefix := fs.String("prefix", "", "logical path prefix")
	all := fs.Bool("all", false, "select all tracked objects in the bucket")
	limit := fs.Int("limit", 0, "max objects for prefix selection; 0 lets the server choose")
	baseURL := fs.String("base-url", "", "public base URL override for generated /a/{bucket}/... URLs")
	cfAccount := fs.String("cloudflare-account", "", "Cloudflare account name")
	cfLibrary := fs.String("cloudflare-library", "", "Cloudflare library name")
	dryRun := fs.Bool("dry-run", false, "generate URLs without purging or requesting them")
	method := fs.String("method", "", "warmup method: HEAD or GET")
	_ = fs.Parse(args)
	if *bucket == "" {
		return nil, "", errors.New("-bucket is required")
	}
	req := map[string]any{
		"path":               *pathValue,
		"paths":              splitCSV(*paths),
		"prefix":             *prefix,
		"all":                *all,
		"limit":              *limit,
		"base_url":           *baseURL,
		"cloudflare_account": *cfAccount,
		"cloudflare_library": *cfLibrary,
		"dry_run":            *dryRun,
	}
	if *method != "" {
		req["method"] = *method
	}
	return req, *bucket, nil
}

func deleteBucketObject(c client, args []string) error {
	fs := flag.NewFlagSet("delete-bucket-object", flag.ExitOnError)
	bucket := fs.String("bucket", "", "bucket slug")
	dst := fs.String("path", "", "logical path inside the bucket")
	deleteRemote := fs.Bool("delete-remote", true, "delete remote replicas before removing local metadata")
	_ = fs.Parse(args)
	if *bucket == "" || *dst == "" {
		return errors.New("-bucket and -path are required")
	}
	path := "/api/v1/asset-buckets/" + url.PathEscape(*bucket) + "/objects?path=" + url.QueryEscape(*dst) + "&delete_remote=" + url.QueryEscape(fmt.Sprint(*deleteRemote))
	return c.do(http.MethodDelete, path, nil, "")
}

func deleteBucket(c client, args []string) error {
	fs := flag.NewFlagSet("delete-bucket", flag.ExitOnError)
	bucket := fs.String("bucket", "", "bucket slug")
	force := fs.Bool("force", false, "delete a non-empty bucket by deleting its tracked objects first")
	deleteObjects := fs.Bool("delete-objects", false, "delete tracked bucket objects before deleting the bucket")
	deleteRemote := fs.Bool("delete-remote", true, "delete remote object replicas before removing local metadata")
	_ = fs.Parse(args)
	if *bucket == "" {
		return errors.New("-bucket is required")
	}
	if *force {
		*deleteObjects = true
	}
	path := "/api/v1/asset-buckets/" + url.PathEscape(*bucket) +
		"?force=" + url.QueryEscape(fmt.Sprint(*force)) +
		"&delete_objects=" + url.QueryEscape(fmt.Sprint(*deleteObjects)) +
		"&delete_remote=" + url.QueryEscape(fmt.Sprint(*deleteRemote))
	return c.do(http.MethodDelete, path, nil, "")
}

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

func (c client) doJSON(method, path string, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	return c.do(method, path, bytes.NewReader(raw), "application/json")
}

func (c client) doJSONQuiet(method, path string, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(method, c.baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("request failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func (c client) doJSONRaw(method, path string, body any) ([]byte, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return c.doRaw(method, path, bytes.NewReader(raw), "application/json")
}

func (c client) uploadFile(path, fieldName, filePath string, fields map[string]string) error {
	raw, err := c.uploadFileRaw(path, fieldName, filePath, fields)
	if err != nil {
		return err
	}
	return printJSON(raw)
}

func (c client) uploadFileRaw(path, fieldName, filePath string, fields map[string]string) ([]byte, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for k, v := range fields {
		if v != "" {
			_ = writer.WriteField(k, v)
		}
	}
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	part, err := writer.CreateFormFile(fieldName, filepath.Base(filePath))
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(part, f); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return c.doRaw(http.MethodPost, path, &body, writer.FormDataContentType())
}

func (c client) do(method, path string, body io.Reader, contentType string) error {
	raw, err := c.doRaw(method, path, body, contentType)
	if err != nil {
		return err
	}
	return printJSON(raw)
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

func (c client) doRaw(method, path string, body io.Reader, contentType string) ([]byte, error) {
	req, err := http.NewRequest(method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("request failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}

func printJSON(raw []byte) error {
	var pretty bytes.Buffer
	if json.Indent(&pretty, raw, "", "  ") == nil {
		_, _ = pretty.WriteTo(os.Stdout)
		fmt.Println()
		return nil
	}
	fmt.Println(string(raw))
	return nil
}

func (c client) waitDeployment(site, deployment string, timeout time.Duration) error {
	raw, err := c.waitDeploymentRaw(site, deployment, timeout)
	if err != nil {
		_ = printJSON(raw)
		return err
	}
	return printJSON(raw)
}

func (c client) waitDeploymentRaw(site, deployment string, timeout time.Duration) ([]byte, error) {
	deadline := time.Now().Add(timeout)
	for {
		raw, err := c.doRaw(http.MethodGet, "/api/v1/sites/"+url.PathEscape(site)+"/deployments/"+url.PathEscape(deployment), nil, "")
		if err != nil {
			return nil, err
		}
		var dep struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(raw, &dep); err != nil {
			return raw, err
		}
		switch dep.Status {
		case "ready", "active":
			return raw, nil
		case "failed":
			return raw, errors.New("deployment failed")
		}
		if time.Now().After(deadline) {
			return raw, errors.New("deployment wait timed out")
		}
		time.Sleep(2 * time.Second)
	}
}

func flagWasSet(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func zipDirectory(dir string) (string, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", dir)
	}
	tmp, err := os.CreateTemp("", "supercdn-site-*.zip")
	if err != nil {
		return "", err
	}
	defer tmp.Close()
	zw := zip.NewWriter(tmp)
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = rel
		header.Method = zip.Deflate
		entry, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(entry, f)
		return err
	})
	if closeErr := zw.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

type directorySummary struct {
	FileCount       int
	TotalSize       int64
	LargestFileSize int64
	SHA256          string
}

func summarizeDirectory(dir string) (directorySummary, error) {
	return summarizeDirectoryFiltered(dir, nil)
}

func summarizeCloudflareStaticDirectory(dir string) (directorySummary, error) {
	return summarizeDirectoryFiltered(dir, func(rel string) bool {
		rel = strings.TrimPrefix(filepath.ToSlash(rel), "/")
		return rel == "_headers" || rel == "_redirects"
	})
}

func summarizeDirectoryFiltered(dir string, skip func(rel string) bool) (directorySummary, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return directorySummary{}, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return directorySummary{}, err
	}
	if !info.IsDir() {
		return directorySummary{}, fmt.Errorf("%s is not a directory", dir)
	}
	var summary directorySummary
	var files []string
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if skip != nil && skip(rel) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		summary.FileCount++
		summary.TotalSize += info.Size()
		if info.Size() > summary.LargestFileSize {
			summary.LargestFileSize = info.Size()
		}
		files = append(files, p)
		return nil
	})
	if err != nil {
		return directorySummary{}, err
	}
	if summary.FileCount == 0 {
		return directorySummary{}, fmt.Errorf("%s contains no files", dir)
	}
	sort.Strings(files)
	h := sha256.New()
	for _, file := range files {
		rel, err := filepath.Rel(root, file)
		if err != nil {
			return directorySummary{}, err
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			return directorySummary{}, err
		}
		_, _ = h.Write([]byte(filepath.ToSlash(rel)))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(raw)
		_, _ = h.Write([]byte{0})
	}
	summary.SHA256 = hex.EncodeToString(h.Sum(nil))
	return summary, nil
}

func splitCSV(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstString(values []string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func mustJSON(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}

func tomlString(value string) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(raw)
}

func tomlPathString(value string) string {
	return tomlString(filepath.ToSlash(value))
}

func usage() {
	fmt.Println(`Usage:
  supercdnctl [global flags] login -invite-token sci_xxx
  supercdnctl [global flags] whoami
  supercdnctl [global flags] invite-user -name alice -role maintainer
  supercdnctl [global flags] list-users
  supercdnctl [global flags] revoke-token -id tok_xxx
  supercdnctl [global flags] create-project -id assets
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
  supercdnctl [global flags] gc-site -site blog
  supercdnctl [global flags] init-libraries -dry-run
  supercdnctl [global flags] init-job -id 1
  supercdnctl [global flags] resource-status -library repo_china_all
  supercdnctl [global flags] routing-policy-status -policy global_smart
  supercdnctl [global flags] health-check -libraries repo_china_all
  supercdnctl [global flags] e2e-probe -profile china_all
  supercdnctl [global flags] create-bucket -slug movie-posters -name 影视海报桶 -profile china_all -types image
  supercdnctl [global flags] create-cdn-bucket -slug movie-posters -name movie-posters -types image
  supercdnctl [global flags] create-domestic-cdn-bucket -slug mobile-posters -line mobile -types image
  supercdnctl [global flags] create-ipfs-bucket -slug durable-assets -types image,archive
  supercdnctl [global flags] init-bucket -bucket movie-posters
  supercdnctl [global flags] upload-bucket -bucket movie-posters -file poster.jpg -path posters/poster.jpg -warmup
  supercdnctl [global flags] list-bucket -bucket movie-posters
  supercdnctl [global flags] purge-bucket -bucket movie-posters -prefix posters/ -dry-run
  supercdnctl [global flags] warmup-bucket -bucket movie-posters -path posters/poster.jpg -dry-run
  supercdnctl [global flags] delete-bucket-object -bucket movie-posters -path posters/poster.jpg
  supercdnctl [global flags] delete-bucket -bucket movie-posters -force
  supercdnctl [global flags] job -id 1
  supercdnctl [global flags] replicas -object-id 1
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
