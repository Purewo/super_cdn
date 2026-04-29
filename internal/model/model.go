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
)

type Project struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
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
	Name             string    `json:"name"`
	Mode             string    `json:"mode"`
	RouteProfile     string    `json:"route_profile"`
	DeploymentTarget string    `json:"deployment_target"`
	Domains          []string  `json:"domains,omitempty"`
	URL              string    `json:"url,omitempty"`
	URLs             []string  `json:"urls,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}
