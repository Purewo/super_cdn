package model

import "time"

const (
	ReplicaPending = "pending"
	ReplicaReady   = "ready"
	ReplicaFailed  = "failed"

	JobQueued  = "queued"
	JobRunning = "running"
	JobDone    = "done"
	JobFailed  = "failed"

	JobReplicateObject       = "replicate_object"
	JobInitResourceLibraries = "init_resource_libraries"

	DefaultWorkspaceID = "default"

	RoleOwner      = "owner"
	RoleMaintainer = "maintainer"
	RoleViewer     = "viewer"
)

type Project struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type Object struct {
	ID            int64     `json:"id"`
	ProjectID     string    `json:"project_id"`
	Path          string    `json:"path"`
	Key           string    `json:"key"`
	RouteProfile  string    `json:"route_profile"`
	Size          int64     `json:"size"`
	SHA256        string    `json:"sha256"`
	ContentType   string    `json:"content_type"`
	CacheControl  string    `json:"cache_control"`
	PrimaryTarget string    `json:"primary_target"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type Replica struct {
	ID        int64     `json:"id"`
	ObjectID  int64     `json:"object_id"`
	Target    string    `json:"target"`
	Status    string    `json:"status"`
	Locator   string    `json:"locator"`
	LastError string    `json:"last_error"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Job struct {
	ID        int64     `json:"id"`
	Type      string    `json:"type"`
	Status    string    `json:"status"`
	Payload   string    `json:"payload"`
	Attempts  int       `json:"attempts"`
	LastError string    `json:"last_error"`
	Result    string    `json:"result,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Site struct {
	ID               string    `json:"id"`
	WorkspaceID      string    `json:"workspace_id,omitempty"`
	Name             string    `json:"name"`
	Mode             string    `json:"mode"`
	RouteProfile     string    `json:"route_profile"`
	DeploymentTarget string    `json:"deployment_target"`
	Domains          []string  `json:"domains,omitempty"`
	URL              string    `json:"url,omitempty"`
	URLs             []string  `json:"urls,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

type Workspace struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type User struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type WorkspaceMember struct {
	WorkspaceID string    `json:"workspace_id"`
	UserID      int64     `json:"user_id"`
	Role        string    `json:"role"`
	CreatedAt   time.Time `json:"created_at"`
}

type APIToken struct {
	ID          string    `json:"id"`
	UserID      int64     `json:"user_id"`
	WorkspaceID string    `json:"workspace_id"`
	Name        string    `json:"name"`
	TokenHash   string    `json:"-"`
	LastUsedAt  time.Time `json:"last_used_at,omitempty"`
	RevokedAt   time.Time `json:"revoked_at,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type Invite struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Name        string    `json:"name"`
	Role        string    `json:"role"`
	TokenHash   string    `json:"-"`
	CreatedBy   int64     `json:"created_by,omitempty"`
	ExpiresAt   time.Time `json:"expires_at"`
	AcceptedAt  time.Time `json:"accepted_at,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}
