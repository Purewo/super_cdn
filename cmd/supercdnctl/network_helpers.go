package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

func httpClientWithDNSResolver(resolverAddress string) (*http.Client, error) {
	resolverAddress = strings.TrimSpace(resolverAddress)
	if resolverAddress == "" {
		return nil, nil
	}
	if !strings.Contains(resolverAddress, ":") {
		resolverAddress += ":53"
	}
	if _, _, err := net.SplitHostPort(resolverAddress); err != nil {
		return nil, fmt.Errorf("invalid resolver %q: %w", resolverAddress, err)
	}
	resolverDialer := &net.Dialer{Timeout: 5 * time.Second}
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return resolverDialer.DialContext(ctx, network, resolverAddress)
		},
	}
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Resolver:  resolver,
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = dialer.DialContext
	return &http.Client{Transport: transport}, nil
}
