package db

import (
	"context"
	"database/sql"

	"supercdn/internal/model"
)

func (d *DB) CreateSiteDeployment(ctx context.Context, dep model.SiteDeployment) (*model.SiteDeployment, error) {
	now := nowString()
	if dep.Status == "" {
		dep.Status = model.SiteDeploymentQueued
	}
	if dep.Environment == "" {
		dep.Environment = model.SiteEnvironmentPreview
	}
	_, err := d.sql.ExecContext(ctx, `
		INSERT INTO site_deployments(
			id, site_id, environment, status, route_profile, deployment_target, routing_policy, resource_failover, version, active, pinned,
			artifact_object_id, artifact_key, artifact_sha256, artifact_size,
			manifest_object_id, manifest_key, file_count, total_size,
			manifest_json, rules_json, last_error,
			created_at, updated_at, ready_at, activated_at, expires_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		dep.ID, dep.SiteID, dep.Environment, dep.Status, dep.RouteProfile, dep.DeploymentTarget, dep.RoutingPolicy, boolInt(dep.ResourceFailover), dep.Version, boolInt(dep.Active), boolInt(dep.Pinned),
		dep.ArtifactObjectID, dep.ArtifactKey, dep.ArtifactSHA256, dep.ArtifactSize,
		dep.ManifestObjectID, dep.ManifestKey, dep.FileCount, dep.TotalSize,
		dep.ManifestJSON, dep.RulesJSON, dep.LastError,
		now, now, formatTime(dep.ReadyAt), formatTime(dep.ActivatedAt), formatTime(dep.ExpiresAt),
	)
	if err != nil {
		return nil, err
	}
	return d.GetSiteDeployment(ctx, dep.ID)
}

func (d *DB) GetSiteDeployment(ctx context.Context, id string) (*model.SiteDeployment, error) {
	row := d.sql.QueryRowContext(ctx, `
		SELECT id, site_id, environment, status, route_profile, deployment_target, routing_policy, resource_failover, version, active, pinned,
			artifact_object_id, artifact_key, artifact_sha256, artifact_size,
			manifest_object_id, manifest_key, file_count, total_size,
			manifest_json, rules_json, last_error,
			created_at, updated_at, ready_at, activated_at, expires_at
		FROM site_deployments WHERE id = ?`, id)
	return scanSiteDeployment(row)
}

func (d *DB) ListSiteDeployments(ctx context.Context, siteID string, limit int) ([]model.SiteDeployment, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := d.sql.QueryContext(ctx, `
		SELECT id, site_id, environment, status, route_profile, deployment_target, routing_policy, resource_failover, version, active, pinned,
			artifact_object_id, artifact_key, artifact_sha256, artifact_size,
			manifest_object_id, manifest_key, file_count, total_size,
			manifest_json, rules_json, last_error,
			created_at, updated_at, ready_at, activated_at, expires_at
		FROM site_deployments WHERE site_id = ? ORDER BY created_at DESC LIMIT ?`, siteID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.SiteDeployment
	for rows.Next() {
		dep, err := scanSiteDeployment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *dep)
	}
	return out, rows.Err()
}

func (d *DB) ActiveSiteDeployment(ctx context.Context, siteID string) (*model.SiteDeployment, error) {
	row := d.sql.QueryRowContext(ctx, `
		SELECT id, site_id, environment, status, route_profile, deployment_target, routing_policy, resource_failover, version, active, pinned,
			artifact_object_id, artifact_key, artifact_sha256, artifact_size,
			manifest_object_id, manifest_key, file_count, total_size,
			manifest_json, rules_json, last_error,
			created_at, updated_at, ready_at, activated_at, expires_at
		FROM site_deployments
		WHERE site_id = ? AND environment = ? AND active = 1
		ORDER BY activated_at DESC LIMIT 1`, siteID, model.SiteEnvironmentProduction)
	return scanSiteDeployment(row)
}

func (d *DB) UpdateSiteDeploymentStatus(ctx context.Context, id, status, lastErr string) error {
	_, err := d.sql.ExecContext(ctx, `
		UPDATE site_deployments SET status = ?, last_error = ?, updated_at = ? WHERE id = ?`,
		status, lastErr, nowString(), id)
	return err
}

func (d *DB) MarkSiteDeploymentReady(ctx context.Context, dep model.SiteDeployment) (*model.SiteDeployment, error) {
	now := nowString()
	_, err := d.sql.ExecContext(ctx, `
		UPDATE site_deployments SET
			status = ?, artifact_object_id = ?, artifact_key = ?, artifact_sha256 = ?, artifact_size = ?,
			manifest_object_id = ?, manifest_key = ?, file_count = ?, total_size = ?,
			manifest_json = ?, rules_json = ?, last_error = '', ready_at = ?, updated_at = ?
		WHERE id = ?`,
		model.SiteDeploymentReady, dep.ArtifactObjectID, dep.ArtifactKey, dep.ArtifactSHA256, dep.ArtifactSize,
		dep.ManifestObjectID, dep.ManifestKey, dep.FileCount, dep.TotalSize,
		dep.ManifestJSON, dep.RulesJSON, now, now, dep.ID)
	if err != nil {
		return nil, err
	}
	return d.GetSiteDeployment(ctx, dep.ID)
}

func (d *DB) ActivateSiteDeployment(ctx context.Context, siteID, deploymentID string) (*model.SiteDeployment, error) {
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE site_deployments SET active = 0, status = ? WHERE site_id = ? AND environment = ? AND active = 1`,
		model.SiteDeploymentReady, siteID, model.SiteEnvironmentProduction); err != nil {
		return nil, err
	}
	now := nowString()
	res, err := tx.ExecContext(ctx, `
		UPDATE site_deployments SET environment = ?, active = 1, status = ?, activated_at = ?, updated_at = ?
		WHERE id = ? AND site_id = ? AND status IN (?, ?)`,
		model.SiteEnvironmentProduction, model.SiteDeploymentActive, now, now, deploymentID, siteID, model.SiteDeploymentReady, model.SiteDeploymentActive)
	if err != nil {
		return nil, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if affected == 0 {
		return nil, sql.ErrNoRows
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return d.GetSiteDeployment(ctx, deploymentID)
}

func (d *DB) AddSiteDeploymentFile(ctx context.Context, file model.SiteDeploymentFile) error {
	now := nowString()
	_, err := d.sql.ExecContext(ctx, `
		INSERT INTO site_deployment_files(deployment_id, path, object_id, size, sha256, content_type, cache_control, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(deployment_id, path) DO UPDATE SET
			object_id = excluded.object_id,
			size = excluded.size,
			sha256 = excluded.sha256,
			content_type = excluded.content_type,
			cache_control = excluded.cache_control`,
		file.DeploymentID, file.Path, file.ObjectID, file.Size, file.SHA256, file.ContentType, file.CacheControl, now)
	return err
}

func (d *DB) SiteDeploymentFileObject(ctx context.Context, deploymentID, p string) (*model.Object, error) {
	var objectID int64
	err := d.sql.QueryRowContext(ctx, `SELECT object_id FROM site_deployment_files WHERE deployment_id = ? AND path = ?`, deploymentID, p).Scan(&objectID)
	if err != nil {
		return nil, err
	}
	return d.GetObject(ctx, objectID)
}

func (d *DB) ListSiteDeploymentFiles(ctx context.Context, deploymentID string) ([]model.SiteDeploymentFile, error) {
	rows, err := d.sql.QueryContext(ctx, `
		SELECT deployment_id, path, object_id, size, sha256, content_type, cache_control, created_at
		FROM site_deployment_files
		WHERE deployment_id = ?
		ORDER BY path`, deploymentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.SiteDeploymentFile
	for rows.Next() {
		var file model.SiteDeploymentFile
		var created string
		if err := rows.Scan(&file.DeploymentID, &file.Path, &file.ObjectID, &file.Size, &file.SHA256, &file.ContentType, &file.CacheControl, &created); err != nil {
			return nil, err
		}
		file.CreatedAt = parseTime(created)
		out = append(out, file)
	}
	return out, rows.Err()
}

func (d *DB) DeleteSiteDeployment(ctx context.Context, siteID, deploymentID string) error {
	res, err := d.sql.ExecContext(ctx, `DELETE FROM site_deployments WHERE site_id = ? AND id = ? AND active = 0`, siteID, deploymentID)
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

func scanSiteDeployment(row scanner) (*model.SiteDeployment, error) {
	var dep model.SiteDeployment
	var active, pinned, resourceFailover int
	var created, updated, ready, activated, expires string
	err := row.Scan(
		&dep.ID, &dep.SiteID, &dep.Environment, &dep.Status, &dep.RouteProfile, &dep.DeploymentTarget, &dep.RoutingPolicy, &resourceFailover, &dep.Version, &active, &pinned,
		&dep.ArtifactObjectID, &dep.ArtifactKey, &dep.ArtifactSHA256, &dep.ArtifactSize,
		&dep.ManifestObjectID, &dep.ManifestKey, &dep.FileCount, &dep.TotalSize,
		&dep.ManifestJSON, &dep.RulesJSON, &dep.LastError,
		&created, &updated, &ready, &activated, &expires,
	)
	if err != nil {
		return nil, err
	}
	dep.Active = active == 1
	dep.Pinned = pinned == 1
	dep.ResourceFailover = resourceFailover == 1
	if dep.DeploymentTarget == "" {
		dep.DeploymentTarget = model.SiteDeploymentTargetOriginAssisted
	}
	dep.CreatedAt = parseTime(created)
	dep.UpdatedAt = parseTime(updated)
	dep.ReadyAt = parseTime(ready)
	dep.ActivatedAt = parseTime(activated)
	dep.ExpiresAt = parseTime(expires)
	dep.PreviewURL = "/p/" + dep.SiteID + "/" + dep.ID + "/"
	return &dep, nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
