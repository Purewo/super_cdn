package server

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"supercdn/internal/db"
	"supercdn/internal/model"
	"supercdn/internal/storage"
)

type deleteSiteDeploymentObjectResult struct {
	Role         string                `json:"role"`
	Path         string                `json:"path,omitempty"`
	ObjectID     int64                 `json:"object_id,omitempty"`
	Key          string                `json:"key,omitempty"`
	DeleteRemote bool                  `json:"delete_remote"`
	Remote       []deleteReplicaResult `json:"remote,omitempty"`
	DeletedLocal bool                  `json:"deleted_local"`
	Errors       []string              `json:"errors,omitempty"`
}

type deleteSiteDeploymentResult struct {
	SiteID            string                             `json:"site_id"`
	DeploymentID      string                             `json:"deployment_id"`
	Deleted           bool                               `json:"deleted"`
	DeleteObjects     bool                               `json:"delete_objects"`
	DeleteRemote      bool                               `json:"delete_remote"`
	ObjectCount       int                                `json:"object_count"`
	Objects           []deleteSiteDeploymentObjectResult `json:"objects,omitempty"`
	DeletedDeployment bool                               `json:"deleted_deployment"`
	Warning           string                             `json:"warning,omitempty"`
	Errors            []string                           `json:"errors,omitempty"`
}

type deleteSiteResult struct {
	SiteID          string                       `json:"site_id"`
	Deleted         bool                         `json:"deleted"`
	DeleteRemote    bool                         `json:"delete_remote"`
	DeploymentCount int                          `json:"deployment_count"`
	ObjectCount     int                          `json:"object_count"`
	Deployments     []deleteSiteDeploymentResult `json:"deployments,omitempty"`
	DeletedSite     bool                         `json:"deleted_site"`
	Warnings        []string                     `json:"warnings,omitempty"`
	Errors          []string                     `json:"errors,omitempty"`
}

type siteDeploymentObjectRef struct {
	Role     string
	Path     string
	ObjectID int64
}

func (s *Server) deleteSite(ctx context.Context, site *model.Site, deleteRemote bool) deleteSiteResult {
	result := deleteSiteResult{
		SiteID:       site.ID,
		DeleteRemote: deleteRemote,
	}
	deployments, err := s.db.ListAllSiteDeployments(ctx, site.ID)
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
		return result
	}
	result.DeploymentCount = len(deployments)
	warnedCloudflare := false
	for i := range deployments {
		dep := deployments[i]
		depResult := deleteSiteDeploymentResult{
			SiteID:        site.ID,
			DeploymentID:  dep.ID,
			DeleteObjects: true,
			DeleteRemote:  deleteRemote,
		}
		if dep.DeploymentTarget == model.SiteDeploymentTargetCloudflareStatic || dep.DeploymentTarget == model.SiteDeploymentTargetHybridEdge {
			depResult.Warning = "deleted Super CDN metadata and tracked resource objects only; Cloudflare Worker versions, custom domains and KV entries are not deleted by this command"
			if !warnedCloudflare {
				result.Warnings = append(result.Warnings, depResult.Warning)
				warnedCloudflare = true
			}
		}
		deleted, err := s.deleteSiteDeploymentObjects(ctx, &dep, deleteRemote)
		if deleted != nil {
			depResult.Objects = deleted
			depResult.ObjectCount = len(deleted)
			result.ObjectCount += len(deleted)
			for _, item := range deleted {
				depResult.Errors = append(depResult.Errors, item.Errors...)
				result.Errors = append(result.Errors, item.Errors...)
			}
		}
		if err != nil {
			if len(depResult.Errors) == 0 {
				depResult.Errors = append(depResult.Errors, err.Error())
			}
			result.Errors = append(result.Errors, err.Error())
		}
		result.Deployments = append(result.Deployments, depResult)
	}
	if len(result.Errors) > 0 {
		return result
	}
	for _, projectID := range siteProjectIDs(site.ID, deployments) {
		if err := s.db.DeleteProject(ctx, projectID); err != nil && !db.IsNotFound(err) {
			result.Errors = append(result.Errors, err.Error())
		}
	}
	if len(result.Errors) > 0 {
		return result
	}
	if err := s.db.DeleteSite(ctx, site.ID); err != nil {
		result.Errors = append(result.Errors, err.Error())
		return result
	}
	for i := range result.Deployments {
		result.Deployments[i].Deleted = true
		result.Deployments[i].DeletedDeployment = true
	}
	result.DeletedSite = true
	result.Deleted = true
	return result
}

func siteProjectIDs(siteID string, deployments []model.SiteDeployment) []string {
	ids := []string{
		"site-artifacts:" + siteID,
		"site-manifests:" + siteID,
	}
	for _, dep := range deployments {
		ids = append(ids, "site-deployment:"+siteID+":"+dep.ID)
	}
	return ids
}

func (s *Server) deleteSiteDeploymentObjects(ctx context.Context, dep *model.SiteDeployment, deleteRemote bool) ([]deleteSiteDeploymentObjectResult, error) {
	refs := []siteDeploymentObjectRef{}
	seen := map[int64]bool{}
	addRef := func(role, path string, objectID int64) {
		if objectID <= 0 || seen[objectID] {
			return
		}
		seen[objectID] = true
		refs = append(refs, siteDeploymentObjectRef{Role: role, Path: path, ObjectID: objectID})
	}
	files, err := s.db.ListSiteDeploymentFiles(ctx, dep.ID)
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		addRef("file", file.Path, file.ObjectID)
	}
	addRef("artifact", dep.ArtifactKey, dep.ArtifactObjectID)
	addRef("manifest", dep.ManifestKey, dep.ManifestObjectID)
	results := make([]deleteSiteDeploymentObjectResult, 0, len(refs))
	var errs []string
	for _, ref := range refs {
		item, err := s.deleteSiteDeploymentObject(ctx, ref, deleteRemote)
		results = append(results, item)
		if err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return results, errors.New(strings.Join(errs, "; "))
	}
	return results, nil
}

func (s *Server) deleteSiteDeploymentObject(ctx context.Context, ref siteDeploymentObjectRef, deleteRemote bool) (deleteSiteDeploymentObjectResult, error) {
	result := deleteSiteDeploymentObjectResult{
		Role:         ref.Role,
		Path:         ref.Path,
		ObjectID:     ref.ObjectID,
		DeleteRemote: deleteRemote,
	}
	obj, err := s.db.GetObject(ctx, ref.ObjectID)
	if err != nil {
		if db.IsNotFound(err) {
			result.DeletedLocal = true
			return result, nil
		}
		result.Errors = append(result.Errors, err.Error())
		return result, err
	}
	result.Key = obj.Key
	if deleteRemote {
		if err := s.withTransferSlot(ctx, func() error {
			remote, err := s.deleteObjectRemoteReplicaResults(ctx, obj)
			result.Remote = remote
			return err
		}); err != nil {
			result.Errors = append(result.Errors, err.Error())
			return result, err
		}
	}
	if err := s.db.DeleteObject(ctx, obj.ID); err != nil {
		result.Errors = append(result.Errors, err.Error())
		return result, err
	}
	result.DeletedLocal = true
	return result, nil
}

func (s *Server) deleteObjectRemoteReplicas(ctx context.Context, obj *model.Object, result *deleteBucketObjectResult) error {
	remote, err := s.deleteObjectRemoteReplicaResults(ctx, obj)
	result.Remote = append(result.Remote, remote...)
	return err
}

func (s *Server) deleteObjectRemoteReplicaResults(ctx context.Context, obj *model.Object) ([]deleteReplicaResult, error) {
	replicas, err := s.db.Replicas(ctx, obj.ID)
	if err != nil {
		return nil, err
	}
	targets := map[string]bool{}
	locators := map[string]string{}
	if obj.PrimaryTarget != "" {
		targets[obj.PrimaryTarget] = true
	}
	for _, replica := range replicas {
		if replica.Target != "" {
			targets[replica.Target] = true
			if replica.Locator != "" {
				locators[replica.Target] = replica.Locator
			}
		}
	}
	names := make([]string, 0, len(targets))
	for target := range targets {
		names = append(names, target)
	}
	sort.Strings(names)
	var errs []string
	results := make([]deleteReplicaResult, 0, len(names))
	for _, target := range names {
		item := deleteReplicaResult{Target: target}
		store, ok := s.stores.Get(target)
		if !ok {
			item.Status = "error"
			item.Error = fmt.Sprintf("storage %q is not configured", target)
			errs = append(errs, item.Error)
			results = append(results, item)
			continue
		}
		locator := locators[target]
		keepShared, err := s.keepSharedIPFSPin(ctx, obj.ID, target, locator)
		if err != nil {
			item.Status = "error"
			item.Error = err.Error()
			errs = append(errs, fmt.Sprintf("%s: %s", target, err.Error()))
			results = append(results, item)
			continue
		}
		if keepShared {
			item.Status = "kept_shared"
			results = append(results, item)
			continue
		}
		if err := deleteStoreObject(ctx, store, obj.Key, locator); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				item.Status = "not_found"
			} else {
				item.Status = "error"
				item.Error = err.Error()
				errs = append(errs, fmt.Sprintf("%s: %s", target, err.Error()))
			}
		} else {
			item.Status = "deleted"
		}
		results = append(results, item)
	}
	if len(errs) > 0 {
		return results, errors.New(strings.Join(errs, "; "))
	}
	return results, nil
}

func (s *Server) keepSharedIPFSPin(ctx context.Context, objectID int64, target, locator string) (bool, error) {
	if providerPinID := storage.IPFSProviderPinIDFromLocator(locator); providerPinID != "" {
		refs, err := s.db.IPFSPinProviderPinIDReferenceCount(ctx, target, providerPinID, objectID)
		if err != nil {
			return false, err
		}
		return refs > 0, nil
	}
	cid, ok := storage.IPFSCIDFromLocator(locator)
	if !ok {
		return false, nil
	}
	refs, err := s.db.IPFSPinReferenceCount(ctx, target, cid, objectID)
	if err != nil {
		return false, err
	}
	return refs > 0, nil
}

func deleteStoreObject(ctx context.Context, store storage.Store, key, locator string) error {
	if deleter, ok := store.(storage.LocatorDeleteStore); ok {
		return deleter.DeleteLocator(ctx, key, locator)
	}
	return store.Delete(ctx, key)
}
