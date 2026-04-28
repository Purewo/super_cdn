package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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

func main() {
	serverURL := flag.String("server", firstNonEmpty(os.Getenv("SUPERCDN_URL"), "http://127.0.0.1:8080"), "Super CDN server URL")
	token := flag.String("token", os.Getenv("SUPERCDN_TOKEN"), "admin token")
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	if *token == "" && args[0] != "inspect-site" && args[0] != "probe-site" && args[0] != "set-r2-credentials" {
		fatal(errors.New("token is required, pass -token or SUPERCDN_TOKEN"))
	}
	c := client{baseURL: strings.TrimRight(*serverURL, "/"), token: *token, http: http.DefaultClient}
	var err error
	switch args[0] {
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
	case "health-check":
		err = healthCheck(c, args[1:])
	case "e2e-probe":
		err = e2eProbe(c, args[1:])
	case "create-bucket":
		err = createBucket(c, args[1:])
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
	if strings.EqualFold(*env, "production") && !flagWasSet(fs, "promote") {
		*promote = true
	}
	fields := map[string]string{
		"route_profile": *profile,
		"environment":   *env,
		"promote":       fmt.Sprint(*promote),
		"pinned":        fmt.Sprint(*pinned),
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
	maxAssets := fs.Int("max-assets", 20, "maximum JS/CSS assets to probe from index HTML")
	timeout := fs.Duration("timeout", 30*time.Second, "overall probe timeout")
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
	report, err := siteprobe.Run(context.Background(), siteprobe.Options{
		URL:       targetURL,
		Origin:    *origin,
		SPAPath:   *spaPath,
		MaxAssets: *maxAssets,
		Timeout:   *timeout,
	})
	if err != nil {
		return err
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
	_ = fs.Parse(args)
	if *site == "" || *deployment == "" {
		return errors.New("-site and -deployment are required")
	}
	return c.do(http.MethodDelete, "/api/v1/sites/"+url.PathEscape(*site)+"/deployments/"+url.PathEscape(*deployment), nil, "")
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
	fs := flag.NewFlagSet("create-bucket", flag.ExitOnError)
	slug := fs.String("slug", "", "bucket slug")
	name := fs.String("name", "", "bucket display name")
	description := fs.String("description", "", "bucket description")
	profile := fs.String("profile", "china_all", "default route profile")
	types := fs.String("types", "", "comma-separated asset types: image,video,document,archive,other")
	maxCapacity := fs.Int64("max-capacity", 0, "bucket capacity limit in bytes; 0 means unlimited")
	maxFileSize := fs.Int64("max-file-size", 0, "single file limit in bytes; 0 means unlimited")
	cacheControl := fs.String("cache-control", "", "default Cache-Control value")
	_ = fs.Parse(args)
	if *slug == "" {
		return errors.New("-slug is required")
	}
	req := map[string]any{
		"slug":                  *slug,
		"name":                  *name,
		"description":           *description,
		"route_profile":         *profile,
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
	_ = fs.Parse(args)
	if *bucket == "" || *file == "" || *dst == "" {
		return errors.New("-bucket, -file and -path are required")
	}
	fields := map[string]string{
		"path":          *dst,
		"asset_type":    *assetType,
		"cache_control": *cacheControl,
	}
	return c.uploadFile("/api/v1/asset-buckets/"+url.PathEscape(*bucket)+"/objects", "file", *file, fields)
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
	deadline := time.Now().Add(timeout)
	for {
		raw, err := c.doRaw(http.MethodGet, "/api/v1/sites/"+url.PathEscape(site)+"/deployments/"+url.PathEscape(deployment), nil, "")
		if err != nil {
			return err
		}
		var dep struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(raw, &dep); err != nil {
			return err
		}
		switch dep.Status {
		case "ready", "active":
			return printJSON(raw)
		case "failed":
			_ = printJSON(raw)
			return errors.New("deployment failed")
		}
		if time.Now().After(deadline) {
			_ = printJSON(raw)
			return errors.New("deployment wait timed out")
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
}

func summarizeDirectory(dir string) (directorySummary, error) {
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
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
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
		return nil
	})
	if err != nil {
		return directorySummary{}, err
	}
	if summary.FileCount == 0 {
		return directorySummary{}, fmt.Errorf("%s contains no files", dir)
	}
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

func usage() {
	fmt.Println(`Usage:
  supercdnctl [global flags] create-project -id assets
  supercdnctl [global flags] upload -file ./logo.png -project assets -path /img/logo.png -profile overseas
  supercdnctl [global flags] create-site -site blog -name "AI学习星图" -profile china_all -domains example.com,www.example.com
  supercdnctl [global flags] bind-domain -site blog -domain-id blog
  supercdnctl [global flags] domain-status -domain blog.sites.qwk.ccwu.cc
  supercdnctl [global flags] cloudflare-status
  supercdnctl [global flags] sync-site-dns -site blog -dry-run
  supercdnctl [global flags] sync-worker-routes -site blog -dry-run
  supercdnctl [global flags] sync-cloudflare-r2 -cloudflare-account cf_business_main -dry-run
  supercdnctl [global flags] provision-cloudflare-r2 -cloudflare-library overseas_accel -dry-run
  supercdnctl [global flags] create-r2-credentials -cloudflare-account cf_business_main -write-config .\configs\config.local.json -dry-run=false
  supercdnctl set-r2-credentials -config .\configs\config.local.json -cloudflare-account cf_business_main -access-key-id <id> -secret-access-key <secret>
  supercdnctl [global flags] deploy-site -site blog -dir ./dist -profile china_all
  supercdnctl [global flags] deploy-site -site blog -bundle ./dist.zip -env preview
  supercdnctl inspect-site -dir ./dist
  supercdnctl [global flags] probe-site -site blog -spa-path /movie/123
  supercdnctl probe-site -url https://blog.example.com/ -max-assets 20
  supercdnctl [global flags] list-deployments -site blog
  supercdnctl [global flags] deployment -site blog -deployment dpl-abc
  supercdnctl [global flags] promote-deployment -site blog -deployment dpl-abc
  supercdnctl [global flags] delete-deployment -site blog -deployment dpl-abc
  supercdnctl [global flags] gc-site -site blog
  supercdnctl [global flags] init-libraries -dry-run
  supercdnctl [global flags] init-job -id 1
  supercdnctl [global flags] resource-status -library repo_china_all
  supercdnctl [global flags] health-check -libraries repo_china_all
  supercdnctl [global flags] e2e-probe -profile china_all
  supercdnctl [global flags] create-bucket -slug movie-posters -name 影视海报桶 -profile china_all -types image
  supercdnctl [global flags] init-bucket -bucket movie-posters
  supercdnctl [global flags] upload-bucket -bucket movie-posters -file poster.jpg -path posters/poster.jpg
  supercdnctl [global flags] list-bucket -bucket movie-posters
  supercdnctl [global flags] purge-bucket -bucket movie-posters -prefix posters/ -dry-run
  supercdnctl [global flags] warmup-bucket -bucket movie-posters -path posters/poster.jpg -dry-run
  supercdnctl [global flags] delete-bucket-object -bucket movie-posters -path posters/poster.jpg
  supercdnctl [global flags] delete-bucket -bucket movie-posters -force
  supercdnctl [global flags] job -id 1
  supercdnctl [global flags] replicas -object-id 1
  supercdnctl [global flags] purge -urls https://example.com/a.css
  supercdnctl [global flags] purge-site -site blog -dry-run`)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
