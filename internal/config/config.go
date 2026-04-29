package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Server              ServerConfig              `json:"server"`
	Database            DatabaseConfig            `json:"database"`
	Storage             []StorageConfig           `json:"storage"`
	MountPoints         []MountPointConfig        `json:"mount_points"`
	ResourceLibraries   []ResourceLibraryConfig   `json:"resource_libraries"`
	RouteProfiles       []RouteProfile            `json:"route_profiles"`
	Cloudflare          CloudflareConfig          `json:"cloudflare"`
	CloudflareAccounts  []CloudflareAccountConfig `json:"cloudflare_accounts"`
	CloudflareLibraries []CloudflareLibraryConfig `json:"cloudflare_libraries"`
	Limits              LimitsConfig              `json:"limits"`
}

type ServerConfig struct {
	Addr          string `json:"addr"`
	PublicBaseURL string `json:"public_base_url"`
	DataDir       string `json:"data_dir"`
	AdminToken    string `json:"admin_token"`
}

type DatabaseConfig struct {
	Path string `json:"path"`
}

type LimitsConfig struct {
	MaxUploadBytes                   int64 `json:"max_upload_bytes"`
	MaxActiveTransfers               int   `json:"max_active_transfers"`
	DefaultMaxSiteFiles              int   `json:"default_max_site_files"`
	ResourceHealthMinIntervalSeconds int   `json:"resource_health_min_interval_seconds"`
	OverclockMode                    bool  `json:"overclock_mode"`
}

type CloudflareConfig struct {
	AccountID        string `json:"account_id"`
	ZoneID           string `json:"zone_id"`
	APIToken         string `json:"api_token"`
	RootDomain       string `json:"root_domain"`
	SiteDomainSuffix string `json:"site_domain_suffix"`
	SiteDNSTarget    string `json:"site_dns_target"`
	WorkerScript     string `json:"worker_script"`
	EdgeBypassSecret string `json:"edge_bypass_secret"`
}

type CloudflareAccountConfig struct {
	Name             string   `json:"name"`
	Default          bool     `json:"default"`
	AccountID        string   `json:"account_id"`
	ZoneID           string   `json:"zone_id"`
	APIToken         string   `json:"api_token"`
	RootDomain       string   `json:"root_domain"`
	SiteDomainSuffix string   `json:"site_domain_suffix"`
	SiteDNSTarget    string   `json:"site_dns_target"`
	WorkerScript     string   `json:"worker_script"`
	EdgeBypassSecret string   `json:"edge_bypass_secret"`
	R2               R2Config `json:"r2"`
}

type CloudflareLibraryConfig struct {
	Name     string                     `json:"name"`
	Policy   ResourceLibraryPolicy      `json:"policy"`
	Bindings []CloudflareLibraryBinding `json:"bindings"`
}

type CloudflareLibraryBinding struct {
	Name        string                            `json:"name"`
	Account     string                            `json:"account"`
	Path        string                            `json:"path,omitempty"`
	Constraints ResourceLibraryBindingConstraints `json:"constraints,omitempty"`
	Notes       string                            `json:"notes,omitempty"`
}

type RouteProfile struct {
	Name                string   `json:"name"`
	Primary             string   `json:"primary"`
	Backups             []string `json:"backups"`
	DefaultCacheControl string   `json:"default_cache_control"`
	AllowRedirect       bool     `json:"allow_redirect"`
	DeploymentTarget    string   `json:"deployment_target"`
}

type StorageConfig struct {
	Name   string       `json:"name"`
	Type   string       `json:"type"`
	Local  LocalConfig  `json:"local"`
	R2     R2Config     `json:"r2"`
	AList  AListConfig  `json:"alist"`
	Pinata PinataConfig `json:"pinata"`
}

type MountPointConfig struct {
	Name  string      `json:"name"`
	Type  string      `json:"type"`
	AList AListConfig `json:"alist"`
}

type ResourceLibraryConfig struct {
	Name     string                   `json:"name"`
	Policy   ResourceLibraryPolicy    `json:"policy"`
	Bindings []ResourceLibraryBinding `json:"bindings"`
}

type ResourceLibraryPolicy struct {
	MaxBindings        *int64 `json:"max_bindings,omitempty"`
	TotalCapacityBytes *int64 `json:"total_capacity_bytes,omitempty"`
	AvailableBytes     *int64 `json:"available_bytes,omitempty"`
	ReserveBytes       *int64 `json:"reserve_bytes,omitempty"`
	Notes              string `json:"notes,omitempty"`
}

type ResourceLibraryBinding struct {
	Name        string                            `json:"name"`
	MountPoint  string                            `json:"mount_point"`
	Path        string                            `json:"path"`
	Constraints ResourceLibraryBindingConstraints `json:"constraints"`
}

type ResourceLibraryBindingConstraints struct {
	MaxCapacityBytes          *int64 `json:"max_capacity_bytes,omitempty"`
	PeakBandwidthMbps         *int64 `json:"peak_bandwidth_mbps,omitempty"`
	MaxBatchFiles             *int   `json:"max_batch_files,omitempty"`
	MaxFileSizeBytes          *int64 `json:"max_file_size_bytes,omitempty"`
	DailyUploadLimitBytes     *int64 `json:"daily_upload_limit_bytes,omitempty"`
	DailyUploadLimitUnlimited bool   `json:"daily_upload_limit_unlimited,omitempty"`
	SupportsOnlineExtract     *bool  `json:"supports_online_extract,omitempty"`
	MaxOnlineExtractBytes     *int64 `json:"max_online_extract_bytes,omitempty"`
	Notes                     string `json:"notes,omitempty"`
}

type LocalConfig struct {
	Root string `json:"root"`
}

type R2Config struct {
	AccountID       string `json:"account_id"`
	APIToken        string `json:"api_token"`
	Bucket          string `json:"bucket"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	Endpoint        string `json:"endpoint"`
	PublicBaseURL   string `json:"public_base_url"`
	ProxyURL        string `json:"proxy_url"`
}

type AListConfig struct {
	BaseURL       string `json:"base_url"`
	Token         string `json:"token"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	Root          string `json:"root"`
	UseProxyURL   bool   `json:"use_proxy_url"`
	PublicBaseURL string `json:"public_base_url"`
	ProxyURL      string `json:"proxy_url"`
}

type PinataConfig struct {
	JWT            string `json:"jwt"`
	GatewayBaseURL string `json:"gateway_base_url"`
	ProxyURL       string `json:"proxy_url"`
}

func Load(path string) (*Config, error) {
	if path == "" {
		path = os.Getenv("SUPERCDN_CONFIG")
	}
	if path == "" {
		path = "config.json"
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})
	expanded := os.ExpandEnv(string(raw))
	var cfg Config
	if err := json.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, err
	}
	if err := cfg.ApplyDefaults(filepath.Dir(path)); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) ApplyDefaults(baseDir string) error {
	if c.Server.Addr == "" {
		c.Server.Addr = "127.0.0.1:8080"
	}
	if c.Server.DataDir == "" {
		c.Server.DataDir = "data"
	}
	c.Server.DataDir = absPath(baseDir, c.Server.DataDir)
	if c.Database.Path == "" {
		c.Database.Path = filepath.Join(c.Server.DataDir, "supercdn.db")
	}
	c.Database.Path = absPath(baseDir, c.Database.Path)
	if c.Limits.MaxUploadBytes == 0 {
		c.Limits.MaxUploadBytes = 2 << 30
	}
	if c.Limits.MaxActiveTransfers == 0 {
		c.Limits.MaxActiveTransfers = 5
	}
	if c.Limits.DefaultMaxSiteFiles == 0 {
		c.Limits.DefaultMaxSiteFiles = 5
	}
	if c.Limits.ResourceHealthMinIntervalSeconds == 0 {
		c.Limits.ResourceHealthMinIntervalSeconds = 300
	}
	if c.Limits.MaxUploadBytes < 0 {
		return errors.New("limits.max_upload_bytes must be non-negative")
	}
	if c.Limits.MaxActiveTransfers < 0 {
		return errors.New("limits.max_active_transfers must be non-negative")
	}
	if c.Limits.DefaultMaxSiteFiles < 0 {
		return errors.New("limits.default_max_site_files must be non-negative")
	}
	if c.Limits.ResourceHealthMinIntervalSeconds < 0 {
		return errors.New("limits.resource_health_min_interval_seconds must be non-negative")
	}
	if c.Server.AdminToken == "" {
		return errors.New("server.admin_token is required")
	}
	if len(c.Storage) == 0 {
		return errors.New("at least one storage target is required")
	}
	if len(c.RouteProfiles) == 0 {
		return errors.New("at least one route profile is required")
	}
	stores := map[string]bool{}
	for i := range c.Storage {
		s := &c.Storage[i]
		s.Type = strings.ToLower(strings.TrimSpace(s.Type))
		if s.Name == "" {
			return fmt.Errorf("storage[%d].name is required", i)
		}
		if s.Type == "" {
			return fmt.Errorf("storage[%s].type is required", s.Name)
		}
		stores[s.Name] = true
		if s.Type == "local" {
			if s.Local.Root == "" {
				s.Local.Root = filepath.Join(c.Server.DataDir, "objects", s.Name)
			}
			s.Local.Root = absPath(baseDir, s.Local.Root)
		}
	}
	mounts := map[string]bool{}
	for i := range c.MountPoints {
		m := &c.MountPoints[i]
		m.Type = strings.ToLower(strings.TrimSpace(m.Type))
		if m.Name == "" {
			return fmt.Errorf("mount_points[%d].name is required", i)
		}
		if m.Type == "" {
			return fmt.Errorf("mount_point %q type is required", m.Name)
		}
		if m.Type != "alist" {
			return fmt.Errorf("mount_point %q has unsupported type %q", m.Name, m.Type)
		}
		mounts[m.Name] = true
	}
	for i, lib := range c.ResourceLibraries {
		if lib.Name == "" {
			return fmt.Errorf("resource_libraries[%d].name is required", i)
		}
		if stores[lib.Name] {
			return fmt.Errorf("resource library %q conflicts with storage target name", lib.Name)
		}
		if len(lib.Bindings) == 0 {
			return fmt.Errorf("resource library %q must bind exactly one path per binding and at least one binding is required", lib.Name)
		}
		if err := validateResourceLibraryPolicy(lib.Name, lib.Policy, len(lib.Bindings), c.Limits.OverclockMode); err != nil {
			return err
		}
		for j, binding := range lib.Bindings {
			if binding.MountPoint == "" {
				return fmt.Errorf("resource library %q binding[%d].mount_point is required", lib.Name, j)
			}
			if !mounts[binding.MountPoint] {
				return fmt.Errorf("resource library %q references missing mount point %q", lib.Name, binding.MountPoint)
			}
			if strings.TrimSpace(binding.Path) == "" {
				return fmt.Errorf("resource library %q binding[%d].path is required", lib.Name, j)
			}
			if err := validateBindingConstraints(lib.Name, j, binding.Constraints); err != nil {
				return err
			}
		}
		stores[lib.Name] = true
	}
	if err := c.normalizeCloudflareTopology(); err != nil {
		return err
	}
	for _, library := range c.CloudflareLibrariesEffective() {
		if stores[library.Name] {
			return fmt.Errorf("cloudflare library %q conflicts with existing storage target name", library.Name)
		}
		if c.CloudflareLibraryHasStorage(library) {
			stores[library.Name] = true
		}
	}
	for i := range c.RouteProfiles {
		p := &c.RouteProfiles[i]
		if p.Name == "" {
			return errors.New("route profile name is required")
		}
		if !stores[p.Primary] {
			return fmt.Errorf("route profile %q references missing primary storage %q", p.Name, p.Primary)
		}
		for _, b := range p.Backups {
			if !stores[b] {
				return fmt.Errorf("route profile %q references missing backup storage %q", p.Name, b)
			}
		}
		target, err := normalizeDeploymentTarget(p.DeploymentTarget)
		if err != nil {
			return fmt.Errorf("route profile %q deployment_target: %w", p.Name, err)
		}
		p.DeploymentTarget = target
	}
	return os.MkdirAll(c.Server.DataDir, 0o755)
}

func normalizeDeploymentTarget(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "", nil
	}
	switch value {
	case "origin", "go_origin", "origin_assisted":
		return "origin_assisted", nil
	case "cloudflare", "cloudflare_static", "workers_static", "workers_assets", "pages":
		return "cloudflare_static", nil
	case "hybrid", "hybrid_edge", "edge":
		return "hybrid_edge", nil
	default:
		return "", fmt.Errorf("must be origin_assisted, cloudflare_static or hybrid_edge")
	}
}

func (c *Config) Profile(name string) (RouteProfile, bool) {
	for _, p := range c.RouteProfiles {
		if p.Name == name {
			return p, true
		}
	}
	return RouteProfile{}, false
}

func (c *Config) StorageByName(name string) (StorageConfig, bool) {
	for _, s := range c.Storage {
		if s.Name == name {
			return s, true
		}
	}
	return StorageConfig{}, false
}

func (c *Config) DefaultCloudflareAccount() (CloudflareAccountConfig, bool) {
	for _, account := range c.CloudflareAccounts {
		if account.Default {
			return account, true
		}
	}
	if len(c.CloudflareAccounts) > 0 {
		return c.CloudflareAccounts[0], true
	}
	if cfg := normalizeCloudflareConfig(c.Cloudflare); cloudflareConfigPresent(cfg) {
		return cloudflareAccountFromConfig("default", true, cfg), true
	}
	return CloudflareAccountConfig{}, false
}

func (c *Config) CloudflareAccountByName(name string) (CloudflareAccountConfig, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return c.DefaultCloudflareAccount()
	}
	for _, account := range c.CloudflareAccounts {
		if account.Name == name {
			return account, true
		}
	}
	if name == "default" {
		return c.DefaultCloudflareAccount()
	}
	return CloudflareAccountConfig{}, false
}

func (c *Config) CloudflareLibraryByName(name string) (CloudflareLibraryConfig, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		if len(c.CloudflareLibraries) > 0 {
			return c.CloudflareLibraries[0], true
		}
		return CloudflareLibraryConfig{}, false
	}
	for _, library := range c.CloudflareLibraries {
		if library.Name == name {
			return library, true
		}
	}
	if len(c.CloudflareLibraries) == 0 {
		if account, ok := c.DefaultCloudflareAccount(); ok && (name == "" || name == "overseas_accel") {
			return CloudflareLibraryConfig{
				Name: "overseas_accel",
				Bindings: []CloudflareLibraryBinding{{
					Name:    account.Name,
					Account: account.Name,
				}},
			}, true
		}
	}
	return CloudflareLibraryConfig{}, false
}

func (c *Config) CloudflareAccountsEffective() []CloudflareAccountConfig {
	if len(c.CloudflareAccounts) > 0 {
		return c.CloudflareAccounts
	}
	if account, ok := c.DefaultCloudflareAccount(); ok {
		return []CloudflareAccountConfig{account}
	}
	return nil
}

func (c *Config) CloudflareLibrariesEffective() []CloudflareLibraryConfig {
	if len(c.CloudflareLibraries) > 0 {
		return c.CloudflareLibraries
	}
	if library, ok := c.CloudflareLibraryByName(""); ok {
		return []CloudflareLibraryConfig{library}
	}
	return nil
}

func (c *Config) CloudflareLibraryHasStorage(library CloudflareLibraryConfig) bool {
	for _, binding := range library.Bindings {
		account, ok := c.CloudflareAccountByName(binding.Account)
		if ok && cloudflareAccountHasR2Config(account) {
			return true
		}
	}
	return false
}

func (a CloudflareAccountConfig) ToCloudflareConfig() CloudflareConfig {
	return CloudflareConfig{
		AccountID:        a.AccountID,
		ZoneID:           a.ZoneID,
		APIToken:         a.APIToken,
		RootDomain:       a.RootDomain,
		SiteDomainSuffix: a.SiteDomainSuffix,
		SiteDNSTarget:    a.SiteDNSTarget,
		WorkerScript:     a.WorkerScript,
		EdgeBypassSecret: a.EdgeBypassSecret,
	}
}

func (a CloudflareAccountConfig) ToCloudflareR2Config() CloudflareConfig {
	cfg := a.ToCloudflareConfig()
	cfg.APIToken = firstNonEmpty(strings.TrimSpace(a.R2.APIToken), strings.TrimSpace(a.APIToken))
	cfg.AccountID = firstNonEmpty(strings.TrimSpace(a.R2.AccountID), strings.TrimSpace(a.AccountID))
	return cfg
}

func absPath(baseDir, p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Clean(filepath.Join(baseDir, p))
}

func (c *Config) normalizeCloudflareTopology() error {
	c.Cloudflare = normalizeCloudflareConfig(c.Cloudflare)
	if len(c.CloudflareAccounts) == 0 && cloudflareConfigPresent(c.Cloudflare) {
		c.CloudflareAccounts = []CloudflareAccountConfig{cloudflareAccountFromConfig("default", true, c.Cloudflare)}
	}
	accountNames := map[string]bool{}
	defaultCount := 0
	for i := range c.CloudflareAccounts {
		account := &c.CloudflareAccounts[i]
		account.Name = strings.TrimSpace(account.Name)
		if account.Name == "" {
			return fmt.Errorf("cloudflare_accounts[%d].name is required", i)
		}
		if accountNames[account.Name] {
			return fmt.Errorf("duplicate cloudflare account %q", account.Name)
		}
		accountNames[account.Name] = true
		if account.Default {
			defaultCount++
		}
		normalized := normalizeCloudflareConfig(account.ToCloudflareConfig())
		account.AccountID = normalized.AccountID
		account.ZoneID = normalized.ZoneID
		account.APIToken = normalized.APIToken
		account.RootDomain = normalized.RootDomain
		account.SiteDomainSuffix = normalized.SiteDomainSuffix
		account.SiteDNSTarget = normalized.SiteDNSTarget
		account.WorkerScript = normalized.WorkerScript
		account.EdgeBypassSecret = normalized.EdgeBypassSecret
		account.R2 = normalizeR2Config(account.R2, account.AccountID)
		if err := validateCloudflareAccountR2Config(*account); err != nil {
			return err
		}
	}
	if defaultCount > 1 {
		return errors.New("only one cloudflare account can be marked default")
	}
	if len(c.CloudflareAccounts) > 0 && defaultCount == 0 {
		c.CloudflareAccounts[0].Default = true
	}
	if account, ok := c.DefaultCloudflareAccount(); ok {
		c.Cloudflare = account.ToCloudflareConfig()
	}
	if len(c.CloudflareLibraries) == 0 && len(c.CloudflareAccounts) > 0 {
		bindings := make([]CloudflareLibraryBinding, 0, len(c.CloudflareAccounts))
		for _, account := range c.CloudflareAccounts {
			bindings = append(bindings, CloudflareLibraryBinding{
				Name:    account.Name,
				Account: account.Name,
			})
		}
		c.CloudflareLibraries = []CloudflareLibraryConfig{{
			Name:     "overseas_accel",
			Bindings: bindings,
		}}
	}
	for i := range c.CloudflareLibraries {
		library := &c.CloudflareLibraries[i]
		library.Name = strings.TrimSpace(library.Name)
		if library.Name == "" {
			return fmt.Errorf("cloudflare_libraries[%d].name is required", i)
		}
		if len(library.Bindings) == 0 {
			return fmt.Errorf("cloudflare library %q requires at least one account binding", library.Name)
		}
		if err := validateResourceLibraryPolicy("cloudflare:"+library.Name, library.Policy, len(library.Bindings), c.Limits.OverclockMode); err != nil {
			return err
		}
		seenBindings := map[string]bool{}
		for j := range library.Bindings {
			binding := &library.Bindings[j]
			binding.Name = strings.TrimSpace(binding.Name)
			binding.Account = strings.TrimSpace(binding.Account)
			binding.Path = normalizeCloudflareLibraryPath(binding.Path)
			if binding.Account == "" {
				return fmt.Errorf("cloudflare library %q binding[%d].account is required", library.Name, j)
			}
			if !accountNames[binding.Account] {
				return fmt.Errorf("cloudflare library %q references missing account %q", library.Name, binding.Account)
			}
			if binding.Name == "" {
				binding.Name = binding.Account
			}
			if seenBindings[binding.Name] {
				return fmt.Errorf("cloudflare library %q has duplicate binding %q", library.Name, binding.Name)
			}
			if err := validateBindingConstraints("cloudflare:"+library.Name, j, binding.Constraints); err != nil {
				return err
			}
			seenBindings[binding.Name] = true
		}
	}
	return nil
}

func normalizeCloudflareConfig(cfg CloudflareConfig) CloudflareConfig {
	cfg.AccountID = strings.TrimSpace(cfg.AccountID)
	cfg.ZoneID = strings.TrimSpace(cfg.ZoneID)
	cfg.APIToken = strings.TrimSpace(cfg.APIToken)
	cfg.RootDomain = cleanDomain(cfg.RootDomain)
	cfg.SiteDomainSuffix = cleanDomain(cfg.SiteDomainSuffix)
	cfg.SiteDNSTarget = cleanDomain(cfg.SiteDNSTarget)
	cfg.WorkerScript = strings.TrimSpace(cfg.WorkerScript)
	cfg.EdgeBypassSecret = strings.TrimSpace(cfg.EdgeBypassSecret)
	if cfg.SiteDomainSuffix == "" && cfg.RootDomain != "" {
		cfg.SiteDomainSuffix = "sites." + cfg.RootDomain
	}
	return cfg
}

func cloudflareConfigPresent(cfg CloudflareConfig) bool {
	return cfg.AccountID != "" || cfg.ZoneID != "" || cfg.APIToken != "" || cfg.RootDomain != "" || cfg.SiteDomainSuffix != "" || cfg.SiteDNSTarget != "" || cfg.WorkerScript != "" || cfg.EdgeBypassSecret != ""
}

func cloudflareAccountFromConfig(name string, isDefault bool, cfg CloudflareConfig) CloudflareAccountConfig {
	return CloudflareAccountConfig{
		Name:             name,
		Default:          isDefault,
		AccountID:        cfg.AccountID,
		ZoneID:           cfg.ZoneID,
		APIToken:         cfg.APIToken,
		RootDomain:       cfg.RootDomain,
		SiteDomainSuffix: cfg.SiteDomainSuffix,
		SiteDNSTarget:    cfg.SiteDNSTarget,
		WorkerScript:     cfg.WorkerScript,
		EdgeBypassSecret: cfg.EdgeBypassSecret,
		R2: R2Config{
			AccountID: cfg.AccountID,
		},
	}
}

func normalizeR2Config(cfg R2Config, fallbackAccountID string) R2Config {
	cfg.AccountID = strings.TrimSpace(cfg.AccountID)
	if cfg.AccountID == "" {
		cfg.AccountID = strings.TrimSpace(fallbackAccountID)
	}
	cfg.APIToken = strings.TrimSpace(cfg.APIToken)
	cfg.Bucket = strings.TrimSpace(cfg.Bucket)
	cfg.AccessKeyID = strings.TrimSpace(cfg.AccessKeyID)
	cfg.SecretAccessKey = strings.TrimSpace(cfg.SecretAccessKey)
	cfg.Endpoint = strings.TrimSpace(cfg.Endpoint)
	cfg.PublicBaseURL = strings.TrimSpace(cfg.PublicBaseURL)
	cfg.ProxyURL = strings.TrimSpace(cfg.ProxyURL)
	return cfg
}

func normalizeCloudflareLibraryPath(v string) string {
	v = strings.Trim(strings.ReplaceAll(strings.TrimSpace(v), "\\", "/"), "/")
	if v == "" {
		return "/"
	}
	return "/" + v
}

func cloudflareAccountHasR2Config(account CloudflareAccountConfig) bool {
	return strings.TrimSpace(account.R2.Bucket) != "" &&
		strings.TrimSpace(account.R2.AccessKeyID) != "" &&
		strings.TrimSpace(account.R2.SecretAccessKey) != ""
}

func validateCloudflareAccountR2Config(account CloudflareAccountConfig) error {
	r2 := account.R2
	hasDataPlane := strings.TrimSpace(r2.AccessKeyID) != "" ||
		strings.TrimSpace(r2.SecretAccessKey) != "" ||
		strings.TrimSpace(r2.Endpoint) != "" ||
		strings.TrimSpace(r2.ProxyURL) != ""
	if !hasDataPlane {
		return nil
	}
	if strings.TrimSpace(r2.Bucket) == "" {
		return fmt.Errorf("cloudflare account %q r2.bucket is required when r2 data-plane credentials are configured", account.Name)
	}
	if strings.TrimSpace(r2.AccessKeyID) == "" {
		return fmt.Errorf("cloudflare account %q r2.access_key_id is required when r2 data-plane credentials are configured", account.Name)
	}
	if strings.TrimSpace(r2.SecretAccessKey) == "" {
		return fmt.Errorf("cloudflare account %q r2.secret_access_key is required when r2 data-plane credentials are configured", account.Name)
	}
	return nil
}

func validateBindingConstraints(library string, index int, c ResourceLibraryBindingConstraints) error {
	prefix := fmt.Sprintf("resource library %q binding[%d]", library, index)
	if err := nonNegativeInt64(prefix+".max_capacity_bytes", c.MaxCapacityBytes); err != nil {
		return err
	}
	if err := nonNegativeInt64(prefix+".peak_bandwidth_mbps", c.PeakBandwidthMbps); err != nil {
		return err
	}
	if err := nonNegativeInt(prefix+".max_batch_files", c.MaxBatchFiles); err != nil {
		return err
	}
	if err := nonNegativeInt64(prefix+".max_file_size_bytes", c.MaxFileSizeBytes); err != nil {
		return err
	}
	if err := nonNegativeInt64(prefix+".daily_upload_limit_bytes", c.DailyUploadLimitBytes); err != nil {
		return err
	}
	if err := nonNegativeInt64(prefix+".max_online_extract_bytes", c.MaxOnlineExtractBytes); err != nil {
		return err
	}
	if c.DailyUploadLimitUnlimited && c.DailyUploadLimitBytes != nil {
		return fmt.Errorf("%s daily upload limit cannot be both unlimited and byte-limited", prefix)
	}
	return nil
}

func validateResourceLibraryPolicy(library string, p ResourceLibraryPolicy, bindingCount int, overclockMode bool) error {
	prefix := fmt.Sprintf("resource library %q policy", library)
	if err := nonNegativeInt64(prefix+".max_bindings", p.MaxBindings); err != nil {
		return err
	}
	if err := nonNegativeInt64(prefix+".total_capacity_bytes", p.TotalCapacityBytes); err != nil {
		return err
	}
	if err := nonNegativeInt64(prefix+".available_bytes", p.AvailableBytes); err != nil {
		return err
	}
	if err := nonNegativeInt64(prefix+".reserve_bytes", p.ReserveBytes); err != nil {
		return err
	}
	if !overclockMode && p.MaxBindings != nil && int64(bindingCount) > *p.MaxBindings {
		return fmt.Errorf("%s max_bindings is %d, got %d bindings", prefix, *p.MaxBindings, bindingCount)
	}
	if !overclockMode && p.TotalCapacityBytes != nil && p.AvailableBytes != nil && *p.AvailableBytes > *p.TotalCapacityBytes {
		return fmt.Errorf("%s available_bytes cannot exceed total_capacity_bytes", prefix)
	}
	return nil
}

func nonNegativeInt64(name string, v *int64) error {
	if v != nil && *v < 0 {
		return fmt.Errorf("%s must be non-negative", name)
	}
	return nil
}

func nonNegativeInt(name string, v *int) error {
	if v != nil && *v < 0 {
		return fmt.Errorf("%s must be non-negative", name)
	}
	return nil
}

func cleanDomain(v string) string {
	v = strings.TrimSpace(strings.ToLower(v))
	v = strings.TrimSuffix(v, ".")
	return v
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
