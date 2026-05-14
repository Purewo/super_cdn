package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"supercdn/internal/cloudflare"
	"supercdn/internal/db"
	"supercdn/internal/model"
	"supercdn/internal/storage"
)

type createAssetBucketRequest struct {
	Slug                string   `json:"slug"`
	Name                string   `json:"name"`
	Description         string   `json:"description"`
	RouteProfile        string   `json:"route_profile"`
	RoutingPolicy       string   `json:"routing_policy"`
	AllowedTypes        []string `json:"allowed_types"`
	MaxCapacityBytes    int64    `json:"max_capacity_bytes"`
	MaxFileSizeBytes    int64    `json:"max_file_size_bytes"`
	DefaultCacheControl string   `json:"default_cache_control"`
	Status              string   `json:"status"`
}

type initAssetBucketRequest struct {
	DryRun bool `json:"dry_run"`
}

type initAssetBucketResult struct {
	Bucket      string              `json:"bucket"`
	DryRun      bool                `json:"dry_run"`
	Directories []string            `json:"directories"`
	Result      *storage.InitResult `json:"result,omitempty"`
	Status      string              `json:"status"`
	Reason      string              `json:"reason,omitempty"`
}

type deleteReplicaResult struct {
	Target string `json:"target"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type deleteBucketObjectResult struct {
	Bucket       string                `json:"bucket"`
	LogicalPath  string                `json:"logical_path"`
	ObjectID     int64                 `json:"object_id,omitempty"`
	Key          string                `json:"key,omitempty"`
	DeleteRemote bool                  `json:"delete_remote"`
	Remote       []deleteReplicaResult `json:"remote,omitempty"`
	DeletedLocal bool                  `json:"deleted_local"`
	Errors       []string              `json:"errors,omitempty"`
}

type deleteBucketObjectsResult struct {
	Bucket       string                     `json:"bucket"`
	DeleteRemote bool                       `json:"delete_remote"`
	Paths        []string                   `json:"paths,omitempty"`
	Prefix       string                     `json:"prefix,omitempty"`
	All          bool                       `json:"all,omitempty"`
	ObjectCount  int                        `json:"object_count"`
	Objects      []deleteBucketObjectResult `json:"objects,omitempty"`
	Errors       []string                   `json:"errors,omitempty"`
}

type deleteAssetBucketResult struct {
	Bucket         string                     `json:"bucket"`
	DeleteObjects  bool                       `json:"delete_objects"`
	DeleteRemote   bool                       `json:"delete_remote"`
	ObjectCount    int                        `json:"object_count"`
	Objects        []deleteBucketObjectResult `json:"objects,omitempty"`
	DeletedBucket  bool                       `json:"deleted_bucket"`
	DeletedProject bool                       `json:"deleted_project,omitempty"`
	Errors         []string                   `json:"errors,omitempty"`
}

type assetBucketCacheRequest struct {
	Path              string   `json:"path"`
	Paths             []string `json:"paths"`
	Prefix            string   `json:"prefix"`
	All               bool     `json:"all"`
	Limit             int      `json:"limit"`
	BaseURL           string   `json:"base_url"`
	CloudflareAccount string   `json:"cloudflare_account"`
	CloudflareLibrary string   `json:"cloudflare_library"`
	DryRun            bool     `json:"dry_run"`
	Method            string   `json:"method"`
}

type purgeAssetBucketCacheResponse struct {
	Bucket            string                        `json:"bucket"`
	CloudflareAccount string                        `json:"cloudflare_account,omitempty"`
	CloudflareLibrary string                        `json:"cloudflare_library,omitempty"`
	DryRun            bool                          `json:"dry_run"`
	Status            string                        `json:"status"`
	URLCount          int                           `json:"url_count"`
	URLs              []string                      `json:"urls,omitempty"`
	Batches           []cloudflare.PurgeBatchResult `json:"batches,omitempty"`
	Warnings          []string                      `json:"warnings,omitempty"`
	Errors            []string                      `json:"errors,omitempty"`
}

type warmupAssetBucketResponse struct {
	Bucket   string               `json:"bucket"`
	DryRun   bool                 `json:"dry_run"`
	Method   string               `json:"method"`
	Status   string               `json:"status"`
	URLCount int                  `json:"url_count"`
	URLs     []string             `json:"urls,omitempty"`
	Results  []bucketWarmupResult `json:"results,omitempty"`
	Warnings []string             `json:"warnings,omitempty"`
	Errors   []string             `json:"errors,omitempty"`
}

type bucketWarmupResult struct {
	URL       string `json:"url"`
	Method    string `json:"method"`
	Status    string `json:"status"`
	HTTPCode  int    `json:"http_code,omitempty"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
	Error     string `json:"error,omitempty"`
}

type refreshAssetBucketReplicasRequest struct {
	Target string   `json:"target,omitempty"`
	Path   string   `json:"path"`
	Paths  []string `json:"paths"`
	Prefix string   `json:"prefix"`
	All    bool     `json:"all"`
	Limit  int      `json:"limit"`
}

type refreshAssetBucketReplicaObjectResult struct {
	BucketSlug  string                       `json:"bucket_slug"`
	LogicalPath string                       `json:"logical_path"`
	ObjectID    int64                        `json:"object_id"`
	Status      string                       `json:"status"`
	Results     []refreshObjectReplicaResult `json:"results,omitempty"`
	Errors      []string                     `json:"errors,omitempty"`
}

type refreshAssetBucketReplicasResponse struct {
	Status      string                                  `json:"status"`
	Bucket      string                                  `json:"bucket"`
	Target      string                                  `json:"target,omitempty"`
	ObjectCount int                                     `json:"object_count"`
	Objects     []refreshAssetBucketReplicaObjectResult `json:"objects,omitempty"`
	Warnings    []string                                `json:"warnings,omitempty"`
	Errors      []string                                `json:"errors,omitempty"`
}

func (s *Server) handleCreateAssetBucket(w http.ResponseWriter, r *http.Request) {
	var req createAssetBucketRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	slug := cleanBucketSlug(req.Slug)
	if slug == "" {
		writeError(w, http.StatusBadRequest, "slug is required")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = slug
	}
	profileName := firstNonEmpty(req.RouteProfile, "china_all")
	profile, ok := s.cfg.Profile(profileName)
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown route_profile")
		return
	}
	routingPolicy := strings.TrimSpace(req.RoutingPolicy)
	if routingPolicy != "" {
		if _, err := s.routingPolicyForProfile(routingPolicy, profileName, profile); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	allowed, err := normalizeAssetTypes(req.AllowedTypes)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.MaxCapacityBytes < 0 || req.MaxFileSizeBytes < 0 {
		writeError(w, http.StatusBadRequest, "bucket limits must be non-negative")
		return
	}
	status := firstNonEmpty(req.Status, model.AssetBucketActive)
	if status != model.AssetBucketActive && status != model.AssetBucketDisabled {
		writeError(w, http.StatusBadRequest, "status must be active or disabled")
		return
	}
	bucket, err := s.db.CreateAssetBucket(r.Context(), model.AssetBucket{
		Slug:                slug,
		WorkspaceID:         workspaceForContext(r.Context()),
		Name:                name,
		Description:         strings.TrimSpace(req.Description),
		RouteProfile:        profileName,
		RoutingPolicy:       routingPolicy,
		AllowedTypes:        allowed,
		MaxCapacityBytes:    req.MaxCapacityBytes,
		MaxFileSizeBytes:    req.MaxFileSizeBytes,
		DefaultCacheControl: strings.TrimSpace(req.DefaultCacheControl),
		Status:              status,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, bucket)
}

func (s *Server) handleListAssetBuckets(w http.ResponseWriter, r *http.Request) {
	principal := currentPrincipal(r.Context())
	workspaceID := ""
	if !principal.Root {
		workspaceID = principal.WorkspaceID
	}
	buckets, err := s.db.ListAssetBucketsInWorkspace(r.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"buckets": buckets})
}

func (s *Server) handleGetAssetBucket(w http.ResponseWriter, r *http.Request) {
	bucket, ok := s.getAssetBucketForAPI(w, r, cleanBucketSlug(r.PathValue("slug")))
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, bucket)
}

func (s *Server) handleDeleteAssetBucket(w http.ResponseWriter, r *http.Request) {
	slug := cleanBucketSlug(r.PathValue("slug"))
	if slug == "" {
		writeError(w, http.StatusBadRequest, "bucket is required")
		return
	}
	bucket, ok := s.getAssetBucketForAPI(w, r, slug)
	if !ok {
		return
	}
	force, err := queryBool(r, "force", false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	deleteObjects, err := queryBool(r, "delete_objects", false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	deleteRemote, err := queryBool(r, "delete_remote", true)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if force {
		deleteObjects = true
	}
	result, err := s.deleteAssetBucket(r.Context(), bucket, deleteObjects, deleteRemote)
	if err != nil {
		status := http.StatusBadGateway
		if db.IsNotFound(err) {
			status = http.StatusNotFound
		} else if strings.Contains(err.Error(), "not empty") {
			status = http.StatusConflict
		}
		if result != nil {
			writeJSON(w, status, result)
		} else {
			writeError(w, status, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleInitAssetBucket(w http.ResponseWriter, r *http.Request) {
	var req initAssetBucketRequest
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	bucket, ok := s.getAssetBucketForAPI(w, r, cleanBucketSlug(r.PathValue("slug")))
	if !ok {
		return
	}
	result, err := s.initAssetBucket(r.Context(), bucket, req.DryRun)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handlePurgeAssetBucketCache(w http.ResponseWriter, r *http.Request) {
	bucket, ok := s.getAssetBucketForAPI(w, r, cleanBucketSlug(r.PathValue("slug")))
	if !ok {
		return
	}
	var req assetBucketCacheRequest
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	writeJSON(w, http.StatusOK, s.purgeAssetBucketCache(r.Context(), bucket, req))
}

func (s *Server) handleWarmupAssetBucket(w http.ResponseWriter, r *http.Request) {
	bucket, ok := s.getAssetBucketForAPI(w, r, cleanBucketSlug(r.PathValue("slug")))
	if !ok {
		return
	}
	var req assetBucketCacheRequest
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	writeJSON(w, http.StatusOK, s.warmupAssetBucket(r.Context(), bucket, req))
}

func (s *Server) handleRefreshAssetBucketReplicas(w http.ResponseWriter, r *http.Request) {
	bucket, ok := s.getAssetBucketForAPI(w, r, cleanBucketSlug(r.PathValue("slug")))
	if !ok {
		return
	}
	var req refreshAssetBucketReplicasRequest
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Path) == "" && len(req.Paths) == 0 && strings.TrimSpace(req.Prefix) == "" && !req.All {
		req.All = true
	}
	resp, err := s.refreshAssetBucketReplicas(r.Context(), bucket, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleUploadBucketObject(w http.ResponseWriter, r *http.Request) {
	slug := cleanBucketSlug(r.PathValue("slug"))
	bucket, ok := s.getAssetBucketForAPI(w, r, slug)
	if !ok {
		return
	}
	if !s.overclockMode() && s.cfg.Limits.MaxUploadBytes > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, s.cfg.Limits.MaxUploadBytes)
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart upload: "+err.Error())
		return
	}
	logicalPath, err := storage.CleanObjectPath(r.FormValue("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file field is required")
		return
	}
	defer file.Close()
	staged, err := s.stageUpload(file, header.Filename)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer os.Remove(staged.Path)
	item, obj, jobs, err := s.putBucketObjectFromStaged(r.Context(), bucket, logicalPath, staged, header.Filename, r.FormValue("asset_type"), r.FormValue("cache_control"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	item.IPFS = obj.IPFS
	publicURL := s.assetBucketPublicURL("", bucket.Slug, item.LogicalPath)
	urls := []string{publicURL}
	resp := map[string]any{
		"bucket":        bucket.Slug,
		"object":        obj,
		"bucket_object": item,
		"jobs":          jobs,
		"url":           item.URL,
		"public_url":    publicURL,
	}
	if len(obj.IPFS) > 0 {
		resp["ipfs"] = obj.IPFS
	}
	if cdnURL, err := s.objectRedirectURL(r.Context(), obj); err == nil {
		resp["cdn_url"] = cdnURL
		resp["storage_url"] = cdnURL
		urls = append(urls, cdnURL)
	}
	resp["urls"] = uniqueStrings(urls)
	writeJSON(w, http.StatusCreated, s.withOverclockWarning(resp))
}

func (s *Server) handleListBucketObjects(w http.ResponseWriter, r *http.Request) {
	slug := cleanBucketSlug(r.PathValue("slug"))
	if _, ok := s.getAssetBucketForAPI(w, r, slug); !ok {
		return
	}
	prefix := strings.TrimPrefix(strings.ReplaceAll(r.URL.Query().Get("prefix"), "\\", "/"), "/")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := s.db.ListAssetBucketObjects(r.Context(), slug, prefix, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items, err = s.hydrateBucketObjectsIPFS(r.Context(), items)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"objects": items})
}

func (s *Server) handleDeleteBucketObject(w http.ResponseWriter, r *http.Request) {
	slug := cleanBucketSlug(r.PathValue("slug"))
	if slug == "" {
		writeError(w, http.StatusBadRequest, "bucket is required")
		return
	}
	deleteRemote, err := queryBool(r, "delete_remote", true)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	force, err := queryBool(r, "force", false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	selector, err := deleteBucketObjectsSelectorFromQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	bucket, ok := s.getAssetBucketForAPI(w, r, slug)
	if !ok {
		return
	}
	if selector.needsForce() && !force {
		writeError(w, http.StatusBadRequest, "force=true is required for prefix or all object deletion")
		return
	}
	if selector.singlePath() {
		result, err := s.deleteBucketObject(r.Context(), slug, selector.Paths[0], deleteRemote)
		if err != nil {
			status := http.StatusBadGateway
			if db.IsNotFound(err) {
				status = http.StatusNotFound
			}
			if result != nil {
				writeJSON(w, status, result)
			} else {
				writeError(w, status, err.Error())
			}
			return
		}
		writeJSON(w, http.StatusOK, result)
		return
	}
	result, err := s.deleteBucketObjects(r.Context(), bucket, selector, deleteRemote)
	if err != nil {
		status := http.StatusBadGateway
		if db.IsNotFound(err) || strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		if result != nil {
			writeJSON(w, status, result)
		} else {
			writeError(w, status, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) initAssetBucket(ctx context.Context, bucket *model.AssetBucket, dryRun bool) (*initAssetBucketResult, error) {
	dirs := bucketInitDirs(bucket.Slug)
	result := &initAssetBucketResult{Bucket: bucket.Slug, DryRun: dryRun, Directories: dirs, Status: "skipped"}
	profile, ok := s.cfg.Profile(bucket.RouteProfile)
	if !ok {
		return result, fmt.Errorf("unknown route_profile %q", bucket.RouteProfile)
	}
	store, ok := s.stores.Get(profile.Primary)
	if !ok {
		return result, fmt.Errorf("primary storage %q is not configured", profile.Primary)
	}
	initializer, ok := store.(storage.InitializableStore)
	if !ok {
		result.Reason = "primary storage does not support directory initialization"
		return result, nil
	}
	run := func() error {
		var err error
		result.Result, err = initializer.InitDirs(ctx, storage.InitOptions{Directories: dirs, DryRun: dryRun})
		return err
	}
	var err error
	if dryRun {
		err = run()
	} else {
		err = s.withTransferSlot(ctx, run)
	}
	if err != nil {
		result.Status = "error"
		return result, err
	}
	result.Status = "ok"
	return result, nil
}

func (s *Server) putBucketObjectFromStaged(ctx context.Context, bucket *model.AssetBucket, logicalPath string, staged *stagedFile, fileName, explicitType, cacheControl string) (*model.AssetBucketObject, *model.Object, []model.Job, error) {
	if bucket.Status != model.AssetBucketActive {
		return nil, nil, nil, fmt.Errorf("bucket %q is %s", bucket.Slug, bucket.Status)
	}
	profileName := firstNonEmpty(bucket.RouteProfile, "china_all")
	profile, ok := s.cfg.Profile(profileName)
	if !ok {
		return nil, nil, nil, fmt.Errorf("unknown route_profile %q", profileName)
	}
	assetType, err := classifyAssetType(logicalPath, fileName, staged.ContentType, explicitType)
	if err != nil {
		return nil, nil, nil, err
	}
	if !s.overclockMode() && !bucketAllowsType(bucket, assetType) {
		return nil, nil, nil, fmt.Errorf("bucket %q does not allow asset type %q", bucket.Slug, assetType)
	}
	if !s.overclockMode() && bucket.MaxFileSizeBytes > 0 && staged.Size > bucket.MaxFileSizeBytes {
		return nil, nil, nil, fmt.Errorf("bucket %q max_file_size_bytes is %d, got %d", bucket.Slug, bucket.MaxFileSizeBytes, staged.Size)
	}
	if !s.overclockMode() && bucket.MaxCapacityBytes > 0 {
		used, err := s.db.AssetBucketUsedBytes(ctx, bucket.Slug)
		if err != nil {
			return nil, nil, nil, err
		}
		if existing, err := s.db.GetAssetBucketObject(ctx, bucket.Slug, logicalPath); err == nil {
			used -= existing.Size
			if used < 0 {
				used = 0
			}
		}
		if used+staged.Size > bucket.MaxCapacityBytes {
			return nil, nil, nil, fmt.Errorf("bucket %q max_capacity_bytes is %d, current used %d, upload got %d", bucket.Slug, bucket.MaxCapacityBytes, used, staged.Size)
		}
	}
	if _, err := s.preflightProfile(ctx, profileName, profile, preflightRequest{
		TotalSize:       staged.Size,
		LargestFileSize: staged.Size,
		BatchFileCount:  1,
	}); err != nil {
		return nil, nil, nil, err
	}
	projectID := "bucket:" + bucket.Slug
	if _, err := s.db.CreateProjectInWorkspace(ctx, projectID, bucket.WorkspaceID); err != nil {
		return nil, nil, nil, err
	}
	key := bucketPhysicalKey(bucket.Slug, assetType, logicalPath, fileName, staged.SHA256)
	obj, jobs, err := s.putObjectFromFile(ctx, putObjectInput{
		ProjectID:      projectID,
		ObjectPath:     logicalPath,
		Key:            key,
		Profile:        profile,
		ProfileName:    profileName,
		CacheControl:   firstNonEmpty(strings.TrimSpace(cacheControl), bucket.DefaultCacheControl, profile.DefaultCacheControl, "public, max-age=3600"),
		ContentType:    staged.ContentType,
		Group:          storageGroupForBucket(bucket.Slug),
		FilePath:       staged.Path,
		FileName:       firstNonEmpty(fileName, path.Base(logicalPath)),
		Size:           staged.Size,
		SHA256:         staged.SHA256,
		BatchFileCount: 1,
	})
	if err != nil {
		return nil, nil, nil, err
	}
	item, err := s.db.SaveAssetBucketObject(ctx, model.AssetBucketObject{
		BucketSlug:  bucket.Slug,
		LogicalPath: logicalPath,
		ObjectID:    obj.ID,
		AssetType:   assetType,
		PhysicalKey: key,
		Size:        staged.Size,
		SHA256:      staged.SHA256,
		ContentType: obj.ContentType,
	})
	if err != nil {
		return nil, nil, nil, err
	}
	return item, obj, jobs, nil
}

func storageGroupForBucket(slug string) string {
	slug = cleanBucketSlug(slug)
	if slug == "" {
		return ""
	}
	return "bucket-" + slug
}

func storageGroupFromProjectID(projectID string) string {
	projectID = strings.TrimSpace(projectID)
	const prefix = "bucket:"
	if !strings.HasPrefix(projectID, prefix) {
		return ""
	}
	return storageGroupForBucket(strings.TrimPrefix(projectID, prefix))
}

type deleteBucketObjectsSelector struct {
	Paths  []string
	Prefix string
	All    bool
}

func (s deleteBucketObjectsSelector) singlePath() bool {
	return len(s.Paths) == 1 && s.Prefix == "" && !s.All
}

func (s deleteBucketObjectsSelector) needsForce() bool {
	return s.Prefix != "" || s.All
}

func deleteBucketObjectsSelectorFromQuery(r *http.Request) (deleteBucketObjectsSelector, error) {
	q := r.URL.Query()
	rawPaths := append([]string(nil), q["path"]...)
	for _, value := range q["paths"] {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				rawPaths = append(rawPaths, part)
			}
		}
	}
	selector := deleteBucketObjectsSelector{}
	seen := map[string]bool{}
	for _, raw := range rawPaths {
		cleaned, err := storage.CleanObjectPath(raw)
		if err != nil {
			return selector, err
		}
		if seen[cleaned] {
			continue
		}
		seen[cleaned] = true
		selector.Paths = append(selector.Paths, cleaned)
	}
	rawPrefix := strings.TrimSpace(q.Get("prefix"))
	if rawPrefix != "" {
		prefix, err := storage.CleanDirectoryPath(rawPrefix)
		if err != nil {
			return selector, err
		}
		if prefix == "" {
			return selector, fmt.Errorf("use all=true to delete every object in a bucket")
		}
		selector.Prefix = prefix
	}
	all, err := queryBool(r, "all", false)
	if err != nil {
		return selector, err
	}
	selector.All = all
	modes := 0
	if len(selector.Paths) > 0 {
		modes++
	}
	if selector.Prefix != "" {
		modes++
	}
	if selector.All {
		modes++
	}
	if modes == 0 {
		return selector, fmt.Errorf("select at least one bucket object with path, paths, prefix, or all=true")
	}
	if modes > 1 {
		return selector, fmt.Errorf("select only one of path/paths, prefix, or all=true")
	}
	return selector, nil
}

func (s *Server) deleteBucketObject(ctx context.Context, bucketSlug, logicalPath string, deleteRemote bool) (*deleteBucketObjectResult, error) {
	bucket, err := s.db.GetAssetBucket(ctx, bucketSlug)
	if err != nil {
		return nil, err
	}
	item, err := s.db.GetAssetBucketObject(ctx, bucket.Slug, logicalPath)
	if err != nil {
		return nil, err
	}
	return s.deleteBucketObjectItem(ctx, bucket, *item, deleteRemote)
}

func (s *Server) deleteBucketObjects(ctx context.Context, bucket *model.AssetBucket, selector deleteBucketObjectsSelector, deleteRemote bool) (*deleteBucketObjectsResult, error) {
	result := &deleteBucketObjectsResult{
		Bucket:       bucket.Slug,
		DeleteRemote: deleteRemote,
		Paths:        append([]string(nil), selector.Paths...),
		Prefix:       selector.Prefix,
		All:          selector.All,
	}
	items, err := s.bucketObjectsForDelete(ctx, bucket.Slug, selector)
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
		return result, err
	}
	result.ObjectCount = len(items)
	for _, item := range items {
		deleted, err := s.deleteBucketObjectItem(ctx, bucket, item, deleteRemote)
		if deleted != nil {
			result.Objects = append(result.Objects, *deleted)
		}
		if err != nil {
			result.Errors = append(result.Errors, err.Error())
		}
	}
	if len(result.Errors) > 0 {
		return result, errors.New(strings.Join(result.Errors, "; "))
	}
	return result, nil
}

func (s *Server) bucketObjectsForDelete(ctx context.Context, bucketSlug string, selector deleteBucketObjectsSelector) ([]model.AssetBucketObject, error) {
	if len(selector.Paths) > 0 {
		items := make([]model.AssetBucketObject, 0, len(selector.Paths))
		for _, logicalPath := range selector.Paths {
			item, err := s.db.GetAssetBucketObject(ctx, bucketSlug, logicalPath)
			if err != nil {
				return nil, fmt.Errorf("bucket object %q not found: %w", logicalPath, err)
			}
			items = append(items, *item)
		}
		return items, nil
	}
	items, err := s.db.ListAllAssetBucketObjects(ctx, bucketSlug)
	if err != nil {
		return nil, err
	}
	if selector.All {
		return items, nil
	}
	filtered := make([]model.AssetBucketObject, 0, len(items))
	for _, item := range items {
		if bucketObjectMatchesPrefix(item.LogicalPath, selector.Prefix) {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func bucketObjectMatchesPrefix(logicalPath, prefix string) bool {
	if prefix == "" {
		return false
	}
	return logicalPath == prefix || strings.HasPrefix(logicalPath, prefix+"/")
}

func (s *Server) deleteAssetBucket(ctx context.Context, bucket *model.AssetBucket, deleteObjects, deleteRemote bool) (*deleteAssetBucketResult, error) {
	items, err := s.db.ListAllAssetBucketObjects(ctx, bucket.Slug)
	if err != nil {
		return nil, err
	}
	result := &deleteAssetBucketResult{
		Bucket:        bucket.Slug,
		DeleteObjects: deleteObjects,
		DeleteRemote:  deleteRemote,
		ObjectCount:   len(items),
	}
	if len(items) > 0 && !deleteObjects {
		err := fmt.Errorf("bucket %q is not empty; pass delete_objects=true or force=true", bucket.Slug)
		result.Errors = append(result.Errors, err.Error())
		return result, err
	}
	for _, item := range items {
		deleted, err := s.deleteBucketObjectItem(ctx, bucket, item, deleteRemote)
		if deleted != nil {
			result.Objects = append(result.Objects, *deleted)
		}
		if err != nil {
			result.Errors = append(result.Errors, err.Error())
		}
	}
	if len(result.Errors) > 0 {
		return result, errors.New(strings.Join(result.Errors, "; "))
	}
	if err := s.db.DeleteAssetBucket(ctx, bucket.Slug); err != nil {
		result.Errors = append(result.Errors, err.Error())
		return result, err
	}
	result.DeletedBucket = true
	if err := s.db.DeleteProject(ctx, "bucket:"+bucket.Slug); err == nil {
		result.DeletedProject = true
	} else if !db.IsNotFound(err) {
		result.Errors = append(result.Errors, err.Error())
		return result, err
	}
	return result, nil
}

func (s *Server) deleteBucketObjectItem(ctx context.Context, bucket *model.AssetBucket, item model.AssetBucketObject, deleteRemote bool) (*deleteBucketObjectResult, error) {
	result := &deleteBucketObjectResult{
		Bucket:       bucket.Slug,
		LogicalPath:  item.LogicalPath,
		ObjectID:     item.ObjectID,
		DeleteRemote: deleteRemote,
	}
	obj, err := s.db.GetObject(ctx, item.ObjectID)
	if err != nil {
		if db.IsNotFound(err) {
			if deleteErr := s.db.DeleteAssetBucketObject(ctx, bucket.Slug, item.LogicalPath); deleteErr != nil {
				result.Errors = append(result.Errors, deleteErr.Error())
				return result, deleteErr
			}
			result.DeletedLocal = true
			return result, nil
		}
		result.Errors = append(result.Errors, err.Error())
		return result, err
	}
	result.Key = obj.Key
	if deleteRemote {
		if err := s.withTransferSlot(ctx, func() error {
			return s.deleteObjectRemoteReplicas(ctx, obj, result)
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

func (s *Server) purgeAssetBucketCache(ctx context.Context, bucket *model.AssetBucket, req assetBucketCacheRequest) purgeAssetBucketCacheResponse {
	resp := purgeAssetBucketCacheResponse{Bucket: bucket.Slug, DryRun: req.DryRun, Status: "planned"}
	urls, warnings, err := s.assetBucketCacheURLs(ctx, bucket.Slug, req)
	resp.URLs = urls
	resp.URLCount = len(urls)
	resp.Warnings = append(resp.Warnings, warnings...)
	if err != nil {
		resp.Status = "failed"
		resp.Errors = append(resp.Errors, err.Error())
		return resp
	}
	if req.DryRun {
		return resp
	}
	account, library, err := s.cloudflareAccountForCacheBase(firstNonEmpty(req.BaseURL, s.cfg.Server.PublicBaseURL), req.CloudflareAccount, req.CloudflareLibrary)
	resp.CloudflareAccount = account.Name
	resp.CloudflareLibrary = library.Name
	if err != nil {
		resp.Status = "failed"
		resp.Errors = append(resp.Errors, err.Error())
		return resp
	}
	cf := s.cloudflareClientForAccount(account)
	if !cf.Configured() {
		resp.Status = "skipped"
		resp.Errors = append(resp.Errors, "cloudflare zone_id/api_token not configured")
		return resp
	}
	batches, err := cf.PurgeCacheBatches(ctx, urls)
	if err != nil {
		resp.Status = "failed"
		resp.Errors = append(resp.Errors, err.Error())
		return resp
	}
	resp.Batches = batches
	resp.Status = "ok"
	for _, batch := range batches {
		if batch.Error != "" {
			resp.Status = "partial"
			resp.Errors = append(resp.Errors, fmt.Sprintf("batch %d: %s", batch.Batch, batch.Error))
		}
	}
	return resp
}

func (s *Server) warmupAssetBucket(ctx context.Context, bucket *model.AssetBucket, req assetBucketCacheRequest) warmupAssetBucketResponse {
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodHead
	}
	resp := warmupAssetBucketResponse{Bucket: bucket.Slug, DryRun: req.DryRun, Method: method, Status: "planned"}
	if method != http.MethodHead && method != http.MethodGet {
		resp.Status = "failed"
		resp.Errors = append(resp.Errors, "method must be HEAD or GET")
		return resp
	}
	urls, warnings, err := s.assetBucketCacheURLs(ctx, bucket.Slug, req)
	resp.URLs = urls
	resp.URLCount = len(urls)
	resp.Warnings = append(resp.Warnings, warnings...)
	if err != nil {
		resp.Status = "failed"
		resp.Errors = append(resp.Errors, err.Error())
		return resp
	}
	if req.DryRun {
		return resp
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp.Results = make([]bucketWarmupResult, 0, len(urls))
	resp.Status = "ok"
	for _, warmURL := range urls {
		result := s.warmupURL(ctx, client, method, warmURL)
		resp.Results = append(resp.Results, result)
		if result.Status != "ok" {
			resp.Status = "partial"
			resp.Errors = append(resp.Errors, warmURL+": "+result.Error)
		}
	}
	return resp
}

func (s *Server) refreshAssetBucketReplicas(ctx context.Context, bucket *model.AssetBucket, req refreshAssetBucketReplicasRequest) (*refreshAssetBucketReplicasResponse, error) {
	items, warnings, err := s.assetBucketCacheObjects(ctx, bucket.Slug, assetBucketCacheRequest{
		Path:   req.Path,
		Paths:  req.Paths,
		Prefix: req.Prefix,
		All:    req.All,
		Limit:  req.Limit,
	})
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("no bucket objects selected")
	}
	resp := &refreshAssetBucketReplicasResponse{
		Status:      "ok",
		Bucket:      bucket.Slug,
		Target:      strings.TrimSpace(req.Target),
		ObjectCount: len(items),
		Warnings:    warnings,
	}
	for _, item := range items {
		result := refreshAssetBucketReplicaObjectResult{
			BucketSlug:  item.BucketSlug,
			LogicalPath: item.LogicalPath,
			ObjectID:    item.ObjectID,
			Status:      "ok",
		}
		obj, err := s.db.GetObject(ctx, item.ObjectID)
		if err != nil {
			result.Status = "failed"
			result.Errors = append(result.Errors, err.Error())
			resp.Errors = append(resp.Errors, item.LogicalPath+": "+err.Error())
			resp.Objects = append(resp.Objects, result)
			continue
		}
		refreshed, refreshErr := s.refreshObjectReplicas(ctx, obj, refreshObjectReplicasRequest{Target: req.Target})
		if refreshErr != nil {
			result.Status = "failed"
			result.Errors = append(result.Errors, refreshErr.Error())
			resp.Errors = append(resp.Errors, item.LogicalPath+": "+refreshErr.Error())
			resp.Objects = append(resp.Objects, result)
			continue
		}
		result.Status = refreshed.Status
		result.Results = refreshed.Results
		for _, itemErr := range refreshed.Errors {
			result.Errors = append(result.Errors, itemErr)
			resp.Errors = append(resp.Errors, item.LogicalPath+": "+itemErr)
		}
		resp.Objects = append(resp.Objects, result)
	}
	if len(resp.Errors) > 0 {
		failed := 0
		for _, object := range resp.Objects {
			if object.Status == "failed" {
				failed++
			}
		}
		if failed == len(resp.Objects) {
			resp.Status = "failed"
		} else {
			resp.Status = "partial"
		}
	}
	return resp, nil
}

func (s *Server) warmupURL(ctx context.Context, client *http.Client, method, warmURL string) bucketWarmupResult {
	result := bucketWarmupResult{URL: warmURL, Method: method, Status: "ok"}
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, method, warmURL, nil)
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		return result
	}
	req.Header.Set("User-Agent", "SuperCDN-Warmup/1.0")
	resp, err := client.Do(req)
	result.LatencyMS = elapsedMS(start)
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		return result
	}
	defer resp.Body.Close()
	result.HTTPCode = resp.StatusCode
	if method == http.MethodGet {
		_, _ = io.Copy(io.Discard, resp.Body)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 399 {
		result.Status = "failed"
		result.Error = resp.Status
	}
	return result
}

func (s *Server) assetBucketCacheURLs(ctx context.Context, bucketSlug string, req assetBucketCacheRequest) ([]string, []string, error) {
	items, warnings, err := s.assetBucketCacheObjects(ctx, bucketSlug, req)
	if err != nil {
		return nil, warnings, err
	}
	urls := make([]string, 0, len(items))
	for _, item := range items {
		urls = append(urls, s.assetBucketPublicURL(req.BaseURL, item.BucketSlug, item.LogicalPath))
	}
	urls = uniqueStrings(urls)
	if len(urls) == 0 {
		return nil, warnings, fmt.Errorf("no asset bucket URLs generated")
	}
	return urls, warnings, nil
}

func (s *Server) assetBucketCacheObjects(ctx context.Context, bucketSlug string, req assetBucketCacheRequest) ([]model.AssetBucketObject, []string, error) {
	var warnings []string
	paths := append([]string{}, req.Paths...)
	if strings.TrimSpace(req.Path) != "" {
		paths = append(paths, req.Path)
	}
	if len(paths) > 0 {
		items := make([]model.AssetBucketObject, 0, len(paths))
		seen := map[string]bool{}
		for _, p := range paths {
			cleaned, err := storage.CleanObjectPath(p)
			if err != nil {
				return nil, warnings, err
			}
			if seen[cleaned] {
				continue
			}
			seen[cleaned] = true
			item, err := s.db.GetAssetBucketObject(ctx, bucketSlug, cleaned)
			if err != nil {
				return nil, warnings, fmt.Errorf("bucket object %q not found", cleaned)
			}
			items = append(items, *item)
		}
		return items, warnings, nil
	}
	prefix, err := storage.CleanDirectoryPath(req.Prefix)
	if err != nil {
		return nil, warnings, err
	}
	if prefix != "" {
		limit := req.Limit
		if limit <= 0 {
			limit = 1000
			warnings = append(warnings, "prefix selection defaulted to limit=1000")
		}
		items, err := s.db.ListAssetBucketObjects(ctx, bucketSlug, prefix, limit)
		return items, warnings, err
	}
	if req.All {
		items, err := s.db.ListAllAssetBucketObjects(ctx, bucketSlug)
		return items, warnings, err
	}
	return nil, warnings, fmt.Errorf("select at least one bucket object with path, paths, prefix, or all=true")
}

func (s *Server) assetBucketPublicURL(baseURL, bucketSlug, logicalPath string) string {
	escaped := "/a/" + url.PathEscape(bucketSlug) + "/" + escapeURLPath(logicalPath)
	if strings.TrimSpace(baseURL) != "" {
		return strings.TrimRight(strings.TrimSpace(baseURL), "/") + escaped
	}
	return s.absolutePublicURL(escaped)
}

func normalizeAssetTypes(values []string) ([]string, error) {
	if len(values) == 0 {
		return []string{model.AssetTypeImage, model.AssetTypeVideo, model.AssetTypeDocument, model.AssetTypeArchive, model.AssetTypeOther}, nil
	}
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if !validAssetType(value) {
			return nil, fmt.Errorf("invalid asset type %q", value)
		}
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("at least one asset type is required")
	}
	return out, nil
}

func validAssetType(value string) bool {
	switch value {
	case model.AssetTypeImage, model.AssetTypeVideo, model.AssetTypeDocument, model.AssetTypeArchive, model.AssetTypeOther:
		return true
	default:
		return false
	}
}

func bucketAllowsType(bucket *model.AssetBucket, assetType string) bool {
	allowed := bucket.AllowedTypes
	if len(allowed) == 0 {
		return true
	}
	for _, value := range allowed {
		if value == assetType {
			return true
		}
	}
	return false
}

func classifyAssetType(logicalPath, fileName, contentType, explicit string) (string, error) {
	explicit = strings.ToLower(strings.TrimSpace(explicit))
	if explicit != "" {
		if !validAssetType(explicit) {
			return "", fmt.Errorf("invalid asset_type %q", explicit)
		}
		return explicit, nil
	}
	ext := strings.ToLower(path.Ext(firstNonEmpty(logicalPath, fileName)))
	ct := strings.ToLower(contentType)
	switch {
	case strings.HasPrefix(ct, "image/") || inSet(ext, ".jpg", ".jpeg", ".png", ".gif", ".webp", ".avif", ".svg", ".bmp", ".ico"):
		return model.AssetTypeImage, nil
	case strings.HasPrefix(ct, "video/") || inSet(ext, ".mp4", ".mkv", ".mov", ".webm", ".avi", ".m4v", ".flv"):
		return model.AssetTypeVideo, nil
	case inSet(ext, ".zip", ".rar", ".7z", ".tar", ".gz", ".bz2", ".xz") ||
		strings.Contains(ct, "zip") || strings.Contains(ct, "x-7z") || strings.Contains(ct, "x-rar") || strings.Contains(ct, "gzip"):
		return model.AssetTypeArchive, nil
	case strings.HasPrefix(ct, "text/") || inSet(ext, ".md", ".markdown", ".txt", ".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx", ".csv", ".json"):
		return model.AssetTypeDocument, nil
	default:
		return model.AssetTypeOther, nil
	}
}

func bucketTypeDir(assetType string) string {
	switch assetType {
	case model.AssetTypeImage:
		return "images"
	case model.AssetTypeVideo:
		return "videos"
	case model.AssetTypeDocument:
		return "documents"
	case model.AssetTypeArchive:
		return "archives"
	default:
		return "other"
	}
}

func bucketInitDirs(slug string) []string {
	base := storage.JoinKey("assets", "buckets", slug)
	return []string{
		base,
		storage.JoinKey(base, "images"),
		storage.JoinKey(base, "videos"),
		storage.JoinKey(base, "documents"),
		storage.JoinKey(base, "archives"),
		storage.JoinKey(base, "other"),
		storage.JoinKey(base, "tmp"),
	}
}

func bucketPhysicalKey(slug, assetType, logicalPath, fileName, sha string) string {
	ext := strings.ToLower(path.Ext(firstNonEmpty(logicalPath, fileName)))
	prefix := sha
	if len(prefix) > 2 {
		prefix = prefix[:2]
	}
	now := time.Now().UTC()
	return storage.JoinKey(
		"assets",
		"buckets",
		slug,
		bucketTypeDir(assetType),
		now.Format("2006"),
		now.Format("01"),
		prefix,
		sha+ext,
	)
}
