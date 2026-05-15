package urlredact

import (
	"net/url"
	"strings"
)

const Replacement = "<redacted>"

func RedactAllQueryValues(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.RawQuery == "" {
		return raw, false
	}
	redactQueryValues(parsed)
	return parsed.String(), true
}

func RedactSignedQueryValues(raw string) string {
	redacted, ok := RedactSignedQueryValuesWithStatus(raw)
	if !ok {
		return raw
	}
	return redacted
}

func RedactSignedQueryValuesWithStatus(raw string) (string, bool) {
	if strings.TrimSpace(raw) == "" {
		return raw, false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.RawQuery == "" {
		return raw, false
	}
	if !queryHasSignedParam(parsed.Query()) {
		return raw, false
	}
	redactQueryValues(parsed)
	return parsed.String(), true
}

func LooksSigned(raw string) bool {
	if strings.TrimSpace(raw) == "" {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return queryHasSignedParam(parsed.Query())
}

func SignedQueryParam(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "sign",
		"signature",
		"expires",
		"policy",
		"key-pair-id",
		"awsaccesskeyid",
		"x-amz-algorithm",
		"x-amz-credential",
		"x-amz-date",
		"x-amz-expires",
		"x-amz-security-token",
		"x-amz-signature",
		"x-amz-signedheaders":
		return true
	default:
		return false
	}
}

func queryHasSignedParam(values url.Values) bool {
	for key := range values {
		if SignedQueryParam(key) {
			return true
		}
	}
	return false
}

func redactQueryValues(parsed *url.URL) {
	values := parsed.Query()
	for key, items := range values {
		for i := range items {
			items[i] = Replacement
		}
		values[key] = items
	}
	parsed.RawQuery = values.Encode()
}
