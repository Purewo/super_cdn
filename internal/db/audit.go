package db

import (
	"context"
	"errors"

	"supercdn/internal/model"
)

func (d *DB) CreateAuditEvent(ctx context.Context, event model.AuditEvent) (*model.AuditEvent, error) {
	if event.WorkspaceID == "" {
		event.WorkspaceID = model.DefaultWorkspaceID
	}
	if event.Action == "" || event.Resource == "" {
		return nil, errors.New("audit action and resource are required")
	}
	now := nowString()
	res, err := d.sql.ExecContext(ctx, `
		INSERT INTO audit_events(workspace_id, user_id, action, resource, created_at)
		VALUES(?, ?, ?, ?, ?)`,
		event.WorkspaceID, event.UserID, event.Action, event.Resource, now)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return d.GetAuditEvent(ctx, id)
}

func (d *DB) GetAuditEvent(ctx context.Context, id int64) (*model.AuditEvent, error) {
	row := d.sql.QueryRowContext(ctx, `
		SELECT id, workspace_id, user_id, action, resource, created_at
		FROM audit_events WHERE id = ?`, id)
	return scanAuditEvent(row)
}

func (d *DB) AuditEvents(ctx context.Context, workspaceID string, limit int) ([]model.AuditEvent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	query := `
		SELECT id, workspace_id, user_id, action, resource, created_at
		FROM audit_events`
	args := []any{}
	if workspaceID != "" {
		query += ` WHERE workspace_id = ?`
		args = append(args, workspaceID)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := d.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.AuditEvent
	for rows.Next() {
		event, err := scanAuditEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *event)
	}
	return out, rows.Err()
}

func scanAuditEvent(row scanner) (*model.AuditEvent, error) {
	var event model.AuditEvent
	var created string
	if err := row.Scan(&event.ID, &event.WorkspaceID, &event.UserID, &event.Action, &event.Resource, &created); err != nil {
		return nil, err
	}
	event.CreatedAt = parseTime(created)
	return &event, nil
}
