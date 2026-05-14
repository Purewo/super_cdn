package server

import (
	"database/sql"
	"errors"
	"net/http"

	"supercdn/internal/db"
	"supercdn/internal/model"
)

func (s *Server) getAssetBucketForAPI(w http.ResponseWriter, r *http.Request, slug string) (*model.AssetBucket, bool) {
	bucket, err := s.db.GetAssetBucket(r.Context(), slug)
	if err != nil {
		if db.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "bucket not found")
			return nil, false
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return nil, false
	}
	if !principalCanAccessWorkspace(currentPrincipal(r.Context()), bucket.WorkspaceID) {
		writeError(w, http.StatusNotFound, "bucket not found")
		return nil, false
	}
	return bucket, true
}

func (s *Server) getSiteForAPI(w http.ResponseWriter, r *http.Request, siteID string) (*model.Site, bool) {
	site, err := s.db.GetSite(r.Context(), siteID)
	if err != nil {
		writeError(w, http.StatusNotFound, "site not found")
		return nil, false
	}
	if !principalCanAccessWorkspace(currentPrincipal(r.Context()), site.WorkspaceID) {
		writeError(w, http.StatusNotFound, "site not found")
		return nil, false
	}
	return site, true
}

func (s *Server) ensureSiteAccessIfExists(w http.ResponseWriter, r *http.Request, siteID string) bool {
	site, err := s.db.GetSite(r.Context(), siteID)
	if errors.Is(err, sql.ErrNoRows) {
		return true
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return false
	}
	if !principalCanAccessWorkspace(currentPrincipal(r.Context()), site.WorkspaceID) {
		writeError(w, http.StatusNotFound, "site not found")
		return false
	}
	return true
}
