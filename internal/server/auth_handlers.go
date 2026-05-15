package server

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"supercdn/internal/model"
)

func (s *Server) handleCreateInvite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name             string `json:"name"`
		Role             string `json:"role"`
		ExpiresInSeconds int64  `json:"expires_in_seconds"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Role = strings.ToLower(strings.TrimSpace(req.Role))
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Role == "" {
		req.Role = model.RoleViewer
	}
	if !validRole(req.Role) {
		writeError(w, http.StatusBadRequest, "role must be owner, maintainer, or viewer")
		return
	}
	if req.ExpiresInSeconds <= 0 {
		req.ExpiresInSeconds = int64((7 * 24 * time.Hour).Seconds())
	}
	principal := currentPrincipal(r.Context())
	inviteToken, err := newSecret("sci_")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	inviteID, err := newTokenID("inv_")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	invite, err := s.db.CreateInvite(r.Context(), model.Invite{
		ID:          inviteID,
		WorkspaceID: principal.WorkspaceID,
		Name:        req.Name,
		Role:        req.Role,
		TokenHash:   hashSecret(inviteToken),
		CreatedBy:   principal.UserID,
		ExpiresAt:   time.Now().UTC().Add(time.Duration(req.ExpiresInSeconds) * time.Second),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !s.auditMutation(w, r, auditActionAuthInviteCreate, "invite:"+invite.ID) {
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"invite":       invite,
		"invite_token": inviteToken,
	})
}

func (s *Server) handleAcceptInvite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		InviteToken string `json:"invite_token"`
		TokenName   string `json:"token_name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.InviteToken = strings.TrimSpace(req.InviteToken)
	if req.InviteToken == "" {
		writeError(w, http.StatusBadRequest, "invite_token is required")
		return
	}
	apiToken, err := newSecret("sct_")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	tokenID, err := newTokenID("tok_")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	user, token, err := s.db.AcceptInvite(r.Context(), hashSecret(req.InviteToken), model.APIToken{
		ID:        tokenID,
		Name:      firstNonEmpty(strings.TrimSpace(req.TokenName), "default"),
		TokenHash: hashSecret(apiToken),
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := s.db.CreateAuditEvent(r.Context(), model.AuditEvent{
		WorkspaceID: token.WorkspaceID,
		UserID:      user.ID,
		Action:      auditActionAuthInviteAccept,
		Resource:    fmt.Sprintf("user:%d;token:%s", user.ID, token.ID),
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "audit event write failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"user":      user,
		"api_token": apiToken,
		"token":     token,
	})
}

func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	principal := currentPrincipal(r.Context())
	writeJSON(w, http.StatusOK, principal)
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	principal := currentPrincipal(r.Context())
	users, err := s.db.UsersInWorkspace(r.Context(), principal.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (s *Server) handleCreateUserToken(w http.ResponseWriter, r *http.Request) {
	principal := currentPrincipal(r.Context())
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "user id is required")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if _, err := s.db.UserInWorkspace(r.Context(), id, principal.WorkspaceID); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		writeError(w, status, "user not found")
		return
	}
	apiToken, err := newSecret("sct_")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	tokenID, err := newTokenID("tok_")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	token, err := s.db.CreateAPIToken(r.Context(), model.APIToken{
		ID:          tokenID,
		UserID:      id,
		WorkspaceID: principal.WorkspaceID,
		Name:        firstNonEmpty(strings.TrimSpace(req.Name), "manual"),
		TokenHash:   hashSecret(apiToken),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !s.auditMutation(w, r, auditActionAuthTokenCreate, "token:"+token.ID) {
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"api_token": apiToken,
		"token":     token,
	})
}

func (s *Server) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	principal := currentPrincipal(r.Context())
	tokenID := strings.TrimSpace(r.PathValue("id"))
	if tokenID == "" {
		writeError(w, http.StatusBadRequest, "token id is required")
		return
	}
	if !principal.Root {
		token, err := s.db.GetAPIToken(r.Context(), tokenID)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, sql.ErrNoRows) {
				status = http.StatusNotFound
			}
			writeError(w, status, "token not found")
			return
		}
		if token.WorkspaceID != principal.WorkspaceID {
			writeError(w, http.StatusNotFound, "token not found")
			return
		}
		if principal.Role != model.RoleOwner && token.UserID != principal.UserID {
			writeError(w, http.StatusForbidden, "insufficient permissions")
			return
		}
	}
	if err := s.db.RevokeAPIToken(r.Context(), tokenID); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	if !s.auditMutation(w, r, auditActionAuthTokenRevoke, "token:"+tokenID) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "token_id": tokenID})
}
