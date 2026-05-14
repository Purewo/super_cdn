package server

import (
	"context"
	"net/http"

	"supercdn/internal/model"
)

func (s *Server) auditMutation(w http.ResponseWriter, r *http.Request, action, resource string) bool {
	if err := s.recordAuditEvent(r.Context(), action, resource); err != nil {
		writeError(w, http.StatusInternalServerError, "audit event write failed: "+err.Error())
		return false
	}
	return true
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
