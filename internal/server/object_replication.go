package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"supercdn/internal/config"
	"supercdn/internal/model"
	"supercdn/internal/storage"
)

type repairObjectReplicasRequest struct {
	Target string `json:"target,omitempty"`
	Force  bool   `json:"force"`
}

type repairObjectReplicaResult struct {
	Target         string `json:"target"`
	PreviousStatus string `json:"previous_status,omitempty"`
	Status         string `json:"status"`
	JobID          int64  `json:"job_id,omitempty"`
	Repaired       bool   `json:"repaired"`
	Skipped        bool   `json:"skipped,omitempty"`
	SkipReason     string `json:"skip_reason,omitempty"`
	Error          string `json:"error,omitempty"`
}

type repairObjectReplicasResponse struct {
	Status   string                      `json:"status"`
	ObjectID int64                       `json:"object_id"`
	Target   string                      `json:"target,omitempty"`
	Force    bool                        `json:"force"`
	Jobs     []model.Job                 `json:"jobs,omitempty"`
	Results  []repairObjectReplicaResult `json:"results,omitempty"`
	Errors   []string                    `json:"errors,omitempty"`
}

type refreshObjectReplicasRequest struct {
	Target string `json:"target,omitempty"`
}

type refreshObjectReplicaResult struct {
	Target          string         `json:"target"`
	PreviousStatus  string         `json:"previous_status,omitempty"`
	Status          string         `json:"status"`
	PreviousLocator string         `json:"previous_locator,omitempty"`
	Locator         string         `json:"locator,omitempty"`
	Size            int64          `json:"size,omitempty"`
	ContentType     string         `json:"content_type,omitempty"`
	CacheControl    string         `json:"cache_control,omitempty"`
	IPFS            *model.IPFSPin `json:"ipfs,omitempty"`
	Refreshed       bool           `json:"refreshed"`
	Skipped         bool           `json:"skipped,omitempty"`
	SkipReason      string         `json:"skip_reason,omitempty"`
	Error           string         `json:"error,omitempty"`
}

type refreshObjectReplicasResponse struct {
	Status   string                       `json:"status"`
	ObjectID int64                        `json:"object_id"`
	Target   string                       `json:"target,omitempty"`
	Results  []refreshObjectReplicaResult `json:"results,omitempty"`
	Errors   []string                     `json:"errors,omitempty"`
}

func (s *Server) recordIPFSReplica(ctx context.Context, objectID int64, target string, store storage.Store, locator string) error {
	pin, ok := ipfsPinFromReplica(objectID, target, store, locator)
	if !ok {
		return s.db.DeleteIPFSPin(ctx, objectID, target)
	}
	_, err := s.db.UpsertIPFSPin(ctx, pin)
	return err
}

func ipfsPinFromReplica(objectID int64, target string, store storage.Store, locator string) (model.IPFSPin, bool) {
	cid, ok := storage.IPFSCIDFromLocator(locator)
	if !ok {
		return model.IPFSPin{}, false
	}
	provider := "ipfs"
	gatewayURL := ""
	if store != nil {
		provider = firstNonEmpty(store.Type(), provider)
		if provider == "pinata" {
			gatewayURL = store.PublicURL(locator)
		}
	}
	return model.IPFSPin{
		ObjectID:      objectID,
		Target:        target,
		Provider:      provider,
		CID:           cid,
		GatewayURL:    gatewayURL,
		Locator:       locator,
		PinStatus:     "pinned",
		ProviderPinID: storage.IPFSProviderPinIDFromLocator(locator),
	}, true
}

func (s *Server) ipfsGatewayURLForReplica(ctx context.Context, replica model.Replica, store storage.Store) string {
	if replica.IPFS != nil && replica.IPFS.GatewayURL != "" {
		return replica.IPFS.GatewayURL
	}
	if pin, err := s.db.GetIPFSPin(ctx, replica.ObjectID, replica.Target); err == nil && pin.GatewayURL != "" {
		return pin.GatewayURL
	}
	if pin, ok := ipfsPinFromReplica(replica.ObjectID, replica.Target, store, replica.Locator); ok {
		return pin.GatewayURL
	}
	return ""
}

func (s *Server) hydrateObjectIPFS(ctx context.Context, obj *model.Object) (*model.Object, error) {
	if obj == nil {
		return nil, nil
	}
	pins, err := s.db.IPFSPins(ctx, obj.ID)
	if err != nil {
		return nil, err
	}
	obj.IPFS = pins
	return obj, nil
}

func (s *Server) hydrateReplicasIPFS(ctx context.Context, objectID int64) ([]model.Replica, error) {
	replicas, err := s.db.Replicas(ctx, objectID)
	if err != nil {
		return nil, err
	}
	pins, err := s.db.IPFSPins(ctx, objectID)
	if err != nil {
		return nil, err
	}
	byTarget := map[string]model.IPFSPin{}
	for _, pin := range pins {
		byTarget[pin.Target] = pin
	}
	for i := range replicas {
		if pin, ok := byTarget[replicas[i].Target]; ok {
			replicas[i].IPFS = &pin
		}
	}
	return replicas, nil
}

func (s *Server) refreshObjectReplicas(ctx context.Context, obj *model.Object, req refreshObjectReplicasRequest) (*refreshObjectReplicasResponse, error) {
	target := strings.TrimSpace(req.Target)
	resp := &refreshObjectReplicasResponse{
		Status:   "ok",
		ObjectID: obj.ID,
		Target:   target,
	}
	replicas, err := s.db.Replicas(ctx, obj.ID)
	if err != nil {
		return nil, err
	}
	if target != "" {
		filtered := replicas[:0]
		for _, replica := range replicas {
			if replica.Target == target {
				filtered = append(filtered, replica)
			}
		}
		replicas = filtered
		if len(replicas) == 0 {
			return nil, fmt.Errorf("replica for object %d target %q not found", obj.ID, target)
		}
	}
	if len(replicas) == 0 {
		return nil, fmt.Errorf("object %d has no replicas", obj.ID)
	}
	for _, replica := range replicas {
		result := s.refreshObjectReplica(ctx, obj, replica)
		if result.Error != "" {
			resp.Errors = append(resp.Errors, fmt.Sprintf("%s: %s", result.Target, result.Error))
		}
		resp.Results = append(resp.Results, result)
	}
	if len(resp.Errors) > 0 {
		if len(resp.Results) > len(resp.Errors) {
			resp.Status = "partial"
		} else {
			resp.Status = "failed"
		}
	}
	return resp, nil
}

func (s *Server) refreshObjectReplica(ctx context.Context, obj *model.Object, replica model.Replica) refreshObjectReplicaResult {
	result := refreshObjectReplicaResult{
		Target:          replica.Target,
		PreviousStatus:  replica.Status,
		Status:          replica.Status,
		PreviousLocator: replica.Locator,
		Locator:         replica.Locator,
	}
	if replica.Status == model.ReplicaDeleted {
		result.Skipped = true
		result.SkipReason = "replica is deleted"
		return result
	}
	if replica.Status == model.ReplicaPending && replica.Locator == "" {
		result.Skipped = true
		result.SkipReason = "replica is pending without locator"
		return result
	}
	store, ok := s.stores.Get(replica.Target)
	if !ok {
		result.Status = model.ReplicaFailed
		result.Error = fmt.Sprintf("storage target %q is not configured", replica.Target)
		_, _ = s.db.UpsertReplica(ctx, obj.ID, replica.Target, model.ReplicaFailed, replica.Locator, result.Error)
		return result
	}
	if ipfsResult, handled := s.refreshObjectIPFSReplica(ctx, obj, replica, store, result); handled {
		return ipfsResult
	}
	stat, err := store.Stat(ctx, obj.Key)
	if err != nil {
		status := model.ReplicaFailed
		message := err.Error()
		if errors.Is(err, storage.ErrNotFound) {
			status = model.ReplicaStale
			message = "remote object not found"
		}
		result.Status = status
		result.Error = message
		_, _ = s.db.UpsertReplica(ctx, obj.ID, replica.Target, status, replica.Locator, message)
		return result
	}
	locator := firstNonEmpty(stat.Locator, replica.Locator, store.PublicURL(obj.Key))
	saved, err := s.db.UpsertReplica(ctx, obj.ID, replica.Target, model.ReplicaReady, locator, "")
	if err != nil {
		result.Status = model.ReplicaFailed
		result.Error = err.Error()
		return result
	}
	if err := s.recordIPFSReplica(ctx, obj.ID, replica.Target, store, locator); err != nil {
		result.Status = model.ReplicaFailed
		result.Error = err.Error()
		return result
	}
	result.Status = saved.Status
	result.Locator = saved.Locator
	result.Size = stat.Size
	result.ContentType = stat.ContentType
	result.CacheControl = stat.CacheControl
	result.Refreshed = true
	return result
}

func (s *Server) refreshObjectIPFSReplica(ctx context.Context, obj *model.Object, replica model.Replica, store storage.Store, result refreshObjectReplicaResult) (refreshObjectReplicaResult, bool) {
	cid, ok := storage.IPFSCIDFromLocator(replica.Locator)
	if !ok {
		if pin, err := s.db.GetIPFSPin(ctx, obj.ID, replica.Target); err == nil {
			cid = pin.CID
			ok = cid != ""
		}
	}
	if !ok {
		return result, false
	}
	refresher, ok := store.(storage.IPFSPinStatusStore)
	if !ok {
		return result, false
	}
	status, err := refresher.RefreshIPFSPin(ctx, cid)
	if err != nil {
		result.Status = model.ReplicaFailed
		result.Error = err.Error()
		_, _ = s.db.UpsertReplica(ctx, obj.ID, replica.Target, model.ReplicaFailed, replica.Locator, err.Error())
		return result, true
	}
	pin := model.IPFSPin{
		ObjectID:      obj.ID,
		Target:        replica.Target,
		Provider:      firstNonEmpty(status.Provider, store.Type(), "ipfs"),
		CID:           firstNonEmpty(status.CID, cid),
		GatewayURL:    status.GatewayURL,
		Locator:       storage.PreserveIPFSProviderQuery(firstNonEmpty(status.Locator, replica.Locator), replica.Locator),
		PinStatus:     firstNonEmpty(status.PinStatus, "unknown"),
		ProviderPinID: status.ProviderPinID,
	}
	savedPin, err := s.db.UpsertIPFSPin(ctx, pin)
	if err != nil {
		result.Status = model.ReplicaFailed
		result.Error = err.Error()
		return result, true
	}
	replicaStatus := model.ReplicaReady
	lastErr := ""
	if savedPin.PinStatus != "pinned" {
		replicaStatus = model.ReplicaStale
		lastErr = "ipfs pin status is " + firstNonEmpty(savedPin.PinStatus, "unknown")
	}
	saved, err := s.db.UpsertReplica(ctx, obj.ID, replica.Target, replicaStatus, firstNonEmpty(savedPin.Locator, replica.Locator), lastErr)
	if err != nil {
		result.Status = model.ReplicaFailed
		result.Error = err.Error()
		return result, true
	}
	result.Status = saved.Status
	result.Locator = saved.Locator
	result.IPFS = savedPin
	result.Refreshed = true
	if lastErr != "" {
		result.Error = lastErr
	}
	return result, true
}

func (s *Server) repairObjectReplicas(ctx context.Context, obj *model.Object, req repairObjectReplicasRequest) (*repairObjectReplicasResponse, error) {
	target := strings.TrimSpace(req.Target)
	resp := &repairObjectReplicasResponse{
		Status:   "noop",
		ObjectID: obj.ID,
		Target:   target,
		Force:    req.Force,
	}
	replicas, err := s.db.Replicas(ctx, obj.ID)
	if err != nil {
		return nil, err
	}
	targets, err := s.objectReplicaRepairTargets(obj, replicas, target)
	if err != nil {
		return nil, err
	}
	byTarget := map[string]model.Replica{}
	for _, replica := range replicas {
		byTarget[replica.Target] = replica
	}
	for _, target := range targets {
		result := repairObjectReplicaResult{Target: target, Status: model.ReplicaPending}
		existing, hasReplica := byTarget[target]
		if hasReplica {
			result.PreviousStatus = existing.Status
			if existing.Status == model.ReplicaReady && !req.Force {
				result.Status = existing.Status
				result.Skipped = true
				result.SkipReason = "replica is already ready"
				resp.Results = append(resp.Results, result)
				continue
			}
			if existing.Status == model.ReplicaPending && !req.Force {
				result.Status = existing.Status
				result.Skipped = true
				result.SkipReason = "replica is already pending"
				resp.Results = append(resp.Results, result)
				continue
			}
			if existing.Status == model.ReplicaDeleted && !req.Force {
				result.Status = existing.Status
				result.Skipped = true
				result.SkipReason = "replica is deleted"
				resp.Results = append(resp.Results, result)
				continue
			}
		}
		if _, ok := s.stores.Get(target); !ok {
			result.Status = firstNonEmpty(result.PreviousStatus, "missing")
			result.Error = fmt.Sprintf("storage target %q is not configured", target)
			resp.Errors = append(resp.Errors, result.Error)
			resp.Results = append(resp.Results, result)
			continue
		}
		if _, err := s.db.UpsertReplica(ctx, obj.ID, target, model.ReplicaPending, "", ""); err != nil {
			result.Status = firstNonEmpty(result.PreviousStatus, "missing")
			result.Error = err.Error()
			resp.Errors = append(resp.Errors, fmt.Sprintf("%s: %s", target, err.Error()))
			resp.Results = append(resp.Results, result)
			continue
		}
		if err := s.db.DeleteIPFSPin(ctx, obj.ID, target); err != nil {
			result.Status = model.ReplicaPending
			result.Error = err.Error()
			resp.Errors = append(resp.Errors, fmt.Sprintf("%s: %s", target, err.Error()))
			resp.Results = append(resp.Results, result)
			continue
		}
		payload, _ := json.Marshal(replicatePayload{ObjectID: obj.ID, Target: target})
		job, err := s.db.CreateJob(ctx, model.JobReplicateObject, string(payload))
		if err != nil {
			result.Status = model.ReplicaPending
			result.Error = err.Error()
			resp.Errors = append(resp.Errors, fmt.Sprintf("%s: %s", target, err.Error()))
			resp.Results = append(resp.Results, result)
			continue
		}
		result.JobID = job.ID
		result.Repaired = true
		resp.Jobs = append(resp.Jobs, *job)
		resp.Results = append(resp.Results, result)
	}
	if len(resp.Errors) > 0 {
		if len(resp.Jobs) > 0 {
			resp.Status = "partial"
		} else {
			resp.Status = "failed"
		}
	} else if len(resp.Jobs) > 0 {
		resp.Status = "queued"
	}
	return resp, nil
}

func (s *Server) objectReplicaRepairTargets(obj *model.Object, replicas []model.Replica, requestedTarget string) ([]string, error) {
	var targets []string
	allowed := map[string]bool{}
	add := func(target string) {
		target = strings.TrimSpace(target)
		if target == "" || allowed[target] {
			return
		}
		allowed[target] = true
		targets = append(targets, target)
	}
	if profile, ok := s.cfg.Profile(obj.RouteProfile); ok {
		add(profile.Primary)
		if replicationPolicyForProfile(profile) != config.ReplicationPolicyPrimaryOnly {
			for _, target := range routeProfileBackupTargets(profile) {
				add(target)
			}
		}
	}
	add(obj.PrimaryTarget)
	for _, replica := range replicas {
		add(replica.Target)
	}
	if requestedTarget != "" {
		if !allowed[requestedTarget] {
			return nil, fmt.Errorf("target %q is not part of object %d route profile or existing replicas", requestedTarget, obj.ID)
		}
		return []string{requestedTarget}, nil
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("object %d has no replica targets to repair", obj.ID)
	}
	return targets, nil
}

func replicationPolicyForProfile(profile config.RouteProfile) string {
	policy := strings.TrimSpace(profile.ReplicationPolicy)
	if policy != "" {
		return policy
	}
	if len(routeProfileBackupTargets(profile)) > 0 {
		return config.ReplicationPolicyBestEffortBackups
	}
	return config.ReplicationPolicyPrimaryOnly
}

func routeProfileBackupTargets(profile config.RouteProfile) []string {
	targets := make([]string, 0, len(profile.Backups))
	seen := map[string]bool{}
	primary := strings.TrimSpace(profile.Primary)
	for _, target := range profile.Backups {
		target = strings.TrimSpace(target)
		if target == "" || target == primary || seen[target] {
			continue
		}
		seen[target] = true
		targets = append(targets, target)
	}
	return targets
}

type replicatePayload struct {
	ObjectID int64  `json:"object_id"`
	Target   string `json:"target"`
}

var (
	replicateSourceGetAttempts = 60
	replicateSourceGetDelay    = 2 * time.Second
)

func (s *Server) replicateObject(ctx context.Context, payload replicatePayload) error {
	obj, err := s.db.GetObject(ctx, payload.ObjectID)
	if err != nil {
		return err
	}
	target, ok := s.stores.Get(payload.Target)
	if !ok {
		return fmt.Errorf("target storage %q is not configured", payload.Target)
	}
	replicas, err := s.db.Replicas(ctx, obj.ID)
	if err != nil {
		return err
	}
	sort.SliceStable(replicas, func(i, j int) bool {
		if replicas[i].Target == obj.PrimaryTarget {
			return true
		}
		if replicas[j].Target == obj.PrimaryTarget {
			return false
		}
		return replicas[i].ID < replicas[j].ID
	})
	var sourceReplica *model.Replica
	for i := range replicas {
		if replicas[i].Target != payload.Target && replicas[i].Status == model.ReplicaReady {
			sourceReplica = &replicas[i]
			break
		}
	}
	if sourceReplica == nil {
		return fmt.Errorf("no ready source replica for object %d", obj.ID)
	}
	source, ok := s.stores.Get(sourceReplica.Target)
	if !ok {
		return fmt.Errorf("source storage %q is not configured", sourceReplica.Target)
	}
	stream, err := getReplicaSourceStream(ctx, source, obj.Key, sourceReplica.Locator)
	if err != nil {
		_, _ = s.db.UpsertReplica(ctx, obj.ID, payload.Target, model.ReplicaFailed, "", err.Error())
		_ = s.db.DeleteIPFSPin(ctx, obj.ID, payload.Target)
		return err
	}
	defer stream.Body.Close()
	tmp, err := os.CreateTemp(s.staging, "replica-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := io.Copy(tmp, stream.Body); closeErr(tmp, err) != nil {
		return closeErr(tmp, err)
	}
	var locator string
	err = s.withTransferSlot(ctx, func() error {
		var putErr error
		locator, putErr = target.Put(ctx, storage.PutOptions{
			Key:            obj.Key,
			FilePath:       tmpPath,
			ContentType:    obj.ContentType,
			CacheControl:   obj.CacheControl,
			Group:          storageGroupFromProjectID(obj.ProjectID),
			SHA256:         obj.SHA256,
			Size:           obj.Size,
			FileName:       path.Base(obj.Path),
			BatchFileCount: 1,
			IgnoreLimits:   s.overclockMode(),
		})
		return putErr
	})
	if err != nil {
		_, _ = s.db.UpsertReplica(ctx, obj.ID, payload.Target, model.ReplicaFailed, "", err.Error())
		_ = s.db.DeleteIPFSPin(ctx, obj.ID, payload.Target)
		return err
	}
	_, err = s.db.UpsertReplica(ctx, obj.ID, payload.Target, model.ReplicaReady, locator, "")
	if err != nil {
		return err
	}
	return s.recordIPFSReplica(ctx, obj.ID, payload.Target, target, locator)
}

func getReplicaSourceStream(ctx context.Context, source storage.Store, key, locator string) (*storage.ObjectStream, error) {
	attempts := replicateSourceGetAttempts
	if attempts <= 0 {
		attempts = 1
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		stream, err := source.Get(ctx, key, storage.GetOptions{Locator: locator})
		if err == nil {
			return stream, nil
		}
		lastErr = err
		if i == attempts-1 {
			break
		}
		delay := replicateSourceGetDelay
		if delay <= 0 {
			continue
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}
	return nil, lastErr
}
