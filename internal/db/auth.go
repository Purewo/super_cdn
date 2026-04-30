package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"supercdn/internal/model"
)

type UserWithRole struct {
	User        model.User `json:"user"`
	WorkspaceID string     `json:"workspace_id"`
	Role        string     `json:"role"`
}

type TokenPrincipal struct {
	Token model.APIToken
	User  model.User
	Role  string
}

func (d *DB) CreateInvite(ctx context.Context, invite model.Invite) (*model.Invite, error) {
	if invite.WorkspaceID == "" {
		invite.WorkspaceID = model.DefaultWorkspaceID
	}
	now := nowString()
	if invite.ID == "" || invite.TokenHash == "" || invite.Name == "" || invite.Role == "" {
		return nil, errors.New("invite id, token hash, name and role are required")
	}
	if invite.ExpiresAt.IsZero() {
		invite.ExpiresAt = time.Now().UTC().Add(7 * 24 * time.Hour)
	}
	_, err := d.sql.ExecContext(ctx, `
		INSERT INTO invites(id, workspace_id, name, role, token_hash, created_by, expires_at, accepted_at, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, '', ?)`,
		invite.ID, invite.WorkspaceID, invite.Name, invite.Role, invite.TokenHash, invite.CreatedBy, formatTime(invite.ExpiresAt), now)
	if err != nil {
		return nil, err
	}
	return d.GetInvite(ctx, invite.ID)
}

func (d *DB) GetInvite(ctx context.Context, id string) (*model.Invite, error) {
	row := d.sql.QueryRowContext(ctx, `
		SELECT id, workspace_id, name, role, token_hash, created_by, expires_at, accepted_at, created_at
		FROM invites WHERE id = ?`, id)
	return scanInvite(row)
}

func (d *DB) InviteByTokenHash(ctx context.Context, tokenHash string) (*model.Invite, error) {
	row := d.sql.QueryRowContext(ctx, `
		SELECT id, workspace_id, name, role, token_hash, created_by, expires_at, accepted_at, created_at
		FROM invites WHERE token_hash = ?`, tokenHash)
	return scanInvite(row)
}

func (d *DB) AcceptInvite(ctx context.Context, inviteHash string, token model.APIToken) (*model.User, *model.APIToken, error) {
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	var invite model.Invite
	var expires, accepted, created string
	err = tx.QueryRowContext(ctx, `
		SELECT id, workspace_id, name, role, token_hash, created_by, expires_at, accepted_at, created_at
		FROM invites WHERE token_hash = ?`, inviteHash).
		Scan(&invite.ID, &invite.WorkspaceID, &invite.Name, &invite.Role, &invite.TokenHash, &invite.CreatedBy, &expires, &accepted, &created)
	if err != nil {
		return nil, nil, err
	}
	invite.ExpiresAt = parseTime(expires)
	invite.AcceptedAt = parseTime(accepted)
	if !invite.AcceptedAt.IsZero() {
		return nil, nil, errors.New("invite has already been accepted")
	}
	if !invite.ExpiresAt.IsZero() && time.Now().UTC().After(invite.ExpiresAt) {
		return nil, nil, errors.New("invite has expired")
	}
	now := nowString()
	var userID int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE name = ?`, invite.Name).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		res, err := tx.ExecContext(ctx, `INSERT INTO users(name, status, created_at) VALUES(?, ?, ?)`, invite.Name, "active", now)
		if err != nil {
			return nil, nil, err
		}
		userID, err = res.LastInsertId()
		if err != nil {
			return nil, nil, err
		}
	} else if err != nil {
		return nil, nil, err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO workspace_members(workspace_id, user_id, role, created_at)
		VALUES(?, ?, ?, ?)
		ON CONFLICT(workspace_id, user_id) DO UPDATE SET role = excluded.role`,
		invite.WorkspaceID, userID, invite.Role, now)
	if err != nil {
		return nil, nil, err
	}
	if token.ID == "" || token.TokenHash == "" {
		return nil, nil, errors.New("api token id and hash are required")
	}
	if token.WorkspaceID == "" {
		token.WorkspaceID = invite.WorkspaceID
	} else if token.WorkspaceID != invite.WorkspaceID {
		return nil, nil, errors.New("api token workspace does not match invite workspace")
	}
	if token.Name == "" {
		token.Name = "default"
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO api_tokens(id, user_id, workspace_id, name, token_hash, last_used_at, revoked_at, created_at)
		VALUES(?, ?, ?, ?, ?, '', '', ?)`,
		token.ID, userID, token.WorkspaceID, token.Name, token.TokenHash, now)
	if err != nil {
		return nil, nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE invites SET accepted_at = ? WHERE id = ?`, now, invite.ID); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	user, err := d.GetUser(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	saved, err := d.GetAPIToken(ctx, token.ID)
	if err != nil {
		return nil, nil, err
	}
	return user, saved, nil
}

func (d *DB) GetUser(ctx context.Context, id int64) (*model.User, error) {
	var user model.User
	var created string
	if err := d.sql.QueryRowContext(ctx, `SELECT id, name, status, created_at FROM users WHERE id = ?`, id).Scan(&user.ID, &user.Name, &user.Status, &created); err != nil {
		return nil, err
	}
	user.CreatedAt = parseTime(created)
	return &user, nil
}

func (d *DB) UsersInWorkspace(ctx context.Context, workspaceID string) ([]UserWithRole, error) {
	if workspaceID == "" {
		workspaceID = model.DefaultWorkspaceID
	}
	rows, err := d.sql.QueryContext(ctx, `
		SELECT u.id, u.name, u.status, u.created_at, m.workspace_id, m.role
		FROM workspace_members m
		JOIN users u ON u.id = m.user_id
		WHERE m.workspace_id = ?
		ORDER BY u.name`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserWithRole
	for rows.Next() {
		var item UserWithRole
		var created string
		if err := rows.Scan(&item.User.ID, &item.User.Name, &item.User.Status, &created, &item.WorkspaceID, &item.Role); err != nil {
			return nil, err
		}
		item.User.CreatedAt = parseTime(created)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (d *DB) UserInWorkspace(ctx context.Context, userID int64, workspaceID string) (*UserWithRole, error) {
	if workspaceID == "" {
		workspaceID = model.DefaultWorkspaceID
	}
	var item UserWithRole
	var created string
	err := d.sql.QueryRowContext(ctx, `
		SELECT u.id, u.name, u.status, u.created_at, m.workspace_id, m.role
		FROM workspace_members m
		JOIN users u ON u.id = m.user_id
		WHERE m.workspace_id = ? AND m.user_id = ?`,
		workspaceID, userID).Scan(&item.User.ID, &item.User.Name, &item.User.Status, &created, &item.WorkspaceID, &item.Role)
	if err != nil {
		return nil, err
	}
	item.User.CreatedAt = parseTime(created)
	return &item, nil
}

func (d *DB) GetAPIToken(ctx context.Context, id string) (*model.APIToken, error) {
	row := d.sql.QueryRowContext(ctx, `
		SELECT id, user_id, workspace_id, name, token_hash, last_used_at, revoked_at, created_at
		FROM api_tokens WHERE id = ?`, id)
	return scanAPIToken(row)
}

func (d *DB) CreateAPIToken(ctx context.Context, token model.APIToken) (*model.APIToken, error) {
	if token.WorkspaceID == "" {
		token.WorkspaceID = model.DefaultWorkspaceID
	}
	now := nowString()
	_, err := d.sql.ExecContext(ctx, `
		INSERT INTO api_tokens(id, user_id, workspace_id, name, token_hash, last_used_at, revoked_at, created_at)
		VALUES(?, ?, ?, ?, ?, '', '', ?)`,
		token.ID, token.UserID, token.WorkspaceID, token.Name, token.TokenHash, now)
	if err != nil {
		return nil, err
	}
	return d.GetAPIToken(ctx, token.ID)
}

func (d *DB) TokenPrincipalByHash(ctx context.Context, tokenHash string) (*TokenPrincipal, error) {
	row := d.sql.QueryRowContext(ctx, `
		SELECT t.id, t.user_id, t.workspace_id, t.name, t.token_hash, t.last_used_at, t.revoked_at, t.created_at,
			u.id, u.name, u.status, u.created_at, m.role
		FROM api_tokens t
		JOIN users u ON u.id = t.user_id
		JOIN workspace_members m ON m.workspace_id = t.workspace_id AND m.user_id = t.user_id
		WHERE t.token_hash = ? AND t.revoked_at = '' AND u.status = 'active'`, tokenHash)
	var principal TokenPrincipal
	var tokenCreated, tokenLast, tokenRevoked, userCreated string
	if err := row.Scan(
		&principal.Token.ID, &principal.Token.UserID, &principal.Token.WorkspaceID, &principal.Token.Name, &principal.Token.TokenHash, &tokenLast, &tokenRevoked, &tokenCreated,
		&principal.User.ID, &principal.User.Name, &principal.User.Status, &userCreated, &principal.Role,
	); err != nil {
		return nil, err
	}
	principal.Token.CreatedAt = parseTime(tokenCreated)
	principal.Token.LastUsedAt = parseTime(tokenLast)
	principal.Token.RevokedAt = parseTime(tokenRevoked)
	principal.User.CreatedAt = parseTime(userCreated)
	return &principal, nil
}

func (d *DB) TouchAPIToken(ctx context.Context, id string) error {
	_, err := d.sql.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, nowString(), id)
	return err
}

func (d *DB) RevokeAPIToken(ctx context.Context, id string) error {
	res, err := d.sql.ExecContext(ctx, `UPDATE api_tokens SET revoked_at = ? WHERE id = ? AND revoked_at = ''`, nowString(), id)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func scanInvite(row scanner) (*model.Invite, error) {
	var invite model.Invite
	var expires, accepted, created string
	if err := row.Scan(&invite.ID, &invite.WorkspaceID, &invite.Name, &invite.Role, &invite.TokenHash, &invite.CreatedBy, &expires, &accepted, &created); err != nil {
		return nil, err
	}
	invite.ExpiresAt = parseTime(expires)
	invite.AcceptedAt = parseTime(accepted)
	invite.CreatedAt = parseTime(created)
	return &invite, nil
}

func scanAPIToken(row scanner) (*model.APIToken, error) {
	var token model.APIToken
	var last, revoked, created string
	if err := row.Scan(&token.ID, &token.UserID, &token.WorkspaceID, &token.Name, &token.TokenHash, &last, &revoked, &created); err != nil {
		return nil, err
	}
	token.LastUsedAt = parseTime(last)
	token.RevokedAt = parseTime(revoked)
	token.CreatedAt = parseTime(created)
	return &token, nil
}
