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
)

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
