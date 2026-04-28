package storage

import (
	"context"
	"testing"

	"supercdn/internal/config"
)

func TestBuildManagerAddsCloudflareLibraryStore(t *testing.T) {
	cfg := &config.Config{
		Server: config.ServerConfig{AdminToken: "token"},
		Storage: []config.StorageConfig{{
			Name: "local_default",
			Type: "local",
		}},
		CloudflareAccounts: []config.CloudflareAccountConfig{{
			Name:      "cf_business",
			Default:   true,
			AccountID: "acct-1",
			R2: config.R2Config{
				Bucket:          "bucket-1",
				AccessKeyID:     "key",
				SecretAccessKey: "secret",
				PublicBaseURL:   "https://pub.example.com",
			},
		}, {
			Name:      "cf_control_only",
			AccountID: "acct-2",
		}},
		CloudflareLibraries: []config.CloudflareLibraryConfig{{
			Name: "overseas_accel",
			Bindings: []config.CloudflareLibraryBinding{{
				Name:    "business_main",
				Account: "cf_business",
				Path:    "/edge/assets",
			}, {
				Name:    "control_plane_only",
				Account: "cf_control_only",
				Path:    "/ignored",
			}},
		}},
		RouteProfiles: []config.RouteProfile{{
			Name:    "overseas",
			Primary: "overseas_accel",
		}},
	}
	if err := cfg.ApplyDefaults(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	manager, err := BuildManager(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	store, ok := manager.Get("overseas_accel")
	if !ok {
		t.Fatal("missing cloudflare library store")
	}
	if store.Type() != "resource_library" {
		t.Fatalf("store type = %q", store.Type())
	}
	if public := store.PublicURL("objects/a.txt"); public != "https://pub.example.com/edge/assets/objects/a.txt" {
		t.Fatalf("public url = %q", public)
	}
}

func TestBuildManagerSkipsControlPlaneOnlyCloudflareLibrary(t *testing.T) {
	cfg := &config.Config{
		Server: config.ServerConfig{AdminToken: "token"},
		Storage: []config.StorageConfig{{
			Name: "local_default",
			Type: "local",
		}},
		CloudflareAccounts: []config.CloudflareAccountConfig{{
			Name:      "cf_business",
			Default:   true,
			AccountID: "acct-1",
		}},
		RouteProfiles: []config.RouteProfile{{
			Name:    "overseas",
			Primary: "local_default",
		}},
	}
	if err := cfg.ApplyDefaults(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	manager, err := BuildManager(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.Get("overseas_accel"); ok {
		t.Fatal("control-plane-only cloudflare library should not build a storage store")
	}
}
