package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

var ErrNotFound = errors.New("object not found")

type PutOptions struct {
	Key            string
	FilePath       string
	ContentType    string
	CacheControl   string
	Group          string
	SHA256         string
	Size           int64
	FileName       string
	BatchFileCount int
	IgnoreLimits   bool
}

type PreflightOptions struct {
	TotalSize       int64
	LargestFileSize int64
	BatchFileCount  int
	IgnoreLimits    bool
}

type PreflightResult struct {
	Target                    string                  `json:"target"`
	TargetType                string                  `json:"target_type"`
	BindingName               string                  `json:"binding_name,omitempty"`
	BindingPath               string                  `json:"binding_path,omitempty"`
	MaxCapacityBytes          *int64                  `json:"max_capacity_bytes,omitempty"`
	PeakBandwidthMbps         *int64                  `json:"peak_bandwidth_mbps,omitempty"`
	MaxBatchFiles             *int                    `json:"max_batch_files,omitempty"`
	MaxFileSizeBytes          *int64                  `json:"max_file_size_bytes,omitempty"`
	DailyUploadLimitBytes     *int64                  `json:"daily_upload_limit_bytes,omitempty"`
	DailyUploadLimitUnlimited bool                    `json:"daily_upload_limit_unlimited,omitempty"`
	DailyUploadUsedBytes      int64                   `json:"daily_upload_used_bytes,omitempty"`
	DailyUploadRemainingBytes *int64                  `json:"daily_upload_remaining_bytes,omitempty"`
	SupportsOnlineExtract     *bool                   `json:"supports_online_extract,omitempty"`
	MaxOnlineExtractBytes     *int64                  `json:"max_online_extract_bytes,omitempty"`
	Notes                     string                  `json:"notes,omitempty"`
	Warnings                  []string                `json:"warnings,omitempty"`
	LibrarySummary            *ResourceLibrarySummary `json:"library_summary,omitempty"`
	OverclockMode             bool                    `json:"overclock_mode,omitempty"`
}

type ResourceLibrarySummary struct {
	Name                     string   `json:"name"`
	BindingCount             int      `json:"binding_count"`
	MaxBindings              *int64   `json:"max_bindings,omitempty"`
	ConfiguredCapacityBytes  *int64   `json:"configured_capacity_bytes,omitempty"`
	BindingCapacityBytes     *int64   `json:"binding_capacity_bytes,omitempty"`
	EffectiveCapacityBytes   *int64   `json:"effective_capacity_bytes,omitempty"`
	ConfiguredAvailableBytes *int64   `json:"configured_available_bytes,omitempty"`
	ReserveBytes             *int64   `json:"reserve_bytes,omitempty"`
	EffectiveAvailableBytes  *int64   `json:"effective_available_bytes,omitempty"`
	UnknownCapacityBindings  []string `json:"unknown_capacity_bindings,omitempty"`
	Notes                    string   `json:"notes,omitempty"`
}

type GetOptions struct {
	Range   string
	Locator string
}

type ObjectStream struct {
	Body         io.ReadCloser
	StatusCode   int
	Size         int64
	ContentType  string
	CacheControl string
	ETag         string
	LastModified time.Time
	ContentRange string
	Locator      string
}

type Stat struct {
	Size         int64
	ContentType  string
	CacheControl string
	ETag         string
	LastModified time.Time
	Locator      string
}

type Store interface {
	Name() string
	Type() string
	Put(ctx context.Context, opts PutOptions) (string, error)
	Get(ctx context.Context, key string, opts GetOptions) (*ObjectStream, error)
	Stat(ctx context.Context, key string) (*Stat, error)
	Delete(ctx context.Context, key string) error
	PublicURL(key string) string
}

type LocatorDeleteStore interface {
	DeleteLocator(ctx context.Context, key, locator string) error
}

type ProviderCheckStatus struct {
	Configured bool   `json:"configured"`
	OK         bool   `json:"ok"`
	Message    string `json:"message,omitempty"`
}

type ProviderStatus struct {
	Target         string              `json:"target"`
	TargetType     string              `json:"target_type"`
	Provider       string              `json:"provider"`
	OK             bool                `json:"ok"`
	APIBaseURL     string              `json:"api_base_url,omitempty"`
	UploadBaseURL  string              `json:"upload_base_url,omitempty"`
	GatewayBaseURL string              `json:"gateway_base_url,omitempty"`
	Token          ProviderCheckStatus `json:"token"`
	Gateway        ProviderCheckStatus `json:"gateway"`
	Warnings       []string            `json:"warnings,omitempty"`
	CheckedAt      time.Time           `json:"checked_at"`
}

type ProviderStatusStore interface {
	ProviderStatus(ctx context.Context) ProviderStatus
}

type IPFSPinStatus struct {
	Provider      string `json:"provider"`
	CID           string `json:"cid"`
	PinStatus     string `json:"pin_status"`
	ProviderPinID string `json:"provider_pin_id,omitempty"`
	GatewayURL    string `json:"gateway_url,omitempty"`
	Locator       string `json:"locator,omitempty"`
}

type IPFSPinStatusStore interface {
	RefreshIPFSPin(ctx context.Context, cid string) (IPFSPinStatus, error)
}

type PreflightStore interface {
	PreflightPut(ctx context.Context, opts PreflightOptions) (*PreflightResult, error)
}

type InitOptions struct {
	Directories   []string
	MarkerPath    string
	MarkerPayload []byte
	DryRun        bool
}

type InitPathResult struct {
	Path       string `json:"path"`
	RemotePath string `json:"remote_path,omitempty"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
}

type InitBindingResult struct {
	BindingName string           `json:"binding_name,omitempty"`
	BindingPath string           `json:"binding_path,omitempty"`
	Target      string           `json:"target"`
	TargetType  string           `json:"target_type"`
	Directories []InitPathResult `json:"directories,omitempty"`
	Files       []InitPathResult `json:"files,omitempty"`
}

type InitResult struct {
	Target      string              `json:"target"`
	TargetType  string              `json:"target_type"`
	Directories []InitPathResult    `json:"directories,omitempty"`
	Files       []InitPathResult    `json:"files,omitempty"`
	Bindings    []InitBindingResult `json:"bindings,omitempty"`
}

type InitializableStore interface {
	InitDirs(ctx context.Context, opts InitOptions) (*InitResult, error)
}

type Manager struct {
	stores map[string]Store
}

func NewManager(stores []Store) *Manager {
	m := &Manager{stores: map[string]Store{}}
	for _, s := range stores {
		m.stores[s.Name()] = s
	}
	return m
}

func (m *Manager) Get(name string) (Store, bool) {
	s, ok := m.stores[name]
	return s, ok
}

func (m *Manager) Names() []string {
	names := make([]string, 0, len(m.stores))
	for name := range m.stores {
		names = append(names, name)
	}
	return names
}

func CloseQuietly(c io.Closer) {
	if c != nil {
		_ = c.Close()
	}
}
