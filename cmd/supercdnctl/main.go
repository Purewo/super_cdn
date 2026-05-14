package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
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
