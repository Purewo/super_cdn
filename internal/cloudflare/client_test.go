package cloudflare

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"supercdn/internal/config"
)

func TestStatusUsesCloudflareEndpoints(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/user/tokens/verify", func(w http.ResponseWriter, r *http.Request) {
		writeCF(t, w, map[string]any{"id": "tok"})
	})
	mux.HandleFunc("/zones/zone-1", func(w http.ResponseWriter, r *http.Request) {
		writeCF(t, w, map[string]any{"id": "zone-1", "name": "example.com", "status": "active"})
	})
	mux.HandleFunc("/zones/zone-1/dns_records", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		writeCF(t, w, []DNSRecord{{ID: "dns-1", Type: "A", Name: name, Content: "127.0.0.1", TTL: 1}})
	})
	mux.HandleFunc("/zones/zone-1/workers/routes", func(w http.ResponseWriter, r *http.Request) {
		writeCF(t, w, []WorkerRoute{{ID: "route-1", Pattern: "example.com/*", Script: "supercdn"}})
	})
	mux.HandleFunc("/accounts/acct-1/r2/buckets", func(w http.ResponseWriter, r *http.Request) {
		writeCF(t, w, map[string]any{"buckets": []R2Bucket{{Name: "assets"}}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := New(config.CloudflareConfig{
		AccountID:        "acct-1",
		ZoneID:           "zone-1",
		APIToken:         "token",
		RootDomain:       "example.com",
		SiteDomainSuffix: "sites.example.com",
	}, server.Client())
	client.baseURL = server.URL

	status := client.Status(context.Background())
	if !status.Token.OK || !status.Zone.OK || !status.DNS.OK || !status.Workers.OK || !status.R2.OK {
		t.Fatalf("unexpected status: %+v", status)
	}
	if status.Workers.RouteCount != 1 || status.R2.BucketCount != 1 {
		t.Fatalf("unexpected counts: %+v", status)
	}
	if len(status.DNS.SiteWildcard) != 1 || status.DNS.SiteWildcard[0].Name != "*.sites.example.com" {
		t.Fatalf("unexpected dns status: %+v", status.DNS)
	}
}

func TestStatusReportsUnconfigured(t *testing.T) {
	status := New(config.CloudflareConfig{}, nil).Status(context.Background())
	if status.Configured || status.Token.OK || status.Zone.OK || status.DNS.OK {
		t.Fatalf("unexpected configured status: %+v", status)
	}
	if status.Token.Message == "" || len(status.Warnings) == 0 {
		t.Fatalf("missing unconfigured warning: %+v", status)
	}
}

func TestVerifyTokenFallsBackToAccountScopedEndpoint(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/user/tokens/verify", func(w http.ResponseWriter, r *http.Request) {
		writeCFError(t, w, http.StatusUnauthorized, 1000, "Invalid API Token")
	})
	mux.HandleFunc("/accounts/acct-1/tokens/verify", func(w http.ResponseWriter, r *http.Request) {
		writeCF(t, w, map[string]any{"id": "tok", "status": "active"})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := New(config.CloudflareConfig{AccountID: "acct-1", APIToken: "token"}, server.Client())
	client.baseURL = server.URL
	if err := client.VerifyToken(context.Background()); err != nil {
		t.Fatalf("expected account-scoped token verify fallback to succeed: %v", err)
	}
}

func TestStatusWithR2ChecksValidatesBucketCORSAndDomains(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/user/tokens/verify", func(w http.ResponseWriter, r *http.Request) {
		writeCF(t, w, map[string]any{"id": "tok"})
	})
	mux.HandleFunc("/accounts/acct-1/r2/buckets", func(w http.ResponseWriter, r *http.Request) {
		writeCF(t, w, map[string]any{"buckets": []R2Bucket{{Name: "assets"}}})
	})
	mux.HandleFunc("/accounts/acct-1/r2/buckets/assets", func(w http.ResponseWriter, r *http.Request) {
		writeCF(t, w, R2Bucket{Name: "assets", Location: "wnam"})
	})
	mux.HandleFunc("/accounts/acct-1/r2/buckets/assets/cors", func(w http.ResponseWriter, r *http.Request) {
		writeCF(t, w, R2CORSPolicy{Rules: []R2CORSRule{{
			Allowed: R2CORSAllowed{
				Methods: []string{"GET", "HEAD"},
				Origins: []string{"https://cdn.example.com"},
				Headers: []string{"*"},
			},
		}}})
	})
	mux.HandleFunc("/accounts/acct-1/r2/buckets/assets/domains/custom", func(w http.ResponseWriter, r *http.Request) {
		writeCF(t, w, map[string]any{"domains": []R2CustomDomain{{
			Domain:  "cdn.example.com",
			Enabled: true,
			Status:  R2DomainValidationStatus{Ownership: "active", SSL: "active"},
		}}})
	})
	mux.HandleFunc("/accounts/acct-1/r2/buckets/assets/domains/managed", func(w http.ResponseWriter, r *http.Request) {
		writeCF(t, w, R2ManagedDomain{Domain: "assets.acct-1.r2.dev", Enabled: false})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := New(config.CloudflareConfig{AccountID: "acct-1", APIToken: "token"}, server.Client())
	client.baseURL = server.URL
	status := client.StatusWithR2Checks(context.Background(), R2CheckOptions{
		Bucket:        "assets",
		PublicBaseURL: "https://cdn.example.com",
	})
	if !status.R2.OK || !status.R2.Bucket.OK || !status.R2.CORS.OK || !status.R2.Domains.OK {
		t.Fatalf("unexpected r2 status: %+v", status.R2)
	}
	if status.R2.CORS.RuleCount != 1 || status.R2.Domains.MatchedDomain != "cdn.example.com" {
		t.Fatalf("unexpected r2 diagnostics: %+v", status.R2)
	}
}

func TestStatusWithR2ChecksReportsMissingPublicDomain(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/user/tokens/verify", func(w http.ResponseWriter, r *http.Request) {
		writeCF(t, w, map[string]any{"id": "tok"})
	})
	mux.HandleFunc("/accounts/acct-1/r2/buckets", func(w http.ResponseWriter, r *http.Request) {
		writeCF(t, w, map[string]any{"buckets": []R2Bucket{{Name: "assets"}}})
	})
	mux.HandleFunc("/accounts/acct-1/r2/buckets/assets", func(w http.ResponseWriter, r *http.Request) {
		writeCF(t, w, R2Bucket{Name: "assets"})
	})
	mux.HandleFunc("/accounts/acct-1/r2/buckets/assets/cors", func(w http.ResponseWriter, r *http.Request) {
		writeCF(t, w, R2CORSPolicy{})
	})
	mux.HandleFunc("/accounts/acct-1/r2/buckets/assets/domains/custom", func(w http.ResponseWriter, r *http.Request) {
		writeCF(t, w, map[string]any{"domains": []R2CustomDomain{}})
	})
	mux.HandleFunc("/accounts/acct-1/r2/buckets/assets/domains/managed", func(w http.ResponseWriter, r *http.Request) {
		writeCF(t, w, R2ManagedDomain{Domain: "assets.acct-1.r2.dev", Enabled: true})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := New(config.CloudflareConfig{AccountID: "acct-1", APIToken: "token"}, server.Client())
	client.baseURL = server.URL
	status := client.StatusWithR2Checks(context.Background(), R2CheckOptions{
		Bucket:        "assets",
		PublicBaseURL: "https://cdn.example.com",
	})
	if !status.R2.OK || status.R2.Domains.OK || !strings.Contains(status.R2.Domains.Message, "not attached") {
		t.Fatalf("unexpected r2 domain status: %+v", status.R2.Domains)
	}
	if status.R2.CORS.OK || status.R2.CORS.RuleCount != 0 {
		t.Fatalf("unexpected r2 cors status: %+v", status.R2.CORS)
	}
}

func TestSyncR2BucketDryRunPlansCORSAndCustomDomain(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/accounts/acct-1/r2/buckets/assets/cors", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected cors method %s", r.Method)
		}
		writeCF(t, w, R2CORSPolicy{})
	})
	mux.HandleFunc("/accounts/acct-1/r2/buckets/assets/domains/custom", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected custom domain method %s", r.Method)
		}
		writeCF(t, w, map[string]any{"domains": []R2CustomDomain{}})
	})
	mux.HandleFunc("/accounts/acct-1/r2/buckets/assets/domains/managed", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected managed domain method %s", r.Method)
		}
		writeCF(t, w, R2ManagedDomain{Domain: "assets.acct-1.r2.dev", Enabled: false})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := New(config.CloudflareConfig{AccountID: "acct-1", ZoneID: "zone-1", APIToken: "token"}, server.Client())
	client.baseURL = server.URL
	result := client.SyncR2Bucket(context.Background(), SyncR2Options{
		Bucket:        "assets",
		PublicBaseURL: "https://cdn.example.com",
		ZoneID:        "zone-1",
		DryRun:        true,
		SyncCORS:      true,
		SyncDomain:    true,
	})
	if result.Status != "planned" || result.CORS == nil || result.Domain == nil {
		t.Fatalf("unexpected sync result: %+v", result)
	}
	if result.CORS.Action != "put" || result.Domain.Action != "create_custom" {
		t.Fatalf("unexpected actions: %+v %+v", result.CORS, result.Domain)
	}
	if got := result.CORS.Desired.Rules[0].Allowed.Origins[0]; got != "*" {
		t.Fatalf("unexpected cors origin %q", got)
	}
}

func TestSyncR2BucketExecutesCORSAndCustomDomain(t *testing.T) {
	var putCORS, createDomain bool
	mux := http.NewServeMux()
	mux.HandleFunc("/accounts/acct-1/r2/buckets/assets/cors", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeCF(t, w, R2CORSPolicy{})
		case http.MethodPut:
			var req R2CORSPolicy
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			if len(req.Rules) != 1 || req.Rules[0].Allowed.Methods[0] != "GET" || req.Rules[0].Allowed.Origins[0] != "*" {
				t.Fatalf("unexpected cors body: %+v", req)
			}
			putCORS = true
			writeCF(t, w, req)
		default:
			t.Fatalf("unexpected cors method %s", r.Method)
		}
	})
	mux.HandleFunc("/accounts/acct-1/r2/buckets/assets/domains/custom", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeCF(t, w, map[string]any{"domains": []R2CustomDomain{}})
		case http.MethodPost:
			var req R2CustomDomainConfig
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			if req.Domain != "cdn.example.com" || req.ZoneID != "zone-1" || !req.Enabled {
				t.Fatalf("unexpected custom domain body: %+v", req)
			}
			createDomain = true
			writeCF(t, w, R2CustomDomain{Domain: req.Domain, Enabled: true, Status: R2DomainValidationStatus{Ownership: "active", SSL: "active"}})
		default:
			t.Fatalf("unexpected custom domain method %s", r.Method)
		}
	})
	mux.HandleFunc("/accounts/acct-1/r2/buckets/assets/domains/managed", func(w http.ResponseWriter, r *http.Request) {
		writeCF(t, w, R2ManagedDomain{Domain: "assets.acct-1.r2.dev", Enabled: false})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := New(config.CloudflareConfig{AccountID: "acct-1", ZoneID: "zone-1", APIToken: "token"}, server.Client())
	client.baseURL = server.URL
	result := client.SyncR2Bucket(context.Background(), SyncR2Options{
		Bucket:        "assets",
		PublicBaseURL: "https://cdn.example.com",
		ZoneID:        "zone-1",
		SyncCORS:      true,
		SyncDomain:    true,
	})
	if result.Status != "ok" || !putCORS || !createDomain {
		t.Fatalf("unexpected sync result putCORS=%v createDomain=%v result=%+v", putCORS, createDomain, result)
	}
}

func TestSyncR2BucketRejectsReplacingDifferentCORSWithoutForce(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/accounts/acct-1/r2/buckets/assets/cors", func(w http.ResponseWriter, r *http.Request) {
		writeCF(t, w, R2CORSPolicy{Rules: []R2CORSRule{{
			Allowed: R2CORSAllowed{Methods: []string{"GET"}, Origins: []string{"https://old.example.com"}},
		}}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := New(config.CloudflareConfig{AccountID: "acct-1", APIToken: "token"}, server.Client())
	client.baseURL = server.URL
	result := client.SyncR2Bucket(context.Background(), SyncR2Options{
		Bucket:        "assets",
		PublicBaseURL: "https://cdn.example.com",
		SyncCORS:      true,
	})
	if result.Status != "partial" || result.CORS == nil || result.CORS.Action != "conflict" {
		t.Fatalf("unexpected conflict result: %+v", result)
	}
}

func TestSyncR2BucketTreatsMissingCORSConfigAsEmpty(t *testing.T) {
	var putCORS bool
	mux := http.NewServeMux()
	mux.HandleFunc("/accounts/acct-1/r2/buckets/assets/cors", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeCFError(t, w, http.StatusNotFound, 10059, "The CORS configuration does not exist.")
		case http.MethodPut:
			putCORS = true
			writeCF(t, w, R2CORSPolicy{Rules: []R2CORSRule{{Allowed: R2CORSAllowed{Methods: []string{"GET", "HEAD"}}}}})
		default:
			t.Fatalf("unexpected cors method %s", r.Method)
		}
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := New(config.CloudflareConfig{AccountID: "acct-1", APIToken: "token"}, server.Client())
	client.baseURL = server.URL
	result := client.SyncR2Bucket(context.Background(), SyncR2Options{
		Bucket:        "assets",
		PublicBaseURL: "https://cdn.example.com",
		SyncCORS:      true,
	})
	if result.Status != "ok" || result.CORS == nil || result.CORS.Action != "put" || !putCORS {
		t.Fatalf("unexpected missing-cors result put=%v result=%+v", putCORS, result)
	}
}

func TestProvisionR2BucketDryRunPlansCreateWithoutBucketScopedCalls(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/accounts/acct-1/r2/buckets", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected buckets method %s", r.Method)
		}
		writeCF(t, w, map[string]any{"buckets": []R2Bucket{}})
	})
	mux.HandleFunc("/accounts/acct-1/r2/buckets/assets/cors", func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("dry-run create should not call bucket CORS endpoint")
	})
	mux.HandleFunc("/accounts/acct-1/r2/buckets/assets/domains/custom", func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("dry-run create should not call bucket custom domain endpoint")
	})
	mux.HandleFunc("/accounts/acct-1/r2/buckets/assets/domains/managed", func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("dry-run create should not call bucket managed domain endpoint")
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := New(config.CloudflareConfig{AccountID: "acct-1", ZoneID: "zone-1", APIToken: "token"}, server.Client())
	client.baseURL = server.URL
	result := client.ProvisionR2Bucket(context.Background(), R2ProvisionOptions{
		Bucket:        "assets",
		PublicBaseURL: "https://cdn.example.com",
		ZoneID:        "zone-1",
		DryRun:        true,
		SyncCORS:      true,
		SyncDomain:    true,
	})
	if result.Status != "planned" || result.BucketResult.Action != "create" || result.Sync == nil {
		t.Fatalf("unexpected provision result: %+v", result)
	}
	if result.Sync.CORS == nil || result.Sync.CORS.Action != "put" || result.Sync.Domain == nil || result.Sync.Domain.Action != "create_custom" {
		t.Fatalf("unexpected post-create sync plan: %+v", result.Sync)
	}
	if got := result.Sync.CORS.Desired.Rules[0].Allowed.Origins[0]; got != "*" {
		t.Fatalf("unexpected post-create cors origin %q", got)
	}
}

func TestProvisionR2BucketCreatesBucketThenSyncsCORSAndDomain(t *testing.T) {
	var created, putCORS, createDomain bool
	mux := http.NewServeMux()
	mux.HandleFunc("/accounts/acct-1/r2/buckets", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeCF(t, w, map[string]any{"buckets": []R2Bucket{}})
		case http.MethodPost:
			var req R2BucketCreateOptions
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			if req.Name != "assets" || req.LocationHint != "wnam" || req.StorageClass != "InfrequentAccess" {
				t.Fatalf("unexpected create bucket body: %+v", req)
			}
			if got := r.Header.Get("cf-r2-jurisdiction"); got != "eu" {
				t.Fatalf("jurisdiction header = %q", got)
			}
			created = true
			writeCF(t, w, R2Bucket{Name: req.Name, Location: req.LocationHint, Jurisdiction: "eu", StorageClass: req.StorageClass})
		default:
			t.Fatalf("unexpected buckets method %s", r.Method)
		}
	})
	mux.HandleFunc("/accounts/acct-1/r2/buckets/assets/cors", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeCF(t, w, R2CORSPolicy{})
		case http.MethodPut:
			var req R2CORSPolicy
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			if len(req.Rules) != 1 || req.Rules[0].Allowed.Origins[0] != "*" {
				t.Fatalf("unexpected cors body: %+v", req)
			}
			putCORS = true
			writeCF(t, w, req)
		default:
			t.Fatalf("unexpected cors method %s", r.Method)
		}
	})
	mux.HandleFunc("/accounts/acct-1/r2/buckets/assets/domains/custom", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeCF(t, w, map[string]any{"domains": []R2CustomDomain{}})
		case http.MethodPost:
			createDomain = true
			writeCF(t, w, R2CustomDomain{Domain: "cdn.example.com", Enabled: true, Status: R2DomainValidationStatus{Ownership: "active", SSL: "active"}})
		default:
			t.Fatalf("unexpected custom domain method %s", r.Method)
		}
	})
	mux.HandleFunc("/accounts/acct-1/r2/buckets/assets/domains/managed", func(w http.ResponseWriter, r *http.Request) {
		writeCF(t, w, R2ManagedDomain{Domain: "assets.acct-1.r2.dev", Enabled: false})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := New(config.CloudflareConfig{AccountID: "acct-1", ZoneID: "zone-1", APIToken: "token"}, server.Client())
	client.baseURL = server.URL
	result := client.ProvisionR2Bucket(context.Background(), R2ProvisionOptions{
		Bucket:        "assets",
		PublicBaseURL: "https://cdn.example.com",
		ZoneID:        "zone-1",
		LocationHint:  "wnam",
		Jurisdiction:  "eu",
		StorageClass:  "InfrequentAccess",
		SyncCORS:      true,
		SyncDomain:    true,
	})
	if result.Status != "ok" || result.BucketResult.Action != "create" || !created || !putCORS || !createDomain {
		t.Fatalf("unexpected provision result created=%v putCORS=%v createDomain=%v result=%+v", created, putCORS, createDomain, result)
	}
}

func TestProvisionR2BucketExistingBucketSkipsCreate(t *testing.T) {
	var posted bool
	mux := http.NewServeMux()
	mux.HandleFunc("/accounts/acct-1/r2/buckets", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posted = true
		}
		writeCF(t, w, map[string]any{"buckets": []R2Bucket{{Name: "assets"}}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := New(config.CloudflareConfig{AccountID: "acct-1", APIToken: "token"}, server.Client())
	client.baseURL = server.URL
	result := client.ProvisionR2Bucket(context.Background(), R2ProvisionOptions{Bucket: "assets"})
	if result.Status != "ok" || result.BucketResult.Action != "exists" || posted {
		t.Fatalf("unexpected existing-bucket result posted=%v result=%+v", posted, result)
	}
}

func TestCreateR2CredentialsCreatesScopedAccountToken(t *testing.T) {
	var posted bool
	mux := http.NewServeMux()
	mux.HandleFunc("/accounts/acct-1/tokens/permission_groups", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected permission groups method %s", r.Method)
		}
		writeCF(t, w, []TokenPermissionGroup{{
			ID:   "pg-r2-write",
			Name: "Workers R2 Storage Bucket Item Write",
		}})
	})
	mux.HandleFunc("/accounts/acct-1/tokens", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected token create method %s", r.Method)
		}
		var req AccountTokenCreateOptions
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Name != "supercdn-r2-assets" || len(req.Policies) != 1 {
			t.Fatalf("unexpected token create body: %+v", req)
		}
		policy := req.Policies[0]
		if policy.Effect != "allow" || len(policy.PermissionGroups) != 1 || policy.PermissionGroups[0].ID != "pg-r2-write" {
			t.Fatalf("unexpected policy: %+v", policy)
		}
		if got := policy.Resources["com.cloudflare.edge.r2.bucket.acct-1_default_assets"]; got != "*" {
			t.Fatalf("unexpected resource policy: %+v", policy.Resources)
		}
		posted = true
		writeCF(t, w, AccountAPIToken{ID: "access-key-id", Name: req.Name, Value: "one-time-token-value"})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := New(config.CloudflareConfig{AccountID: "acct-1", APIToken: "token"}, server.Client())
	client.baseURL = server.URL
	result := client.CreateR2Credentials(context.Background(), R2CredentialsOptions{
		Bucket:    "assets",
		TokenName: "supercdn-r2-assets",
	})
	wantSecret := sha256.Sum256([]byte("one-time-token-value"))
	if result.Status != "ok" || !posted || result.AccessKeyID != "access-key-id" || result.SecretAccessKey != fmt.Sprintf("%x", wantSecret[:]) {
		t.Fatalf("unexpected credentials result posted=%v result=%+v", posted, result)
	}
}

func TestCreateR2CredentialsDryRunDoesNotCallCloudflare(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/accounts/acct-1/tokens/permission_groups", func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("dry-run should not call permission groups")
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := New(config.CloudflareConfig{AccountID: "acct-1", APIToken: "token"}, server.Client())
	client.baseURL = server.URL
	result := client.CreateR2Credentials(context.Background(), R2CredentialsOptions{
		Bucket: "assets",
		DryRun: true,
	})
	if result.Status != "planned" || result.Action != "create_token" || result.Resource != "com.cloudflare.edge.r2.bucket.acct-1_default_assets" {
		t.Fatalf("unexpected dry-run credentials result: %+v", result)
	}
}

func TestSyncDNSRecordsCreatesUpdatesAndReportsConflicts(t *testing.T) {
	records := map[string][]DNSRecord{
		"demo.example.com": {{ID: "dns-1", Type: "CNAME", Name: "demo.example.com", Content: "old.example.com", Proxied: false, TTL: 1}},
		"mx.example.com":   {{ID: "dns-2", Type: "MX", Name: "mx.example.com", Content: "mail.example.com", TTL: 1}},
	}
	var created, updated bool
	mux := http.NewServeMux()
	mux.HandleFunc("/zones/zone-1/dns_records", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeCF(t, w, records[r.URL.Query().Get("name")])
		case http.MethodPost:
			var req DNSRecord
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			if req.Name != "new.example.com" || req.Type != "CNAME" || req.Content != "origin.example.com" || !req.Proxied {
				t.Fatalf("unexpected create body: %+v", req)
			}
			created = true
			req.ID = "dns-3"
			records[req.Name] = []DNSRecord{req}
			writeCF(t, w, req)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	})
	mux.HandleFunc("/zones/zone-1/dns_records/dns-1", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("unexpected method %s", r.Method)
		}
		var req DNSRecord
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Name != "demo.example.com" || req.Type != "CNAME" || req.Content != "origin.example.com" || !req.Proxied {
			t.Fatalf("unexpected update body: %+v", req)
		}
		updated = true
		req.ID = "dns-1"
		records[req.Name] = []DNSRecord{req}
		writeCF(t, w, req)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := New(config.CloudflareConfig{ZoneID: "zone-1", APIToken: "token"}, server.Client())
	client.baseURL = server.URL
	conflict, err := client.SyncDNSRecords(context.Background(), []DNSRecord{{
		Type: "CNAME", Name: "demo.example.com", Content: "origin.example.com", Proxied: true, TTL: 1,
	}}, SyncDNSRecordOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(conflict) != 1 || conflict[0].Action != "conflict" || !strings.Contains(conflict[0].Error, "different content") {
		t.Fatalf("unexpected conflict: %+v", conflict)
	}

	results, err := client.SyncDNSRecords(context.Background(), []DNSRecord{
		{Type: "CNAME", Name: "demo.example.com", Content: "origin.example.com", Proxied: true, TTL: 1},
		{Type: "CNAME", Name: "new.example.com", Content: "origin.example.com", Proxied: true, TTL: 1},
		{Type: "CNAME", Name: "mx.example.com", Content: "origin.example.com", Proxied: true, TTL: 1},
	}, SyncDNSRecordOptions{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 || results[0].Action != "update" || results[1].Action != "create" || results[2].Action != "conflict" || !updated || !created {
		t.Fatalf("unexpected sync result updated=%v created=%v results=%+v", updated, created, results)
	}
}

func TestSyncWorkerRoutesCreatesUpdatesAndReportsConflicts(t *testing.T) {
	var routes = []WorkerRoute{{ID: "route-1", Pattern: "demo.example.com/*", Script: "old-worker"}}
	var created, updated bool
	mux := http.NewServeMux()
	mux.HandleFunc("/zones/zone-1/workers/routes", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeCF(t, w, routes)
		case http.MethodPost:
			var req WorkerRoute
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			if req.Pattern != "new.example.com/*" || req.Script != "supercdn-edge" {
				t.Fatalf("unexpected create body: %+v", req)
			}
			created = true
			route := WorkerRoute{ID: "route-2", Pattern: req.Pattern, Script: req.Script}
			routes = append(routes, route)
			writeCF(t, w, route)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	})
	mux.HandleFunc("/zones/zone-1/workers/routes/route-1", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("unexpected method %s", r.Method)
		}
		var req WorkerRoute
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Pattern != "demo.example.com/*" || req.Script != "supercdn-edge" {
			t.Fatalf("unexpected update body: %+v", req)
		}
		updated = true
		routes[0].Script = req.Script
		writeCF(t, w, routes[0])
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := New(config.CloudflareConfig{ZoneID: "zone-1", APIToken: "token"}, server.Client())
	client.baseURL = server.URL

	conflict, err := client.SyncWorkerRoutes(context.Background(), []string{"demo.example.com/*"}, "supercdn-edge", SyncWorkerRouteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(conflict) != 1 || conflict[0].Action != "conflict" || !strings.Contains(conflict[0].Error, "different worker") {
		t.Fatalf("unexpected conflict result: %+v", conflict)
	}

	results, err := client.SyncWorkerRoutes(context.Background(), []string{"demo.example.com/*", "new.example.com/*"}, "supercdn-edge", SyncWorkerRouteOptions{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].Action != "update" || results[1].Action != "create" || !updated || !created {
		t.Fatalf("unexpected sync results updated=%v created=%v results=%+v", updated, created, results)
	}
}

func TestKVNamespaceLookupAndPutValue(t *testing.T) {
	var gotBody, gotContentType string
	mux := http.NewServeMux()
	mux.HandleFunc("/accounts/acct-1/storage/kv/namespaces", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected namespace method %s", r.Method)
		}
		writeCF(t, w, []KVNamespace{{ID: "ns-1", Title: "supercdn-edge-manifest"}})
	})
	mux.HandleFunc("/accounts/acct-1/storage/kv/namespaces/ns-1/values/sites%2Fdemo.example.com%2Factive%2Fedge-manifest", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("unexpected put method %s", r.Method)
		}
		gotContentType = r.Header.Get("Content-Type")
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		gotBody = string(raw)
		writeCF(t, w, map[string]any{"key": "ok"})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := New(config.CloudflareConfig{AccountID: "acct-1", APIToken: "token"}, server.Client())
	client.baseURL = server.URL
	namespace, err := client.FindKVNamespace(context.Background(), "supercdn-edge-manifest")
	if err != nil {
		t.Fatal(err)
	}
	if namespace.ID != "ns-1" {
		t.Fatalf("namespace = %+v", namespace)
	}
	if err := client.PutKVValue(context.Background(), namespace.ID, "sites/demo.example.com/active/edge-manifest", []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	if gotBody != `{"ok":true}` {
		t.Fatalf("body = %q", gotBody)
	}
	if !strings.Contains(gotContentType, "application/json") {
		t.Fatalf("content-type = %q", gotContentType)
	}
}

func TestPurgeCacheBatchesSplitsLargeURLLists(t *testing.T) {
	var batchSizes []int
	mux := http.NewServeMux()
	mux.HandleFunc("/zones/zone-1/purge_cache", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		var req struct {
			Files []string `json:"files"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		batchSizes = append(batchSizes, len(req.Files))
		writeCF(t, w, map[string]any{"id": "purge"})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := New(config.CloudflareConfig{ZoneID: "zone-1", APIToken: "token"}, server.Client())
	client.baseURL = server.URL
	urls := make([]string, 0, MaxPurgeFilesPerRequest+3)
	for i := 0; i < MaxPurgeFilesPerRequest+3; i++ {
		urls = append(urls, fmt.Sprintf("https://example.com/file-%03d", i))
	}
	results, err := client.PurgeCacheBatches(context.Background(), urls)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || len(batchSizes) != 2 || batchSizes[0] != MaxPurgeFilesPerRequest || batchSizes[1] != 3 {
		t.Fatalf("unexpected batch split sizes=%v results=%+v", batchSizes, results)
	}
}

func writeCF(t *testing.T, w http.ResponseWriter, result any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"result":  result,
		"errors":  []any{},
	}); err != nil {
		t.Fatal(err)
	}
}

func writeCFError(t *testing.T, w http.ResponseWriter, status, code int, message string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]any{
		"success":  false,
		"result":   nil,
		"messages": []any{},
		"errors": []map[string]any{{
			"code":    code,
			"message": message,
		}},
	}); err != nil {
		t.Fatal(err)
	}
}
