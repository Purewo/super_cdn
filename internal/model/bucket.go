package model

import "time"

const (
	AssetBucketActive   = "active"
	AssetBucketDisabled = "disabled"

	AssetTypeImage    = "image"
	AssetTypeVideo    = "video"
	AssetTypeDocument = "document"
	AssetTypeArchive  = "archive"
	AssetTypeOther    = "other"
)

type AssetBucket struct {
	Slug                string    `json:"slug"`
	WorkspaceID         string    `json:"workspace_id,omitempty"`
	Name                string    `json:"name"`
	Description         string    `json:"description,omitempty"`
	RouteProfile        string    `json:"route_profile"`
	RoutingPolicy       string    `json:"routing_policy,omitempty"`
	AllowedTypes        []string  `json:"allowed_types,omitempty"`
	MaxCapacityBytes    int64     `json:"max_capacity_bytes,omitempty"`
	MaxFileSizeBytes    int64     `json:"max_file_size_bytes,omitempty"`
	DefaultCacheControl string    `json:"default_cache_control,omitempty"`
	Status              string    `json:"status"`
	UsedBytes           int64     `json:"used_bytes,omitempty"`
	ObjectCount         int64     `json:"object_count,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type AssetBucketObject struct {
	BucketSlug  string    `json:"bucket_slug"`
	LogicalPath string    `json:"logical_path"`
	ObjectID    int64     `json:"object_id"`
	AssetType   string    `json:"asset_type"`
	PhysicalKey string    `json:"physical_key"`
	Size        int64     `json:"size"`
	SHA256      string    `json:"sha256"`
	ContentType string    `json:"content_type"`
	URL         string    `json:"url,omitempty"`
	IPFS        []IPFSPin `json:"ipfs,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
