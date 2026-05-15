package server

import (
	"database/sql"
	"errors"
	"net/http"
	"os"
	"path"
	"strings"

	"supercdn/internal/storage"
)

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.ID = cleanID(req.ID)
	if req.ID == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	project, err := s.db.CreateProjectInWorkspace(r.Context(), req.ID, workspaceForContext(r.Context()))
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "belongs to another workspace") {
			status = http.StatusForbidden
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, project)
}

type preflightRequest struct {
	RouteProfile    string `json:"route_profile"`
	SiteID          string `json:"site_id"`
	TotalSize       int64  `json:"total_size"`
	LargestFileSize int64  `json:"largest_file_size"`
	BatchFileCount  int    `json:"batch_file_count"`
}

func (s *Server) handlePreflightUpload(w http.ResponseWriter, r *http.Request) {
	var req preflightRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.RouteProfile = firstNonEmpty(req.RouteProfile, "overseas")
	profile, ok := s.cfg.Profile(req.RouteProfile)
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown route_profile")
		return
	}
	result, err := s.preflightProfile(r.Context(), req.RouteProfile, profile, req)
	if err != nil {
		if writeQuotaError(w, err) {
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handlePreflightSiteDeploy(w http.ResponseWriter, r *http.Request) {
	var req preflightRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.SiteID = cleanID(req.SiteID)
	if req.SiteID == "" {
		writeError(w, http.StatusBadRequest, "site_id is required")
		return
	}
	profileName := req.RouteProfile
	site, err := s.db.GetSite(r.Context(), req.SiteID)
	if err == nil {
		if !principalCanAccessWorkspace(currentPrincipal(r.Context()), site.WorkspaceID) {
			writeError(w, http.StatusNotFound, "site not found")
			return
		}
		profileName = firstNonEmpty(profileName, site.RouteProfile)
	} else if !errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	profileName = firstNonEmpty(profileName, "overseas")
	profile, ok := s.cfg.Profile(profileName)
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown route_profile")
		return
	}
	if err := s.checkSiteFileCount(req.BatchFileCount); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := s.preflightProfile(r.Context(), profileName, profile, req)
	if err != nil {
		if writeQuotaError(w, err) {
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleUploadAsset(w http.ResponseWriter, r *http.Request) {
	if !s.overclockMode() && s.cfg.Limits.MaxUploadBytes > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, s.cfg.Limits.MaxUploadBytes)
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart upload: "+err.Error())
		return
	}
	projectID := cleanID(r.FormValue("project_id"))
	objectPath, err := storage.CleanObjectPath(r.FormValue("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	profileName := firstNonEmpty(r.FormValue("route_profile"), "overseas")
	profile, ok := s.cfg.Profile(profileName)
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown route_profile")
		return
	}
	if projectID == "" {
		writeError(w, http.StatusBadRequest, "project_id is required")
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
	if _, err := s.preflightProfile(r.Context(), profileName, profile, preflightRequest{
		TotalSize:       staged.Size,
		LargestFileSize: staged.Size,
		BatchFileCount:  1,
	}); err != nil {
		if writeQuotaError(w, err) {
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := s.db.CreateProjectInWorkspace(r.Context(), projectID, workspaceForContext(r.Context())); err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "belongs to another workspace") {
			status = http.StatusForbidden
		}
		writeError(w, status, err.Error())
		return
	}
	cacheControl := firstNonEmpty(r.FormValue("cache_control"), profile.DefaultCacheControl, "public, max-age=3600")
	key := storage.JoinKey("objects", projectID, objectPath)
	reservation, _, err := s.reserveUploadQuota(r.Context(), staged.Size)
	if err != nil {
		if writeQuotaError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	quotaCommitted := false
	defer func() {
		if !quotaCommitted {
			s.releaseUploadQuota(r.Context(), reservation)
		}
	}()
	obj, jobs, err := s.putObjectFromFile(r.Context(), putObjectInput{
		ProjectID:      projectID,
		ObjectPath:     objectPath,
		Key:            key,
		Profile:        profile,
		ProfileName:    profileName,
		CacheControl:   cacheControl,
		ContentType:    staged.ContentType,
		FilePath:       staged.Path,
		FileName:       firstNonEmpty(header.Filename, path.Base(objectPath)),
		Size:           staged.Size,
		SHA256:         staged.SHA256,
		BatchFileCount: 1,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	quotaCommitted = true
	writeJSON(w, http.StatusCreated, s.withOverclockWarning(map[string]any{
		"object": obj,
		"jobs":   jobs,
		"url":    "/o/" + projectID + "/" + objectPath,
	}))
}
