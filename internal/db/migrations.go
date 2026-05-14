package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

const latestSchemaMigration = "20260514_0012_asset_buckets_routing_policy"

type schemaMigration struct {
	Version     string
	Description string
	Run         func(context.Context, *sql.Tx) error
}

var schemaMigrations = []schemaMigration{
	{
		Version:     "20260514_0001_projects_workspace_id",
		Description: "add workspace ownership to projects",
		Run: func(ctx context.Context, tx *sql.Tx) error {
			return ensureColumn(ctx, tx, "projects", "workspace_id", "TEXT NOT NULL DEFAULT 'default'")
		},
	},
	{
		Version:     "20260514_0002_sites_workspace_id",
		Description: "add workspace ownership to sites",
		Run: func(ctx context.Context, tx *sql.Tx) error {
			return ensureColumn(ctx, tx, "sites", "workspace_id", "TEXT NOT NULL DEFAULT 'default'")
		},
	},
	{
		Version:     "20260514_0003_asset_buckets_workspace_id",
		Description: "add workspace ownership to asset buckets",
		Run: func(ctx context.Context, tx *sql.Tx) error {
			return ensureColumn(ctx, tx, "asset_buckets", "workspace_id", "TEXT NOT NULL DEFAULT 'default'")
		},
	},
	{
		Version:     "20260514_0004_jobs_result",
		Description: "store async job results",
		Run: func(ctx context.Context, tx *sql.Tx) error {
			return ensureColumn(ctx, tx, "jobs", "result", "TEXT NOT NULL DEFAULT ''")
		},
	},
	{
		Version:     "20260514_0005_sites_name",
		Description: "store site display names",
		Run: func(ctx context.Context, tx *sql.Tx) error {
			return ensureColumn(ctx, tx, "sites", "name", "TEXT NOT NULL DEFAULT ''")
		},
	},
	{
		Version:     "20260514_0006_sites_deployment_target",
		Description: "store site deployment targets",
		Run: func(ctx context.Context, tx *sql.Tx) error {
			return ensureColumn(ctx, tx, "sites", "deployment_target", "TEXT NOT NULL DEFAULT ''")
		},
	},
	{
		Version:     "20260514_0007_sites_routing_policy",
		Description: "store site routing policies",
		Run: func(ctx context.Context, tx *sql.Tx) error {
			return ensureColumn(ctx, tx, "sites", "routing_policy", "TEXT NOT NULL DEFAULT ''")
		},
	},
	{
		Version:     "20260514_0008_sites_status",
		Description: "store site lifecycle status",
		Run: func(ctx context.Context, tx *sql.Tx) error {
			return ensureColumn(ctx, tx, "sites", "status", "TEXT NOT NULL DEFAULT 'active'")
		},
	},
	{
		Version:     "20260514_0009_site_deployments_deployment_target",
		Description: "store deployment targets on site deployments",
		Run: func(ctx context.Context, tx *sql.Tx) error {
			return ensureColumn(ctx, tx, "site_deployments", "deployment_target", "TEXT NOT NULL DEFAULT ''")
		},
	},
	{
		Version:     "20260514_0010_site_deployments_routing_policy",
		Description: "store routing policies on site deployments",
		Run: func(ctx context.Context, tx *sql.Tx) error {
			return ensureColumn(ctx, tx, "site_deployments", "routing_policy", "TEXT NOT NULL DEFAULT ''")
		},
	},
	{
		Version:     "20260514_0011_site_deployments_resource_failover",
		Description: "store resource failover flag on site deployments",
		Run: func(ctx context.Context, tx *sql.Tx) error {
			return ensureColumn(ctx, tx, "site_deployments", "resource_failover", "INTEGER NOT NULL DEFAULT 0")
		},
	},
	{
		Version:     latestSchemaMigration,
		Description: "store routing policies on asset buckets",
		Run: func(ctx context.Context, tx *sql.Tx) error {
			return ensureColumn(ctx, tx, "asset_buckets", "routing_policy", "TEXT NOT NULL DEFAULT ''")
		},
	},
}

func (d *DB) applySchemaMigrations(ctx context.Context) error {
	for _, migration := range schemaMigrations {
		applied, err := d.schemaMigrationApplied(ctx, migration.Version)
		if err != nil {
			return fmt.Errorf("check schema migration %s (%s): %w", migration.Version, migration.Description, err)
		}
		if applied {
			continue
		}
		tx, err := d.sql.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("start schema migration %s (%s): %w", migration.Version, migration.Description, err)
		}
		if err := migration.Run(ctx, tx); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply schema migration %s (%s): %w", migration.Version, migration.Description, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(?, ?)`, migration.Version, nowString()); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record schema migration %s (%s): %w", migration.Version, migration.Description, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit schema migration %s (%s): %w", migration.Version, migration.Description, err)
		}
	}
	return nil
}

func (d *DB) schemaMigrationApplied(ctx context.Context, version string) (bool, error) {
	var count int
	if err := d.sql.QueryRowContext(ctx, `SELECT COUNT(1) FROM schema_migrations WHERE version = ?`, version).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (d *DB) currentSchemaVersion(ctx context.Context) (string, error) {
	var version string
	err := d.sql.QueryRowContext(ctx, `SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1`).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return version, nil
}
