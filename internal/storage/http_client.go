package storage

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func newHTTPClient(proxyURL string) (*http.Client, error) {
	return newHTTPClientWithNetwork(proxyURL, "")
}

func newHTTPClientWithNetwork(proxyURL, network string) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	network = strings.ToLower(strings.TrimSpace(network))
	switch network {
	case "", "tcp":
	case "tcp4", "tcp6":
		dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
		transport.DialContext = func(ctx context.Context, _, addr string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, addr)
		}
	default:
		return nil, fmt.Errorf("invalid network %q: expected tcp, tcp4 or tcp6", network)
	}
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
	if transport.ResponseHeaderTimeout == 0 {
		transport.ResponseHeaderTimeout = 30 * time.Second
	}
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			req.Header.Del("Referer")
			return nil
		},
	}, nil
}
