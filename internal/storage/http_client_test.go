package storage

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestHTTPClientEmptyProxyIgnoresEnvironment(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	client, err := newHTTPClient("")
	if err != nil {
		t.Fatal(err)
	}
	transport := client.Transport.(*http.Transport)
	if transport.Proxy != nil {
		req := &http.Request{URL: mustURL(t, "https://example.com")}
		proxy, err := transport.Proxy(req)
		if err != nil {
			t.Fatal(err)
		}
		if proxy != nil {
			t.Fatalf("empty proxy_url used environment proxy %s", proxy)
		}
	}
}

func TestHTTPClientExplicitProxy(t *testing.T) {
	client, err := newHTTPClient("http://127.0.0.1:10808")
	if err != nil {
		t.Fatal(err)
	}
	transport := client.Transport.(*http.Transport)
	if transport.Proxy == nil {
		t.Fatal("expected explicit proxy")
	}
	req := &http.Request{URL: mustURL(t, "https://example.com")}
	proxy, err := transport.Proxy(req)
	if err != nil {
		t.Fatal(err)
	}
	if proxy == nil || proxy.String() != "http://127.0.0.1:10808" {
		t.Fatalf("proxy = %v", proxy)
	}
}

func TestHTTPClientNetworkOption(t *testing.T) {
	client, err := newHTTPClientWithNetwork("", "tcp4")
	if err != nil {
		t.Fatal(err)
	}
	transport := client.Transport.(*http.Transport)
	if transport.DialContext == nil {
		t.Fatal("expected custom dialer for tcp4")
	}
	if _, err := newHTTPClientWithNetwork("", "udp"); err == nil {
		t.Fatal("expected invalid network error")
	}
}

func TestHTTPClientRedirectDoesNotSendReferer(t *testing.T) {
	var redirectedReferer string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			http.Redirect(w, r, "/target", http.StatusFound)
		case "/target":
			redirectedReferer = r.Header.Get("Referer")
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := newHTTPClient("")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Get(server.URL + "/start")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if redirectedReferer != "" {
		t.Fatalf("redirect sent Referer %q", redirectedReferer)
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
