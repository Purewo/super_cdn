package storage

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

type ResourceLibraryStore struct {
	name       string
	policy     ResourceLibraryPolicy
	bindings   []ResourceLibraryBindingStore
	usageMu    sync.Mutex
	dailyUsage map[string]int64
}

type ResourceLibraryPolicy struct {
	MaxBindings        *int64
	TotalCapacityBytes *int64
	AvailableBytes     *int64
	ReserveBytes       *int64
	Notes              string
	IgnoreLimits       bool
}

type ResourceLibraryBindingStore struct {
	Name        string
	Path        string
	Store       Store
	Constraints BindingConstraints
}

type BindingConstraints struct {
	MaxCapacityBytes          *int64
	PeakBandwidthMbps         *int64
	MaxBatchFiles             *int
	MaxFileSizeBytes          *int64
	DailyUploadLimitBytes     *int64
	DailyUploadLimitUnlimited bool
	SupportsOnlineExtract     *bool
	MaxOnlineExtractBytes     *int64
	Notes                     string
}

func NewResourceLibraryStore(name string, bindings []ResourceLibraryBindingStore, policy ...ResourceLibraryPolicy) (*ResourceLibraryStore, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("resource library name is required")
	}
	if len(bindings) == 0 {
		return nil, fmt.Errorf("resource library %q requires at least one binding", name)
	}
	for i := range bindings {
		if strings.TrimSpace(bindings[i].Name) == "" {
			return nil, fmt.Errorf("resource library %q binding[%d] name is required", name, i)
		}
		if bindings[i].Store == nil {
			return nil, fmt.Errorf("resource library %q binding %q has no store", name, bindings[i].Name)
		}
	}
	var p ResourceLibraryPolicy
	if len(policy) > 0 {
		p = policy[0]
	}
	if !p.IgnoreLimits && p.MaxBindings != nil && int64(len(bindings)) > *p.MaxBindings {
		return nil, fmt.Errorf("resource library %q allows at most %d bindings, got %d", name, *p.MaxBindings, len(bindings))
	}
	return &ResourceLibraryStore{name: name, policy: p, bindings: bindings, dailyUsage: map[string]int64{}}, nil
}

func (s *ResourceLibraryStore) Name() string { return s.name }
func (s *ResourceLibraryStore) Type() string { return "resource_library" }
func (s *ResourceLibraryStore) Capabilities() Capabilities {
	if len(s.bindings) == 0 {
		return StoreCapabilities(nil)
	}
	capabilities := StoreCapabilities(s.bindings[0].Store)
	capabilities.Notes = append(
		capabilities.Notes,
		fmt.Sprintf("resource library uses binding %q as the primary write target", s.bindings[0].Name),
	)
	if len(s.bindings) > 1 {
		capabilities.Notes = append(capabilities.Notes, fmt.Sprintf("library has %d bindings; current writes go to the primary binding and reads can fall through to other bindings", len(s.bindings)))
	}
	return capabilities
}

func (s *ResourceLibraryStore) BindingCapabilities(binding string) (Capabilities, bool) {
	for _, item := range s.bindings {
		if item.Name == binding {
			return StoreCapabilities(item.Store), true
		}
	}
	return Capabilities{}, false
}

func (s *ResourceLibraryStore) Put(ctx context.Context, opts PutOptions) (string, error) {
	binding := s.bindings[0]
	usageKey := ""
	if !opts.IgnoreLimits && !s.policy.IgnoreLimits {
		if err := s.checkUploadConstraints(binding, opts); err != nil {
			return "", err
		}
		var err error
		usageKey, err = s.reserveDailyUpload(binding, opts.Size)
		if err != nil {
			return "", err
		}
	}
	rollbackUsage := usageKey != ""
	if rollbackUsage {
		defer func() {
			if rollbackUsage {
				s.releaseDailyUpload(usageKey, opts.Size)
			}
		}()
	}
	locator, err := binding.Store.Put(ctx, opts)
	if err != nil {
		return "", err
	}
	locator, err = waitResourceLibraryPutVisible(ctx, binding.Store, opts.Key, locator)
	if err != nil {
		return "", fmt.Errorf("resource library %q binding %q upload %q not visible: %w", s.name, binding.Name, opts.Key, err)
	}
	rollbackUsage = false
	return encodeResourceLocator(binding.Name, locator), nil
}

var (
	resourceLibraryPutStatAttempts = 60
	resourceLibraryPutStatDelay    = 2 * time.Second
)

func waitResourceLibraryPutVisible(ctx context.Context, store Store, key, fallbackLocator string) (string, error) {
	attempts := resourceLibraryPutStatAttempts
	if attempts <= 0 {
		attempts = 1
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		stat, err := store.Stat(ctx, key)
		if err == nil {
			return firstNonEmpty(stat.Locator, fallbackLocator), nil
		}
		lastErr = err
		if i == attempts-1 {
			break
		}
		delay := resourceLibraryPutStatDelay
		if delay <= 0 {
			continue
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(delay):
		}
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", ErrNotFound
}

func (s *ResourceLibraryStore) PreflightPut(_ context.Context, opts PreflightOptions) (*PreflightResult, error) {
	binding := s.bindings[0]
	if opts.BatchFileCount <= 0 {
		opts.BatchFileCount = 1
	}
	if opts.LargestFileSize <= 0 {
		opts.LargestFileSize = opts.TotalSize
	}
	if opts.TotalSize <= 0 {
		opts.TotalSize = opts.LargestFileSize
	}
	if !opts.IgnoreLimits && !s.policy.IgnoreLimits {
		if err := s.checkPreflightConstraints(binding, opts); err != nil {
			return nil, err
		}
		if err := s.checkLibraryPreflightConstraints(opts); err != nil {
			return nil, err
		}
	}
	result := s.preflightResult(binding, opts)
	if opts.IgnoreLimits || s.policy.IgnoreLimits {
		result.OverclockMode = true
		result.Warnings = append(result.Warnings, "overclock mode is enabled: configured resource-library limits are ignored; this can cause unpredictable or catastrophic results")
	}
	return result, nil
}

func (s *ResourceLibraryStore) Get(ctx context.Context, key string, opts GetOptions) (*ObjectStream, error) {
	var lastErr error
	if opts.Locator != "" {
		if bindingName, innerLocator, ok := decodeResourceLocator(opts.Locator); ok {
			if binding := s.binding(bindingName); binding != nil {
				targetOpts := opts
				targetOpts.Locator = innerLocator
				stream, err := binding.Store.Get(ctx, key, targetOpts)
				if err == nil {
					return stream, nil
				}
				lastErr = err

				fallbackOpts := opts
				fallbackOpts.Locator = ""
				for _, fallback := range s.bindings {
					if fallback.Name == bindingName {
						continue
					}
					stream, err := fallback.Store.Get(ctx, key, fallbackOpts)
					if err == nil {
						return stream, nil
					}
					lastErr = err
				}
				return nil, lastErr
			}
		}
	}
	for _, binding := range s.bindings {
		stream, err := binding.Store.Get(ctx, key, opts)
		if err == nil {
			return stream, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, ErrNotFound
}

func (s *ResourceLibraryStore) Stat(ctx context.Context, key string) (*Stat, error) {
	var lastErr error
	for _, binding := range s.bindings {
		stat, err := binding.Store.Stat(ctx, key)
		if err == nil {
			return stat, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, ErrNotFound
}

func (s *ResourceLibraryStore) Delete(ctx context.Context, key string) error {
	return s.bindings[0].Store.Delete(ctx, key)
}

func (s *ResourceLibraryStore) PublicURL(key string) string {
	return s.bindings[0].Store.PublicURL(key)
}

func (s *ResourceLibraryStore) HealthCheck(ctx context.Context, opts HealthCheckOptions) (*HealthCheckResult, error) {
	result := &HealthCheckResult{Target: s.name, TargetType: s.Type()}
	var firstErr error
	for _, binding := range s.bindings {
		checker, ok := binding.Store.(HealthCheckStore)
		if !ok {
			err := fmt.Errorf("store %q does not support health checks", binding.Store.Name())
			result.Items = append(result.Items, HealthCheckItem{
				BindingName: binding.Name,
				BindingPath: binding.Path,
				Target:      binding.Store.Name(),
				TargetType:  binding.Store.Type(),
				Status:      HealthStatusFailed,
				CheckMode:   healthMode(opts),
				LastError:   err.Error(),
				CheckedAt:   time.Now().UTC(),
			})
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		inner, err := checker.HealthCheck(ctx, opts)
		if inner != nil {
			for _, item := range inner.Items {
				item.BindingName = binding.Name
				item.BindingPath = binding.Path
				result.Items = append(result.Items, item)
			}
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return result, firstErr
}

func (s *ResourceLibraryStore) InitDirs(ctx context.Context, opts InitOptions) (*InitResult, error) {
	result := &InitResult{Target: s.name, TargetType: s.Type()}
	var firstErr error
	for _, binding := range s.bindings {
		bindingResult := InitBindingResult{
			BindingName: binding.Name,
			BindingPath: binding.Path,
			Target:      binding.Store.Name(),
			TargetType:  binding.Store.Type(),
		}
		initializer, ok := binding.Store.(InitializableStore)
		if !ok {
			err := fmt.Errorf("store %q does not support directory initialization", binding.Store.Name())
			bindingResult.Directories = append(bindingResult.Directories, InitPathResult{Status: "error", Error: err.Error()})
			if firstErr == nil {
				firstErr = err
			}
			result.Bindings = append(result.Bindings, bindingResult)
			continue
		}
		inner, err := initializer.InitDirs(ctx, opts)
		if inner != nil {
			bindingResult.Directories = inner.Directories
			bindingResult.Files = inner.Files
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
		result.Bindings = append(result.Bindings, bindingResult)
	}
	return result, firstErr
}

func (s *ResourceLibraryStore) binding(name string) *ResourceLibraryBindingStore {
	for i := range s.bindings {
		if s.bindings[i].Name == name {
			return &s.bindings[i]
		}
	}
	return nil
}

func (s *ResourceLibraryStore) checkUploadConstraints(binding ResourceLibraryBindingStore, opts PutOptions) error {
	c := binding.Constraints
	batchCount := opts.BatchFileCount
	if batchCount <= 0 {
		batchCount = 1
	}
	if c.MaxBatchFiles != nil && batchCount > *c.MaxBatchFiles {
		return fmt.Errorf("resource library %q binding %q allows at most %d files per upload, got %d", s.name, binding.Name, *c.MaxBatchFiles, batchCount)
	}
	if c.MaxCapacityBytes != nil && opts.Size > *c.MaxCapacityBytes {
		return fmt.Errorf("resource library %q binding %q capacity is %d bytes, upload got %d bytes", s.name, binding.Name, *c.MaxCapacityBytes, opts.Size)
	}
	if c.MaxFileSizeBytes != nil && opts.Size > *c.MaxFileSizeBytes {
		return fmt.Errorf("resource library %q binding %q allows files up to %d bytes, got %d bytes", s.name, binding.Name, *c.MaxFileSizeBytes, opts.Size)
	}
	if !c.DailyUploadLimitUnlimited && c.DailyUploadLimitBytes != nil && opts.Size > *c.DailyUploadLimitBytes {
		return fmt.Errorf("resource library %q binding %q daily upload limit is %d bytes, single upload got %d bytes", s.name, binding.Name, *c.DailyUploadLimitBytes, opts.Size)
	}
	return nil
}

func (s *ResourceLibraryStore) checkPreflightConstraints(binding ResourceLibraryBindingStore, opts PreflightOptions) error {
	c := binding.Constraints
	if c.MaxBatchFiles != nil && opts.BatchFileCount > *c.MaxBatchFiles {
		return fmt.Errorf("resource library %q binding %q allows at most %d files per upload, got %d", s.name, binding.Name, *c.MaxBatchFiles, opts.BatchFileCount)
	}
	if c.MaxCapacityBytes != nil && opts.TotalSize > *c.MaxCapacityBytes {
		return fmt.Errorf("resource library %q binding %q capacity is %d bytes, upload total got %d bytes", s.name, binding.Name, *c.MaxCapacityBytes, opts.TotalSize)
	}
	if c.MaxFileSizeBytes != nil && opts.LargestFileSize > *c.MaxFileSizeBytes {
		return fmt.Errorf("resource library %q binding %q allows files up to %d bytes, largest file got %d bytes", s.name, binding.Name, *c.MaxFileSizeBytes, opts.LargestFileSize)
	}
	if !c.DailyUploadLimitUnlimited && c.DailyUploadLimitBytes != nil {
		used := s.dailyUploadUsed(binding)
		if used+opts.TotalSize > *c.DailyUploadLimitBytes {
			return fmt.Errorf("resource library %q binding %q daily upload limit is %d bytes, already used %d bytes, upload total got %d bytes", s.name, binding.Name, *c.DailyUploadLimitBytes, used, opts.TotalSize)
		}
	}
	return nil
}

func (s *ResourceLibraryStore) checkLibraryPreflightConstraints(opts PreflightOptions) error {
	summary := s.librarySummary()
	if summary.EffectiveCapacityBytes != nil && opts.TotalSize > *summary.EffectiveCapacityBytes {
		return fmt.Errorf("resource library %q effective capacity is %d bytes, upload total got %d bytes", s.name, *summary.EffectiveCapacityBytes, opts.TotalSize)
	}
	if summary.EffectiveAvailableBytes != nil && opts.TotalSize > *summary.EffectiveAvailableBytes {
		return fmt.Errorf("resource library %q effective available capacity is %d bytes, upload total got %d bytes", s.name, *summary.EffectiveAvailableBytes, opts.TotalSize)
	}
	return nil
}

func (s *ResourceLibraryStore) preflightResult(binding ResourceLibraryBindingStore, opts PreflightOptions) *PreflightResult {
	c := binding.Constraints
	result := &PreflightResult{
		Target:                    s.name,
		TargetType:                s.Type(),
		BindingName:               binding.Name,
		BindingPath:               binding.Path,
		MaxCapacityBytes:          c.MaxCapacityBytes,
		PeakBandwidthMbps:         c.PeakBandwidthMbps,
		MaxBatchFiles:             c.MaxBatchFiles,
		MaxFileSizeBytes:          c.MaxFileSizeBytes,
		DailyUploadLimitBytes:     c.DailyUploadLimitBytes,
		DailyUploadLimitUnlimited: c.DailyUploadLimitUnlimited,
		DailyUploadUsedBytes:      s.dailyUploadUsed(binding),
		SupportsOnlineExtract:     c.SupportsOnlineExtract,
		MaxOnlineExtractBytes:     c.MaxOnlineExtractBytes,
		Notes:                     c.Notes,
		LibrarySummary:            s.librarySummary(),
	}
	if c.DailyUploadLimitBytes != nil && !c.DailyUploadLimitUnlimited {
		remaining := *c.DailyUploadLimitBytes - result.DailyUploadUsedBytes
		if remaining < 0 {
			remaining = 0
		}
		result.DailyUploadRemainingBytes = &remaining
	}
	if c.MaxBatchFiles == nil {
		result.Warnings = append(result.Warnings, "max_batch_files is unknown and not enforced")
	}
	if c.DailyUploadLimitBytes == nil && !c.DailyUploadLimitUnlimited {
		result.Warnings = append(result.Warnings, "daily upload limit is unknown and not enforced")
	}
	if c.PeakBandwidthMbps != nil {
		result.Warnings = append(result.Warnings, "peak bandwidth is metadata only and not rate-limited yet")
	}
	if c.MaxCapacityBytes != nil {
		result.Warnings = append(result.Warnings, "capacity is checked against this upload size only; remote used capacity is not measured yet")
	}
	return result
}

func (s *ResourceLibraryStore) librarySummary() *ResourceLibrarySummary {
	summary := &ResourceLibrarySummary{
		Name:                     s.name,
		BindingCount:             len(s.bindings),
		MaxBindings:              s.policy.MaxBindings,
		ConfiguredCapacityBytes:  s.policy.TotalCapacityBytes,
		ConfiguredAvailableBytes: s.policy.AvailableBytes,
		ReserveBytes:             s.policy.ReserveBytes,
		Notes:                    s.policy.Notes,
	}
	var total int64
	allKnown := true
	for _, binding := range s.bindings {
		if binding.Constraints.MaxCapacityBytes == nil {
			allKnown = false
			summary.UnknownCapacityBindings = append(summary.UnknownCapacityBindings, binding.Name)
			continue
		}
		total += *binding.Constraints.MaxCapacityBytes
	}
	if allKnown {
		summary.BindingCapacityBytes = &total
	}
	if summary.ConfiguredCapacityBytes != nil {
		summary.EffectiveCapacityBytes = summary.ConfiguredCapacityBytes
	} else if summary.BindingCapacityBytes != nil {
		summary.EffectiveCapacityBytes = summary.BindingCapacityBytes
	}
	if summary.ConfiguredAvailableBytes != nil {
		available := *summary.ConfiguredAvailableBytes
		if summary.ReserveBytes != nil {
			available -= *summary.ReserveBytes
			if available < 0 {
				available = 0
			}
		}
		summary.EffectiveAvailableBytes = &available
	}
	return summary
}

func (s *ResourceLibraryStore) reserveDailyUpload(binding ResourceLibraryBindingStore, size int64) (string, error) {
	c := binding.Constraints
	if c.DailyUploadLimitUnlimited || c.DailyUploadLimitBytes == nil {
		return "", nil
	}
	key := s.name + "/" + binding.Name + "/" + time.Now().Local().Format("2006-01-02")
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	used := s.dailyUsage[key]
	if used+size > *c.DailyUploadLimitBytes {
		return "", fmt.Errorf("resource library %q binding %q daily upload limit is %d bytes, already used %d bytes, upload got %d bytes", s.name, binding.Name, *c.DailyUploadLimitBytes, used, size)
	}
	s.dailyUsage[key] = used + size
	return key, nil
}

func (s *ResourceLibraryStore) dailyUploadUsed(binding ResourceLibraryBindingStore) int64 {
	key := s.name + "/" + binding.Name + "/" + time.Now().Local().Format("2006-01-02")
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	return s.dailyUsage[key]
}

func (s *ResourceLibraryStore) releaseDailyUpload(key string, size int64) {
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	if used := s.dailyUsage[key]; used <= size {
		delete(s.dailyUsage, key)
	} else {
		s.dailyUsage[key] = used - size
	}
}

func encodeResourceLocator(bindingName, innerLocator string) string {
	values := url.Values{}
	values.Set("locator", innerLocator)
	return "resource-library://" + url.PathEscape(bindingName) + "?" + values.Encode()
}

func decodeResourceLocator(raw string) (bindingName, innerLocator string, ok bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "resource-library" {
		return "", "", false
	}
	bindingName, err = url.PathUnescape(u.Host)
	if err != nil || bindingName == "" {
		return "", "", false
	}
	return bindingName, u.Query().Get("locator"), true
}

func healthMode(opts HealthCheckOptions) string {
	if opts.WriteProbe {
		return HealthModeWriteProbe
	}
	return HealthModePassive
}
