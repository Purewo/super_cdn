package cloudflare

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"supercdn/internal/config"
)

const defaultBaseURL = "https://api.cloudflare.com/client/v4"
const MaxPurgeFilesPerRequest = 100

type Client struct {
	baseURL       string
	accountID     string
	zoneID        string
	apiToken      string
	rootDomain    string
	siteSuffix    string
	siteDNSTarget string
	workerScript  string
	http          *http.Client
}

type DNSRecord struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
	TTL     int    `json:"ttl,omitempty"`
}

type SyncDNSRecordOptions struct {
	DryRun bool `json:"dry_run"`
	Force  bool `json:"force"`
}

type DNSRecordSyncResult struct {
	Name            string     `json:"name"`
	Type            string     `json:"type"`
	Content         string     `json:"content"`
	Proxied         bool       `json:"proxied"`
	TTL             int        `json:"ttl,omitempty"`
	Action          string     `json:"action"`
	DryRun          bool       `json:"dry_run,omitempty"`
	RecordID        string     `json:"record_id,omitempty"`
	ExistingType    string     `json:"existing_type,omitempty"`
	ExistingContent string     `json:"existing_content,omitempty"`
	ExistingProxied bool       `json:"existing_proxied,omitempty"`
	Record          *DNSRecord `json:"record,omitempty"`
	Error           string     `json:"error,omitempty"`
}

type Status struct {
	Configured       bool          `json:"configured"`
	AccountID        string        `json:"account_id,omitempty"`
	ZoneID           string        `json:"zone_id,omitempty"`
	RootDomain       string        `json:"root_domain,omitempty"`
	SiteDomainSuffix string        `json:"site_domain_suffix,omitempty"`
	SiteDNSTarget    string        `json:"site_dns_target,omitempty"`
	Token            CheckStatus   `json:"token"`
	Zone             ZoneStatus    `json:"zone"`
	DNS              DNSStatus     `json:"dns"`
	Workers          WorkersStatus `json:"workers"`
	R2               R2Status      `json:"r2"`
	Warnings         []string      `json:"warnings,omitempty"`
	CheckedAt        time.Time     `json:"checked_at"`
}

type R2CheckOptions struct {
	Bucket        string
	PublicBaseURL string
}

type CheckStatus struct {
	Configured bool   `json:"configured"`
	OK         bool   `json:"ok"`
	Message    string `json:"message,omitempty"`
}

type ZoneStatus struct {
	Configured bool   `json:"configured"`
	OK         bool   `json:"ok"`
	ID         string `json:"id,omitempty"`
	Name       string `json:"name,omitempty"`
	Status     string `json:"status,omitempty"`
	Message    string `json:"message,omitempty"`
}

type DNSStatus struct {
	Configured      bool        `json:"configured"`
	OK              bool        `json:"ok"`
	RootRecords     []DNSRecord `json:"root_records,omitempty"`
	SiteWildcard    []DNSRecord `json:"site_wildcard_records,omitempty"`
	ManagedWildcard []DNSRecord `json:"managed_wildcard_records,omitempty"`
	Message         string      `json:"message,omitempty"`
}

type WorkersStatus struct {
	Configured bool          `json:"configured"`
	OK         bool          `json:"ok"`
	RouteCount int           `json:"route_count"`
	Routes     []WorkerRoute `json:"routes,omitempty"`
	Message    string        `json:"message,omitempty"`
}

type WorkerRoute struct {
	ID      string `json:"id,omitempty"`
	Pattern string `json:"pattern"`
	Script  string `json:"script,omitempty"`
}

type SyncWorkerRouteOptions struct {
	DryRun bool `json:"dry_run"`
	Force  bool `json:"force"`
}

type WorkerRouteSyncResult struct {
	Pattern        string       `json:"pattern"`
	Script         string       `json:"script"`
	Action         string       `json:"action"`
	DryRun         bool         `json:"dry_run,omitempty"`
	RouteID        string       `json:"route_id,omitempty"`
	ExistingScript string       `json:"existing_script,omitempty"`
	Route          *WorkerRoute `json:"route,omitempty"`
	Error          string       `json:"error,omitempty"`
}

type KVNamespace struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type R2Status struct {
	Configured  bool                `json:"configured"`
	OK          bool                `json:"ok"`
	BucketCount int                 `json:"bucket_count"`
	Buckets     []R2Bucket          `json:"buckets,omitempty"`
	Bucket      R2BucketCheckStatus `json:"bucket"`
	CORS        R2CORSStatus        `json:"cors"`
	Domains     R2DomainStatus      `json:"domains"`
	Message     string              `json:"message,omitempty"`
}

type R2Bucket struct {
	Name         string `json:"name"`
	CreationDate string `json:"creation_date,omitempty"`
	Location     string `json:"location,omitempty"`
	Jurisdiction string `json:"jurisdiction,omitempty"`
	StorageClass string `json:"storage_class,omitempty"`
}

type R2BucketCheckStatus struct {
	Configured bool      `json:"configured"`
	OK         bool      `json:"ok"`
	Name       string    `json:"name,omitempty"`
	Bucket     *R2Bucket `json:"bucket,omitempty"`
	Message    string    `json:"message,omitempty"`
}

type R2CORSStatus struct {
	Configured bool         `json:"configured"`
	OK         bool         `json:"ok"`
	RuleCount  int          `json:"rule_count"`
	Rules      []R2CORSRule `json:"rules,omitempty"`
	Message    string       `json:"message,omitempty"`
}

type R2DomainStatus struct {
	Configured        bool             `json:"configured"`
	OK                bool             `json:"ok"`
	PublicBaseURL     string           `json:"public_base_url,omitempty"`
	PublicHost        string           `json:"public_host,omitempty"`
	MatchedDomain     string           `json:"matched_domain,omitempty"`
	CustomDomainCount int              `json:"custom_domain_count"`
	CustomDomains     []R2CustomDomain `json:"custom_domains,omitempty"`
	ManagedDomain     *R2ManagedDomain `json:"managed_domain,omitempty"`
	Message           string           `json:"message,omitempty"`
}

type R2CORSPolicy struct {
	Rules []R2CORSRule `json:"rules"`
}

type R2CORSRule struct {
	ID            string        `json:"id,omitempty"`
	Allowed       R2CORSAllowed `json:"allowed"`
	ExposeHeaders []string      `json:"exposeHeaders,omitempty"`
	MaxAgeSeconds int           `json:"maxAgeSeconds,omitempty"`
}

type R2CORSAllowed struct {
	Methods []string `json:"methods"`
	Origins []string `json:"origins"`
	Headers []string `json:"headers,omitempty"`
}

type R2CustomDomain struct {
	Domain   string                   `json:"domain"`
	Enabled  bool                     `json:"enabled"`
	Status   R2DomainValidationStatus `json:"status"`
	ZoneID   string                   `json:"zoneId,omitempty"`
	ZoneName string                   `json:"zoneName,omitempty"`
	MinTLS   string                   `json:"minTLS,omitempty"`
	Ciphers  []string                 `json:"ciphers,omitempty"`
}

type R2DomainValidationStatus struct {
	Ownership string `json:"ownership,omitempty"`
	SSL       string `json:"ssl,omitempty"`
}

type R2ManagedDomain struct {
	BucketID string `json:"bucketId,omitempty"`
	Domain   string `json:"domain"`
	Enabled  bool   `json:"enabled"`
}

type R2BucketCreateOptions struct {
	Name         string `json:"name"`
	LocationHint string `json:"locationHint,omitempty"`
	Jurisdiction string `json:"jurisdiction,omitempty"`
	StorageClass string `json:"storageClass,omitempty"`
}

type SyncR2Options struct {
	Bucket             string
	PublicBaseURL      string
	ZoneID             string
	DryRun             bool
	Force              bool
	SyncCORS           bool
	SyncDomain         bool
	CORSAllowedOrigins []string
	CORSAllowedMethods []string
	CORSAllowedHeaders []string
	CORSExposeHeaders  []string
	CORSMaxAgeSeconds  int
}

type R2ProvisionOptions struct {
	Bucket             string
	PublicBaseURL      string
	ZoneID             string
	LocationHint       string
	Jurisdiction       string
	StorageClass       string
	DryRun             bool
	Force              bool
	SyncCORS           bool
	SyncDomain         bool
	CORSAllowedOrigins []string
	CORSAllowedMethods []string
	CORSAllowedHeaders []string
	CORSExposeHeaders  []string
	CORSMaxAgeSeconds  int
}

type R2CredentialsOptions struct {
	Bucket              string
	Jurisdiction        string
	TokenName           string
	PermissionGroupName string
	DryRun              bool
}

type R2SyncResult struct {
	Bucket        string              `json:"bucket"`
	PublicBaseURL string              `json:"public_base_url,omitempty"`
	DryRun        bool                `json:"dry_run"`
	Force         bool                `json:"force"`
	Status        string              `json:"status"`
	CORS          *R2CORSSyncResult   `json:"cors,omitempty"`
	Domain        *R2DomainSyncResult `json:"domain,omitempty"`
	Warnings      []string            `json:"warnings,omitempty"`
	Errors        []string            `json:"errors,omitempty"`
}

type R2ProvisionResult struct {
	Bucket        string                  `json:"bucket"`
	PublicBaseURL string                  `json:"public_base_url,omitempty"`
	DryRun        bool                    `json:"dry_run"`
	Force         bool                    `json:"force"`
	Status        string                  `json:"status"`
	BucketResult  R2BucketProvisionResult `json:"bucket_result"`
	Sync          *R2SyncResult           `json:"sync,omitempty"`
	Warnings      []string                `json:"warnings,omitempty"`
	Errors        []string                `json:"errors,omitempty"`
}

type R2CredentialsResult struct {
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

type R2BucketProvisionResult struct {
	Action  string                `json:"action"`
	DryRun  bool                  `json:"dry_run,omitempty"`
	Desired R2BucketCreateOptions `json:"desired"`
	Current *R2Bucket             `json:"current,omitempty"`
	Result  *R2Bucket             `json:"result,omitempty"`
	Error   string                `json:"error,omitempty"`
}

type R2CORSSyncResult struct {
	Action  string        `json:"action"`
	DryRun  bool          `json:"dry_run,omitempty"`
	Current *R2CORSPolicy `json:"current,omitempty"`
	Desired R2CORSPolicy  `json:"desired"`
	Result  *R2CORSPolicy `json:"result,omitempty"`
	Error   string        `json:"error,omitempty"`
}

type R2DomainSyncResult struct {
	Action         string           `json:"action"`
	DryRun         bool             `json:"dry_run,omitempty"`
	Domain         string           `json:"domain,omitempty"`
	DomainType     string           `json:"domain_type,omitempty"`
	CurrentCustom  *R2CustomDomain  `json:"current_custom,omitempty"`
	CurrentManaged *R2ManagedDomain `json:"current_managed,omitempty"`
	ResultCustom   *R2CustomDomain  `json:"result_custom,omitempty"`
	ResultManaged  *R2ManagedDomain `json:"result_managed,omitempty"`
	Error          string           `json:"error,omitempty"`
}

type R2CustomDomainConfig struct {
	Domain  string   `json:"domain"`
	Enabled bool     `json:"enabled"`
	ZoneID  string   `json:"zoneId"`
	MinTLS  string   `json:"minTLS,omitempty"`
	Ciphers []string `json:"ciphers,omitempty"`
}

type TokenPermissionGroup struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Scopes []string `json:"scopes,omitempty"`
}

type AccountAPIToken struct {
	ID       string               `json:"id"`
	Name     string               `json:"name"`
	Status   string               `json:"status,omitempty"`
	Value    string               `json:"value,omitempty"`
	Policies []AccountTokenPolicy `json:"policies,omitempty"`
}

type AccountTokenPolicy struct {
	ID               string                        `json:"id,omitempty"`
	Effect           string                        `json:"effect"`
	Resources        map[string]string             `json:"resources"`
	PermissionGroups []AccountTokenPermissionGroup `json:"permission_groups"`
}

type AccountTokenPermissionGroup struct {
	ID string `json:"id"`
}

type AccountTokenCreateOptions struct {
	Name     string               `json:"name"`
	Policies []AccountTokenPolicy `json:"policies"`
}

type PurgeBatchResult struct {
	Batch    int             `json:"batch"`
	URLCount int             `json:"url_count"`
	Result   json.RawMessage `json:"result,omitempty"`
	Error    string          `json:"error,omitempty"`
}

type envelope struct {
	Success  bool              `json:"success"`
	Errors   []cloudflareError `json:"errors"`
	Messages []cloudflareError `json:"messages"`
	Result   json.RawMessage   `json:"result"`
}

type cloudflareError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func New(cfg config.CloudflareConfig, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		baseURL:       defaultBaseURL,
		accountID:     strings.TrimSpace(cfg.AccountID),
		zoneID:        strings.TrimSpace(cfg.ZoneID),
		apiToken:      strings.TrimSpace(cfg.APIToken),
		rootDomain:    strings.TrimSpace(cfg.RootDomain),
		siteSuffix:    strings.TrimSpace(cfg.SiteDomainSuffix),
		siteDNSTarget: strings.TrimSpace(cfg.SiteDNSTarget),
		workerScript:  strings.TrimSpace(cfg.WorkerScript),
		http:          httpClient,
	}
}

func (c *Client) Configured() bool {
	return c != nil && c.zoneID != "" && c.apiToken != ""
}

func (c *Client) AccountConfigured() bool {
	return c != nil && c.accountID != "" && c.apiToken != ""
}

func (c *Client) ZoneID() string {
	if c == nil {
		return ""
	}
	return c.zoneID
}

func (c *Client) AccountID() string {
	if c == nil {
		return ""
	}
	return c.accountID
}

func (c *Client) WorkerScript() string {
	if c == nil {
		return ""
	}
	return c.workerScript
}

func (c *Client) Status(ctx context.Context) Status {
	return c.status(ctx, R2CheckOptions{})
}

func (c *Client) StatusWithR2Checks(ctx context.Context, r2Opts R2CheckOptions) Status {
	return c.status(ctx, r2Opts)
}

func (c *Client) status(ctx context.Context, r2Opts R2CheckOptions) Status {
	status := Status{
		Configured:       c.Configured(),
		AccountID:        c.accountID,
		ZoneID:           c.zoneID,
		RootDomain:       c.rootDomain,
		SiteDomainSuffix: c.siteSuffix,
		SiteDNSTarget:    c.siteDNSTarget,
		CheckedAt:        time.Now().UTC(),
	}
	if c.apiToken == "" {
		status.Token = CheckStatus{Configured: false, Message: "cloudflare api_token is not configured"}
		status.Warnings = append(status.Warnings, "cloudflare api_token is not configured")
	} else if err := c.VerifyToken(ctx); err != nil {
		status.Token = CheckStatus{Configured: true, OK: false, Message: err.Error()}
	} else {
		status.Token = CheckStatus{Configured: true, OK: true}
	}
	status.Zone = c.zoneStatus(ctx)
	status.DNS = c.dnsStatus(ctx)
	status.Workers = c.workersStatus(ctx)
	status.R2 = c.r2Status(ctx, r2Opts)
	if status.Token.Configured && !status.Token.OK && (status.Zone.OK || status.DNS.OK || status.Workers.OK || status.R2.OK) {
		verifyMessage := status.Token.Message
		status.Token.OK = true
		status.Token.Message = "token verify endpoint failed, but scoped Cloudflare API calls succeeded"
		if verifyMessage != "" {
			status.Warnings = append(status.Warnings, verifyMessage)
		}
	}
	return status
}

func (c *Client) VerifyToken(ctx context.Context) error {
	var out json.RawMessage
	if err := c.get(ctx, "/user/tokens/verify", nil, &out); err == nil {
		return nil
	} else if c.accountID == "" {
		return err
	} else {
		userErr := err
		if err := c.get(ctx, "/accounts/"+url.PathEscape(c.accountID)+"/tokens/verify", nil, &out); err == nil {
			return nil
		} else {
			return fmt.Errorf("user token verify failed: %w; account token verify failed: %w", userErr, err)
		}
	}
}

func (c *Client) Zone(ctx context.Context) (ZoneStatus, error) {
	if c.zoneID == "" {
		return ZoneStatus{Configured: false}, fmt.Errorf("cloudflare zone_id is not configured")
	}
	var zone struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	if err := c.get(ctx, "/zones/"+url.PathEscape(c.zoneID), nil, &zone); err != nil {
		return ZoneStatus{Configured: true, ID: c.zoneID}, err
	}
	return ZoneStatus{Configured: true, OK: true, ID: zone.ID, Name: zone.Name, Status: zone.Status}, nil
}

func (c *Client) ListDNSRecords(ctx context.Context, name string) ([]DNSRecord, error) {
	if c.zoneID == "" {
		return nil, fmt.Errorf("cloudflare zone_id is not configured")
	}
	q := url.Values{"per_page": {"100"}}
	if strings.TrimSpace(name) != "" {
		q.Set("name", strings.TrimSpace(name))
	}
	var records []DNSRecord
	if err := c.get(ctx, "/zones/"+url.PathEscape(c.zoneID)+"/dns_records", q, &records); err != nil {
		return nil, err
	}
	return records, nil
}

func (c *Client) CreateDNSRecord(ctx context.Context, record DNSRecord) (DNSRecord, error) {
	if c.zoneID == "" {
		return DNSRecord{}, fmt.Errorf("cloudflare zone_id is not configured")
	}
	record = normalizeDNSRecord(record)
	body, _ := json.Marshal(record)
	var out DNSRecord
	if err := c.post(ctx, "/zones/"+url.PathEscape(c.zoneID)+"/dns_records", body, &out); err != nil {
		return DNSRecord{}, err
	}
	return out, nil
}

func (c *Client) UpdateDNSRecord(ctx context.Context, id string, record DNSRecord) (DNSRecord, error) {
	if c.zoneID == "" {
		return DNSRecord{}, fmt.Errorf("cloudflare zone_id is not configured")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return DNSRecord{}, fmt.Errorf("cloudflare dns record id is required")
	}
	record = normalizeDNSRecord(record)
	body, _ := json.Marshal(record)
	var out DNSRecord
	if err := c.put(ctx, "/zones/"+url.PathEscape(c.zoneID)+"/dns_records/"+url.PathEscape(id), body, &out); err != nil {
		return DNSRecord{}, err
	}
	return out, nil
}

func (c *Client) SyncDNSRecords(ctx context.Context, records []DNSRecord, opts SyncDNSRecordOptions) ([]DNSRecordSyncResult, error) {
	cleaned := cleanDNSRecords(records)
	out := make([]DNSRecordSyncResult, 0, len(cleaned))
	for _, record := range cleaned {
		result := DNSRecordSyncResult{
			Name:    record.Name,
			Type:    record.Type,
			Content: record.Content,
			Proxied: record.Proxied,
			TTL:     record.TTL,
			DryRun:  opts.DryRun,
		}
		existing, err := c.ListDNSRecords(ctx, record.Name)
		if err != nil {
			return nil, err
		}
		sameType := findDNSRecord(existing, record.Type)
		if sameType != nil {
			result.RecordID = sameType.ID
			result.ExistingType = sameType.Type
			result.ExistingContent = sameType.Content
			result.ExistingProxied = sameType.Proxied
			if dnsRecordMatches(*sameType, record) {
				result.Action = "unchanged"
				routeCopy := *sameType
				result.Record = &routeCopy
				out = append(out, result)
				continue
			}
			if !opts.Force {
				result.Action = "conflict"
				result.Error = "existing DNS record has different content or proxy status; pass force to update it"
				out = append(out, result)
				continue
			}
			result.Action = "update"
			if !opts.DryRun {
				updated, err := c.UpdateDNSRecord(ctx, sameType.ID, record)
				if err != nil {
					result.Error = err.Error()
				} else {
					result.RecordID = updated.ID
					result.Record = &updated
				}
			}
			out = append(out, result)
			continue
		}
		if conflictsWithDNSRecord(existing, record) {
			result.Action = "conflict"
			result.Error = "existing DNS record with another type may conflict with requested record; resolve it manually"
			if len(existing) > 0 {
				result.RecordID = existing[0].ID
				result.ExistingType = existing[0].Type
				result.ExistingContent = existing[0].Content
				result.ExistingProxied = existing[0].Proxied
			}
			out = append(out, result)
			continue
		}
		result.Action = "create"
		if !opts.DryRun {
			created, err := c.CreateDNSRecord(ctx, record)
			if err != nil {
				result.Error = err.Error()
			} else {
				result.RecordID = created.ID
				result.Record = &created
			}
		}
		out = append(out, result)
	}
	return out, nil
}

func (c *Client) ListWorkerRoutes(ctx context.Context) ([]WorkerRoute, error) {
	if c.zoneID == "" {
		return nil, fmt.Errorf("cloudflare zone_id is not configured")
	}
	var routes []WorkerRoute
	if err := c.get(ctx, "/zones/"+url.PathEscape(c.zoneID)+"/workers/routes", url.Values{"per_page": {"100"}}, &routes); err != nil {
		return nil, err
	}
	return routes, nil
}

func (c *Client) CreateWorkerRoute(ctx context.Context, pattern, script string) (WorkerRoute, error) {
	if c.zoneID == "" {
		return WorkerRoute{}, fmt.Errorf("cloudflare zone_id is not configured")
	}
	body, _ := json.Marshal(WorkerRoute{Pattern: strings.TrimSpace(pattern), Script: strings.TrimSpace(script)})
	var route WorkerRoute
	if err := c.post(ctx, "/zones/"+url.PathEscape(c.zoneID)+"/workers/routes", body, &route); err != nil {
		return WorkerRoute{}, err
	}
	return route, nil
}

func (c *Client) UpdateWorkerRoute(ctx context.Context, id, pattern, script string) (WorkerRoute, error) {
	if c.zoneID == "" {
		return WorkerRoute{}, fmt.Errorf("cloudflare zone_id is not configured")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return WorkerRoute{}, fmt.Errorf("cloudflare worker route id is required")
	}
	body, _ := json.Marshal(WorkerRoute{Pattern: strings.TrimSpace(pattern), Script: strings.TrimSpace(script)})
	var route WorkerRoute
	if err := c.put(ctx, "/zones/"+url.PathEscape(c.zoneID)+"/workers/routes/"+url.PathEscape(id), body, &route); err != nil {
		return WorkerRoute{}, err
	}
	return route, nil
}

func (c *Client) SyncWorkerRoutes(ctx context.Context, patterns []string, script string, opts SyncWorkerRouteOptions) ([]WorkerRouteSyncResult, error) {
	script = strings.TrimSpace(script)
	if script == "" {
		return nil, fmt.Errorf("cloudflare worker script is not configured")
	}
	routes, err := c.ListWorkerRoutes(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]WorkerRouteSyncResult, 0, len(patterns))
	for _, pattern := range cleanPatterns(patterns) {
		result := WorkerRouteSyncResult{Pattern: pattern, Script: script, DryRun: opts.DryRun}
		existing := findWorkerRoute(routes, pattern)
		if existing != nil {
			result.RouteID = existing.ID
			result.ExistingScript = existing.Script
			if existing.Script == script {
				result.Action = "unchanged"
				routeCopy := *existing
				result.Route = &routeCopy
				out = append(out, result)
				continue
			}
			if !opts.Force {
				result.Action = "conflict"
				result.Error = "existing route points to a different worker script; pass force to update it"
				out = append(out, result)
				continue
			}
			result.Action = "update"
			if !opts.DryRun {
				route, err := c.UpdateWorkerRoute(ctx, existing.ID, pattern, script)
				if err != nil {
					result.Error = err.Error()
				} else {
					result.RouteID = route.ID
					result.ExistingScript = existing.Script
					result.Route = &route
				}
			}
			out = append(out, result)
			continue
		}
		result.Action = "create"
		if !opts.DryRun {
			route, err := c.CreateWorkerRoute(ctx, pattern, script)
			if err != nil {
				result.Error = err.Error()
			} else {
				result.RouteID = route.ID
				result.Route = &route
			}
		}
		out = append(out, result)
	}
	return out, nil
}

func (c *Client) ListKVNamespaces(ctx context.Context) ([]KVNamespace, error) {
	if c.accountID == "" {
		return nil, fmt.Errorf("cloudflare account_id is not configured")
	}
	var namespaces []KVNamespace
	if err := c.get(ctx, "/accounts/"+url.PathEscape(c.accountID)+"/storage/kv/namespaces", url.Values{"per_page": {"100"}}, &namespaces); err != nil {
		return nil, err
	}
	return namespaces, nil
}

func (c *Client) FindKVNamespace(ctx context.Context, title string) (KVNamespace, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return KVNamespace{}, fmt.Errorf("cloudflare kv namespace title is required")
	}
	namespaces, err := c.ListKVNamespaces(ctx)
	if err != nil {
		return KVNamespace{}, err
	}
	for _, namespace := range namespaces {
		if namespace.Title == title {
			return namespace, nil
		}
	}
	return KVNamespace{}, fmt.Errorf("cloudflare kv namespace %q not found", title)
}

func (c *Client) PutKVValue(ctx context.Context, namespaceID, key string, value []byte) error {
	if c.accountID == "" {
		return fmt.Errorf("cloudflare account_id is not configured")
	}
	namespaceID = strings.TrimSpace(namespaceID)
	key = strings.TrimSpace(strings.ReplaceAll(key, "\\", "/"))
	if namespaceID == "" {
		return fmt.Errorf("cloudflare kv namespace id is required")
	}
	if key == "" {
		return fmt.Errorf("cloudflare kv key is required")
	}
	headers := http.Header{"Content-Type": []string{"application/json; charset=utf-8"}}
	return c.doWithHeaders(ctx, http.MethodPut, "/accounts/"+url.PathEscape(c.accountID)+"/storage/kv/namespaces/"+url.PathEscape(namespaceID)+"/values/"+url.PathEscape(key), nil, value, headers, nil)
}

func (c *Client) ListR2Buckets(ctx context.Context) ([]R2Bucket, error) {
	if c.accountID == "" {
		return nil, fmt.Errorf("cloudflare account_id is not configured")
	}
	var raw json.RawMessage
	if err := c.get(ctx, "/accounts/"+url.PathEscape(c.accountID)+"/r2/buckets", nil, &raw); err != nil {
		return nil, err
	}
	var wrapped struct {
		Buckets []R2Bucket `json:"buckets"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil && wrapped.Buckets != nil {
		return wrapped.Buckets, nil
	}
	var direct []R2Bucket
	if err := json.Unmarshal(raw, &direct); err == nil {
		return direct, nil
	}
	return nil, fmt.Errorf("unexpected r2 buckets response")
}

func (c *Client) GetR2Bucket(ctx context.Context, bucket string) (R2Bucket, error) {
	if c.accountID == "" {
		return R2Bucket{}, fmt.Errorf("cloudflare account_id is not configured")
	}
	bucket = strings.TrimSpace(bucket)
	if bucket == "" {
		return R2Bucket{}, fmt.Errorf("r2 bucket is required")
	}
	var out R2Bucket
	if err := c.get(ctx, "/accounts/"+url.PathEscape(c.accountID)+"/r2/buckets/"+url.PathEscape(bucket), nil, &out); err != nil {
		return R2Bucket{}, err
	}
	return out, nil
}

func (c *Client) CreateR2Bucket(ctx context.Context, opts R2BucketCreateOptions) (R2Bucket, error) {
	if c.accountID == "" {
		return R2Bucket{}, fmt.Errorf("cloudflare account_id is not configured")
	}
	opts = normalizeR2BucketCreateOptions(opts)
	if opts.Name == "" {
		return R2Bucket{}, fmt.Errorf("r2 bucket name is required")
	}
	body, _ := json.Marshal(struct {
		Name         string `json:"name"`
		LocationHint string `json:"locationHint,omitempty"`
		StorageClass string `json:"storageClass,omitempty"`
	}{
		Name:         opts.Name,
		LocationHint: opts.LocationHint,
		StorageClass: opts.StorageClass,
	})
	headers := http.Header{}
	if opts.Jurisdiction != "" {
		headers.Set("cf-r2-jurisdiction", opts.Jurisdiction)
	}
	var out R2Bucket
	if err := c.postWithHeaders(ctx, "/accounts/"+url.PathEscape(c.accountID)+"/r2/buckets", body, headers, &out); err != nil {
		return R2Bucket{}, err
	}
	if out.Name == "" {
		out.Name = opts.Name
	}
	return out, nil
}

func (c *Client) GetR2BucketCORS(ctx context.Context, bucket string) (R2CORSPolicy, error) {
	if c.accountID == "" {
		return R2CORSPolicy{}, fmt.Errorf("cloudflare account_id is not configured")
	}
	bucket = strings.TrimSpace(bucket)
	if bucket == "" {
		return R2CORSPolicy{}, fmt.Errorf("r2 bucket is required")
	}
	var raw json.RawMessage
	if err := c.get(ctx, "/accounts/"+url.PathEscape(c.accountID)+"/r2/buckets/"+url.PathEscape(bucket)+"/cors", nil, &raw); err != nil {
		return R2CORSPolicy{}, err
	}
	var policy R2CORSPolicy
	if err := json.Unmarshal(raw, &policy); err == nil {
		if policy.Rules == nil {
			policy.Rules = []R2CORSRule{}
		}
		return policy, nil
	}
	var rules []R2CORSRule
	if err := json.Unmarshal(raw, &rules); err == nil {
		return R2CORSPolicy{Rules: rules}, nil
	}
	return R2CORSPolicy{}, fmt.Errorf("unexpected r2 cors response")
}

func (c *Client) ListR2CustomDomains(ctx context.Context, bucket string) ([]R2CustomDomain, error) {
	if c.accountID == "" {
		return nil, fmt.Errorf("cloudflare account_id is not configured")
	}
	bucket = strings.TrimSpace(bucket)
	if bucket == "" {
		return nil, fmt.Errorf("r2 bucket is required")
	}
	var raw json.RawMessage
	if err := c.get(ctx, "/accounts/"+url.PathEscape(c.accountID)+"/r2/buckets/"+url.PathEscape(bucket)+"/domains/custom", nil, &raw); err != nil {
		return nil, err
	}
	var wrapped struct {
		Domains []R2CustomDomain `json:"domains"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil && wrapped.Domains != nil {
		return wrapped.Domains, nil
	}
	var direct []R2CustomDomain
	if err := json.Unmarshal(raw, &direct); err == nil {
		return direct, nil
	}
	return nil, fmt.Errorf("unexpected r2 custom domains response")
}

func (c *Client) GetR2ManagedDomain(ctx context.Context, bucket string) (R2ManagedDomain, error) {
	if c.accountID == "" {
		return R2ManagedDomain{}, fmt.Errorf("cloudflare account_id is not configured")
	}
	bucket = strings.TrimSpace(bucket)
	if bucket == "" {
		return R2ManagedDomain{}, fmt.Errorf("r2 bucket is required")
	}
	var out R2ManagedDomain
	if err := c.get(ctx, "/accounts/"+url.PathEscape(c.accountID)+"/r2/buckets/"+url.PathEscape(bucket)+"/domains/managed", nil, &out); err != nil {
		return R2ManagedDomain{}, err
	}
	return out, nil
}

func (c *Client) PutR2BucketCORS(ctx context.Context, bucket string, policy R2CORSPolicy) (R2CORSPolicy, error) {
	if c.accountID == "" {
		return R2CORSPolicy{}, fmt.Errorf("cloudflare account_id is not configured")
	}
	bucket = strings.TrimSpace(bucket)
	if bucket == "" {
		return R2CORSPolicy{}, fmt.Errorf("r2 bucket is required")
	}
	body, _ := json.Marshal(policy)
	var raw json.RawMessage
	if err := c.put(ctx, "/accounts/"+url.PathEscape(c.accountID)+"/r2/buckets/"+url.PathEscape(bucket)+"/cors", body, &raw); err != nil {
		return R2CORSPolicy{}, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return policy, nil
	}
	var out R2CORSPolicy
	if err := json.Unmarshal(raw, &out); err == nil {
		if out.Rules == nil {
			out.Rules = []R2CORSRule{}
		}
		if len(out.Rules) == 0 && len(policy.Rules) > 0 {
			return policy, nil
		}
		return out, nil
	}
	var rules []R2CORSRule
	if err := json.Unmarshal(raw, &rules); err == nil {
		if len(rules) == 0 && len(policy.Rules) > 0 {
			return policy, nil
		}
		return R2CORSPolicy{Rules: rules}, nil
	}
	return policy, nil
}

func (c *Client) CreateR2CustomDomain(ctx context.Context, bucket string, cfg R2CustomDomainConfig) (R2CustomDomain, error) {
	if c.accountID == "" {
		return R2CustomDomain{}, fmt.Errorf("cloudflare account_id is not configured")
	}
	bucket = strings.TrimSpace(bucket)
	if bucket == "" {
		return R2CustomDomain{}, fmt.Errorf("r2 bucket is required")
	}
	cfg.Domain = cleanDomain(cfg.Domain)
	cfg.ZoneID = strings.TrimSpace(cfg.ZoneID)
	if cfg.Domain == "" || cfg.ZoneID == "" {
		return R2CustomDomain{}, fmt.Errorf("r2 custom domain and zone_id are required")
	}
	body, _ := json.Marshal(cfg)
	var out R2CustomDomain
	if err := c.post(ctx, "/accounts/"+url.PathEscape(c.accountID)+"/r2/buckets/"+url.PathEscape(bucket)+"/domains/custom", body, &out); err != nil {
		return R2CustomDomain{}, err
	}
	return out, nil
}

func (c *Client) UpdateR2CustomDomain(ctx context.Context, bucket string, cfg R2CustomDomainConfig) (R2CustomDomain, error) {
	if c.accountID == "" {
		return R2CustomDomain{}, fmt.Errorf("cloudflare account_id is not configured")
	}
	bucket = strings.TrimSpace(bucket)
	if bucket == "" {
		return R2CustomDomain{}, fmt.Errorf("r2 bucket is required")
	}
	cfg.Domain = cleanDomain(cfg.Domain)
	cfg.ZoneID = strings.TrimSpace(cfg.ZoneID)
	if cfg.Domain == "" || cfg.ZoneID == "" {
		return R2CustomDomain{}, fmt.Errorf("r2 custom domain and zone_id are required")
	}
	body, _ := json.Marshal(cfg)
	var out R2CustomDomain
	if err := c.put(ctx, "/accounts/"+url.PathEscape(c.accountID)+"/r2/buckets/"+url.PathEscape(bucket)+"/domains/custom/"+url.PathEscape(cfg.Domain), body, &out); err != nil {
		return R2CustomDomain{}, err
	}
	return out, nil
}

func (c *Client) UpdateR2ManagedDomain(ctx context.Context, bucket string, enabled bool) (R2ManagedDomain, error) {
	if c.accountID == "" {
		return R2ManagedDomain{}, fmt.Errorf("cloudflare account_id is not configured")
	}
	bucket = strings.TrimSpace(bucket)
	if bucket == "" {
		return R2ManagedDomain{}, fmt.Errorf("r2 bucket is required")
	}
	body, _ := json.Marshal(map[string]bool{"enabled": enabled})
	var out R2ManagedDomain
	if err := c.put(ctx, "/accounts/"+url.PathEscape(c.accountID)+"/r2/buckets/"+url.PathEscape(bucket)+"/domains/managed", body, &out); err != nil {
		return R2ManagedDomain{}, err
	}
	return out, nil
}

func (c *Client) SyncR2Bucket(ctx context.Context, opts SyncR2Options) R2SyncResult {
	opts = normalizeSyncR2Options(opts)
	result := R2SyncResult{
		Bucket:        opts.Bucket,
		PublicBaseURL: opts.PublicBaseURL,
		DryRun:        opts.DryRun,
		Force:         opts.Force,
		Status:        "ok",
	}
	if opts.Bucket == "" {
		result.Status = "failed"
		result.Errors = append(result.Errors, "r2 bucket is required")
		return result
	}
	if opts.SyncCORS {
		cors := c.syncR2CORS(ctx, opts)
		result.CORS = &cors
		if cors.Error != "" {
			result.Status = "partial"
			result.Errors = append(result.Errors, "cors: "+cors.Error)
		}
	}
	if opts.SyncDomain {
		domain := c.syncR2Domain(ctx, opts)
		result.Domain = &domain
		if domain.Error != "" {
			result.Status = "partial"
			result.Errors = append(result.Errors, "domain: "+domain.Error)
		}
	}
	if result.Status == "ok" && opts.DryRun && ((result.CORS != nil && result.CORS.Action != "unchanged" && result.CORS.Action != "skipped") || (result.Domain != nil && result.Domain.Action != "unchanged" && result.Domain.Action != "skipped")) {
		result.Status = "planned"
	}
	return result
}

func (c *Client) ProvisionR2Bucket(ctx context.Context, opts R2ProvisionOptions) R2ProvisionResult {
	opts = normalizeR2ProvisionOptions(opts)
	desired := R2BucketCreateOptions{
		Name:         opts.Bucket,
		LocationHint: opts.LocationHint,
		Jurisdiction: opts.Jurisdiction,
		StorageClass: opts.StorageClass,
	}
	result := R2ProvisionResult{
		Bucket:        opts.Bucket,
		PublicBaseURL: opts.PublicBaseURL,
		DryRun:        opts.DryRun,
		Force:         opts.Force,
		Status:        "ok",
		BucketResult: R2BucketProvisionResult{
			DryRun:  opts.DryRun,
			Desired: desired,
		},
	}
	if opts.Bucket == "" {
		result.Status = "failed"
		result.BucketResult.Action = "failed"
		result.BucketResult.Error = "r2 bucket is required"
		result.Errors = append(result.Errors, result.BucketResult.Error)
		return result
	}
	buckets, err := c.ListR2Buckets(ctx)
	if err != nil {
		result.Status = "failed"
		result.BucketResult.Action = "failed"
		result.BucketResult.Error = err.Error()
		result.Errors = append(result.Errors, "bucket: "+err.Error())
		return result
	}
	if current := findR2Bucket(buckets, opts.Bucket); current != nil {
		result.BucketResult.Action = "exists"
		result.BucketResult.Current = current
		sync := c.SyncR2Bucket(ctx, provisionSyncOptions(opts))
		result.Sync = &sync
		result.Status = provisionStatusFromSync(sync, opts.DryRun)
		result.Errors = append(result.Errors, sync.Errors...)
		return result
	}
	result.BucketResult.Action = "create"
	if opts.DryRun {
		sync := plannedR2PostCreateSync(opts)
		result.Sync = &sync
		result.Status = "planned"
		return result
	}
	created, err := c.CreateR2Bucket(ctx, desired)
	if err != nil {
		result.Status = "failed"
		result.BucketResult.Error = err.Error()
		result.Errors = append(result.Errors, "bucket: "+err.Error())
		return result
	}
	result.BucketResult.Result = &created
	sync := c.SyncR2Bucket(ctx, provisionSyncOptions(opts))
	result.Sync = &sync
	result.Status = provisionStatusFromSync(sync, opts.DryRun)
	result.Errors = append(result.Errors, sync.Errors...)
	return result
}

func (c *Client) ListAccountTokenPermissionGroups(ctx context.Context) ([]TokenPermissionGroup, error) {
	if c.accountID == "" {
		return nil, fmt.Errorf("cloudflare account_id is not configured")
	}
	var raw json.RawMessage
	if err := c.get(ctx, "/accounts/"+url.PathEscape(c.accountID)+"/tokens/permission_groups", nil, &raw); err != nil {
		return nil, err
	}
	var direct []TokenPermissionGroup
	if err := json.Unmarshal(raw, &direct); err == nil {
		return direct, nil
	}
	var wrapped struct {
		PermissionGroups []TokenPermissionGroup `json:"permission_groups"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil && wrapped.PermissionGroups != nil {
		return wrapped.PermissionGroups, nil
	}
	return nil, fmt.Errorf("unexpected account token permission groups response")
}

func (c *Client) CreateAccountToken(ctx context.Context, opts AccountTokenCreateOptions) (AccountAPIToken, error) {
	if c.accountID == "" {
		return AccountAPIToken{}, fmt.Errorf("cloudflare account_id is not configured")
	}
	opts.Name = strings.TrimSpace(opts.Name)
	if opts.Name == "" {
		return AccountAPIToken{}, fmt.Errorf("account token name is required")
	}
	if len(opts.Policies) == 0 {
		return AccountAPIToken{}, fmt.Errorf("account token policy is required")
	}
	body, _ := json.Marshal(opts)
	var out AccountAPIToken
	if err := c.post(ctx, "/accounts/"+url.PathEscape(c.accountID)+"/tokens", body, &out); err != nil {
		return AccountAPIToken{}, err
	}
	return out, nil
}

func (c *Client) CreateR2Credentials(ctx context.Context, opts R2CredentialsOptions) R2CredentialsResult {
	opts = normalizeR2CredentialsOptions(opts)
	result := R2CredentialsResult{
		Bucket:              opts.Bucket,
		Jurisdiction:        opts.Jurisdiction,
		TokenName:           opts.TokenName,
		PermissionGroupName: opts.PermissionGroupName,
		Resource:            c.r2BucketTokenResource(opts.Bucket, opts.Jurisdiction),
		DryRun:              opts.DryRun,
		Action:              "create_token",
		Status:              "planned",
	}
	if opts.Bucket == "" {
		result.Action = "failed"
		result.Status = "failed"
		result.Error = "r2 bucket is required"
		return result
	}
	if c.accountID == "" {
		result.Action = "failed"
		result.Status = "failed"
		result.Error = "cloudflare account_id is not configured"
		return result
	}
	if opts.DryRun {
		return result
	}
	groups, err := c.ListAccountTokenPermissionGroups(ctx)
	if err != nil {
		result.Action = "failed"
		result.Status = "failed"
		result.Error = err.Error()
		return result
	}
	group := findTokenPermissionGroup(groups, opts.PermissionGroupName)
	if group == nil {
		result.Action = "failed"
		result.Status = "failed"
		result.Error = fmt.Sprintf("cloudflare permission group %q was not found", opts.PermissionGroupName)
		return result
	}
	result.PermissionGroupID = group.ID
	policyID, err := randomPolicyID()
	if err != nil {
		result.Action = "failed"
		result.Status = "failed"
		result.Error = err.Error()
		return result
	}
	token, err := c.CreateAccountToken(ctx, AccountTokenCreateOptions{
		Name: opts.TokenName,
		Policies: []AccountTokenPolicy{{
			ID:     policyID,
			Effect: "allow",
			Resources: map[string]string{
				result.Resource: "*",
			},
			PermissionGroups: []AccountTokenPermissionGroup{{ID: group.ID}},
		}},
	})
	if err != nil {
		result.Action = "failed"
		result.Status = "failed"
		result.Error = err.Error()
		return result
	}
	if token.ID == "" || token.Value == "" {
		result.Action = "failed"
		result.Status = "failed"
		result.Error = "cloudflare account token response did not include id and one-time value"
		return result
	}
	secret := sha256.Sum256([]byte(token.Value))
	result.AccessKeyID = token.ID
	result.SecretAccessKey = fmt.Sprintf("%x", secret[:])
	result.Status = "ok"
	return result
}

func (c *Client) PurgeCache(ctx context.Context, urls []string) (json.RawMessage, error) {
	body, _ := json.Marshal(map[string]any{"files": urls})
	var raw json.RawMessage
	if err := c.post(ctx, "/zones/"+url.PathEscape(c.zoneID)+"/purge_cache", body, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func (c *Client) PurgeCacheBatches(ctx context.Context, urls []string) ([]PurgeBatchResult, error) {
	if c.zoneID == "" {
		return nil, fmt.Errorf("cloudflare zone_id is not configured")
	}
	urls = uniqueStrings(urls)
	results := make([]PurgeBatchResult, 0, (len(urls)+MaxPurgeFilesPerRequest-1)/MaxPurgeFilesPerRequest)
	for start := 0; start < len(urls); start += MaxPurgeFilesPerRequest {
		end := start + MaxPurgeFilesPerRequest
		if end > len(urls) {
			end = len(urls)
		}
		batch := PurgeBatchResult{Batch: len(results) + 1, URLCount: end - start}
		raw, err := c.PurgeCache(ctx, urls[start:end])
		if err != nil {
			batch.Error = err.Error()
		} else {
			batch.Result = raw
		}
		results = append(results, batch)
	}
	return results, nil
}

func (c *Client) zoneStatus(ctx context.Context) ZoneStatus {
	if c.zoneID == "" {
		return ZoneStatus{Configured: false, Message: "cloudflare zone_id is not configured"}
	}
	zone, err := c.Zone(ctx)
	if err != nil {
		zone.Configured = true
		zone.ID = c.zoneID
		zone.Message = err.Error()
		return zone
	}
	return zone
}

func (c *Client) dnsStatus(ctx context.Context) DNSStatus {
	out := DNSStatus{Configured: c.Configured()}
	if !c.Configured() {
		out.Message = "cloudflare zone_id/api_token not configured"
		return out
	}
	if c.rootDomain != "" {
		records, err := c.ListDNSRecords(ctx, c.rootDomain)
		if err != nil {
			out.Message = err.Error()
			return out
		}
		out.RootRecords = records
	}
	if c.siteSuffix != "" {
		records, err := c.ListDNSRecords(ctx, "*."+c.siteSuffix)
		if err != nil {
			out.Message = err.Error()
			return out
		}
		out.SiteWildcard = records
	}
	if c.rootDomain != "" {
		records, err := c.ListDNSRecords(ctx, "*."+c.rootDomain)
		if err != nil {
			out.Message = err.Error()
			return out
		}
		out.ManagedWildcard = records
	}
	out.OK = true
	return out
}

func (c *Client) workersStatus(ctx context.Context) WorkersStatus {
	out := WorkersStatus{Configured: c.Configured()}
	if !c.Configured() {
		out.Message = "cloudflare zone_id/api_token not configured"
		return out
	}
	routes, err := c.ListWorkerRoutes(ctx)
	if err != nil {
		out.Message = err.Error()
		return out
	}
	out.OK = true
	out.Routes = routes
	out.RouteCount = len(routes)
	return out
}

func (c *Client) r2Status(ctx context.Context, opts R2CheckOptions) R2Status {
	opts = normalizeR2CheckOptions(opts)
	out := R2Status{
		Configured: c.accountID != "" && c.apiToken != "",
		Bucket: R2BucketCheckStatus{
			Configured: opts.Bucket != "",
			Name:       opts.Bucket,
		},
		CORS: R2CORSStatus{
			Configured: opts.Bucket != "",
		},
		Domains: R2DomainStatus{
			Configured:    opts.Bucket != "",
			PublicBaseURL: opts.PublicBaseURL,
			PublicHost:    publicURLHost(opts.PublicBaseURL),
		},
	}
	if c.accountID == "" {
		out.Message = "cloudflare account_id is not configured"
		return out
	}
	if c.apiToken == "" {
		out.Message = "cloudflare api_token is not configured"
		return out
	}
	buckets, err := c.ListR2Buckets(ctx)
	if err != nil {
		out.Message = err.Error()
		return out
	}
	out.OK = true
	out.Buckets = buckets
	out.BucketCount = len(buckets)
	if opts.Bucket == "" {
		return out
	}
	bucket := findR2Bucket(buckets, opts.Bucket)
	if bucket == nil {
		out.OK = false
		out.Bucket.Message = "configured r2 bucket was not found in this Cloudflare account"
		return out
	}
	out.Bucket.OK = true
	out.Bucket.Bucket = bucket
	if detailed, err := c.GetR2Bucket(ctx, opts.Bucket); err == nil {
		out.Bucket.Bucket = &detailed
	} else {
		out.Bucket.Message = err.Error()
	}
	out.CORS = c.r2CORSStatus(ctx, opts.Bucket)
	out.Domains = c.r2DomainStatus(ctx, opts)
	return out
}

func (c *Client) r2CORSStatus(ctx context.Context, bucket string) R2CORSStatus {
	out := R2CORSStatus{Configured: true}
	policy, err := c.GetR2BucketCORS(ctx, bucket)
	if err != nil {
		if isMissingR2CORSConfigError(err) {
			out.Rules = []R2CORSRule{}
			out.Message = "r2 bucket has no cors rules configured"
			return out
		}
		out.Message = err.Error()
		return out
	}
	out.Rules = policy.Rules
	out.RuleCount = len(policy.Rules)
	if out.RuleCount == 0 {
		out.Message = "r2 bucket has no cors rules configured"
		return out
	}
	out.OK = true
	return out
}

func (c *Client) r2DomainStatus(ctx context.Context, opts R2CheckOptions) R2DomainStatus {
	out := R2DomainStatus{
		Configured:    true,
		PublicBaseURL: opts.PublicBaseURL,
		PublicHost:    publicURLHost(opts.PublicBaseURL),
	}
	customDomains, err := c.ListR2CustomDomains(ctx, opts.Bucket)
	if err != nil {
		out.Message = err.Error()
		return out
	}
	out.CustomDomains = customDomains
	out.CustomDomainCount = len(customDomains)
	managed, err := c.GetR2ManagedDomain(ctx, opts.Bucket)
	if err != nil {
		out.Message = err.Error()
		return out
	}
	out.ManagedDomain = &managed
	if out.PublicHost == "" {
		out.OK = true
		out.Message = "r2 public_base_url is not configured"
		return out
	}
	for _, domain := range customDomains {
		if cleanDomain(domain.Domain) != out.PublicHost {
			continue
		}
		out.MatchedDomain = domain.Domain
		if domain.Enabled && domainStatusReady(domain.Status) {
			out.OK = true
			return out
		}
		out.Message = "matched r2 custom domain is not enabled or not active"
		return out
	}
	if cleanDomain(managed.Domain) == out.PublicHost {
		out.MatchedDomain = managed.Domain
		if managed.Enabled {
			out.OK = true
			return out
		}
		out.Message = "matched r2.dev managed domain is disabled"
		return out
	}
	out.Message = "r2 public_base_url host is not attached to this bucket"
	return out
}

func (c *Client) syncR2CORS(ctx context.Context, opts SyncR2Options) R2CORSSyncResult {
	desired := R2CORSPolicy{Rules: []R2CORSRule{{
		Allowed: R2CORSAllowed{
			Methods: opts.CORSAllowedMethods,
			Origins: opts.CORSAllowedOrigins,
			Headers: opts.CORSAllowedHeaders,
		},
		ExposeHeaders: opts.CORSExposeHeaders,
		MaxAgeSeconds: opts.CORSMaxAgeSeconds,
	}}}
	result := R2CORSSyncResult{DryRun: opts.DryRun, Desired: desired}
	current, err := c.GetR2BucketCORS(ctx, opts.Bucket)
	if err != nil {
		if isMissingR2CORSConfigError(err) {
			current = R2CORSPolicy{Rules: []R2CORSRule{}}
		} else {
			result.Action = "failed"
			result.Error = err.Error()
			return result
		}
	}
	current = normalizeR2CORSPolicy(current)
	desired = normalizeR2CORSPolicy(desired)
	result.Current = &current
	result.Desired = desired
	if r2CORSPolicyEqual(current, desired) {
		result.Action = "unchanged"
		return result
	}
	if len(current.Rules) > 0 && !opts.Force {
		result.Action = "conflict"
		result.Error = "existing r2 cors policy differs; pass force to replace it"
		return result
	}
	result.Action = "put"
	if opts.DryRun {
		return result
	}
	updated, err := c.PutR2BucketCORS(ctx, opts.Bucket, desired)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Result = &updated
	return result
}

func (c *Client) syncR2Domain(ctx context.Context, opts SyncR2Options) R2DomainSyncResult {
	host := publicURLHost(opts.PublicBaseURL)
	result := R2DomainSyncResult{DryRun: opts.DryRun, Domain: host}
	if host == "" {
		result.Action = "skipped"
		return result
	}
	customDomains, err := c.ListR2CustomDomains(ctx, opts.Bucket)
	if err != nil {
		result.Action = "failed"
		result.Error = err.Error()
		return result
	}
	managed, err := c.GetR2ManagedDomain(ctx, opts.Bucket)
	if err != nil {
		result.Action = "failed"
		result.Error = err.Error()
		return result
	}
	result.CurrentManaged = &managed
	if cleanDomain(managed.Domain) == host {
		result.DomainType = "managed"
		if managed.Enabled {
			result.Action = "unchanged"
			return result
		}
		result.Action = "enable_managed"
		if opts.DryRun {
			return result
		}
		updated, err := c.UpdateR2ManagedDomain(ctx, opts.Bucket, true)
		if err != nil {
			result.Error = err.Error()
			return result
		}
		result.ResultManaged = &updated
		return result
	}
	for _, domain := range customDomains {
		if cleanDomain(domain.Domain) != host {
			continue
		}
		domainCopy := domain
		result.CurrentCustom = &domainCopy
		result.DomainType = "custom"
		if domain.Enabled && domainStatusReady(domain.Status) {
			result.Action = "unchanged"
			return result
		}
		if !opts.Force {
			result.Action = "conflict"
			result.Error = "matched r2 custom domain is not enabled or active; pass force to update it"
			return result
		}
		result.Action = "update_custom"
		if opts.DryRun {
			return result
		}
		updated, err := c.UpdateR2CustomDomain(ctx, opts.Bucket, R2CustomDomainConfig{Domain: host, Enabled: true, ZoneID: opts.ZoneID})
		if err != nil {
			result.Error = err.Error()
			return result
		}
		result.ResultCustom = &updated
		return result
	}
	if opts.ZoneID == "" {
		result.Action = "skipped"
		result.Error = "cloudflare zone_id is required to attach an r2 custom domain"
		return result
	}
	result.DomainType = "custom"
	result.Action = "create_custom"
	if opts.DryRun {
		return result
	}
	created, err := c.CreateR2CustomDomain(ctx, opts.Bucket, R2CustomDomainConfig{Domain: host, Enabled: true, ZoneID: opts.ZoneID})
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.ResultCustom = &created
	return result
}

func normalizeR2CheckOptions(opts R2CheckOptions) R2CheckOptions {
	opts.Bucket = strings.TrimSpace(opts.Bucket)
	opts.PublicBaseURL = strings.TrimRight(strings.TrimSpace(opts.PublicBaseURL), "/")
	return opts
}

func normalizeR2BucketCreateOptions(opts R2BucketCreateOptions) R2BucketCreateOptions {
	opts.Name = strings.TrimSpace(opts.Name)
	opts.LocationHint = strings.TrimSpace(opts.LocationHint)
	opts.Jurisdiction = strings.TrimSpace(opts.Jurisdiction)
	opts.StorageClass = strings.TrimSpace(opts.StorageClass)
	return opts
}

func normalizeR2ProvisionOptions(opts R2ProvisionOptions) R2ProvisionOptions {
	sync := normalizeSyncR2Options(SyncR2Options{
		Bucket:             opts.Bucket,
		PublicBaseURL:      opts.PublicBaseURL,
		ZoneID:             opts.ZoneID,
		DryRun:             opts.DryRun,
		Force:              opts.Force,
		SyncCORS:           opts.SyncCORS,
		SyncDomain:         opts.SyncDomain,
		CORSAllowedOrigins: opts.CORSAllowedOrigins,
		CORSAllowedMethods: opts.CORSAllowedMethods,
		CORSAllowedHeaders: opts.CORSAllowedHeaders,
		CORSExposeHeaders:  opts.CORSExposeHeaders,
		CORSMaxAgeSeconds:  opts.CORSMaxAgeSeconds,
	})
	opts.Bucket = sync.Bucket
	opts.PublicBaseURL = sync.PublicBaseURL
	opts.ZoneID = sync.ZoneID
	opts.CORSAllowedOrigins = sync.CORSAllowedOrigins
	opts.CORSAllowedMethods = sync.CORSAllowedMethods
	opts.CORSAllowedHeaders = sync.CORSAllowedHeaders
	opts.CORSExposeHeaders = sync.CORSExposeHeaders
	opts.CORSMaxAgeSeconds = sync.CORSMaxAgeSeconds
	opts.LocationHint = strings.TrimSpace(opts.LocationHint)
	opts.Jurisdiction = strings.TrimSpace(opts.Jurisdiction)
	opts.StorageClass = strings.TrimSpace(opts.StorageClass)
	return opts
}

func normalizeR2CredentialsOptions(opts R2CredentialsOptions) R2CredentialsOptions {
	opts.Bucket = strings.TrimSpace(opts.Bucket)
	opts.Jurisdiction = cleanTokenResourcePart(opts.Jurisdiction)
	if opts.Jurisdiction == "" {
		opts.Jurisdiction = "default"
	}
	opts.TokenName = strings.TrimSpace(opts.TokenName)
	if opts.TokenName == "" {
		name := strings.Trim(cleanTokenResourcePart(opts.Bucket), "-")
		if name == "" {
			name = "bucket"
		}
		opts.TokenName = "supercdn-r2-" + name + "-" + time.Now().UTC().Format("20060102T150405Z")
	}
	opts.PermissionGroupName = strings.TrimSpace(opts.PermissionGroupName)
	if opts.PermissionGroupName == "" {
		opts.PermissionGroupName = "Workers R2 Storage Bucket Item Write"
	}
	return opts
}

func normalizeSyncR2Options(opts SyncR2Options) SyncR2Options {
	opts.Bucket = strings.TrimSpace(opts.Bucket)
	opts.PublicBaseURL = strings.TrimRight(strings.TrimSpace(opts.PublicBaseURL), "/")
	opts.ZoneID = strings.TrimSpace(opts.ZoneID)
	opts.CORSAllowedMethods = cleanUpperValues(opts.CORSAllowedMethods)
	if len(opts.CORSAllowedMethods) == 0 {
		opts.CORSAllowedMethods = []string{"GET", "HEAD"}
	}
	opts.CORSAllowedOrigins = cleanStrings(opts.CORSAllowedOrigins)
	if len(opts.CORSAllowedOrigins) == 0 {
		opts.CORSAllowedOrigins = []string{"*"}
	}
	opts.CORSAllowedHeaders = cleanStrings(opts.CORSAllowedHeaders)
	if len(opts.CORSAllowedHeaders) == 0 {
		opts.CORSAllowedHeaders = []string{"*"}
	}
	opts.CORSExposeHeaders = cleanStrings(opts.CORSExposeHeaders)
	if len(opts.CORSExposeHeaders) == 0 {
		opts.CORSExposeHeaders = []string{"ETag", "Content-Length", "Content-Type", "Cache-Control"}
	}
	if opts.CORSMaxAgeSeconds == 0 {
		opts.CORSMaxAgeSeconds = 86400
	}
	return opts
}

func provisionSyncOptions(opts R2ProvisionOptions) SyncR2Options {
	return SyncR2Options{
		Bucket:             opts.Bucket,
		PublicBaseURL:      opts.PublicBaseURL,
		ZoneID:             opts.ZoneID,
		DryRun:             opts.DryRun,
		Force:              opts.Force,
		SyncCORS:           opts.SyncCORS,
		SyncDomain:         opts.SyncDomain,
		CORSAllowedOrigins: opts.CORSAllowedOrigins,
		CORSAllowedMethods: opts.CORSAllowedMethods,
		CORSAllowedHeaders: opts.CORSAllowedHeaders,
		CORSExposeHeaders:  opts.CORSExposeHeaders,
		CORSMaxAgeSeconds:  opts.CORSMaxAgeSeconds,
	}
}

func plannedR2PostCreateSync(opts R2ProvisionOptions) R2SyncResult {
	result := R2SyncResult{
		Bucket:        opts.Bucket,
		PublicBaseURL: opts.PublicBaseURL,
		DryRun:        true,
		Force:         opts.Force,
		Status:        "planned",
		Warnings:      []string{"bucket does not exist yet; CORS/domain will be applied after bucket creation"},
	}
	if opts.SyncCORS {
		result.CORS = &R2CORSSyncResult{
			Action: "put",
			DryRun: true,
			Desired: R2CORSPolicy{Rules: []R2CORSRule{{
				Allowed: R2CORSAllowed{
					Methods: opts.CORSAllowedMethods,
					Origins: opts.CORSAllowedOrigins,
					Headers: opts.CORSAllowedHeaders,
				},
				ExposeHeaders: opts.CORSExposeHeaders,
				MaxAgeSeconds: opts.CORSMaxAgeSeconds,
			}}},
		}
	}
	if opts.SyncDomain {
		host := publicURLHost(opts.PublicBaseURL)
		domain := R2DomainSyncResult{Action: "skipped", DryRun: true, Domain: host}
		if host != "" {
			domain.DomainType = "custom"
			domain.Action = "create_custom"
			if strings.HasSuffix(host, ".r2.dev") {
				domain.DomainType = "managed"
				domain.Action = "enable_managed"
			}
			if domain.DomainType == "custom" && opts.ZoneID == "" {
				domain.Action = "skipped"
				domain.Error = "cloudflare zone_id is required to attach an r2 custom domain"
				result.Status = "partial"
				result.Errors = append(result.Errors, "domain: "+domain.Error)
			}
		}
		result.Domain = &domain
	}
	return result
}

func provisionStatusFromSync(sync R2SyncResult, dryRun bool) string {
	if sync.Status == "failed" || sync.Status == "partial" || sync.Status == "planned" {
		return sync.Status
	}
	if dryRun && ((sync.CORS != nil && sync.CORS.Action != "unchanged" && sync.CORS.Action != "skipped") || (sync.Domain != nil && sync.Domain.Action != "unchanged" && sync.Domain.Action != "skipped")) {
		return "planned"
	}
	return "ok"
}

func publicURLHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		parsed, err = url.Parse("https://" + raw)
		if err != nil {
			return ""
		}
	}
	return cleanDomain(parsed.Hostname())
}

func findR2Bucket(buckets []R2Bucket, name string) *R2Bucket {
	name = strings.TrimSpace(name)
	for i := range buckets {
		if buckets[i].Name == name {
			bucket := buckets[i]
			return &bucket
		}
	}
	return nil
}

func normalizeR2CORSPolicy(policy R2CORSPolicy) R2CORSPolicy {
	rules := make([]R2CORSRule, 0, len(policy.Rules))
	for _, rule := range policy.Rules {
		rule.Allowed.Methods = cleanUpperValues(rule.Allowed.Methods)
		rule.Allowed.Origins = cleanStrings(rule.Allowed.Origins)
		rule.Allowed.Headers = cleanStrings(rule.Allowed.Headers)
		rule.ExposeHeaders = cleanStrings(rule.ExposeHeaders)
		rules = append(rules, rule)
	}
	return R2CORSPolicy{Rules: rules}
}

func r2CORSPolicyEqual(a, b R2CORSPolicy) bool {
	rawA, _ := json.Marshal(normalizeR2CORSPolicy(a))
	rawB, _ := json.Marshal(normalizeR2CORSPolicy(b))
	return bytes.Equal(rawA, rawB)
}

func cleanStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func cleanUpperValues(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToUpper(strings.TrimSpace(value))
		if value != "" {
			out = append(out, value)
		}
	}
	return cleanStrings(out)
}

func cleanDomain(v string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(v)), ".")
}

func domainStatusReady(status R2DomainValidationStatus) bool {
	ownership := strings.ToLower(strings.TrimSpace(status.Ownership))
	ssl := strings.ToLower(strings.TrimSpace(status.SSL))
	ownershipOK := ownership == "" || ownership == "active" || ownership == "verified"
	sslOK := ssl == "" || ssl == "active"
	return ownershipOK && sslOK
}

func (c *Client) r2BucketTokenResource(bucket, jurisdiction string) string {
	accountID := cleanTokenResourcePart(c.accountID)
	jurisdiction = cleanTokenResourcePart(jurisdiction)
	if jurisdiction == "" {
		jurisdiction = "default"
	}
	bucket = cleanTokenResourcePart(bucket)
	return "com.cloudflare.edge.r2.bucket." + accountID + "_" + jurisdiction + "_" + bucket
}

func findTokenPermissionGroup(groups []TokenPermissionGroup, name string) *TokenPermissionGroup {
	name = strings.ToLower(strings.TrimSpace(name))
	for i := range groups {
		if strings.ToLower(strings.TrimSpace(groups[i].Name)) == name {
			group := groups[i]
			return &group
		}
	}
	return nil
}

func randomPolicyID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", raw[:]), nil
}

func cleanTokenResourcePart(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	var b strings.Builder
	for _, r := range v {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isMissingR2CORSConfigError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "10059") || strings.Contains(msg, "cors configuration does not exist")
}

func (c *Client) get(ctx context.Context, pathValue string, q url.Values, dst any) error {
	return c.do(ctx, http.MethodGet, pathValue, q, nil, dst)
}

func (c *Client) post(ctx context.Context, pathValue string, body []byte, dst any) error {
	return c.do(ctx, http.MethodPost, pathValue, nil, body, dst)
}

func (c *Client) postWithHeaders(ctx context.Context, pathValue string, body []byte, headers http.Header, dst any) error {
	return c.doWithHeaders(ctx, http.MethodPost, pathValue, nil, body, headers, dst)
}

func (c *Client) put(ctx context.Context, pathValue string, body []byte, dst any) error {
	return c.do(ctx, http.MethodPut, pathValue, nil, body, dst)
}

func (c *Client) do(ctx context.Context, method, pathValue string, q url.Values, body []byte, dst any) error {
	return c.doWithHeaders(ctx, method, pathValue, q, body, nil, dst)
}

func (c *Client) doWithHeaders(ctx context.Context, method, pathValue string, q url.Values, body []byte, headers http.Header, dst any) error {
	if c.apiToken == "" {
		return fmt.Errorf("cloudflare api_token is not configured")
	}
	u := strings.TrimRight(c.baseURL, "/") + pathValue
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	if body != nil && headers.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("cloudflare api failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("decode cloudflare response: %w", err)
	}
	if !env.Success {
		return fmt.Errorf("cloudflare api failed: %s", cloudflareMessages(env.Errors))
	}
	if dst == nil {
		return nil
	}
	if rawDst, ok := dst.(*json.RawMessage); ok {
		*rawDst = append((*rawDst)[:0], env.Result...)
		return nil
	}
	if len(env.Result) == 0 || string(env.Result) == "null" {
		return nil
	}
	return json.Unmarshal(env.Result, dst)
}

func cloudflareMessages(values []cloudflareError) string {
	if len(values) == 0 {
		return "unknown error"
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		if value.Code != 0 {
			parts = append(parts, fmt.Sprintf("%d: %s", value.Code, value.Message))
		} else {
			parts = append(parts, value.Message)
		}
	}
	return strings.Join(parts, "; ")
}

func cleanPatterns(patterns []string) []string {
	out := make([]string, 0, len(patterns))
	seen := map[string]bool{}
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" || seen[pattern] {
			continue
		}
		seen[pattern] = true
		out = append(out, pattern)
	}
	return out
}

func findWorkerRoute(routes []WorkerRoute, pattern string) *WorkerRoute {
	for i := range routes {
		if strings.EqualFold(routes[i].Pattern, pattern) {
			return &routes[i]
		}
	}
	return nil
}

func uniqueStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func cleanDNSRecords(records []DNSRecord) []DNSRecord {
	out := make([]DNSRecord, 0, len(records))
	seen := map[string]bool{}
	for _, record := range records {
		record = normalizeDNSRecord(record)
		if record.Name == "" || record.Type == "" || record.Content == "" {
			continue
		}
		key := record.Type + "\x00" + strings.ToLower(record.Name)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, record)
	}
	return out
}

func normalizeDNSRecord(record DNSRecord) DNSRecord {
	record.Type = strings.ToUpper(strings.TrimSpace(record.Type))
	record.Name = strings.Trim(strings.ToLower(strings.TrimSpace(record.Name)), ".")
	record.Content = strings.TrimSpace(record.Content)
	record.Content = strings.Trim(record.Content, ".")
	if record.TTL <= 0 {
		record.TTL = 1
	}
	return record
}

func findDNSRecord(records []DNSRecord, recordType string) *DNSRecord {
	recordType = strings.ToUpper(strings.TrimSpace(recordType))
	for i := range records {
		if strings.EqualFold(records[i].Type, recordType) {
			return &records[i]
		}
	}
	return nil
}

func dnsRecordMatches(existing, desired DNSRecord) bool {
	existing = normalizeDNSRecord(existing)
	desired = normalizeDNSRecord(desired)
	return strings.EqualFold(existing.Type, desired.Type) &&
		strings.EqualFold(existing.Name, desired.Name) &&
		strings.EqualFold(existing.Content, desired.Content) &&
		existing.Proxied == desired.Proxied
}

func conflictsWithDNSRecord(existing []DNSRecord, desired DNSRecord) bool {
	if len(existing) == 0 {
		return false
	}
	if strings.EqualFold(desired.Type, "CNAME") {
		return true
	}
	for _, record := range existing {
		if strings.EqualFold(record.Type, "CNAME") {
			return true
		}
	}
	return false
}
