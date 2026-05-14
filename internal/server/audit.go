package server

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"supercdn/internal/db"
	"supercdn/internal/model"
)

type auditEventsResponse struct {
	Events []model.AuditEvent `json:"events"`
	Limit  int                `json:"limit"`
}

func (s *Server) handleAuditEvents(w http.ResponseWriter, r *http.Request) {
	principal := currentPrincipal(r.Context())
	workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
	if !principal.Root {
		if workspaceID != "" && workspaceID != principal.WorkspaceID {
			writeError(w, http.StatusForbidden, "cannot read audit events from another workspace")
			return
		}
		workspaceID = principal.WorkspaceID
	}
	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = parsed
	}
	events, err := s.db.AuditEventsFiltered(r.Context(), db.AuditEventFilter{
		WorkspaceID:      workspaceID,
		Action:           r.URL.Query().Get("action"),
		ResourceContains: r.URL.Query().Get("resource"),
		Limit:            limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, auditEventsResponse{Events: events, Limit: limit})
}

func (s *Server) auditMutation(w http.ResponseWriter, r *http.Request, action, resource string) bool {
	if err := s.recordAuditEvent(r.Context(), action, resource); err != nil {
		writeError(w, http.StatusInternalServerError, "audit event write failed: "+err.Error())
		return false
	}
	return true
}

func (s *Server) auditRejectedMutation(r *http.Request, action, resource string) {
	if err := s.recordAuditEvent(r.Context(), action, resource); err != nil {
		s.logger.Warn("audit event write failed for rejected mutation", "action", action, "resource", resource, "error", err)
	}
}

func (s *Server) recordAuditEvent(ctx context.Context, action, resource string) error {
	principal := currentPrincipal(ctx)
	workspaceID := principal.WorkspaceID
	if workspaceID == "" {
		workspaceID = model.DefaultWorkspaceID
	}
	_, err := s.db.CreateAuditEvent(ctx, model.AuditEvent{
		WorkspaceID: workspaceID,
		UserID:      principal.UserID,
		Action:      action,
		Resource:    resource,
	})
	return err
}
