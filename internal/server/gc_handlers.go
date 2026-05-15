package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type gcRequest struct {
	DryRun           bool   `json:"dry_run"`
	DeleteRemote     bool   `json:"delete_remote"`
	OlderThanSeconds int64  `json:"older_than_seconds"`
	Bucket           string `json:"bucket"`
	Site             string `json:"site"`
	Force            bool   `json:"force"`
}

type gcItem struct {
	Scope     string    `json:"scope"`
	Path      string    `json:"path"`
	Size      int64     `json:"size,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
	Status    string    `json:"status"`
	Error     string    `json:"error,omitempty"`
}

type gcResponse struct {
	Status           string    `json:"status"`
	DryRun           bool      `json:"dry_run"`
	DeleteRemote     bool      `json:"delete_remote"`
	OlderThanSeconds int64     `json:"older_than_seconds"`
	Cutoff           time.Time `json:"cutoff"`
	Bucket           string    `json:"bucket,omitempty"`
	Site             string    `json:"site,omitempty"`
	Planned          int       `json:"planned"`
	Deleted          int       `json:"deleted"`
	Kept             int       `json:"kept"`
	ErrorCount       int       `json:"error_count"`
	Items            []gcItem  `json:"items,omitempty"`
	Warnings         []string  `json:"warnings,omitempty"`
	Errors           []string  `json:"errors,omitempty"`
}

func (s *Server) handleSiteGC(w http.ResponseWriter, r *http.Request) {
	siteID := cleanID(r.PathValue("id"))
	if siteID == "" {
		writeError(w, http.StatusBadRequest, "site id is required")
		return
	}
	if _, ok := s.getSiteForAPI(w, r, siteID); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"site_id": siteID,
		"status":  "noop",
		"message": "site content GC is tracked by deployment manifests; destructive cleanup is intentionally manual in this version",
	})
}

func (s *Server) handleManualGC(w http.ResponseWriter, r *http.Request) {
	var req gcRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.runManualGC(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	action := auditActionGCDelete
	if req.DryRun {
		action = auditActionGCDryRun
	}
	if !s.auditMutation(w, r, action, "gc:manual") {
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) runManualGC(ctx context.Context, req gcRequest) (*gcResponse, error) {
	req.Bucket = cleanBucketSlug(req.Bucket)
	req.Site = cleanID(req.Site)
	if req.Bucket != "" && req.Site != "" {
		return nil, errors.New("choose only one of bucket or site")
	}
	olderThan := time.Duration(req.OlderThanSeconds) * time.Second
	if req.OlderThanSeconds <= 0 {
		olderThan = time.Hour
		req.OlderThanSeconds = int64(olderThan.Seconds())
	}
	if olderThan < 5*time.Minute && !req.Force {
		return nil, errors.New("force is required when older_than_seconds is less than 300")
	}
	cutoff := time.Now().UTC().Add(-olderThan)
	resp := &gcResponse{
		Status:           "ok",
		DryRun:           req.DryRun,
		DeleteRemote:     req.DeleteRemote,
		OlderThanSeconds: req.OlderThanSeconds,
		Cutoff:           cutoff,
		Bucket:           req.Bucket,
		Site:             req.Site,
	}
	if req.Bucket != "" || req.Site != "" {
		resp.Warnings = append(resp.Warnings, "bucket/site scoped remote cleanup is not implemented in this conservative pass; local staging cleanup is still global")
	}
	if req.DeleteRemote {
		resp.Warnings = append(resp.Warnings, "delete_remote is accepted for future remote cleanup; this pass only removes local staging files")
	}
	items, err := s.gcStagingFiles(ctx, cutoff, req.DryRun)
	if err != nil {
		return nil, err
	}
	resp.Items = append(resp.Items, items...)
	for _, item := range resp.Items {
		switch item.Status {
		case "planned":
			resp.Planned++
		case "deleted":
			resp.Deleted++
		case "error":
			resp.ErrorCount++
			resp.Errors = append(resp.Errors, item.Error)
		default:
			resp.Kept++
		}
	}
	if resp.ErrorCount > 0 {
		resp.Status = "error"
	} else if resp.DryRun && resp.Planned > 0 {
		resp.Status = "planned"
	}
	return resp, nil
}

func (s *Server) gcStagingFiles(ctx context.Context, cutoff time.Time, dryRun bool) ([]gcItem, error) {
	root, err := filepath.Abs(s.staging)
	if err != nil {
		return nil, err
	}
	var items []gcItem
	err = filepath.WalkDir(root, func(p string, entry os.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		absPath, err := filepath.Abs(p)
		if err != nil {
			return err
		}
		if !pathWithin(root, absPath) {
			return fmt.Errorf("refusing to clean path outside staging directory: %s", absPath)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		item := gcItem{
			Scope:     "staging",
			Path:      absPath,
			Size:      info.Size(),
			UpdatedAt: info.ModTime().UTC(),
		}
		if !info.Mode().IsRegular() {
			item.Status = "kept_non_regular"
			items = append(items, item)
			return nil
		}
		if !info.ModTime().UTC().Before(cutoff) {
			item.Status = "kept_recent"
			items = append(items, item)
			return nil
		}
		if dryRun {
			item.Status = "planned"
			items = append(items, item)
			return nil
		}
		if err := os.Remove(absPath); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				item.Status = "not_found"
			} else {
				item.Status = "error"
				item.Error = err.Error()
			}
		} else {
			item.Status = "deleted"
		}
		items = append(items, item)
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return items, err
}

func pathWithin(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}
