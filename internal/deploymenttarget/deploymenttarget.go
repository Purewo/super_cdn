package deploymenttarget

import (
	"errors"
	"strings"

	"supercdn/internal/model"
)

const allowedMessage = "must be origin_assisted, cloudflare_static or hybrid_edge"

// Normalize returns the canonical deployment target name used by API payloads,
// config files and deployment metadata.
func Normalize(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "", nil
	}
	switch value {
	case "origin", "go_origin", model.SiteDeploymentTargetOriginAssisted:
		return model.SiteDeploymentTargetOriginAssisted, nil
	case "cloudflare", model.SiteDeploymentTargetCloudflareStatic, "workers_static", "workers_assets", "pages":
		return model.SiteDeploymentTargetCloudflareStatic, nil
	case "hybrid", model.SiteDeploymentTargetHybridEdge, "edge":
		return model.SiteDeploymentTargetHybridEdge, nil
	default:
		return "", errors.New(allowedMessage)
	}
}

// Alias normalizes known aliases and returns the lower-cased input for
// unknown values so CLI validation can stay command-specific.
func Alias(value string) string {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	normalized, err := Normalize(trimmed)
	if err == nil && normalized != "" {
		return normalized
	}
	return trimmed
}
