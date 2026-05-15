package edgeheaders

import "testing"

func TestProtocolConstants(t *testing.T) {
	tests := map[string]string{
		"HeaderSource":           HeaderSource,
		"HeaderManifest":         HeaderManifest,
		"HeaderAction":           HeaderAction,
		"HeaderFile":             HeaderFile,
		"HeaderRedirect":         HeaderRedirect,
		"HeaderRoutePolicy":      HeaderRoutePolicy,
		"HeaderRouteTarget":      HeaderRouteTarget,
		"HeaderRouteReason":      HeaderRouteReason,
		"HeaderManifestDryRun":   HeaderManifestDryRun,
		"SourceCloudflareStatic": SourceCloudflareStatic,
		"SourceIPFSGateway":      SourceIPFSGateway,
		"SourceManifest":         SourceManifest,
		"SourceResourceFailover": SourceResourceFailover,
		"SourceStorage":          SourceStorage,
		"ManifestRoute":          ManifestRoute,
		"RedirectStorage":        RedirectStorage,
	}
	for name, value := range tests {
		if value == "" {
			t.Fatalf("%s is empty", name)
		}
	}
	if HeaderSource != "X-SuperCDN-Edge-Source" {
		t.Fatalf("HeaderSource changed: %q", HeaderSource)
	}
	if SourceCloudflareStatic != "cloudflare_static" {
		t.Fatalf("SourceCloudflareStatic changed: %q", SourceCloudflareStatic)
	}
}
