package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"supercdn/internal/db"
	"supercdn/internal/model"
)

type uploadQuotaView struct {
	WorkspaceID    string `json:"workspace_id"`
	UserID         int64  `json:"user_id"`
	MaxBytes       int64  `json:"max_bytes"`
	UsedBytes      int64  `json:"used_bytes"`
	RemainingBytes int64  `json:"remaining_bytes"`
	Unlimited      bool   `json:"unlimited,omitempty"`
	ApprovedBy     int64  `json:"approved_by,omitempty"`
	ApprovedAt     string `json:"approved_at,omitempty"`
	UpdatedAt      string `json:"updated_at,omitempty"`
}

type quotaReservation struct {
	WorkspaceID string
	UserID      int64
	Bytes       int64
}

func (s *Server) handleGetQuota(w http.ResponseWriter, r *http.Request) {
	principal := currentPrincipal(r.Context())
	if principal.Root {
		writeJSON(w, http.StatusOK, map[string]any{
			"quota":             rootQuotaView(principal),
			"default_max_bytes": model.DefaultUserUploadQuotaBytes,
		})
		return
	}
	quota, err := s.db.UserUploadQuota(r.Context(), principal.WorkspaceID, principal.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"quota":             quotaView(quota),
		"default_max_bytes": model.DefaultUserUploadQuotaBytes,
	})
}

func (s *Server) handleCreateQuotaRequest(w http.ResponseWriter, r *http.Request) {
	principal := currentPrincipal(r.Context())
	if principal.Root {
		writeError(w, http.StatusBadRequest, "root tokens do not need quota requests")
		return
	}
	var req struct {
		RequestedMaxBytes int64  `json:"requested_max_bytes"`
		Reason            string `json:"reason"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.RequestedMaxBytes <= 0 {
		writeError(w, http.StatusBadRequest, "requested_max_bytes must be positive")
		return
	}
	current, err := s.db.UserUploadQuota(r.Context(), principal.WorkspaceID, principal.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.RequestedMaxBytes <= current.MaxBytes {
		writeError(w, http.StatusBadRequest, "requested_max_bytes must be greater than current max_bytes")
		return
	}
	requestID, err := newTokenID("qr_")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	request, err := s.db.CreateUserQuotaRequest(r.Context(), model.UserQuotaRequest{
		ID:                requestID,
		WorkspaceID:       principal.WorkspaceID,
		UserID:            principal.UserID,
		RequestedMaxBytes: req.RequestedMaxBytes,
		Reason:            strings.TrimSpace(req.Reason),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !s.auditMutation(w, r, auditActionQuotaRequestCreate, "quota_request:"+request.ID+";user:"+strconv.FormatInt(principal.UserID, 10)) {
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"request": request})
}

func (s *Server) handleListQuotaRequests(w http.ResponseWriter, r *http.Request) {
	principal := currentPrincipal(r.Context())
	filter := db.QuotaRequestFilter{
		WorkspaceID: firstNonEmpty(strings.TrimSpace(r.URL.Query().Get("workspace_id")), principal.WorkspaceID),
		Status:      strings.TrimSpace(r.URL.Query().Get("status")),
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit <= 0 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		filter.Limit = limit
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("user_id")); raw != "" {
		userID, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || userID <= 0 {
			writeError(w, http.StatusBadRequest, "user_id must be a positive integer")
			return
		}
		filter.UserID = userID
	}
	if !principal.Root {
		if filter.WorkspaceID != principal.WorkspaceID {
			writeError(w, http.StatusForbidden, "cannot read quota requests from another workspace")
			return
		}
		filter.UserID = principal.UserID
	}
	if filter.Status != "" && !validQuotaRequestStatus(filter.Status) {
		writeError(w, http.StatusBadRequest, "status must be pending, approved, or rejected")
		return
	}
	requests, err := s.db.UserQuotaRequests(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": requests})
}

func (s *Server) handleApproveQuotaRequest(w http.ResponseWriter, r *http.Request) {
	s.handleDecideQuotaRequest(w, r, model.QuotaRequestApproved)
}

func (s *Server) handleRejectQuotaRequest(w http.ResponseWriter, r *http.Request) {
	s.handleDecideQuotaRequest(w, r, model.QuotaRequestRejected)
}

func (s *Server) handleDecideQuotaRequest(w http.ResponseWriter, r *http.Request, status string) {
	principal := currentPrincipal(r.Context())
	if !principal.Root {
		writeError(w, http.StatusForbidden, "only root admin tokens can approve quota requests")
		return
	}
	requestID := strings.TrimSpace(r.PathValue("id"))
	if requestID == "" {
		writeError(w, http.StatusBadRequest, "quota request id is required")
		return
	}
	var req struct {
		WorkspaceID      string `json:"workspace_id"`
		ApprovedMaxBytes int64  `json:"approved_max_bytes"`
		Note             string `json:"note"`
	}
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	workspaceID := firstNonEmpty(strings.TrimSpace(req.WorkspaceID), strings.TrimSpace(r.URL.Query().Get("workspace_id")), model.DefaultWorkspaceID)
	request, quota, err := s.db.DecideUserQuotaRequest(r.Context(), workspaceID, requestID, status, req.ApprovedMaxBytes, principal.UserID, req.Note)
	if err != nil {
		statusCode := http.StatusInternalServerError
		if errors.Is(err, sql.ErrNoRows) {
			statusCode = http.StatusNotFound
		} else if strings.Contains(err.Error(), "already") || strings.Contains(err.Error(), "must be") {
			statusCode = http.StatusBadRequest
		}
		writeError(w, statusCode, err.Error())
		return
	}
	action := auditActionQuotaRequestApprove
	if status == model.QuotaRequestRejected {
		action = auditActionQuotaRequestReject
	}
	if !s.auditMutation(w, r, action, "quota_request:"+request.ID+";user:"+strconv.FormatInt(request.UserID, 10)) {
		return
	}
	resp := map[string]any{"request": request}
	if quota != nil {
		resp["quota"] = quotaView(quota)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleSetUserQuota(w http.ResponseWriter, r *http.Request) {
	principal := currentPrincipal(r.Context())
	if !principal.Root {
		writeError(w, http.StatusForbidden, "only root admin tokens can set user quota")
		return
	}
	userID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || userID <= 0 {
		writeError(w, http.StatusBadRequest, "user id is required")
		return
	}
	var req struct {
		WorkspaceID string `json:"workspace_id"`
		MaxBytes    int64  `json:"max_bytes"`
		Note        string `json:"note"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.MaxBytes <= 0 {
		writeError(w, http.StatusBadRequest, "max_bytes must be positive")
		return
	}
	workspaceID := firstNonEmpty(strings.TrimSpace(req.WorkspaceID), model.DefaultWorkspaceID)
	quota, err := s.db.SetUserUploadQuota(r.Context(), workspaceID, userID, req.MaxBytes, principal.UserID)
	if err != nil {
		statusCode := http.StatusInternalServerError
		if errors.Is(err, sql.ErrNoRows) {
			statusCode = http.StatusNotFound
		} else if strings.Contains(err.Error(), "must be") {
			statusCode = http.StatusBadRequest
		}
		writeError(w, statusCode, err.Error())
		return
	}
	if !s.auditMutation(w, r, auditActionQuotaSet, "user:"+strconv.FormatInt(userID, 10)+";max_bytes:"+strconv.FormatInt(req.MaxBytes, 10)) {
		return
	}
	_ = req.Note
	writeJSON(w, http.StatusOK, map[string]any{"quota": quotaView(quota)})
}

func (s *Server) checkUserUploadQuota(ctx context.Context, uploadBytes int64) (*model.UserUploadQuota, error) {
	principal := currentPrincipal(ctx)
	if principal.Root || principal.UserID == 0 || uploadBytes <= 0 {
		return nil, nil
	}
	quota, err := s.db.UserUploadQuota(ctx, principal.WorkspaceID, principal.UserID)
	if err != nil {
		return nil, err
	}
	if uploadBytes > quota.MaxBytes-quota.UsedBytes {
		return nil, &db.UserQuotaExceededError{
			WorkspaceID:    principal.WorkspaceID,
			UserID:         principal.UserID,
			MaxBytes:       quota.MaxBytes,
			UsedBytes:      quota.UsedBytes,
			RequestedBytes: uploadBytes,
		}
	}
	return quota, nil
}

func (s *Server) reserveUploadQuota(ctx context.Context, uploadBytes int64) (*quotaReservation, *model.UserUploadQuota, error) {
	principal := currentPrincipal(ctx)
	if principal.Root || principal.UserID == 0 || uploadBytes <= 0 {
		return nil, nil, nil
	}
	quota, err := s.db.ReserveUserUploadQuota(ctx, principal.WorkspaceID, principal.UserID, uploadBytes)
	if err != nil {
		return nil, nil, err
	}
	return &quotaReservation{WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, Bytes: uploadBytes}, quota, nil
}

func (s *Server) releaseUploadQuota(ctx context.Context, reservation *quotaReservation) {
	if reservation == nil || reservation.Bytes <= 0 {
		return
	}
	if err := s.db.ReleaseUserUploadQuota(context.WithoutCancel(ctx), reservation.WorkspaceID, reservation.UserID, reservation.Bytes); err != nil {
		s.logger.Warn("upload quota release failed", "workspace_id", reservation.WorkspaceID, "user_id", reservation.UserID, "bytes", reservation.Bytes, "error", err)
	}
}

func writeQuotaError(w http.ResponseWriter, err error) bool {
	var quotaErr *db.UserQuotaExceededError
	if !errors.As(err, &quotaErr) {
		return false
	}
	remaining := quotaErr.MaxBytes - quotaErr.UsedBytes
	if remaining < 0 {
		remaining = 0
	}
	writeJSON(w, http.StatusForbidden, map[string]any{
		"error":           quotaErr.Error(),
		"code":            "user_upload_quota_exceeded",
		"workspace_id":    quotaErr.WorkspaceID,
		"user_id":         quotaErr.UserID,
		"max_bytes":       quotaErr.MaxBytes,
		"used_bytes":      quotaErr.UsedBytes,
		"remaining_bytes": remaining,
		"upload_bytes":    quotaErr.RequestedBytes,
	})
	return true
}

func quotaView(quota *model.UserUploadQuota) uploadQuotaView {
	if quota == nil {
		return uploadQuotaView{}
	}
	remaining := quota.MaxBytes - quota.UsedBytes
	if remaining < 0 {
		remaining = 0
	}
	view := uploadQuotaView{
		WorkspaceID:    quota.WorkspaceID,
		UserID:         quota.UserID,
		MaxBytes:       quota.MaxBytes,
		UsedBytes:      quota.UsedBytes,
		RemainingBytes: remaining,
		ApprovedBy:     quota.ApprovedBy,
	}
	if !quota.ApprovedAt.IsZero() {
		view.ApprovedAt = quota.ApprovedAt.Format("2006-01-02T15:04:05Z07:00")
	}
	if !quota.UpdatedAt.IsZero() {
		view.UpdatedAt = quota.UpdatedAt.Format("2006-01-02T15:04:05Z07:00")
	}
	return view
}

func rootQuotaView(principal authPrincipal) uploadQuotaView {
	return uploadQuotaView{
		WorkspaceID: principal.WorkspaceID,
		UserID:      principal.UserID,
		Unlimited:   true,
	}
}

func validQuotaRequestStatus(status string) bool {
	switch status {
	case model.QuotaRequestPending, model.QuotaRequestApproved, model.QuotaRequestRejected:
		return true
	default:
		return false
	}
}
