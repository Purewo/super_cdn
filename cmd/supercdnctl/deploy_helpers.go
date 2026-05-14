package main

import (
	"strings"

	"supercdn/internal/deploymenttarget"
)

func cleanWorkerName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func deploymentTargetAlias(value string) string {
	return deploymenttarget.Alias(value)
}

func extractCloudflareVersionID(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if value, ok := strings.CutPrefix(line, "Current Version ID:"); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
