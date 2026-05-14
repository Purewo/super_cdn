package server

import (
	"net/url"
	"strings"
)

func redactDiagnosticURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.RawQuery == "" {
		return raw, false
	}
	values := parsed.Query()
	for key, items := range values {
		for i := range items {
			items[i] = "<redacted>"
		}
		values[key] = items
	}
	parsed.RawQuery = values.Encode()
	return parsed.String(), true
}
