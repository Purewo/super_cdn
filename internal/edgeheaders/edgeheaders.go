package edgeheaders

const (
	HeaderSource         = "X-SuperCDN-Edge-Source"
	HeaderManifest       = "X-SuperCDN-Edge-Manifest"
	HeaderAction         = "X-SuperCDN-Edge-Action"
	HeaderFile           = "X-SuperCDN-Edge-File"
	HeaderRedirect       = "X-SuperCDN-Redirect"
	HeaderRoutePolicy    = "X-SuperCDN-Route-Policy"
	HeaderRouteTarget    = "X-SuperCDN-Route-Target"
	HeaderRouteReason    = "X-SuperCDN-Route-Reason"
	HeaderManifestDryRun = "X-SuperCDN-Edge-Manifest-Dry-Run"

	SourceCloudflareStatic = "cloudflare_static"
	SourceIPFSGateway      = "ipfs_gateway"
	SourceManifest         = "manifest"
	SourceResourceFailover = "resource_failover"
	SourceStorage          = "storage"

	ManifestRoute   = "route"
	RedirectStorage = "storage"
)
