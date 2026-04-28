package model

import "time"

type ResourceLibraryHealth struct {
	Library             string    `json:"library"`
	Binding             string    `json:"binding"`
	BindingPath         string    `json:"binding_path"`
	Target              string    `json:"target"`
	TargetType          string    `json:"target_type"`
	Status              string    `json:"status"`
	CheckMode           string    `json:"check_mode"`
	ListLatencyMS       int64     `json:"list_latency_ms,omitempty"`
	WriteLatencyMS      int64     `json:"write_latency_ms,omitempty"`
	ReadLatencyMS       int64     `json:"read_latency_ms,omitempty"`
	DeleteLatencyMS     int64     `json:"delete_latency_ms,omitempty"`
	LastError           string    `json:"last_error,omitempty"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
	LastCheckedAt       time.Time `json:"last_checked_at"`
	LastSuccessAt       time.Time `json:"last_success_at,omitempty"`
	LastFailureAt       time.Time `json:"last_failure_at,omitempty"`
	UpdatedAt           time.Time `json:"updated_at"`
}
