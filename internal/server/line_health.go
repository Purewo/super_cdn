package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"supercdn/internal/storage"
)

const lineHealthBudget = 8 * time.Second

type lineHealthResponse struct {
	Status       string           `json:"status"`
	CheckedAtUTC string           `json:"checked_at_utc"`
	DurationMS   int64            `json:"duration_ms"`
	DeadlineMS   int64            `json:"deadline_ms"`
	Mode         string           `json:"mode"`
	Lines        []lineHealthLine `json:"lines"`
}

type lineHealthLine struct {
	Name       string              `json:"name"`
	TargetType string              `json:"target_type,omitempty"`
	Status     string              `json:"status"`
	LatencyMS  int64               `json:"latency_ms"`
	Reason     string              `json:"reason,omitempty"`
	Bindings   []lineHealthBinding `json:"bindings,omitempty"`
}

type lineHealthBinding struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	LatencyMS int64  `json:"latency_ms"`
	Reason    string `json:"reason,omitempty"`
}

type lineHealthResult struct {
	index int
	line  lineHealthLine
}

func (s *Server) handleLineHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.lineHealth(r.Context(), lineHealthBudget))
}

func (s *Server) lineHealth(parent context.Context, budget time.Duration) lineHealthResponse {
	started := time.Now()
	ctx, cancel := context.WithTimeout(parent, budget)
	defer cancel()

	names := s.lineHealthTargets()
	resp := lineHealthResponse{
		Status:       "unknown",
		CheckedAtUTC: started.UTC().Format(time.RFC3339),
		DeadlineMS:   budget.Milliseconds(),
		Mode:         storage.HealthModePassive,
		Lines:        make([]lineHealthLine, len(names)),
	}
	if len(names) == 0 {
		return resp
	}

	results := make(chan lineHealthResult, len(names))
	for i, name := range names {
		resp.Lines[i] = lineHealthLine{Name: name, Status: "timeout", Reason: "deadline_exceeded"}
		if store, ok := s.stores.Get(name); ok {
			resp.Lines[i].TargetType = store.Type()
		}
		go func(index int, target string) {
			line := s.probeLine(ctx, target)
			results <- lineHealthResult{index: index, line: line}
		}(i, name)
	}
	for remaining := len(names); remaining > 0; {
		select {
		case result := <-results:
			resp.Lines[result.index] = result.line
			remaining--
		case <-ctx.Done():
			remaining = 0
		}
	}
	resp.DurationMS = time.Since(started).Milliseconds()
	resp.Status = "ok"
	for i := range resp.Lines {
		line := &resp.Lines[i]
		if line.Status == "timeout" && line.LatencyMS == 0 {
			line.LatencyMS = resp.DurationMS
		}
		if line.Status != "ok" {
			resp.Status = "degraded"
			break
		}
	}
	return resp
}

func (s *Server) lineHealthTargets() []string {
	var names []string
	seen := map[string]bool{}
	add := func(name string) {
		if name != "" && !seen[name] {
			names = append(names, name)
			seen[name] = true
		}
	}
	for _, library := range s.cfg.ResourceLibraries {
		add(library.Name)
	}
	for _, library := range s.cfg.CloudflareLibrariesEffective() {
		if s.cfg.CloudflareLibraryHasStorage(library) {
			add(library.Name)
		}
	}
	for _, target := range s.cfg.Storage {
		if target.Type == "alist" || target.Type == "r2" || target.Type == "pinata" {
			add(target.Name)
		}
	}
	return names
}

func (s *Server) probeLine(ctx context.Context, name string) (line lineHealthLine) {
	started := time.Now()
	line = lineHealthLine{Name: name, Status: "failed"}
	defer func() { line.LatencyMS = time.Since(started).Milliseconds() }()
	store, ok := s.stores.Get(name)
	if !ok {
		line.Reason = "not_configured"
		return line
	}
	line.TargetType = store.Type()
	if checker, ok := store.(storage.HealthCheckStore); ok {
		var result *storage.HealthCheckResult
		err := s.withTransferSlot(ctx, func() error {
			var checkErr error
			result, checkErr = checker.HealthCheck(ctx, storage.HealthCheckOptions{})
			return checkErr
		})
		if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
			line.Status, line.Reason = "timeout", "deadline_exceeded"
			return line
		}
		if result == nil || len(result.Items) == 0 {
			line.Reason = lineHealthErrorCode(err)
			return line
		}
		line.Status = "ok"
		for _, item := range result.Items {
			binding := lineHealthBinding{
				Name:      item.BindingName,
				Status:    item.Status,
				LatencyMS: item.ListLatencyMS,
			}
			if binding.Name == "" {
				binding.Name = item.Target
			}
			if binding.Status != "ok" {
				line.Status = "failed"
				binding.Reason = lineHealthErrorCode(errors.New(item.LastError))
				if line.Reason == "" {
					line.Reason = binding.Reason
				}
			}
			line.Bindings = append(line.Bindings, binding)
		}
		if err != nil && line.Status == "ok" {
			line.Status, line.Reason = "failed", lineHealthErrorCode(err)
		}
		return line
	}
	if provider, ok := store.(storage.ProviderStatusStore); ok {
		var status storage.ProviderStatus
		err := s.withTransferSlot(ctx, func() error {
			status = provider.ProviderStatus(ctx)
			return nil
		})
		if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
			line.Status, line.Reason = "timeout", "deadline_exceeded"
		} else if err != nil {
			line.Reason = lineHealthErrorCode(err)
		} else if status.OK {
			line.Status = "ok"
		} else if !status.Token.Configured {
			line.Reason = "credential_missing"
		} else if !status.Gateway.Configured {
			line.Reason = "gateway_missing"
		} else {
			line.Reason = "provider_error"
		}
		return line
	}
	line.Status, line.Reason = "unsupported", "health_check_unsupported"
	return line
}

func lineHealthErrorCode(err error) string {
	if err == nil {
		return "provider_error"
	}
	message := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, context.DeadlineExceeded), strings.Contains(message, "timeout"), strings.Contains(message, "deadline exceeded"):
		return "timeout"
	case strings.Contains(message, "root path") && strings.Contains(message, "does not exist"):
		return "root_missing"
	case strings.Contains(message, "not configured"):
		return "not_configured"
	case strings.Contains(message, "unauthorized"), strings.Contains(message, "code=401"), strings.Contains(message, "status=401"):
		return "unauthorized"
	case strings.Contains(message, "forbidden"), strings.Contains(message, "code=403"), strings.Contains(message, "status=403"):
		return "forbidden"
	case strings.Contains(message, "connection refused"), strings.Contains(message, "no such host"), strings.Contains(message, "dial tcp"):
		return "unreachable"
	default:
		return "provider_error"
	}
}
