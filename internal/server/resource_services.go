package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"supercdn/internal/config"
	"supercdn/internal/db"
	"supercdn/internal/model"
	"supercdn/internal/storage"
)

func (s *Server) runResourceLibraryHealthCheck(ctx context.Context, library string, writeProbe bool) error {
	store, ok := s.stores.Get(library)
	if !ok {
		return fmt.Errorf("resource library %q is not configured", library)
	}
	checker, ok := store.(storage.HealthCheckStore)
	if !ok {
		return fmt.Errorf("resource library %q does not support health checks", library)
	}
	var result *storage.HealthCheckResult
	err := s.withTransferSlot(ctx, func() error {
		var checkErr error
		result, checkErr = checker.HealthCheck(ctx, storage.HealthCheckOptions{WriteProbe: writeProbe})
		return checkErr
	})
	if result != nil {
		for _, item := range result.Items {
			binding := item.BindingName
			if binding == "" {
				binding = item.Target
			}
			if _, saveErr := s.db.UpsertResourceLibraryHealth(ctx, model.ResourceLibraryHealth{
				Library:         library,
				Binding:         binding,
				BindingPath:     item.BindingPath,
				Target:          item.Target,
				TargetType:      item.TargetType,
				Status:          item.Status,
				CheckMode:       item.CheckMode,
				ListLatencyMS:   item.ListLatencyMS,
				WriteLatencyMS:  item.WriteLatencyMS,
				ReadLatencyMS:   item.ReadLatencyMS,
				DeleteLatencyMS: item.DeleteLatencyMS,
				LastError:       item.LastError,
				LastCheckedAt:   item.CheckedAt,
			}); saveErr != nil {
				return saveErr
			}
		}
	}
	return err
}

func (s *Server) resourceLibraryHealthFresh(ctx context.Context, library string, minIntervalSeconds int) bool {
	if minIntervalSeconds <= 0 {
		return false
	}
	config, ok := s.resourceLibraryConfig(library)
	if !ok || len(config.Bindings) == 0 {
		return false
	}
	cutoff := time.Now().UTC().Add(-time.Duration(minIntervalSeconds) * time.Second)
	for i, binding := range config.Bindings {
		name := bindingConfigName(binding, i)
		health, err := s.db.GetResourceLibraryHealth(ctx, library, name)
		if err != nil || health.LastCheckedAt.Before(cutoff) {
			return false
		}
	}
	return true
}

func (s *Server) resourceLibraryStatusViews(ctx context.Context, libraries []string, skipped map[string]string) ([]resourceLibraryStatusView, error) {
	healthRows, err := s.db.ResourceLibraryHealth(ctx, "")
	if err != nil {
		return nil, err
	}
	healthByKey := map[string]model.ResourceLibraryHealth{}
	for _, health := range healthRows {
		healthByKey[health.Library+"/"+health.Binding] = health
	}
	views := make([]resourceLibraryStatusView, 0, len(libraries))
	for _, name := range libraries {
		config, ok := s.resourceLibraryConfig(name)
		if !ok {
			if direct, ok := s.directStorageStatusView(ctx, name, skipped); ok {
				views = append(views, direct)
			}
			continue
		}
		view := resourceLibraryStatusView{Name: name, TargetType: "resource_library"}
		if store, ok := s.stores.Get(name); ok {
			view.TargetType = store.Type()
			view.Capabilities = storage.StoreCapabilities(store)
		} else {
			view.Capabilities = storage.StoreCapabilities(nil)
		}
		for i, binding := range config.Bindings {
			bindingName := bindingConfigName(binding, i)
			bindingView := resourceLibraryBindingView{
				Name:         bindingName,
				Path:         binding.Path,
				MountPoint:   binding.MountPoint,
				Status:       "unknown",
				Capabilities: view.Capabilities,
			}
			if store, ok := s.stores.Get(name); ok {
				if bindingCapable, ok := store.(storage.BindingCapabilityStore); ok {
					if capabilities, ok := bindingCapable.BindingCapabilities(bindingName); ok {
						bindingView.Capabilities = capabilities
					}
				}
			}
			if reason := skipped[name]; reason != "" {
				bindingView.Skipped = true
				bindingView.SkipReason = reason
			}
			if health, ok := healthByKey[name+"/"+bindingName]; ok {
				healthCopy := health
				bindingView.Status = health.Status
				bindingView.TargetType = health.TargetType
				bindingView.Health = &healthCopy
			}
			view.Bindings = append(view.Bindings, bindingView)
		}
		views = append(views, view)
	}
	return views, nil
}

func (s *Server) directStorageStatusView(_ context.Context, name string, skipped map[string]string) (resourceLibraryStatusView, bool) {
	store, ok := s.stores.Get(name)
	if !ok || !directResourceStatusStoreType(store.Type()) {
		return resourceLibraryStatusView{}, false
	}
	capabilities := storage.StoreCapabilities(store)
	binding := resourceLibraryBindingView{
		Name:         name,
		Path:         "/",
		TargetType:   store.Type(),
		Status:       "configured",
		Capabilities: capabilities,
	}
	if reason := skipped[name]; reason != "" {
		binding.Skipped = true
		binding.SkipReason = reason
	}
	return resourceLibraryStatusView{
		Name:         name,
		TargetType:   store.Type(),
		Capabilities: capabilities,
		Bindings:     []resourceLibraryBindingView{binding},
	}, true
}

func (s *Server) checkRecentResourceLibraryHealth(ctx context.Context, target string) error {
	config, ok := s.resourceLibraryConfig(target)
	if !ok || len(config.Bindings) == 0 || s.cfg.Limits.ResourceHealthMinIntervalSeconds <= 0 {
		return nil
	}
	binding := bindingConfigName(config.Bindings[0], 0)
	health, err := s.db.GetResourceLibraryHealth(ctx, target, binding)
	if err != nil {
		if db.IsNotFound(err) {
			return nil
		}
		return err
	}
	cutoff := time.Now().UTC().Add(-time.Duration(s.cfg.Limits.ResourceHealthMinIntervalSeconds) * time.Second)
	if health.Status != storage.HealthStatusOK && health.LastCheckedAt.After(cutoff) {
		return fmt.Errorf("resource library %q binding %q recent health check failed: %s", target, binding, firstNonEmpty(health.LastError, health.Status))
	}
	return nil
}

func (s *Server) recentResourceLibraryHealthFailure(ctx context.Context, target string) (string, bool) {
	config, ok := s.resourceLibraryConfig(target)
	if !ok || len(config.Bindings) == 0 || s.cfg.Limits.ResourceHealthMinIntervalSeconds <= 0 {
		return "", false
	}
	binding := bindingConfigName(config.Bindings[0], 0)
	health, err := s.db.GetResourceLibraryHealth(ctx, target, binding)
	if err != nil || health == nil || health.Status == storage.HealthStatusOK {
		return "", false
	}
	cutoff := time.Now().UTC().Add(-time.Duration(s.cfg.Limits.ResourceHealthMinIntervalSeconds) * time.Second)
	if health.LastCheckedAt.Before(cutoff) {
		return "", false
	}
	return fmt.Sprintf("binding %q is %s: %s", binding, health.Status, firstNonEmpty(health.LastError, health.Status)), true
}

func (s *Server) runResourceLibraryE2EProbe(ctx context.Context, req resourceLibraryE2EProbeRequest) (*resourceLibraryE2EProbeResult, error) {
	profileName := firstNonEmpty(req.RouteProfile, "china_all")
	profile, ok := s.cfg.Profile(profileName)
	if !ok {
		return &resourceLibraryE2EProbeResult{RouteProfile: profileName, Errors: []string{"unknown route_profile"}}, fmt.Errorf("unknown route_profile")
	}
	primary, ok := s.stores.Get(profile.Primary)
	if !ok {
		err := fmt.Errorf("primary storage %q is not configured", profile.Primary)
		return &resourceLibraryE2EProbeResult{RouteProfile: profileName, PrimaryTarget: profile.Primary, Errors: []string{err.Error()}}, err
	}
	projectID := cleanID(req.ProjectID)
	if projectID == "" {
		projectID = fmt.Sprintf("probe-%d", time.Now().UTC().UnixNano())
	}
	objectPath := req.Path
	if objectPath == "" {
		objectPath = fmt.Sprintf("assets/tmp/e2e-probe-%s.txt", time.Now().UTC().Format("20060102T150405.000000000Z"))
	}
	cleanPath, err := storage.CleanObjectPath(objectPath)
	result := &resourceLibraryE2EProbeResult{
		RouteProfile:  profileName,
		PrimaryTarget: profile.Primary,
		ProjectID:     projectID,
		ObjectPath:    cleanPath,
		Key:           cleanPath,
	}
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
		return result, err
	}
	payload := []byte("supercdn e2e probe " + time.Now().UTC().Format(time.RFC3339Nano) + "\n")
	sum := sha256.Sum256(payload)
	result.Size = int64(len(payload))
	result.SHA256 = hex.EncodeToString(sum[:])
	if _, err := s.preflightProfile(ctx, profileName, profile, preflightRequest{
		TotalSize:       result.Size,
		LargestFileSize: result.Size,
		BatchFileCount:  1,
	}); err != nil {
		result.Errors = append(result.Errors, err.Error())
		return result, err
	}
	tmp, err := os.CreateTemp(s.staging, "e2e-probe-*")
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
		return result, err
	}
	tmpPath := tmp.Name()
	_, err = tmp.Write(payload)
	if err := closeErr(tmp, err); err != nil {
		_ = os.Remove(tmpPath)
		result.Errors = append(result.Errors, err.Error())
		return result, err
	}
	defer os.Remove(tmpPath)
	if _, err := s.db.CreateProject(ctx, projectID); err != nil {
		result.Errors = append(result.Errors, err.Error())
		return result, err
	}
	probeProfile := profile
	probeProfile.Backups = nil
	start := time.Now()
	obj, _, err := s.putObjectFromFile(ctx, putObjectInput{
		ProjectID:      projectID,
		ObjectPath:     cleanPath,
		Key:            cleanPath,
		Profile:        probeProfile,
		ProfileName:    profileName,
		CacheControl:   "no-store",
		ContentType:    "text/plain; charset=utf-8",
		FilePath:       tmpPath,
		FileName:       path.Base(cleanPath),
		Size:           result.Size,
		SHA256:         result.SHA256,
		BatchFileCount: 1,
	})
	result.UploadLatencyMS = elapsedSince(start)
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
		_ = s.db.DeleteProject(ctx, projectID)
		return result, err
	}
	result.ObjectID = obj.ID
	defer func() {
		if req.Keep {
			return
		}
		if err := primary.Delete(context.WithoutCancel(ctx), cleanPath); err != nil {
			result.CleanupRemote = "failed: " + err.Error()
			result.Errors = append(result.Errors, result.CleanupRemote)
		} else {
			result.CleanupRemote = "deleted"
		}
		if err := s.db.DeleteObject(context.WithoutCancel(ctx), obj.ID); err != nil {
			result.CleanupDB = "failed object: " + err.Error()
			result.Errors = append(result.Errors, result.CleanupDB)
			return
		}
		if err := s.db.DeleteProject(context.WithoutCancel(ctx), projectID); err != nil {
			result.CleanupDB = "failed project: " + err.Error()
			result.Errors = append(result.Errors, result.CleanupDB)
			return
		}
		result.CleanupDB = "deleted"
	}()
	start = time.Now()
	status, headers, body, err := s.readProbeObject(ctx, projectID, cleanPath)
	result.ReadLatencyMS = elapsedSince(start)
	result.HTTPStatus = status
	result.ETag = headers.Get("ETag")
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
		return result, err
	}
	if status != http.StatusOK {
		err := fmt.Errorf("public read returned status %d", status)
		result.Errors = append(result.Errors, err.Error())
		return result, err
	}
	if !bytes.Equal(body, payload) {
		err := fmt.Errorf("public read payload mismatch")
		result.Errors = append(result.Errors, err.Error())
		return result, err
	}
	result.OK = true
	return result, nil
}

func (s *Server) readProbeObject(ctx context.Context, projectID, objectPath string) (int, http.Header, []byte, error) {
	req := httptest.NewRequest(http.MethodGet, "/o/"+projectID+"/"+objectPath, nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	resp := rec.Result()
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, resp.Header, nil, err
	}
	return resp.StatusCode, resp.Header, raw, nil
}

func (s *Server) resourceLibraryConfig(name string) (config.ResourceLibraryConfig, bool) {
	for _, library := range s.cfg.ResourceLibraries {
		if library.Name == name {
			return library, true
		}
	}
	if library, ok := s.cfg.CloudflareLibraryByName(name); ok {
		return cloudflareLibraryStatusConfig(library), true
	}
	return config.ResourceLibraryConfig{}, false
}

func cloudflareLibraryStatusConfig(library config.CloudflareLibraryConfig) config.ResourceLibraryConfig {
	bindings := make([]config.ResourceLibraryBinding, 0, len(library.Bindings))
	for _, binding := range library.Bindings {
		bindings = append(bindings, config.ResourceLibraryBinding{
			Name:        binding.Name,
			MountPoint:  binding.Account,
			Path:        binding.Path,
			Constraints: binding.Constraints,
		})
	}
	return config.ResourceLibraryConfig{Name: library.Name, Policy: library.Policy, Bindings: bindings}
}

func bindingConfigName(binding config.ResourceLibraryBinding, index int) string {
	if binding.Name != "" {
		return binding.Name
	}
	return fmt.Sprintf("%s_%d", binding.MountPoint, index+1)
}

func optionalLibrary(name string) []string {
	if name == "" {
		return nil
	}
	return []string{name}
}

func directResourceStatusStoreType(targetType string) bool {
	switch strings.ToLower(strings.TrimSpace(targetType)) {
	case "alist", "pinata", "r2":
		return true
	default:
		return false
	}
}

func normalizeInitDirectories(dirs []string) ([]string, error) {
	if len(dirs) == 0 {
		dirs = defaultResourceLibraryInitDirs
	}
	out := make([]string, 0, len(dirs))
	seen := map[string]bool{}
	for _, dir := range dirs {
		clean, err := storage.CleanDirectoryPath(dir)
		if err != nil {
			return nil, fmt.Errorf("invalid init directory %q: %w", dir, err)
		}
		if clean == "" || seen[clean] {
			continue
		}
		seen[clean] = true
		out = append(out, clean)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("at least one init directory is required")
	}
	return out, nil
}

func (s *Server) resolveResourceLibraries(requested []string) ([]string, error) {
	configured := map[string]bool{}
	for _, library := range s.cfg.ResourceLibraries {
		configured[library.Name] = true
	}
	for _, library := range s.cfg.CloudflareLibrariesEffective() {
		if s.cfg.CloudflareLibraryHasStorage(library) {
			configured[library.Name] = true
		}
	}
	if len(configured) == 0 {
		return nil, fmt.Errorf("no resource libraries are configured")
	}
	if len(requested) == 0 {
		names := make([]string, 0, len(configured))
		for _, library := range s.cfg.ResourceLibraries {
			names = append(names, library.Name)
		}
		for _, library := range s.cfg.CloudflareLibrariesEffective() {
			if s.cfg.CloudflareLibraryHasStorage(library) {
				names = append(names, library.Name)
			}
		}
		return names, nil
	}
	names := make([]string, 0, len(requested))
	seen := map[string]bool{}
	for _, name := range requested {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		if !configured[name] {
			return nil, fmt.Errorf("unknown resource library %q", name)
		}
		seen[name] = true
		names = append(names, name)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("at least one resource library is required")
	}
	return names, nil
}

func (s *Server) resolveResourceStatusTargets(requested []string) ([]string, error) {
	configured := map[string]bool{}
	for _, library := range s.cfg.ResourceLibraries {
		configured[library.Name] = true
	}
	for _, library := range s.cfg.CloudflareLibrariesEffective() {
		if s.cfg.CloudflareLibraryHasStorage(library) {
			configured[library.Name] = true
		}
	}
	for _, name := range s.stores.Names() {
		store, ok := s.stores.Get(name)
		if ok && directResourceStatusStoreType(store.Type()) {
			configured[name] = true
		}
	}
	if len(configured) == 0 {
		return nil, fmt.Errorf("no resource libraries or resource-capable storage targets are configured")
	}
	if len(requested) == 0 {
		names := make([]string, 0, len(configured))
		seen := map[string]bool{}
		add := func(name string) {
			name = strings.TrimSpace(name)
			if name == "" || seen[name] || !configured[name] {
				return
			}
			seen[name] = true
			names = append(names, name)
		}
		for _, library := range s.cfg.ResourceLibraries {
			add(library.Name)
		}
		for _, library := range s.cfg.CloudflareLibrariesEffective() {
			if s.cfg.CloudflareLibraryHasStorage(library) {
				add(library.Name)
			}
		}
		directNames := s.stores.Names()
		sort.Strings(directNames)
		for _, name := range directNames {
			if store, ok := s.stores.Get(name); ok && directResourceStatusStoreType(store.Type()) {
				add(name)
			}
		}
		return names, nil
	}
	names := make([]string, 0, len(requested))
	seen := map[string]bool{}
	for _, name := range requested {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		if !configured[name] {
			return nil, fmt.Errorf("unknown resource library or resource-capable storage target %q", name)
		}
		seen[name] = true
		names = append(names, name)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("at least one resource library or resource-capable storage target is required")
	}
	return names, nil
}

func (s *Server) initResourceLibraries(ctx context.Context, payload initResourceLibrariesPayload, dryRun bool) (*initResourceLibrariesResult, error) {
	result := &initResourceLibrariesResult{
		DryRun:      dryRun,
		Directories: payload.Directories,
	}
	markerPayload, err := json.MarshalIndent(map[string]any{
		"service":            "supercdn",
		"version":            1,
		"requested_at_utc":   payload.RequestedAtUTC,
		"initialized_at_utc": time.Now().UTC().Format(time.RFC3339Nano),
		"directories":        payload.Directories,
		"libraries":          payload.Libraries,
	}, "", "  ")
	if err != nil {
		return result, err
	}
	var firstErr error
	for _, name := range payload.Libraries {
		store, ok := s.stores.Get(name)
		if !ok {
			err := fmt.Errorf("resource library %q is not configured", name)
			if firstErr == nil {
				firstErr = err
			}
			result.Libraries = append(result.Libraries, storage.InitResult{
				Target:     name,
				TargetType: "resource_library",
				Bindings: []storage.InitBindingResult{{
					Target:     name,
					TargetType: "resource_library",
					Directories: []storage.InitPathResult{{
						Status: "error",
						Error:  err.Error(),
					}},
				}},
			})
			continue
		}
		initializer, ok := store.(storage.InitializableStore)
		if !ok {
			err := fmt.Errorf("resource library %q does not support initialization", name)
			if firstErr == nil {
				firstErr = err
			}
			result.Libraries = append(result.Libraries, storage.InitResult{
				Target:      store.Name(),
				TargetType:  store.Type(),
				Directories: []storage.InitPathResult{{Status: "error", Error: err.Error()}},
			})
			continue
		}
		var initResult *storage.InitResult
		run := func() error {
			var initErr error
			initResult, initErr = initializer.InitDirs(ctx, storage.InitOptions{
				Directories:   payload.Directories,
				MarkerPath:    payload.MarkerPath,
				MarkerPayload: markerPayload,
				DryRun:        dryRun,
			})
			return initErr
		}
		if dryRun {
			err = run()
		} else {
			err = s.withTransferSlot(ctx, run)
		}
		if initResult != nil {
			result.Libraries = append(result.Libraries, *initResult)
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return result, firstErr
}
