package deploymenttarget

import "testing"

func TestNormalizeDeploymentTargetAliases(t *testing.T) {
	tests := map[string]string{
		"origin":          "origin_assisted",
		"go_origin":       "origin_assisted",
		"origin_assisted": "origin_assisted",
		"cloudflare":      "cloudflare_static",
		"workers_static":  "cloudflare_static",
		"workers_assets":  "cloudflare_static",
		"pages":           "cloudflare_static",
		"hybrid":          "hybrid_edge",
		"edge":            "hybrid_edge",
		"hybrid_edge":     "hybrid_edge",
	}
	for input, want := range tests {
		got, err := Normalize(input)
		if err != nil {
			t.Fatalf("Normalize(%q) error: %v", input, err)
		}
		if got != want {
			t.Fatalf("Normalize(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeDeploymentTargetRejectsUnknown(t *testing.T) {
	if _, err := Normalize("r2_website"); err == nil {
		t.Fatal("expected unknown deployment target to fail")
	}
}

func TestDeploymentTargetAliasPassesUnknownThrough(t *testing.T) {
	if got := Alias(" R2_WebSite "); got != "r2_website" {
		t.Fatalf("Alias unknown = %q", got)
	}
	if got := Alias("workers_assets"); got != "cloudflare_static" {
		t.Fatalf("Alias known = %q", got)
	}
}
