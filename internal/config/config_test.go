package config

import "testing"

func TestCloudflareLegacyConfigBuildsDefaultAccountAndLibrary(t *testing.T) {
	cfg := minimalConfig(t)
	cfg.Cloudflare = CloudflareConfig{
		AccountID:        "acct-1",
		ZoneID:           "zone-1",
		APIToken:         "token",
		RootDomain:       "example.com",
		SiteDNSTarget:    "origin.example.com",
		WorkerScript:     "supercdn-edge",
		EdgeBypassSecret: "edge-secret",
	}
	if err := cfg.ApplyDefaults(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	account, ok := cfg.DefaultCloudflareAccount()
	if !ok || account.Name != "default" || account.RootDomain != "example.com" || account.SiteDomainSuffix != "sites.example.com" {
		t.Fatalf("unexpected default cloudflare account: %+v ok=%v", account, ok)
	}
	library, ok := cfg.CloudflareLibraryByName("overseas_accel")
	if !ok || len(library.Bindings) != 1 || library.Bindings[0].Account != "default" {
		t.Fatalf("unexpected default cloudflare library: %+v ok=%v", library, ok)
	}
}

func TestCloudflareAccountsAndLibrariesValidateBindings(t *testing.T) {
	cfg := minimalConfig(t)
	cfg.CloudflareAccounts = []CloudflareAccountConfig{
		{Name: "cf_business", Default: true, AccountID: "acct-1", ZoneID: "zone-1", APIToken: "token-1", RootDomain: "qwk.ccwu.cc", R2: R2Config{Bucket: "bucket-1", AccessKeyID: "key", SecretAccessKey: "secret"}},
		{Name: "cf_backup", AccountID: "acct-2", ZoneID: "zone-2", APIToken: "token-2", RootDomain: "backup.example.com"},
	}
	cfg.CloudflareLibraries = []CloudflareLibraryConfig{{
		Name: "overseas_accel",
		Bindings: []CloudflareLibraryBinding{
			{Name: "business", Account: "cf_business"},
			{Name: "backup", Account: "cf_backup"},
		},
	}}
	if err := cfg.ApplyDefaults(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	account, ok := cfg.DefaultCloudflareAccount()
	if !ok || account.Name != "cf_business" || cfg.Cloudflare.ZoneID != "zone-1" {
		t.Fatalf("unexpected default account=%+v ok=%v legacy=%+v", account, ok, cfg.Cloudflare)
	}
	library, ok := cfg.CloudflareLibraryByName("overseas_accel")
	if !ok || len(library.Bindings) != 2 {
		t.Fatalf("unexpected library: %+v ok=%v", library, ok)
	}
	if !cfg.CloudflareLibraryHasStorage(library) {
		t.Fatal("expected cloudflare library to be storage-capable")
	}
}

func TestCloudflareLibraryRejectsMissingAccount(t *testing.T) {
	cfg := minimalConfig(t)
	cfg.CloudflareAccounts = []CloudflareAccountConfig{{Name: "cf_business", RootDomain: "qwk.ccwu.cc"}}
	cfg.CloudflareLibraries = []CloudflareLibraryConfig{{
		Name:     "overseas_accel",
		Bindings: []CloudflareLibraryBinding{{Account: "missing"}},
	}}
	if err := cfg.ApplyDefaults(t.TempDir()); err == nil {
		t.Fatal("expected missing account error")
	}
}

func TestRouteProfileMayReferenceStorageCapableCloudflareLibrary(t *testing.T) {
	cfg := minimalConfig(t)
	cfg.CloudflareAccounts = []CloudflareAccountConfig{{
		Name:       "cf_business",
		Default:    true,
		AccountID:  "acct-1",
		RootDomain: "example.com",
		R2: R2Config{
			Bucket:          "bucket-1",
			AccessKeyID:     "key",
			SecretAccessKey: "secret",
		},
	}}
	cfg.CloudflareLibraries = []CloudflareLibraryConfig{{
		Name: "overseas_accel",
		Bindings: []CloudflareLibraryBinding{{
			Account: "cf_business",
		}},
	}}
	cfg.RouteProfiles = []RouteProfile{{Name: "overseas", Primary: "overseas_accel"}}
	if err := cfg.ApplyDefaults(t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

func TestRouteProfileRejectsControlPlaneOnlyCloudflareLibrary(t *testing.T) {
	cfg := minimalConfig(t)
	cfg.CloudflareAccounts = []CloudflareAccountConfig{{Name: "cf_business", RootDomain: "example.com"}}
	cfg.CloudflareLibraries = []CloudflareLibraryConfig{{
		Name: "overseas_accel",
		Bindings: []CloudflareLibraryBinding{{
			Account: "cf_business",
		}},
	}}
	cfg.RouteProfiles = []RouteProfile{{Name: "overseas", Primary: "overseas_accel"}}
	if err := cfg.ApplyDefaults(t.TempDir()); err == nil {
		t.Fatal("expected missing cloudflare storage backing to fail route profile validation")
	}
}

func TestRouteProfileNormalizesDeploymentTarget(t *testing.T) {
	cfg := minimalConfig(t)
	cfg.RouteProfiles[0].DeploymentTarget = "workers_static"
	if err := cfg.ApplyDefaults(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if got := cfg.RouteProfiles[0].DeploymentTarget; got != "cloudflare_static" {
		t.Fatalf("deployment target = %q", got)
	}
}

func TestRouteProfileRejectsUnknownDeploymentTarget(t *testing.T) {
	cfg := minimalConfig(t)
	cfg.RouteProfiles[0].DeploymentTarget = "r2_website"
	if err := cfg.ApplyDefaults(t.TempDir()); err == nil {
		t.Fatal("expected unknown deployment target to fail")
	}
}

func TestCloudflareAccountAllowsControlPlaneOnlyR2Config(t *testing.T) {
	cfg := minimalConfig(t)
	cfg.CloudflareAccounts = []CloudflareAccountConfig{{
		Name:       "cf_business",
		Default:    true,
		AccountID:  "acct-1",
		RootDomain: "example.com",
		R2: R2Config{
			Bucket: "bucket-1",
		},
	}}
	if err := cfg.ApplyDefaults(t.TempDir()); err != nil {
		t.Fatalf("expected control-plane-only r2 config to pass: %v", err)
	}
}

func TestCloudflareAccountAllowsSeparateR2APIToken(t *testing.T) {
	cfg := minimalConfig(t)
	cfg.CloudflareAccounts = []CloudflareAccountConfig{{
		Name:       "cf_business",
		Default:    true,
		AccountID:  "acct-1",
		ZoneID:     "zone-1",
		APIToken:   "zone-token",
		RootDomain: "example.com",
		R2: R2Config{
			AccountID:       "acct-r2",
			APIToken:        "r2-token",
			Bucket:          "bucket-1",
			AccessKeyID:     "key",
			SecretAccessKey: "secret",
		},
	}}
	if err := cfg.ApplyDefaults(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	account, ok := cfg.DefaultCloudflareAccount()
	if !ok {
		t.Fatal("missing default account")
	}
	control := account.ToCloudflareConfig()
	if control.AccountID != "acct-1" || control.APIToken != "zone-token" {
		t.Fatalf("unexpected control config: %+v", control)
	}
	r2 := account.ToCloudflareR2Config()
	if r2.AccountID != "acct-r2" || r2.APIToken != "r2-token" || r2.ZoneID != "zone-1" {
		t.Fatalf("unexpected r2 config: %+v", r2)
	}
}

func TestCloudflareAccountRejectsPartialR2Credentials(t *testing.T) {
	cfg := minimalConfig(t)
	cfg.CloudflareAccounts = []CloudflareAccountConfig{{
		Name:       "cf_business",
		Default:    true,
		AccountID:  "acct-1",
		RootDomain: "example.com",
		R2: R2Config{
			Bucket:      "bucket-1",
			AccessKeyID: "key",
		},
	}}
	if err := cfg.ApplyDefaults(t.TempDir()); err == nil {
		t.Fatal("expected partial cloudflare r2 credentials to fail")
	}
}

func minimalConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		Server: ServerConfig{AdminToken: "token"},
		Storage: []StorageConfig{{
			Name: "local_default",
			Type: "local",
		}},
		RouteProfiles: []RouteProfile{{
			Name:    "overseas",
			Primary: "local_default",
		}},
	}
}
