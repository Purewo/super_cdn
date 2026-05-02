package storage

import (
	"context"
	"fmt"
	"path"
	"strings"

	"supercdn/internal/config"
)

func BuildManager(ctx context.Context, cfg *config.Config) (*Manager, error) {
	stores := make([]Store, 0, len(cfg.Storage))
	for _, s := range cfg.Storage {
		switch s.Type {
		case "local":
			store, err := NewLocalStore(s.Name, s.Local.Root)
			if err != nil {
				return nil, err
			}
			stores = append(stores, store)
		case "r2":
			store, err := NewR2Store(ctx, R2Options{
				Name:            s.Name,
				AccountID:       s.R2.AccountID,
				Endpoint:        s.R2.Endpoint,
				Bucket:          s.R2.Bucket,
				AccessKeyID:     s.R2.AccessKeyID,
				SecretAccessKey: s.R2.SecretAccessKey,
				PublicBaseURL:   s.R2.PublicBaseURL,
				ProxyURL:        s.R2.ProxyURL,
			})
			if err != nil {
				return nil, err
			}
			stores = append(stores, store)
		case "alist":
			store, err := NewAListStore(AListOptions{
				Name:          s.Name,
				BaseURL:       s.AList.BaseURL,
				Token:         s.AList.Token,
				Username:      s.AList.Username,
				Password:      s.AList.Password,
				Root:          s.AList.Root,
				UseProxyURL:   s.AList.UseProxyURL,
				PublicBaseURL: s.AList.PublicBaseURL,
				ProxyURL:      s.AList.ProxyURL,
				Network:       s.AList.Network,
			})
			if err != nil {
				return nil, err
			}
			stores = append(stores, store)
		case "pinata":
			store, err := NewPinataStore(PinataOptions{
				APIBaseURL:     s.Pinata.APIBaseURL,
				UploadBaseURL:  s.Pinata.UploadBaseURL,
				Name:           s.Name,
				JWT:            s.Pinata.JWT,
				GatewayBaseURL: s.Pinata.GatewayBaseURL,
				GroupPrefix:    s.Pinata.GroupPrefix,
				ProxyURL:       s.Pinata.ProxyURL,
			})
			if err != nil {
				return nil, err
			}
			stores = append(stores, store)
		default:
			return nil, fmt.Errorf("unsupported storage type %q for %q", s.Type, s.Name)
		}
	}
	mounts := map[string]config.MountPointConfig{}
	for _, mount := range cfg.MountPoints {
		mounts[mount.Name] = mount
	}
	for _, library := range cfg.ResourceLibraries {
		bindings := make([]ResourceLibraryBindingStore, 0, len(library.Bindings))
		for i, binding := range library.Bindings {
			mount, ok := mounts[binding.MountPoint]
			if !ok {
				return nil, fmt.Errorf("resource library %q references missing mount point %q", library.Name, binding.MountPoint)
			}
			bindingName := binding.Name
			if bindingName == "" {
				bindingName = fmt.Sprintf("%s_%d", binding.MountPoint, i+1)
			}
			switch mount.Type {
			case "alist":
				store, err := NewAListStore(AListOptions{
					Name:          library.Name + ":" + bindingName,
					BaseURL:       mount.AList.BaseURL,
					Token:         mount.AList.Token,
					Username:      mount.AList.Username,
					Password:      mount.AList.Password,
					Root:          joinMountPath(mount.AList.Root, binding.Path),
					UseProxyURL:   mount.AList.UseProxyURL,
					PublicBaseURL: mount.AList.PublicBaseURL,
					ProxyURL:      mount.AList.ProxyURL,
					Network:       mount.AList.Network,
				})
				if err != nil {
					return nil, err
				}
				bindings = append(bindings, ResourceLibraryBindingStore{
					Name:        bindingName,
					Path:        binding.Path,
					Store:       store,
					Constraints: toStorageConstraints(binding.Constraints),
				})
			default:
				return nil, fmt.Errorf("resource library %q binding %q uses unsupported mount point type %q", library.Name, bindingName, mount.Type)
			}
		}
		store, err := NewResourceLibraryStore(library.Name, bindings, toStorageLibraryPolicy(library.Policy, cfg.Limits.OverclockMode))
		if err != nil {
			return nil, err
		}
		stores = append(stores, store)
	}
	cfAccountStores, err := buildCloudflareAccountStores(ctx, cfg)
	if err != nil {
		return nil, err
	}
	for _, library := range cfg.CloudflareLibrariesEffective() {
		if !cfg.CloudflareLibraryHasStorage(library) {
			continue
		}
		bindings := make([]ResourceLibraryBindingStore, 0, len(library.Bindings))
		for i, binding := range library.Bindings {
			accountStore, ok := cfAccountStores[binding.Account]
			if !ok {
				continue
			}
			bindingName := binding.Name
			if bindingName == "" {
				bindingName = fmt.Sprintf("%s_%d", binding.Account, i+1)
			}
			store := Store(accountStore)
			if prefix := normalizeCloudflareBindingPrefix(binding.Path); prefix != "" {
				store = NewPrefixStore(library.Name+":"+bindingName, prefix, store)
			}
			bindings = append(bindings, ResourceLibraryBindingStore{
				Name:        bindingName,
				Path:        binding.Path,
				Store:       store,
				Constraints: toStorageConstraints(binding.Constraints),
			})
		}
		if len(bindings) == 0 {
			return nil, fmt.Errorf("cloudflare library %q does not have any storage-capable account bindings", library.Name)
		}
		store, err := NewResourceLibraryStore(library.Name, bindings, toStorageLibraryPolicy(library.Policy, cfg.Limits.OverclockMode))
		if err != nil {
			return nil, err
		}
		stores = append(stores, store)
	}
	return NewManager(stores), nil
}

func buildCloudflareAccountStores(ctx context.Context, cfg *config.Config) (map[string]Store, error) {
	accounts := cfg.CloudflareAccountsEffective()
	result := make(map[string]Store, len(accounts))
	for _, account := range accounts {
		if !cloudflareAccountHasR2(account) {
			continue
		}
		options, err := cloudflareAccountR2Options(account)
		if err != nil {
			return nil, err
		}
		store, err := NewR2Store(ctx, options)
		if err != nil {
			return nil, err
		}
		result[account.Name] = store
	}
	return result, nil
}

func cloudflareAccountHasR2(account config.CloudflareAccountConfig) bool {
	return strings.TrimSpace(account.R2.Bucket) != "" &&
		strings.TrimSpace(account.R2.AccessKeyID) != "" &&
		strings.TrimSpace(account.R2.SecretAccessKey) != ""
}

func cloudflareAccountR2Options(account config.CloudflareAccountConfig) (R2Options, error) {
	r2 := account.R2
	accountID := firstNonEmpty(strings.TrimSpace(r2.AccountID), strings.TrimSpace(account.AccountID))
	if accountID == "" {
		return R2Options{}, fmt.Errorf("cloudflare account %q r2.account_id is required", account.Name)
	}
	if strings.TrimSpace(r2.Bucket) == "" {
		return R2Options{}, fmt.Errorf("cloudflare account %q r2.bucket is required", account.Name)
	}
	if strings.TrimSpace(r2.AccessKeyID) == "" || strings.TrimSpace(r2.SecretAccessKey) == "" {
		return R2Options{}, fmt.Errorf("cloudflare account %q r2 credentials are required", account.Name)
	}
	return R2Options{
		Name:            "cloudflare_account:" + account.Name,
		AccountID:       accountID,
		Endpoint:        r2.Endpoint,
		Bucket:          r2.Bucket,
		AccessKeyID:     r2.AccessKeyID,
		SecretAccessKey: r2.SecretAccessKey,
		PublicBaseURL:   r2.PublicBaseURL,
		ProxyURL:        r2.ProxyURL,
	}, nil
}

func normalizeCloudflareBindingPrefix(v string) string {
	v = strings.Trim(strings.ReplaceAll(strings.TrimSpace(v), "\\", "/"), "/")
	return v
}

func joinMountPath(base, binding string) string {
	base = "/" + strings.Trim(strings.ReplaceAll(base, "\\", "/"), "/")
	binding = "/" + strings.Trim(strings.ReplaceAll(binding, "\\", "/"), "/")
	if base == "/" {
		return path.Clean(binding)
	}
	return path.Join(base, binding)
}

func toStorageConstraints(c config.ResourceLibraryBindingConstraints) BindingConstraints {
	return BindingConstraints{
		MaxCapacityBytes:          c.MaxCapacityBytes,
		PeakBandwidthMbps:         c.PeakBandwidthMbps,
		MaxBatchFiles:             c.MaxBatchFiles,
		MaxFileSizeBytes:          c.MaxFileSizeBytes,
		DailyUploadLimitBytes:     c.DailyUploadLimitBytes,
		DailyUploadLimitUnlimited: c.DailyUploadLimitUnlimited,
		SupportsOnlineExtract:     c.SupportsOnlineExtract,
		MaxOnlineExtractBytes:     c.MaxOnlineExtractBytes,
		Notes:                     c.Notes,
	}
}

func toStorageLibraryPolicy(p config.ResourceLibraryPolicy, ignoreLimits bool) ResourceLibraryPolicy {
	return ResourceLibraryPolicy{
		MaxBindings:        p.MaxBindings,
		TotalCapacityBytes: p.TotalCapacityBytes,
		AvailableBytes:     p.AvailableBytes,
		ReserveBytes:       p.ReserveBytes,
		Notes:              p.Notes,
		IgnoreLimits:       ignoreLimits,
	}
}
