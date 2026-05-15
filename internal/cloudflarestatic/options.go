package cloudflarestatic

import (
	"fmt"
	"strings"
)

const (
	CachePolicyAuto  = "auto"
	CachePolicyForce = "force"
	CachePolicyNone  = "none"

	NotFoundHandlingNone = "none"
	NotFoundHandling404  = "404-page"
	NotFoundHandlingSPA  = "single-page-application"

	HTMLCacheControl      = "public, max-age=0, must-revalidate"
	ShortCacheControl     = "public, max-age=300, must-revalidate"
	ImmutableCacheControl = "public, max-age=31536000, immutable"

	VerifyModeWait = "wait"
	VerifyModeWarn = "warn"
	VerifyModeNone = "none"
)

func NormalizeCachePolicy(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return CachePolicyAuto, nil
	}
	switch value {
	case CachePolicyAuto, CachePolicyForce, CachePolicyNone:
		return value, nil
	default:
		return "", fmt.Errorf("static cache policy must be auto, force or none")
	}
}

func NotFoundHandlingFlag(value string, spa bool) string {
	if spa {
		return NotFoundHandlingSPA
	}
	return value
}

func NormalizeNotFoundHandling(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || value == NotFoundHandlingNone {
		return "", nil
	}
	switch value {
	case NotFoundHandling404, NotFoundHandlingSPA:
		return value, nil
	default:
		return "", fmt.Errorf("static not found handling must be none, 404-page or single-page-application")
	}
}

func NormalizeVerifyMode(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", VerifyModeWait:
		return VerifyModeWait, nil
	case VerifyModeWarn:
		return VerifyModeWarn, nil
	case VerifyModeNone:
		return VerifyModeNone, nil
	default:
		return "", fmt.Errorf("static-verify must be wait, warn, or none")
	}
}
