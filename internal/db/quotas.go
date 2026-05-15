package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"supercdn/internal/model"
)

type UserQuotaExceededError struct {
	WorkspaceID    string
	UserID         int64
	MaxBytes       int64
	UsedBytes      int64
	RequestedBytes int64
}

func (e *UserQuotaExceededError) Error() string {
	remaining := e.MaxBytes - e.UsedBytes
	if remaining < 0 {
		remaining = 0
	}
	return fmt.Sprintf("user upload quota exceeded: max_bytes=%d used_bytes=%d remaining_bytes=%d upload_bytes=%d", e.MaxBytes, e.UsedBytes, remaining, e.RequestedBytes)
}

type QuotaRequestFilter struct {
	WorkspaceID string
	UserID      int64
	Status      string
	Limit       int
}

func (d *DB) UserUploadQuota(ctx context.Context, workspaceID string, userID int64) (*model.UserUploadQuota, error) {
	if workspaceID == "" {
		workspaceID = model.DefaultWorkspaceID
	}
	row := d.sql.QueryRowContext(ctx, `
		SELECT workspace_id, user_id, max_bytes, used_bytes, approved_by, approved_at, updated_at
		FROM user_upload_quotas WHERE workspace_id = ? AND user_id = ?`, workspaceID, userID)
	quota, err := scanUserUploadQuota(row)
	if errors.Is(err, sql.ErrNoRows) {
		return &model.UserUploadQuota{
			WorkspaceID: workspaceID,
			UserID:      userID,
			MaxBytes:    model.DefaultUserUploadQuotaBytes,
			UsedBytes:   0,
		}, nil
	}
	if err != nil {
		return nil, err
	}
	return quota, nil
}

func (d *DB) ReserveUserUploadQuota(ctx context.Context, workspaceID string, userID, uploadBytes int64) (*model.UserUploadQuota, error) {
	if workspaceID == "" {
		workspaceID = model.DefaultWorkspaceID
	}
	if uploadBytes <= 0 {
		return d.UserUploadQuota(ctx, workspaceID, userID)
	}
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := ensureUserUploadQuotaRow(ctx, tx, workspaceID, userID); err != nil {
		return nil, err
	}
	row := tx.QueryRowContext(ctx, `
		SELECT workspace_id, user_id, max_bytes, used_bytes, approved_by, approved_at, updated_at
		FROM user_upload_quotas WHERE workspace_id = ? AND user_id = ?`, workspaceID, userID)
	quota, err := scanUserUploadQuota(row)
	if err != nil {
		return nil, err
	}
	if uploadBytes > quota.MaxBytes-quota.UsedBytes {
		return nil, &UserQuotaExceededError{
			WorkspaceID:    workspaceID,
			UserID:         userID,
			MaxBytes:       quota.MaxBytes,
			UsedBytes:      quota.UsedBytes,
			RequestedBytes: uploadBytes,
		}
	}
	quota.UsedBytes += uploadBytes
	quota.UpdatedAt = parseTime(nowString())
	if _, err := tx.ExecContext(ctx, `
		UPDATE user_upload_quotas SET used_bytes = ?, updated_at = ?
		WHERE workspace_id = ? AND user_id = ?`,
		quota.UsedBytes, formatTime(quota.UpdatedAt), workspaceID, userID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return d.UserUploadQuota(ctx, workspaceID, userID)
}

func (d *DB) ReleaseUserUploadQuota(ctx context.Context, workspaceID string, userID, uploadBytes int64) error {
	if workspaceID == "" {
		workspaceID = model.DefaultWorkspaceID
	}
	if uploadBytes <= 0 {
		return nil
	}
	_, err := d.sql.ExecContext(ctx, `
		UPDATE user_upload_quotas
		SET used_bytes = CASE WHEN used_bytes > ? THEN used_bytes - ? ELSE 0 END,
			updated_at = ?
		WHERE workspace_id = ? AND user_id = ?`,
		uploadBytes, uploadBytes, nowString(), workspaceID, userID)
	return err
}

func (d *DB) SetUserUploadQuota(ctx context.Context, workspaceID string, userID, maxBytes, approvedBy int64) (*model.UserUploadQuota, error) {
	if workspaceID == "" {
		workspaceID = model.DefaultWorkspaceID
	}
	if maxBytes < 0 {
		return nil, errors.New("max_bytes must be non-negative")
	}
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := userInWorkspaceTx(ctx, tx, workspaceID, userID); err != nil {
		return nil, err
	}
	if err := ensureUserUploadQuotaRow(ctx, tx, workspaceID, userID); err != nil {
		return nil, err
	}
	var usedBytes int64
	if err := tx.QueryRowContext(ctx, `
		SELECT used_bytes FROM user_upload_quotas WHERE workspace_id = ? AND user_id = ?`,
		workspaceID, userID).Scan(&usedBytes); err != nil {
		return nil, err
	}
	if maxBytes < usedBytes {
		return nil, fmt.Errorf("max_bytes must be at least current used_bytes %d", usedBytes)
	}
	now := nowString()
	if _, err := tx.ExecContext(ctx, `
		UPDATE user_upload_quotas
		SET max_bytes = ?, approved_by = ?, approved_at = ?, updated_at = ?
		WHERE workspace_id = ? AND user_id = ?`,
		maxBytes, approvedBy, now, now, workspaceID, userID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return d.UserUploadQuota(ctx, workspaceID, userID)
}

func (d *DB) CreateUserQuotaRequest(ctx context.Context, request model.UserQuotaRequest) (*model.UserQuotaRequest, error) {
	if request.WorkspaceID == "" {
		request.WorkspaceID = model.DefaultWorkspaceID
	}
	if request.ID == "" || request.UserID <= 0 || request.RequestedMaxBytes <= 0 {
		return nil, errors.New("quota request id, user_id and requested_max_bytes are required")
	}
	if request.Status == "" {
		request.Status = model.QuotaRequestPending
	}
	if request.Status != model.QuotaRequestPending {
		return nil, errors.New("new quota requests must be pending")
	}
	now := nowString()
	_, err := d.sql.ExecContext(ctx, `
		INSERT INTO user_quota_requests(
			id, workspace_id, user_id, requested_max_bytes, reason, status,
			decided_by, decided_at, decision_note, created_at, updated_at
		) VALUES(?, ?, ?, ?, ?, ?, 0, '', '', ?, ?)`,
		request.ID, request.WorkspaceID, request.UserID, request.RequestedMaxBytes, strings.TrimSpace(request.Reason), request.Status, now, now)
	if err != nil {
		return nil, err
	}
	return d.GetUserQuotaRequest(ctx, request.ID)
}

func (d *DB) GetUserQuotaRequest(ctx context.Context, id string) (*model.UserQuotaRequest, error) {
	row := d.sql.QueryRowContext(ctx, `
		SELECT id, workspace_id, user_id, requested_max_bytes, reason, status,
			decided_by, decided_at, decision_note, created_at, updated_at
		FROM user_quota_requests WHERE id = ?`, id)
	return scanUserQuotaRequest(row)
}

func (d *DB) UserQuotaRequests(ctx context.Context, filter QuotaRequestFilter) ([]model.UserQuotaRequest, error) {
	if filter.WorkspaceID == "" {
		filter.WorkspaceID = model.DefaultWorkspaceID
	}
	limit := filter.Limit
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	query := `
		SELECT id, workspace_id, user_id, requested_max_bytes, reason, status,
			decided_by, decided_at, decision_note, created_at, updated_at
		FROM user_quota_requests WHERE workspace_id = ?`
	args := []any{filter.WorkspaceID}
	if filter.UserID > 0 {
		query += " AND user_id = ?"
		args = append(args, filter.UserID)
	}
	if status := strings.TrimSpace(filter.Status); status != "" {
		query += " AND status = ?"
		args = append(args, status)
	}
	query += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit)
	rows, err := d.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.UserQuotaRequest
	for rows.Next() {
		request, err := scanUserQuotaRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *request)
	}
	return out, rows.Err()
}

func (d *DB) DecideUserQuotaRequest(ctx context.Context, workspaceID, id, status string, approvedMaxBytes, decidedBy int64, note string) (*model.UserQuotaRequest, *model.UserUploadQuota, error) {
	if workspaceID == "" {
		workspaceID = model.DefaultWorkspaceID
	}
	status = strings.TrimSpace(status)
	if status != model.QuotaRequestApproved && status != model.QuotaRequestRejected {
		return nil, nil, errors.New("status must be approved or rejected")
	}
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	row := tx.QueryRowContext(ctx, `
		SELECT id, workspace_id, user_id, requested_max_bytes, reason, status,
			decided_by, decided_at, decision_note, created_at, updated_at
		FROM user_quota_requests WHERE workspace_id = ? AND id = ?`, workspaceID, id)
	request, err := scanUserQuotaRequest(row)
	if err != nil {
		return nil, nil, err
	}
	if request.Status != model.QuotaRequestPending {
		return nil, nil, fmt.Errorf("quota request is already %s", request.Status)
	}
	if status == model.QuotaRequestApproved {
		if approvedMaxBytes <= 0 {
			approvedMaxBytes = request.RequestedMaxBytes
		}
		if err := ensureUserUploadQuotaRow(ctx, tx, workspaceID, request.UserID); err != nil {
			return nil, nil, err
		}
		var usedBytes int64
		if err := tx.QueryRowContext(ctx, `
			SELECT used_bytes FROM user_upload_quotas WHERE workspace_id = ? AND user_id = ?`,
			workspaceID, request.UserID).Scan(&usedBytes); err != nil {
			return nil, nil, err
		}
		if approvedMaxBytes < usedBytes {
			return nil, nil, fmt.Errorf("approved_max_bytes must be at least current used_bytes %d", usedBytes)
		}
		nowQuota := nowString()
		if _, err := tx.ExecContext(ctx, `
			UPDATE user_upload_quotas
			SET max_bytes = ?, approved_by = ?, approved_at = ?, updated_at = ?
			WHERE workspace_id = ? AND user_id = ?`,
			approvedMaxBytes, decidedBy, nowQuota, nowQuota, workspaceID, request.UserID); err != nil {
			return nil, nil, err
		}
	}
	now := nowString()
	if _, err := tx.ExecContext(ctx, `
		UPDATE user_quota_requests
		SET status = ?, decided_by = ?, decided_at = ?, decision_note = ?, updated_at = ?
		WHERE id = ?`,
		status, decidedBy, now, strings.TrimSpace(note), now, id); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	decided, err := d.GetUserQuotaRequest(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	var quota *model.UserUploadQuota
	if status == model.QuotaRequestApproved {
		quota, err = d.UserUploadQuota(ctx, workspaceID, decided.UserID)
		if err != nil {
			return nil, nil, err
		}
	}
	return decided, quota, nil
}

func ensureUserUploadQuotaRow(ctx context.Context, tx *sql.Tx, workspaceID string, userID int64) error {
	if _, err := userInWorkspaceTx(ctx, tx, workspaceID, userID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO user_upload_quotas(workspace_id, user_id, max_bytes, used_bytes, approved_by, approved_at, updated_at)
		VALUES(?, ?, ?, 0, 0, '', ?)
		ON CONFLICT(workspace_id, user_id) DO NOTHING`,
		workspaceID, userID, model.DefaultUserUploadQuotaBytes, nowString())
	return err
}

func userInWorkspaceTx(ctx context.Context, tx *sql.Tx, workspaceID string, userID int64) (UserWithRole, error) {
	var item UserWithRole
	var created string
	err := tx.QueryRowContext(ctx, `
		SELECT u.id, u.name, u.status, u.created_at, m.workspace_id, m.role
		FROM workspace_members m
		JOIN users u ON u.id = m.user_id
		WHERE m.workspace_id = ? AND m.user_id = ?`,
		workspaceID, userID).Scan(&item.User.ID, &item.User.Name, &item.User.Status, &created, &item.WorkspaceID, &item.Role)
	if err != nil {
		return item, err
	}
	item.User.CreatedAt = parseTime(created)
	return item, nil
}

func scanUserUploadQuota(row scanner) (*model.UserUploadQuota, error) {
	var quota model.UserUploadQuota
	var approvedAt, updatedAt string
	if err := row.Scan(&quota.WorkspaceID, &quota.UserID, &quota.MaxBytes, &quota.UsedBytes, &quota.ApprovedBy, &approvedAt, &updatedAt); err != nil {
		return nil, err
	}
	quota.ApprovedAt = parseTime(approvedAt)
	quota.UpdatedAt = parseTime(updatedAt)
	return &quota, nil
}

func scanUserQuotaRequest(row scanner) (*model.UserQuotaRequest, error) {
	var request model.UserQuotaRequest
	var decidedAt, createdAt, updatedAt string
	if err := row.Scan(
		&request.ID, &request.WorkspaceID, &request.UserID, &request.RequestedMaxBytes, &request.Reason, &request.Status,
		&request.DecidedBy, &decidedAt, &request.DecisionNote, &createdAt, &updatedAt,
	); err != nil {
		return nil, err
	}
	request.DecidedAt = parseTime(decidedAt)
	request.CreatedAt = parseTime(createdAt)
	request.UpdatedAt = parseTime(updatedAt)
	return &request, nil
}
