package storage

import (
	"context"
	"time"
)

const (
	HealthStatusOK     = "ok"
	HealthStatusFailed = "failed"

	HealthModePassive    = "passive"
	HealthModeWriteProbe = "write_probe"
)

type HealthCheckOptions struct {
	WriteProbe   bool
	ProbeKey     string
	Prefix       string
	ProbePayload []byte
}

type HealthCheckItem struct {
	BindingName     string    `json:"binding_name,omitempty"`
	BindingPath     string    `json:"binding_path,omitempty"`
	Target          string    `json:"target"`
	TargetType      string    `json:"target_type"`
	Status          string    `json:"status"`
	CheckMode       string    `json:"check_mode"`
	ListLatencyMS   int64     `json:"list_latency_ms,omitempty"`
	WriteLatencyMS  int64     `json:"write_latency_ms,omitempty"`
	ReadLatencyMS   int64     `json:"read_latency_ms,omitempty"`
	DeleteLatencyMS int64     `json:"delete_latency_ms,omitempty"`
	LastError       string    `json:"last_error,omitempty"`
	CheckedAt       time.Time `json:"checked_at"`
}

type HealthCheckResult struct {
	Target     string            `json:"target"`
	TargetType string            `json:"target_type"`
	Items      []HealthCheckItem `json:"items"`
}

type HealthCheckStore interface {
	HealthCheck(ctx context.Context, opts HealthCheckOptions) (*HealthCheckResult, error)
}
