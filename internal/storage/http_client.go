package storage

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func newHTTPClient(proxyURL string) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL != "" {
		parsed, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy_url %q: %w", proxyURL, err)
		}
		if parsed.Scheme == "" || parsed.Host == "" {
			return nil, fmt.Errorf("invalid proxy_url %q: scheme and host are required", proxyURL)
		}
		transport.Proxy = http.ProxyURL(parsed)
	}
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			req.Header.Del("Referer")
			return nil
		},
	}, nil
}
