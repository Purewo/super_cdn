package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"supercdn/internal/model"
)

type DB struct {
	sql *sql.DB
}

func Open(ctx context.Context, path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(1)
	db := &DB{sql: conn}
	if err := db.migrate(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return db, nil
}

func (d *DB) Close() error { return d.sql.Close() }

func (d *DB) SQL() *sql.DB { return d.sql }

func (d *DB) migrate(ctx context.Context) error {
	stmts := []string{
		`PRAGMA journal_mode = WAL;`,
		`PRAGMA foreign_keys = ON;`,
		`CREATE TABLE IF NOT EXISTS workspaces (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			created_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			status TEXT NOT NULL,
			created_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS workspace_members (
			workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			role TEXT NOT NULL,
			created_at TEXT NOT NULL,
			PRIMARY KEY(workspace_id, user_id)
		);`,
		`CREATE TABLE IF NOT EXISTS api_tokens (
			id TEXT PRIMARY KEY,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			token_hash TEXT NOT NULL UNIQUE,
			last_used_at TEXT NOT NULL DEFAULT '',
			revoked_at TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS invites (
			id TEXT PRIMARY KEY,
			workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			role TEXT NOT NULL,
			token_hash TEXT NOT NULL UNIQUE,
			created_by INTEGER NOT NULL DEFAULT 0,
			expires_at TEXT NOT NULL,
			accepted_at TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS audit_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			workspace_id TEXT NOT NULL,
			user_id INTEGER NOT NULL DEFAULT 0,
			action TEXT NOT NULL,
			resource TEXT NOT NULL,
			created_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS projects (
			id TEXT PRIMARY KEY,
			workspace_id TEXT NOT NULL DEFAULT 'default',
			created_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS objects (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id TEXT NOT NULL,
			path TEXT NOT NULL,
			key TEXT NOT NULL,
			route_profile TEXT NOT NULL,
			size INTEGER NOT NULL,
			sha256 TEXT NOT NULL,
			content_type TEXT NOT NULL,
			cache_control TEXT NOT NULL,
			primary_target TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			UNIQUE(project_id, path)
		);`,
		`CREATE TABLE IF NOT EXISTS replicas (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			object_id INTEGER NOT NULL REFERENCES objects(id) ON DELETE CASCADE,
			target TEXT NOT NULL,
			status TEXT NOT NULL,
			locator TEXT NOT NULL,
			last_error TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			UNIQUE(object_id, target)
		);`,
		`CREATE TABLE IF NOT EXISTS object_ipfs_pins (
			object_id INTEGER NOT NULL REFERENCES objects(id) ON DELETE CASCADE,
			target TEXT NOT NULL,
			provider TEXT NOT NULL,
			cid TEXT NOT NULL,
			gateway_url TEXT NOT NULL DEFAULT '',
			locator TEXT NOT NULL DEFAULT '',
			pin_status TEXT NOT NULL DEFAULT '',
			provider_pin_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY(object_id, target)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_object_ipfs_pins_cid ON object_ipfs_pins(cid);`,
		`CREATE TABLE IF NOT EXISTS jobs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			type TEXT NOT NULL,
			status TEXT NOT NULL,
			payload TEXT NOT NULL,
			attempts INTEGER NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL,
			result TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS resource_library_health (
			library TEXT NOT NULL,
			binding TEXT NOT NULL,
			binding_path TEXT NOT NULL,
			target TEXT NOT NULL,
			target_type TEXT NOT NULL,
			status TEXT NOT NULL,
			check_mode TEXT NOT NULL,
			list_latency_ms INTEGER NOT NULL DEFAULT 0,
			write_latency_ms INTEGER NOT NULL DEFAULT 0,
			read_latency_ms INTEGER NOT NULL DEFAULT 0,
			delete_latency_ms INTEGER NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL,
			consecutive_failures INTEGER NOT NULL DEFAULT 0,
			last_checked_at TEXT NOT NULL,
			last_success_at TEXT NOT NULL,
			last_failure_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY(library, binding)
		);`,
		`CREATE TABLE IF NOT EXISTS asset_buckets (
			slug TEXT PRIMARY KEY,
			workspace_id TEXT NOT NULL DEFAULT 'default',
			name TEXT NOT NULL,
			description TEXT NOT NULL,
			route_profile TEXT NOT NULL,
			routing_policy TEXT NOT NULL DEFAULT '',
			allowed_types TEXT NOT NULL,
			max_capacity_bytes INTEGER NOT NULL DEFAULT 0,
			max_file_size_bytes INTEGER NOT NULL DEFAULT 0,
			default_cache_control TEXT NOT NULL,
			status TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS asset_bucket_objects (
			bucket_slug TEXT NOT NULL REFERENCES asset_buckets(slug) ON DELETE CASCADE,
			logical_path TEXT NOT NULL,
			object_id INTEGER NOT NULL REFERENCES objects(id) ON DELETE CASCADE,
			asset_type TEXT NOT NULL,
			physical_key TEXT NOT NULL,
			size INTEGER NOT NULL,
			sha256 TEXT NOT NULL,
			content_type TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY(bucket_slug, logical_path)
		);`,
		`CREATE TABLE IF NOT EXISTS sites (
			id TEXT PRIMARY KEY,
			workspace_id TEXT NOT NULL DEFAULT 'default',
			name TEXT NOT NULL DEFAULT '',
			mode TEXT NOT NULL,
			route_profile TEXT NOT NULL,
			deployment_target TEXT NOT NULL DEFAULT '',
			routing_policy TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active',
			created_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS site_deployments (
			id TEXT PRIMARY KEY,
			site_id TEXT NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
			environment TEXT NOT NULL,
			status TEXT NOT NULL,
			route_profile TEXT NOT NULL,
			deployment_target TEXT NOT NULL DEFAULT '',
			routing_policy TEXT NOT NULL DEFAULT '',
			resource_failover INTEGER NOT NULL DEFAULT 0,
			version TEXT NOT NULL,
			active INTEGER NOT NULL DEFAULT 0,
			pinned INTEGER NOT NULL DEFAULT 0,
			artifact_object_id INTEGER NOT NULL DEFAULT 0,
			artifact_key TEXT NOT NULL DEFAULT '',
			artifact_sha256 TEXT NOT NULL DEFAULT '',
			artifact_size INTEGER NOT NULL DEFAULT 0,
			manifest_object_id INTEGER NOT NULL DEFAULT 0,
			manifest_key TEXT NOT NULL DEFAULT '',
			file_count INTEGER NOT NULL DEFAULT 0,
			total_size INTEGER NOT NULL DEFAULT 0,
			manifest_json TEXT NOT NULL DEFAULT '',
			rules_json TEXT NOT NULL DEFAULT '',
			last_error TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			ready_at TEXT NOT NULL DEFAULT '',
			activated_at TEXT NOT NULL DEFAULT '',
			expires_at TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE TABLE IF NOT EXISTS site_deployment_files (
			deployment_id TEXT NOT NULL REFERENCES site_deployments(id) ON DELETE CASCADE,
			path TEXT NOT NULL,
			object_id INTEGER NOT NULL REFERENCES objects(id) ON DELETE CASCADE,
			size INTEGER NOT NULL,
			sha256 TEXT NOT NULL,
			content_type TEXT NOT NULL,
			cache_control TEXT NOT NULL,
			created_at TEXT NOT NULL,
			PRIMARY KEY(deployment_id, path)
		);`,
		`CREATE TABLE IF NOT EXISTS domains (
			host TEXT PRIMARY KEY,
			site_id TEXT NOT NULL REFERENCES sites(id) ON DELETE CASCADE
		);`,
	}
	for _, stmt := range stmts {
		if _, err := d.sql.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	if _, err := d.sql.ExecContext(ctx, `INSERT INTO workspaces(id, name, created_at) VALUES(?, ?, ?) ON CONFLICT(id) DO NOTHING`, model.DefaultWorkspaceID, "Default", nowString()); err != nil {
		return err
	}
	if err := d.ensureColumn(ctx, "projects", "workspace_id", "TEXT NOT NULL DEFAULT 'default'"); err != nil {
		return err
	}
	if err := d.ensureColumn(ctx, "sites", "workspace_id", "TEXT NOT NULL DEFAULT 'default'"); err != nil {
		return err
	}
	if err := d.ensureColumn(ctx, "asset_buckets", "workspace_id", "TEXT NOT NULL DEFAULT 'default'"); err != nil {
		return err
	}
	if err := d.ensureColumn(ctx, "jobs", "result", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := d.ensureColumn(ctx, "sites", "name", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := d.ensureColumn(ctx, "sites", "deployment_target", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := d.ensureColumn(ctx, "sites", "routing_policy", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := d.ensureColumn(ctx, "sites", "status", "TEXT NOT NULL DEFAULT 'active'"); err != nil {
		return err
	}
	if err := d.ensureColumn(ctx, "site_deployments", "deployment_target", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := d.ensureColumn(ctx, "site_deployments", "routing_policy", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := d.ensureColumn(ctx, "site_deployments", "resource_failover", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := d.ensureColumn(ctx, "asset_buckets", "routing_policy", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	return nil
}

func (d *DB) ensureColumn(ctx context.Context, table, column, definition string) error {
	rows, err := d.sql.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = d.sql.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, definition))
	return err
}

func (d *DB) CreateProject(ctx context.Context, id string) (*model.Project, error) {
	return d.CreateProjectInWorkspace(ctx, id, model.DefaultWorkspaceID)
}

func (d *DB) CreateProjectInWorkspace(ctx context.Context, id, workspaceID string) (*model.Project, error) {
	if workspaceID == "" {
		workspaceID = model.DefaultWorkspaceID
	}
	if existing, err := d.GetProject(ctx, id); err == nil {
		if existing.WorkspaceID != workspaceID {
			return nil, fmt.Errorf("project %q belongs to another workspace", id)
		}
		return existing, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	now := nowString()
	_, err := d.sql.ExecContext(ctx, `INSERT INTO projects(id, workspace_id, created_at) VALUES(?, ?, ?) ON CONFLICT(id) DO NOTHING`, id, workspaceID, now)
	if err != nil {
		return nil, err
	}
	return d.GetProject(ctx, id)
}

func (d *DB) GetProject(ctx context.Context, id string) (*model.Project, error) {
	var p model.Project
	var created string
	err := d.sql.QueryRowContext(ctx, `SELECT id, workspace_id, created_at FROM projects WHERE id = ?`, id).Scan(&p.ID, &p.WorkspaceID, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, sql.ErrNoRows
	}
	if err != nil {
		return nil, err
	}
	p.CreatedAt = parseTime(created)
	return &p, nil
}

func (d *DB) SaveObject(ctx context.Context, obj model.Object) (*model.Object, error) {
	now := nowString()
	err := d.sql.QueryRowContext(ctx, `
		INSERT INTO objects(project_id, path, key, route_profile, size, sha256, content_type, cache_control, primary_target, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(project_id, path) DO UPDATE SET
			key = excluded.key,
			route_profile = excluded.route_profile,
			size = excluded.size,
			sha256 = excluded.sha256,
			content_type = excluded.content_type,
			cache_control = excluded.cache_control,
			primary_target = excluded.primary_target,
			updated_at = excluded.updated_at
		RETURNING id`,
		obj.ProjectID, obj.Path, obj.Key, obj.RouteProfile, obj.Size, obj.SHA256, obj.ContentType, obj.CacheControl, obj.PrimaryTarget, now, now,
	).Scan(&obj.ID)
	if err != nil {
		return nil, err
	}
	return d.GetObject(ctx, obj.ID)
}

func (d *DB) GetObject(ctx context.Context, id int64) (*model.Object, error) {
	var obj model.Object
	var created, updated string
	err := d.sql.QueryRowContext(ctx, `
		SELECT id, project_id, path, key, route_profile, size, sha256, content_type, cache_control, primary_target, created_at, updated_at
		FROM objects WHERE id = ?`, id).
		Scan(&obj.ID, &obj.ProjectID, &obj.Path, &obj.Key, &obj.RouteProfile, &obj.Size, &obj.SHA256, &obj.ContentType, &obj.CacheControl, &obj.PrimaryTarget, &created, &updated)
	if err != nil {
		return nil, err
	}
	obj.CreatedAt = parseTime(created)
	obj.UpdatedAt = parseTime(updated)
	return &obj, nil
}

func (d *DB) GetObjectByProjectPath(ctx context.Context, projectID, objectPath string) (*model.Object, error) {
	var id int64
	err := d.sql.QueryRowContext(ctx, `SELECT id FROM objects WHERE project_id = ? AND path = ?`, projectID, objectPath).Scan(&id)
	if err != nil {
		return nil, err
	}
	return d.GetObject(ctx, id)
}

func (d *DB) DeleteObject(ctx context.Context, id int64) error {
	_, err := d.sql.ExecContext(ctx, `DELETE FROM objects WHERE id = ?`, id)
	return err
}

func (d *DB) DeleteProject(ctx context.Context, id string) error {
	_, err := d.sql.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id)
	return err
}

func (d *DB) UpsertReplica(ctx context.Context, objectID int64, target, status, locator, lastErr string) (*model.Replica, error) {
	now := nowString()
	var id int64
	err := d.sql.QueryRowContext(ctx, `
		INSERT INTO replicas(object_id, target, status, locator, last_error, updated_at)
		VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(object_id, target) DO UPDATE SET
			status = excluded.status,
			locator = excluded.locator,
			last_error = excluded.last_error,
			updated_at = excluded.updated_at
		RETURNING id`, objectID, target, status, locator, lastErr, now).Scan(&id)
	if err != nil {
		return nil, err
	}
	return d.GetReplica(ctx, id)
}

func (d *DB) GetReplica(ctx context.Context, id int64) (*model.Replica, error) {
	var r model.Replica
	var updated string
	err := d.sql.QueryRowContext(ctx, `
		SELECT id, object_id, target, status, locator, last_error, updated_at FROM replicas WHERE id = ?`, id).
		Scan(&r.ID, &r.ObjectID, &r.Target, &r.Status, &r.Locator, &r.LastError, &updated)
	if err != nil {
		return nil, err
	}
	r.UpdatedAt = parseTime(updated)
	return &r, nil
}

func (d *DB) Replicas(ctx context.Context, objectID int64) ([]model.Replica, error) {
	rows, err := d.sql.QueryContext(ctx, `
		SELECT id, object_id, target, status, locator, last_error, updated_at
		FROM replicas WHERE object_id = ? ORDER BY id`, objectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var replicas []model.Replica
	for rows.Next() {
		var r model.Replica
		var updated string
		if err := rows.Scan(&r.ID, &r.ObjectID, &r.Target, &r.Status, &r.Locator, &r.LastError, &updated); err != nil {
			return nil, err
		}
		r.UpdatedAt = parseTime(updated)
		replicas = append(replicas, r)
	}
	return replicas, rows.Err()
}

func (d *DB) UpsertIPFSPin(ctx context.Context, pin model.IPFSPin) (*model.IPFSPin, error) {
	if pin.ObjectID == 0 {
		return nil, errors.New("ipfs pin object_id is required")
	}
	if strings.TrimSpace(pin.Target) == "" {
		return nil, errors.New("ipfs pin target is required")
	}
	if strings.TrimSpace(pin.CID) == "" {
		return nil, errors.New("ipfs pin cid is required")
	}
	now := nowString()
	var created string
	_ = d.sql.QueryRowContext(ctx, `
		SELECT created_at FROM object_ipfs_pins WHERE object_id = ? AND target = ?`,
		pin.ObjectID, pin.Target).Scan(&created)
	if created == "" {
		created = now
	}
	_, err := d.sql.ExecContext(ctx, `
		INSERT INTO object_ipfs_pins(object_id, target, provider, cid, gateway_url, locator, pin_status, provider_pin_id, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(object_id, target) DO UPDATE SET
			provider = excluded.provider,
			cid = excluded.cid,
			gateway_url = excluded.gateway_url,
			locator = excluded.locator,
			pin_status = excluded.pin_status,
			provider_pin_id = excluded.provider_pin_id,
			updated_at = excluded.updated_at`,
		pin.ObjectID, pin.Target, pin.Provider, pin.CID, pin.GatewayURL, pin.Locator, pin.PinStatus, pin.ProviderPinID, created, now)
	if err != nil {
		return nil, err
	}
	return d.GetIPFSPin(ctx, pin.ObjectID, pin.Target)
}

func (d *DB) GetIPFSPin(ctx context.Context, objectID int64, target string) (*model.IPFSPin, error) {
	row := d.sql.QueryRowContext(ctx, `
		SELECT object_id, target, provider, cid, gateway_url, locator, pin_status, provider_pin_id, created_at, updated_at
		FROM object_ipfs_pins WHERE object_id = ? AND target = ?`, objectID, target)
	return scanIPFSPin(row)
}

func (d *DB) IPFSPins(ctx context.Context, objectID int64) ([]model.IPFSPin, error) {
	rows, err := d.sql.QueryContext(ctx, `
		SELECT object_id, target, provider, cid, gateway_url, locator, pin_status, provider_pin_id, created_at, updated_at
		FROM object_ipfs_pins WHERE object_id = ? ORDER BY target`, objectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var pins []model.IPFSPin
	for rows.Next() {
		pin, err := scanIPFSPin(rows)
		if err != nil {
			return nil, err
		}
		pins = append(pins, *pin)
	}
	return pins, rows.Err()
}

func (d *DB) IPFSPinsByObjectIDs(ctx context.Context, objectIDs []int64) (map[int64][]model.IPFSPin, error) {
	result := make(map[int64][]model.IPFSPin)
	for _, objectID := range objectIDs {
		if objectID == 0 {
			continue
		}
		if _, ok := result[objectID]; ok {
			continue
		}
		pins, err := d.IPFSPins(ctx, objectID)
		if err != nil {
			return nil, err
		}
		result[objectID] = pins
	}
	return result, nil
}

func (d *DB) DeleteIPFSPin(ctx context.Context, objectID int64, target string) error {
	_, err := d.sql.ExecContext(ctx, `DELETE FROM object_ipfs_pins WHERE object_id = ? AND target = ?`, objectID, target)
	return err
}

func (d *DB) IPFSPinReferenceCount(ctx context.Context, target, cid string, excludeObjectID int64) (int64, error) {
	var count int64
	err := d.sql.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM object_ipfs_pins
		WHERE target = ? AND cid = ? AND object_id <> ?`, target, cid, excludeObjectID).Scan(&count)
	return count, err
}

func (d *DB) IPFSPinProviderPinIDReferenceCount(ctx context.Context, target, providerPinID string, excludeObjectID int64) (int64, error) {
	var count int64
	err := d.sql.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM object_ipfs_pins
		WHERE target = ? AND provider_pin_id = ? AND object_id <> ?`, target, providerPinID, excludeObjectID).Scan(&count)
	return count, err
}

func (d *DB) CreateJob(ctx context.Context, typ, payload string) (*model.Job, error) {
	now := nowString()
	res, err := d.sql.ExecContext(ctx, `
		INSERT INTO jobs(type, status, payload, attempts, last_error, created_at, updated_at)
		VALUES(?, ?, ?, 0, '', ?, ?)`, typ, model.JobQueued, payload, now, now)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return d.GetJob(ctx, id)
}

func (d *DB) GetJob(ctx context.Context, id int64) (*model.Job, error) {
	var j model.Job
	var created, updated string
	err := d.sql.QueryRowContext(ctx, `
		SELECT id, type, status, payload, attempts, last_error, result, created_at, updated_at FROM jobs WHERE id = ?`, id).
		Scan(&j.ID, &j.Type, &j.Status, &j.Payload, &j.Attempts, &j.LastError, &j.Result, &created, &updated)
	if err != nil {
		return nil, err
	}
	j.CreatedAt = parseTime(created)
	j.UpdatedAt = parseTime(updated)
	return &j, nil
}

func (d *DB) NextQueuedJob(ctx context.Context) (*model.Job, error) {
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var id int64
	err = tx.QueryRowContext(ctx, `
		SELECT id FROM jobs
		WHERE status = ?
		ORDER BY id
		LIMIT 1`, model.JobQueued).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, sql.ErrNoRows
	}
	if err != nil {
		return nil, err
	}
	now := nowString()
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET status = ?, attempts = attempts + 1, updated_at = ? WHERE id = ?`, model.JobRunning, now, id); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return d.GetJob(ctx, id)
}

func (d *DB) FinishJob(ctx context.Context, id int64) error {
	return d.FinishJobWithResult(ctx, id, "")
}

func (d *DB) FinishJobWithResult(ctx context.Context, id int64, result string) error {
	_, err := d.sql.ExecContext(ctx, `UPDATE jobs SET status = ?, last_error = '', result = ?, updated_at = ? WHERE id = ?`, model.JobDone, result, nowString(), id)
	return err
}

func (d *DB) FailJob(ctx context.Context, id int64, errText string, retry bool) error {
	return d.FailJobWithResult(ctx, id, errText, retry, "")
}

func (d *DB) FailJobWithResult(ctx context.Context, id int64, errText string, retry bool, result string) error {
	status := model.JobFailed
	if retry {
		status = model.JobQueued
	}
	_, err := d.sql.ExecContext(ctx, `UPDATE jobs SET status = ?, last_error = ?, result = ?, updated_at = ? WHERE id = ?`, status, errText, result, nowString(), id)
	return err
}

func (d *DB) UpsertResourceLibraryHealth(ctx context.Context, h model.ResourceLibraryHealth) (*model.ResourceLibraryHealth, error) {
	now := time.Now().UTC()
	if h.LastCheckedAt.IsZero() {
		h.LastCheckedAt = now
	}
	if h.UpdatedAt.IsZero() {
		h.UpdatedAt = now
	}
	prev, err := d.GetResourceLibraryHealth(ctx, h.Library, h.Binding)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if prev != nil {
		h.LastSuccessAt = prev.LastSuccessAt
		h.LastFailureAt = prev.LastFailureAt
		h.ConsecutiveFailures = prev.ConsecutiveFailures
	}
	if h.Status == "ok" {
		h.LastSuccessAt = h.LastCheckedAt
		h.ConsecutiveFailures = 0
	} else {
		h.LastFailureAt = h.LastCheckedAt
		h.ConsecutiveFailures++
	}
	_, err = d.sql.ExecContext(ctx, `
		INSERT INTO resource_library_health(
			library, binding, binding_path, target, target_type, status, check_mode,
			list_latency_ms, write_latency_ms, read_latency_ms, delete_latency_ms,
			last_error, consecutive_failures, last_checked_at, last_success_at, last_failure_at, updated_at
		)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(library, binding) DO UPDATE SET
			binding_path = excluded.binding_path,
			target = excluded.target,
			target_type = excluded.target_type,
			status = excluded.status,
			check_mode = excluded.check_mode,
			list_latency_ms = excluded.list_latency_ms,
			write_latency_ms = excluded.write_latency_ms,
			read_latency_ms = excluded.read_latency_ms,
			delete_latency_ms = excluded.delete_latency_ms,
			last_error = excluded.last_error,
			consecutive_failures = excluded.consecutive_failures,
			last_checked_at = excluded.last_checked_at,
			last_success_at = excluded.last_success_at,
			last_failure_at = excluded.last_failure_at,
			updated_at = excluded.updated_at`,
		h.Library, h.Binding, h.BindingPath, h.Target, h.TargetType, h.Status, h.CheckMode,
		h.ListLatencyMS, h.WriteLatencyMS, h.ReadLatencyMS, h.DeleteLatencyMS,
		h.LastError, h.ConsecutiveFailures, formatTime(h.LastCheckedAt), formatTime(h.LastSuccessAt), formatTime(h.LastFailureAt), formatTime(h.UpdatedAt))
	if err != nil {
		return nil, err
	}
	return d.GetResourceLibraryHealth(ctx, h.Library, h.Binding)
}

func (d *DB) GetResourceLibraryHealth(ctx context.Context, library, binding string) (*model.ResourceLibraryHealth, error) {
	row := d.sql.QueryRowContext(ctx, `
		SELECT library, binding, binding_path, target, target_type, status, check_mode,
			list_latency_ms, write_latency_ms, read_latency_ms, delete_latency_ms,
			last_error, consecutive_failures, last_checked_at, last_success_at, last_failure_at, updated_at
		FROM resource_library_health
		WHERE library = ? AND binding = ?`, library, binding)
	return scanResourceLibraryHealth(row)
}

func (d *DB) ResourceLibraryHealth(ctx context.Context, library string) ([]model.ResourceLibraryHealth, error) {
	query := `
		SELECT library, binding, binding_path, target, target_type, status, check_mode,
			list_latency_ms, write_latency_ms, read_latency_ms, delete_latency_ms,
			last_error, consecutive_failures, last_checked_at, last_success_at, last_failure_at, updated_at
		FROM resource_library_health`
	var (
		rows *sql.Rows
		err  error
	)
	if library == "" {
		rows, err = d.sql.QueryContext(ctx, query+` ORDER BY library, binding`)
	} else {
		rows, err = d.sql.QueryContext(ctx, query+` WHERE library = ? ORDER BY binding`, library)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ResourceLibraryHealth
	for rows.Next() {
		h, err := scanResourceLibraryHealth(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *h)
	}
	return out, rows.Err()
}

func (d *DB) CreateAssetBucket(ctx context.Context, bucket model.AssetBucket) (*model.AssetBucket, error) {
	now := nowString()
	if bucket.Status == "" {
		bucket.Status = model.AssetBucketActive
	}
	if bucket.WorkspaceID == "" {
		bucket.WorkspaceID = model.DefaultWorkspaceID
	}
	_, err := d.sql.ExecContext(ctx, `
		INSERT INTO asset_buckets(slug, workspace_id, name, description, route_profile, routing_policy, allowed_types, max_capacity_bytes, max_file_size_bytes, default_cache_control, status, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		bucket.Slug, bucket.WorkspaceID, bucket.Name, bucket.Description, bucket.RouteProfile, bucket.RoutingPolicy, joinCSV(bucket.AllowedTypes), bucket.MaxCapacityBytes, bucket.MaxFileSizeBytes, bucket.DefaultCacheControl, bucket.Status, now, now)
	if err != nil {
		return nil, err
	}
	return d.GetAssetBucket(ctx, bucket.Slug)
}

func (d *DB) GetAssetBucket(ctx context.Context, slug string) (*model.AssetBucket, error) {
	row := d.sql.QueryRowContext(ctx, `
		SELECT slug, workspace_id, name, description, route_profile, routing_policy, allowed_types, max_capacity_bytes, max_file_size_bytes, default_cache_control, status, created_at, updated_at
		FROM asset_buckets WHERE slug = ?`, slug)
	bucket, err := scanAssetBucket(row)
	if err != nil {
		return nil, err
	}
	if err := d.fillAssetBucketUsage(ctx, bucket); err != nil {
		return nil, err
	}
	return bucket, nil
}

func (d *DB) ListAssetBuckets(ctx context.Context) ([]model.AssetBucket, error) {
	return d.ListAssetBucketsInWorkspace(ctx, "")
}

func (d *DB) ListAssetBucketsInWorkspace(ctx context.Context, workspaceID string) ([]model.AssetBucket, error) {
	query := `
		SELECT slug, workspace_id, name, description, route_profile, routing_policy, allowed_types, max_capacity_bytes, max_file_size_bytes, default_cache_control, status, created_at, updated_at
		FROM asset_buckets`
	var (
		rows *sql.Rows
		err  error
	)
	if workspaceID == "" {
		rows, err = d.sql.QueryContext(ctx, query+` ORDER BY slug`)
	} else {
		rows, err = d.sql.QueryContext(ctx, query+` WHERE workspace_id = ? ORDER BY slug`, workspaceID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var buckets []model.AssetBucket
	for rows.Next() {
		bucket, err := scanAssetBucket(rows)
		if err != nil {
			return nil, err
		}
		buckets = append(buckets, *bucket)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range buckets {
		if err := d.fillAssetBucketUsage(ctx, &buckets[i]); err != nil {
			return nil, err
		}
	}
	return buckets, nil
}

func (d *DB) AssetBucketUsedBytes(ctx context.Context, slug string) (int64, error) {
	var used sql.NullInt64
	if err := d.sql.QueryRowContext(ctx, `SELECT COALESCE(SUM(size), 0) FROM asset_bucket_objects WHERE bucket_slug = ?`, slug).Scan(&used); err != nil {
		return 0, err
	}
	if used.Valid {
		return used.Int64, nil
	}
	return 0, nil
}

func (d *DB) AssetBucketObjectCount(ctx context.Context, slug string) (int64, error) {
	var count int64
	err := d.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM asset_bucket_objects WHERE bucket_slug = ?`, slug).Scan(&count)
	return count, err
}

func (d *DB) SaveAssetBucketObject(ctx context.Context, item model.AssetBucketObject) (*model.AssetBucketObject, error) {
	now := nowString()
	var created string
	_ = d.sql.QueryRowContext(ctx, `
		SELECT created_at FROM asset_bucket_objects WHERE bucket_slug = ? AND logical_path = ?`,
		item.BucketSlug, item.LogicalPath).Scan(&created)
	if created == "" {
		created = now
	}
	_, err := d.sql.ExecContext(ctx, `
		INSERT INTO asset_bucket_objects(bucket_slug, logical_path, object_id, asset_type, physical_key, size, sha256, content_type, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(bucket_slug, logical_path) DO UPDATE SET
			object_id = excluded.object_id,
			asset_type = excluded.asset_type,
			physical_key = excluded.physical_key,
			size = excluded.size,
			sha256 = excluded.sha256,
			content_type = excluded.content_type,
			updated_at = excluded.updated_at`,
		item.BucketSlug, item.LogicalPath, item.ObjectID, item.AssetType, item.PhysicalKey, item.Size, item.SHA256, item.ContentType, created, now)
	if err != nil {
		return nil, err
	}
	return d.GetAssetBucketObject(ctx, item.BucketSlug, item.LogicalPath)
}

func (d *DB) GetAssetBucketObject(ctx context.Context, slug, logicalPath string) (*model.AssetBucketObject, error) {
	row := d.sql.QueryRowContext(ctx, `
		SELECT bucket_slug, logical_path, object_id, asset_type, physical_key, size, sha256, content_type, created_at, updated_at
		FROM asset_bucket_objects WHERE bucket_slug = ? AND logical_path = ?`, slug, logicalPath)
	return scanAssetBucketObject(row)
}

func (d *DB) DeleteAssetBucketObject(ctx context.Context, slug, logicalPath string) error {
	_, err := d.sql.ExecContext(ctx, `DELETE FROM asset_bucket_objects WHERE bucket_slug = ? AND logical_path = ?`, slug, logicalPath)
	return err
}

func (d *DB) ListAssetBucketObjects(ctx context.Context, slug, prefix string, limit int) ([]model.AssetBucketObject, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	query := `
		SELECT bucket_slug, logical_path, object_id, asset_type, physical_key, size, sha256, content_type, created_at, updated_at
		FROM asset_bucket_objects WHERE bucket_slug = ?`
	var (
		rows *sql.Rows
		err  error
	)
	if prefix != "" {
		rows, err = d.sql.QueryContext(ctx, query+` AND logical_path LIKE ? ORDER BY logical_path LIMIT ?`, slug, prefix+"%", limit)
	} else {
		rows, err = d.sql.QueryContext(ctx, query+` ORDER BY logical_path LIMIT ?`, slug, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []model.AssetBucketObject
	for rows.Next() {
		item, err := scanAssetBucketObject(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func (d *DB) ListAllAssetBucketObjects(ctx context.Context, slug string) ([]model.AssetBucketObject, error) {
	rows, err := d.sql.QueryContext(ctx, `
		SELECT bucket_slug, logical_path, object_id, asset_type, physical_key, size, sha256, content_type, created_at, updated_at
		FROM asset_bucket_objects WHERE bucket_slug = ? ORDER BY logical_path`, slug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []model.AssetBucketObject
	for rows.Next() {
		item, err := scanAssetBucketObject(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func (d *DB) DeleteAssetBucket(ctx context.Context, slug string) error {
	res, err := d.sql.ExecContext(ctx, `DELETE FROM asset_buckets WHERE slug = ?`, slug)
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

func (d *DB) CreateSite(ctx context.Context, id, name, mode, routeProfile, deploymentTarget string, domains []string) (*model.Site, error) {
	return d.CreateSiteInWorkspace(ctx, model.DefaultWorkspaceID, id, name, mode, routeProfile, deploymentTarget, "", domains)
}

func (d *DB) CreateSiteInWorkspace(ctx context.Context, workspaceID, id, name, mode, routeProfile, deploymentTarget, routingPolicy string, domains []string) (*model.Site, error) {
	if workspaceID == "" {
		workspaceID = model.DefaultWorkspaceID
	}
	if existing, err := d.GetSite(ctx, id); err == nil {
		if existing.WorkspaceID != workspaceID {
			return nil, fmt.Errorf("site %q belongs to another workspace", id)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if mode == "" {
		mode = "standard"
	}
	now := nowString()
	_, err := d.sql.ExecContext(ctx, `
		INSERT INTO sites(id, workspace_id, name, mode, route_profile, deployment_target, routing_policy, status, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name = excluded.name, mode = excluded.mode, route_profile = excluded.route_profile, deployment_target = excluded.deployment_target, routing_policy = excluded.routing_policy`, id, workspaceID, name, mode, routeProfile, deploymentTarget, routingPolicy, model.SiteStatusActive, now)
	if err != nil {
		return nil, err
	}
	if err := d.SetDomains(ctx, id, domains); err != nil {
		return nil, err
	}
	return d.GetSite(ctx, id)
}

func (d *DB) GetSite(ctx context.Context, id string) (*model.Site, error) {
	var s model.Site
	var created string
	err := d.sql.QueryRowContext(ctx, `SELECT id, workspace_id, name, mode, route_profile, deployment_target, routing_policy, status, created_at FROM sites WHERE id = ?`, id).
		Scan(&s.ID, &s.WorkspaceID, &s.Name, &s.Mode, &s.RouteProfile, &s.DeploymentTarget, &s.RoutingPolicy, &s.Status, &created)
	if err != nil {
		return nil, err
	}
	if s.DeploymentTarget == "" {
		s.DeploymentTarget = model.SiteDeploymentTargetOriginAssisted
	}
	if s.Status == "" {
		s.Status = model.SiteStatusActive
	}
	s.CreatedAt = parseTime(created)
	domains, err := d.DomainsForSite(ctx, id)
	if err != nil {
		return nil, err
	}
	s.Domains = domains
	return &s, nil
}

func (d *DB) ListSitesInWorkspace(ctx context.Context, workspaceID string) ([]model.Site, error) {
	query := `SELECT id, workspace_id, name, mode, route_profile, deployment_target, routing_policy, status, created_at FROM sites`
	args := []any{}
	if workspaceID != "" {
		query += ` WHERE workspace_id = ?`
		args = append(args, workspaceID)
	}
	query += ` ORDER BY created_at DESC, id`
	rows, err := d.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	sites := []model.Site{}
	for rows.Next() {
		var s model.Site
		var created string
		if err := rows.Scan(&s.ID, &s.WorkspaceID, &s.Name, &s.Mode, &s.RouteProfile, &s.DeploymentTarget, &s.RoutingPolicy, &s.Status, &created); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if s.DeploymentTarget == "" {
			s.DeploymentTarget = model.SiteDeploymentTargetOriginAssisted
		}
		if s.Status == "" {
			s.Status = model.SiteStatusActive
		}
		s.CreatedAt = parseTime(created)
		sites = append(sites, s)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range sites {
		domains, err := d.DomainsForSite(ctx, sites[i].ID)
		if err != nil {
			return nil, err
		}
		sites[i].Domains = domains
	}
	return sites, nil
}

func (d *DB) SetSiteStatus(ctx context.Context, siteID, status string) (*model.Site, error) {
	res, err := d.sql.ExecContext(ctx, `UPDATE sites SET status = ? WHERE id = ?`, status, siteID)
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
	return d.GetSite(ctx, siteID)
}

func (d *DB) DeleteSite(ctx context.Context, siteID string) error {
	res, err := d.sql.ExecContext(ctx, `DELETE FROM sites WHERE id = ?`, siteID)
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

func (d *DB) SetDomains(ctx context.Context, siteID string, domains []string) error {
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM domains WHERE site_id = ?`, siteID); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, host := range domains {
		if host == "" {
			continue
		}
		if seen[host] {
			continue
		}
		seen[host] = true
		var owner string
		err := tx.QueryRowContext(ctx, `SELECT site_id FROM domains WHERE host = ?`, host).Scan(&owner)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if owner != "" && owner != siteID {
			return fmt.Errorf("domain %q is already bound to site %q", host, owner)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO domains(host, site_id) VALUES(?, ?)`, host, siteID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *DB) DomainsForSite(ctx context.Context, siteID string) ([]string, error) {
	rows, err := d.sql.QueryContext(ctx, `SELECT host FROM domains WHERE site_id = ? ORDER BY host`, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var domains []string
	for rows.Next() {
		var host string
		if err := rows.Scan(&host); err != nil {
			return nil, err
		}
		domains = append(domains, host)
	}
	return domains, rows.Err()
}

func (d *DB) SiteByHost(ctx context.Context, host string) (*model.Site, error) {
	var siteID string
	if err := d.sql.QueryRowContext(ctx, `SELECT site_id FROM domains WHERE host = ?`, host).Scan(&siteID); err != nil {
		return nil, err
	}
	return d.GetSite(ctx, siteID)
}

func nowString() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func parseTime(v string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, v)
	if err == nil {
		return t
	}
	return time.Time{}
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func joinCSV(values []string) string {
	return strings.Join(values, ",")
}

func splitCSV(v string) []string {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

type scanner interface {
	Scan(dest ...any) error
}

func scanResourceLibraryHealth(row scanner) (*model.ResourceLibraryHealth, error) {
	var h model.ResourceLibraryHealth
	var checked, success, failure, updated string
	err := row.Scan(
		&h.Library, &h.Binding, &h.BindingPath, &h.Target, &h.TargetType, &h.Status, &h.CheckMode,
		&h.ListLatencyMS, &h.WriteLatencyMS, &h.ReadLatencyMS, &h.DeleteLatencyMS,
		&h.LastError, &h.ConsecutiveFailures, &checked, &success, &failure, &updated,
	)
	if err != nil {
		return nil, err
	}
	h.LastCheckedAt = parseTime(checked)
	h.LastSuccessAt = parseTime(success)
	h.LastFailureAt = parseTime(failure)
	h.UpdatedAt = parseTime(updated)
	return &h, nil
}

func scanAssetBucket(row scanner) (*model.AssetBucket, error) {
	var bucket model.AssetBucket
	var allowed, created, updated string
	err := row.Scan(
		&bucket.Slug, &bucket.WorkspaceID, &bucket.Name, &bucket.Description, &bucket.RouteProfile, &bucket.RoutingPolicy, &allowed,
		&bucket.MaxCapacityBytes, &bucket.MaxFileSizeBytes, &bucket.DefaultCacheControl, &bucket.Status, &created, &updated,
	)
	if err != nil {
		return nil, err
	}
	bucket.AllowedTypes = splitCSV(allowed)
	bucket.CreatedAt = parseTime(created)
	bucket.UpdatedAt = parseTime(updated)
	return &bucket, nil
}

func scanAssetBucketObject(row scanner) (*model.AssetBucketObject, error) {
	var item model.AssetBucketObject
	var created, updated string
	err := row.Scan(
		&item.BucketSlug, &item.LogicalPath, &item.ObjectID, &item.AssetType, &item.PhysicalKey,
		&item.Size, &item.SHA256, &item.ContentType, &created, &updated,
	)
	if err != nil {
		return nil, err
	}
	item.CreatedAt = parseTime(created)
	item.UpdatedAt = parseTime(updated)
	item.URL = "/a/" + item.BucketSlug + "/" + item.LogicalPath
	return &item, nil
}

func scanIPFSPin(row scanner) (*model.IPFSPin, error) {
	var pin model.IPFSPin
	var created, updated string
	err := row.Scan(
		&pin.ObjectID, &pin.Target, &pin.Provider, &pin.CID, &pin.GatewayURL,
		&pin.Locator, &pin.PinStatus, &pin.ProviderPinID, &created, &updated,
	)
	if err != nil {
		return nil, err
	}
	pin.CreatedAt = parseTime(created)
	pin.UpdatedAt = parseTime(updated)
	return &pin, nil
}

func (d *DB) fillAssetBucketUsage(ctx context.Context, bucket *model.AssetBucket) error {
	used, err := d.AssetBucketUsedBytes(ctx, bucket.Slug)
	if err != nil {
		return err
	}
	count, err := d.AssetBucketObjectCount(ctx, bucket.Slug)
	if err != nil {
		return err
	}
	bucket.UsedBytes = used
	bucket.ObjectCount = count
	return nil
}

func IsNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}

func WrapNotFound(name string, err error) error {
	if IsNotFound(err) {
		return fmt.Errorf("%s not found", name)
	}
	return err
}
