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
