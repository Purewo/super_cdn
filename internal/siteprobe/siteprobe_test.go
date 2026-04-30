package siteprobe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRunFollowsRedirectedAssetsAndChecksCORS(t *testing.T) {
	var storageOrigin string
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") == "" {
			t.Fatalf("expected Origin header on storage fetch")
		}
		w.Header().Set("Access-Control-Allow-Origin", "*")
		switch r.URL.Path {
		case "/app.js":
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = w.Write([]byte("console.log('ok')"))
		case "/app.css":
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
			_, _ = w.Write([]byte("body{}"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer storage.Close()
	storageOrigin = storage.URL

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<html><head><script type="module" src="/assets/app.js"></script><link rel="stylesheet" href="/assets/app.css"></head></html>`))
		case "/assets/app.js":
			http.Redirect(w, r, storageOrigin+"/app.js", http.StatusFound)
		case "/assets/app.css":
			http.Redirect(w, r, storageOrigin+"/app.css", http.StatusFound)
		case "/movie/123":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("fallback"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer origin.Close()

	report, err := Run(context.Background(), Options{
		URL:     origin.URL + "/",
		SPAPath: "/movie/123",
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK {
		t.Fatalf("expected ok report: %+v", report)
	}
	if len(report.Assets) != 2 {
		t.Fatalf("expected 2 assets, got %d", len(report.Assets))
	}
	for _, asset := range report.Assets {
		if !asset.Redirected || asset.CORS != "ok" || !asset.OK {
			t.Fatalf("unexpected asset result: %+v", asset)
		}
	}
	if report.SPA == nil || !report.SPA.OK {
		t.Fatalf("expected SPA fallback ok: %+v", report.SPA)
	}
}

func TestRunFailsOnHTMLServedAsScript(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte(`<script src="/assets/app.js"></script>`))
		case "/assets/app.js":
			_, _ = w.Write([]byte(`<html>not js</html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer origin.Close()

	report, err := Run(context.Background(), Options{URL: origin.URL + "/", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if report.OK {
		t.Fatalf("expected failed report: %+v", report)
	}
	if len(report.Assets) != 1 || len(report.Assets[0].Errors) == 0 {
		t.Fatalf("expected asset MIME error: %+v", report.Assets)
	}
}

func TestRunCanRequireCloudflareStaticCacheAndDirectAssets(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
			_, _ = w.Write([]byte(`<script type="module" src="/assets/app.js"></script><link rel="stylesheet" href="/assets/app.css">`))
		case "/assets/app.js":
			w.Header().Set("Content-Type", "text/javascript")
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			_, _ = w.Write([]byte("console.log('ok')"))
		case "/assets/app.css":
			w.Header().Set("Content-Type", "text/css")
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			_, _ = w.Write([]byte("body{}"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer origin.Close()

	report, err := Run(context.Background(), Options{
		URL:                        origin.URL + "/",
		Timeout:                    5 * time.Second,
		RequireDirectAssets:        true,
		RequireHTMLRevalidate:      true,
		RequireImmutableAssetCache: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK {
		t.Fatalf("expected ok report: %+v", report)
	}
}

func TestRunCanRequireHybridEdgeHeaders(t *testing.T) {
	var storageOrigin string
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Type", "text/javascript")
		_, _ = w.Write([]byte("console.log('ok')"))
	}))
	defer storage.Close()
	storageOrigin = storage.URL

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/", "/movie/123":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("X-SuperCDN-Edge-Source", "cloudflare_static")
			_, _ = w.Write([]byte(`<script type="module" src="/assets/app.js"></script>`))
		case "/assets/app.js":
			w.Header().Set("X-SuperCDN-Edge-Source", "manifest")
			w.Header().Set("X-SuperCDN-Edge-Manifest", "route")
			w.Header().Set("X-SuperCDN-Edge-Action", "route")
			http.Redirect(w, r, storageOrigin+"/app.js", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer origin.Close()

	report, err := Run(context.Background(), Options{
		URL:                       origin.URL + "/",
		SPAPath:                   "/movie/123",
		Timeout:                   5 * time.Second,
		RequireEdgeStaticHTML:     true,
		RequireEdgeManifestAssets: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK {
		t.Fatalf("expected ok report: %+v", report)
	}
	if report.HTML.EdgeSource != "cloudflare_static" {
		t.Fatalf("html edge source = %q", report.HTML.EdgeSource)
	}
	if len(report.Assets) != 1 {
		t.Fatalf("expected 1 asset, got %d", len(report.Assets))
	}
	if report.Assets[0].EdgeSource != "manifest" || report.Assets[0].EdgeManifest != "route" || report.Assets[0].EdgeAction != "route" {
		t.Fatalf("unexpected edge asset headers: %+v", report.Assets[0])
	}
}

func TestRunFailsHybridEdgeWhenHeadersMissing(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<script type="module" src="/assets/app.js"></script>`))
		case "/assets/app.js":
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = w.Write([]byte("console.log('ok')"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer origin.Close()

	report, err := Run(context.Background(), Options{
		URL:                       origin.URL + "/",
		Timeout:                   5 * time.Second,
		RequireEdgeStaticHTML:     true,
		RequireEdgeManifestAssets: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.OK {
		t.Fatalf("expected failed report: %+v", report)
	}
	if len(report.Errors) == 0 {
		t.Fatalf("expected report errors: %+v", report)
	}
	if len(report.Assets) != 1 || len(report.Assets[0].Errors) == 0 {
		t.Fatalf("expected asset errors: %+v", report.Assets)
	}
}

func TestRunMarksLikelyExpiredStorageSignature(t *testing.T) {
	var storageOrigin string
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		http.Error(w, "expired", http.StatusForbidden)
	}))
	defer storage.Close()
	storageOrigin = storage.URL

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("X-SuperCDN-Edge-Source", "cloudflare_static")
			_, _ = w.Write([]byte(`<script type="module" src="/assets/app.js"></script>`))
		case "/assets/app.js":
			w.Header().Set("X-SuperCDN-Edge-Source", "manifest")
			w.Header().Set("X-SuperCDN-Edge-Manifest", "route")
			w.Header().Set("X-SuperCDN-Edge-Action", "route")
			http.Redirect(w, r, storageOrigin+"/app.js?sign=expired", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer origin.Close()

	report, err := Run(context.Background(), Options{
		URL:                       origin.URL + "/",
		Timeout:                   5 * time.Second,
		RequireEdgeStaticHTML:     true,
		RequireEdgeManifestAssets: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.OK {
		t.Fatalf("expected failed report: %+v", report)
	}
	if report.Summary["signature_suspect"] != 1 {
		t.Fatalf("signature_suspect summary = %#v", report.Summary)
	}
	if len(report.Assets) != 1 || !report.Assets[0].SignatureSuspect || len(report.Assets[0].Warnings) == 0 {
		t.Fatalf("expected signature suspect asset: %+v", report.Assets)
	}
	if len(report.Warnings) == 0 {
		t.Fatalf("expected report warning: %+v", report)
	}
}
