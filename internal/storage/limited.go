package storage

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type LimitedStore struct {
	store       Store
	policy      ResourceLibraryPolicy
	constraints BindingConstraints
	usageMu     sync.Mutex
	dailyUsage  map[string]int64
}

func NewLimitedStore(store Store, policy ResourceLibraryPolicy, constraints BindingConstraints) *LimitedStore {
	return &LimitedStore{
		store:       store,
		policy:      policy,
		constraints: constraints,
		dailyUsage:  map[string]int64{},
	}
}

func (s *LimitedStore) Name() string { return s.store.Name() }
func (s *LimitedStore) Type() string { return s.store.Type() }

func (s *LimitedStore) Capabilities() Capabilities {
	capabilities := StoreCapabilities(s.store)
	capabilities.Notes = append(capabilities.Notes, "direct storage target has configured Super CDN upload limits")
	return capabilities
}

func (s *LimitedStore) BindingCapabilities(binding string) (Capabilities, bool) {
	if binding == "" || binding == s.store.Name() {
		return s.Capabilities(), true
	}
	return Capabilities{}, false
}

func (s *LimitedStore) Put(ctx context.Context, opts PutOptions) (string, error) {
	usageKey := ""
	if !opts.IgnoreLimits && !s.policy.IgnoreLimits {
		if err := s.checkUploadConstraints(opts); err != nil {
			return "", err
		}
		var err error
		usageKey, err = s.reserveDailyUpload(opts.Size)
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
	locator, err := s.store.Put(ctx, opts)
	if err != nil {
		return "", err
	}
	rollbackUsage = false
	return locator, nil
}

func (s *LimitedStore) Get(ctx context.Context, key string, opts GetOptions) (*ObjectStream, error) {
	return s.store.Get(ctx, key, opts)
}

func (s *LimitedStore) Stat(ctx context.Context, key string) (*Stat, error) {
	return s.store.Stat(ctx, key)
}

func (s *LimitedStore) Delete(ctx context.Context, key string) error {
	return s.store.Delete(ctx, key)
}

func (s *LimitedStore) DeleteLocator(ctx context.Context, key, locator string) error {
	if deleter, ok := s.store.(LocatorDeleteStore); ok {
		return deleter.DeleteLocator(ctx, key, locator)
	}
	return s.store.Delete(ctx, key)
}

func (s *LimitedStore) PublicURL(key string) string {
	return s.store.PublicURL(key)
}

func (s *LimitedStore) PreflightPut(_ context.Context, opts PreflightOptions) (*PreflightResult, error) {
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
		if err := s.checkPreflightConstraints(opts); err != nil {
			return nil, err
		}
		if err := s.checkPolicyPreflightConstraints(opts); err != nil {
			return nil, err
		}
	}
	result := s.preflightResult()
	if opts.IgnoreLimits || s.policy.IgnoreLimits {
		result.OverclockMode = true
		result.Warnings = append(result.Warnings, "overclock mode is enabled: configured direct-storage limits are ignored")
	}
	return result, nil
}

func (s *LimitedStore) ProviderStatus(ctx context.Context) ProviderStatus {
	if provider, ok := s.store.(ProviderStatusStore); ok {
		return provider.ProviderStatus(ctx)
	}
	return ProviderStatus{
		Target:     s.Name(),
		TargetType: s.Type(),
		Provider:   s.Type(),
		OK:         true,
		Warnings:   []string{"provider status is not implemented for this storage type"},
		CheckedAt:  time.Now().UTC(),
	}
}

func (s *LimitedStore) RefreshIPFSPin(ctx context.Context, cid string) (IPFSPinStatus, error) {
	refresher, ok := s.store.(IPFSPinStatusStore)
	if !ok {
		return IPFSPinStatus{}, fmt.Errorf("storage %q does not support IPFS pin refresh", s.Name())
	}
	return refresher.RefreshIPFSPin(ctx, cid)
}

func (s *LimitedStore) HealthCheck(ctx context.Context, opts HealthCheckOptions) (*HealthCheckResult, error) {
	checker, ok := s.store.(HealthCheckStore)
	if !ok {
		return nil, fmt.Errorf("storage %q does not support health checks", s.Name())
	}
	return checker.HealthCheck(ctx, opts)
}

func (s *LimitedStore) InitDirs(ctx context.Context, opts InitOptions) (*InitResult, error) {
	initializer, ok := s.store.(InitializableStore)
	if !ok {
		return nil, fmt.Errorf("storage %q does not support directory initialization", s.Name())
	}
	return initializer.InitDirs(ctx, opts)
}

func (s *LimitedStore) checkUploadConstraints(opts PutOptions) error {
	c := s.constraints
	batchCount := opts.BatchFileCount
	if batchCount <= 0 {
		batchCount = 1
	}
	if c.MaxBatchFiles != nil && batchCount > *c.MaxBatchFiles {
		return fmt.Errorf("storage %q allows at most %d files per upload, got %d", s.Name(), *c.MaxBatchFiles, batchCount)
	}
	if c.MaxCapacityBytes != nil && opts.Size > *c.MaxCapacityBytes {
		return fmt.Errorf("storage %q capacity is %d bytes, upload got %d bytes", s.Name(), *c.MaxCapacityBytes, opts.Size)
	}
	if c.MaxFileSizeBytes != nil && opts.Size > *c.MaxFileSizeBytes {
		return fmt.Errorf("storage %q allows files up to %d bytes, got %d bytes", s.Name(), *c.MaxFileSizeBytes, opts.Size)
	}
	if !c.DailyUploadLimitUnlimited && c.DailyUploadLimitBytes != nil && opts.Size > *c.DailyUploadLimitBytes {
		return fmt.Errorf("storage %q daily upload limit is %d bytes, single upload got %d bytes", s.Name(), *c.DailyUploadLimitBytes, opts.Size)
	}
	return nil
}

func (s *LimitedStore) checkPreflightConstraints(opts PreflightOptions) error {
	c := s.constraints
	if c.MaxBatchFiles != nil && opts.BatchFileCount > *c.MaxBatchFiles {
		return fmt.Errorf("storage %q allows at most %d files per upload, got %d", s.Name(), *c.MaxBatchFiles, opts.BatchFileCount)
	}
	if c.MaxCapacityBytes != nil && opts.TotalSize > *c.MaxCapacityBytes {
		return fmt.Errorf("storage %q capacity is %d bytes, upload total got %d bytes", s.Name(), *c.MaxCapacityBytes, opts.TotalSize)
	}
	if c.MaxFileSizeBytes != nil && opts.LargestFileSize > *c.MaxFileSizeBytes {
		return fmt.Errorf("storage %q allows files up to %d bytes, largest file got %d bytes", s.Name(), *c.MaxFileSizeBytes, opts.LargestFileSize)
	}
	if !c.DailyUploadLimitUnlimited && c.DailyUploadLimitBytes != nil {
		used := s.dailyUploadUsed()
		if used+opts.TotalSize > *c.DailyUploadLimitBytes {
			return fmt.Errorf("storage %q daily upload limit is %d bytes, already used %d bytes, upload total got %d bytes", s.Name(), *c.DailyUploadLimitBytes, used, opts.TotalSize)
		}
	}
	return nil
}

func (s *LimitedStore) checkPolicyPreflightConstraints(opts PreflightOptions) error {
	summary := s.librarySummary()
	if summary.EffectiveCapacityBytes != nil && opts.TotalSize > *summary.EffectiveCapacityBytes {
		return fmt.Errorf("storage %q effective capacity is %d bytes, upload total got %d bytes", s.Name(), *summary.EffectiveCapacityBytes, opts.TotalSize)
	}
	if summary.EffectiveAvailableBytes != nil && opts.TotalSize > *summary.EffectiveAvailableBytes {
		return fmt.Errorf("storage %q effective available capacity is %d bytes, upload total got %d bytes", s.Name(), *summary.EffectiveAvailableBytes, opts.TotalSize)
	}
	return nil
}

func (s *LimitedStore) preflightResult() *PreflightResult {
	c := s.constraints
	result := &PreflightResult{
		Target:                    s.Name(),
		TargetType:                s.Type(),
		BindingName:               s.Name(),
		MaxCapacityBytes:          c.MaxCapacityBytes,
		PeakBandwidthMbps:         c.PeakBandwidthMbps,
		MaxBatchFiles:             c.MaxBatchFiles,
		MaxFileSizeBytes:          c.MaxFileSizeBytes,
		DailyUploadLimitBytes:     c.DailyUploadLimitBytes,
		DailyUploadLimitUnlimited: c.DailyUploadLimitUnlimited,
		DailyUploadUsedBytes:      s.dailyUploadUsed(),
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
	if c.PeakBandwidthMbps != nil {
		result.Warnings = append(result.Warnings, "peak bandwidth is metadata only and not rate-limited yet")
	}
	if c.MaxCapacityBytes != nil {
		result.Warnings = append(result.Warnings, "capacity is checked against this upload size only; remote used capacity is not measured yet")
	}
	return result
}

func (s *LimitedStore) librarySummary() *ResourceLibrarySummary {
	summary := &ResourceLibrarySummary{
		Name:                     s.Name(),
		BindingCount:             1,
		MaxBindings:              s.policy.MaxBindings,
		ConfiguredCapacityBytes:  s.policy.TotalCapacityBytes,
		ConfiguredAvailableBytes: s.policy.AvailableBytes,
		ReserveBytes:             s.policy.ReserveBytes,
		Notes:                    s.policy.Notes,
	}
	if s.constraints.MaxCapacityBytes != nil {
		summary.BindingCapacityBytes = s.constraints.MaxCapacityBytes
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

func (s *LimitedStore) reserveDailyUpload(size int64) (string, error) {
	c := s.constraints
	if c.DailyUploadLimitUnlimited || c.DailyUploadLimitBytes == nil {
		return "", nil
	}
	key := s.Name() + "/" + time.Now().Local().Format("2006-01-02")
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	used := s.dailyUsage[key]
	if used+size > *c.DailyUploadLimitBytes {
		return "", fmt.Errorf("storage %q daily upload limit is %d bytes, already used %d bytes, upload got %d bytes", s.Name(), *c.DailyUploadLimitBytes, used, size)
	}
	s.dailyUsage[key] = used + size
	return key, nil
}

func (s *LimitedStore) dailyUploadUsed() int64 {
	key := s.Name() + "/" + time.Now().Local().Format("2006-01-02")
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	return s.dailyUsage[key]
}

func (s *LimitedStore) releaseDailyUpload(key string, size int64) {
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	if used := s.dailyUsage[key]; used <= size {
		delete(s.dailyUsage, key)
	} else {
		s.dailyUsage[key] = used - size
	}
}
