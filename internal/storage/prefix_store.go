package storage

import (
	"context"
	"path"
	"strings"
)

type PrefixStore struct {
	name   string
	prefix string
	store  Store
}

func NewPrefixStore(name, prefix string, store Store) *PrefixStore {
	return &PrefixStore{
		name:   strings.TrimSpace(name),
		prefix: normalizePrefix(prefix),
		store:  store,
	}
}

func (s *PrefixStore) Name() string {
	if s.name != "" {
		return s.name
	}
	return s.store.Name()
}

func (s *PrefixStore) Type() string { return s.store.Type() }

func (s *PrefixStore) Put(ctx context.Context, opts PutOptions) (string, error) {
	opts.Key = s.withPrefix(opts.Key)
	return s.store.Put(ctx, opts)
}

func (s *PrefixStore) Get(ctx context.Context, key string, opts GetOptions) (*ObjectStream, error) {
	return s.store.Get(ctx, s.withPrefix(key), opts)
}

func (s *PrefixStore) Stat(ctx context.Context, key string) (*Stat, error) {
	return s.store.Stat(ctx, s.withPrefix(key))
}

func (s *PrefixStore) Delete(ctx context.Context, key string) error {
	return s.store.Delete(ctx, s.withPrefix(key))
}

func (s *PrefixStore) PublicURL(key string) string {
	return s.store.PublicURL(s.withPrefix(key))
}

func (s *PrefixStore) PreflightPut(ctx context.Context, opts PreflightOptions) (*PreflightResult, error) {
	preflight, ok := s.store.(PreflightStore)
	if !ok {
		return nil, nil
	}
	return preflight.PreflightPut(ctx, opts)
}

func (s *PrefixStore) HealthCheck(ctx context.Context, opts HealthCheckOptions) (*HealthCheckResult, error) {
	checker, ok := s.store.(HealthCheckStore)
	if !ok {
		return nil, nil
	}
	copyOpts := opts
	copyOpts.Prefix = joinStorePrefix(s.prefix, opts.Prefix)
	if opts.ProbeKey != "" {
		copyOpts.ProbeKey = s.withPrefix(opts.ProbeKey)
	}
	return checker.HealthCheck(ctx, copyOpts)
}

func (s *PrefixStore) InitDirs(ctx context.Context, opts InitOptions) (*InitResult, error) {
	initializer, ok := s.store.(InitializableStore)
	if !ok {
		return nil, nil
	}
	copyOpts := opts
	copyOpts.Directories = prefixDirectories(opts.Directories, s.prefix)
	copyOpts.MarkerPath = s.withPrefix(opts.MarkerPath)
	return initializer.InitDirs(ctx, copyOpts)
}

func (s *PrefixStore) withPrefix(key string) string {
	return joinStorePrefix(s.prefix, key)
}

func normalizePrefix(v string) string {
	v = strings.Trim(strings.ReplaceAll(strings.TrimSpace(v), "\\", "/"), "/")
	if v == "" {
		return ""
	}
	return v
}

func joinStorePrefix(prefix, key string) string {
	key = strings.Trim(strings.ReplaceAll(strings.TrimSpace(key), "\\", "/"), "/")
	if prefix == "" {
		return key
	}
	if key == "" {
		return prefix
	}
	return path.Join(prefix, key)
}

func prefixDirectories(values []string, prefix string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, joinStorePrefix(prefix, value))
	}
	return out
}
