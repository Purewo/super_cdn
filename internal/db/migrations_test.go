package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenReportsSchemaVersionForNewDatabase(t *testing.T) {
	ctx := context.Background()
	state, err := Open(ctx, filepath.Join(t.TempDir(), "new.sqlite"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer state.Close()

	if got := state.SchemaVersion(); got != latestSchemaMigration {
		t.Fatalf("SchemaVersion() = %q, want %q", got, latestSchemaMigration)
	}

	var count int
	if err := state.SQL().QueryRowContext(ctx, `SELECT COUNT(1) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if count != len(schemaMigrations) {
		t.Fatalf("schema_migrations count = %d, want %d", count, len(schemaMigrations))
	}
}

func TestOpenAppliesNamedSchemaMigrationsToOldDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.sqlite")
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	for _, stmt := range oldSchemaStatements() {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			_ = conn.Close()
			t.Fatalf("create old schema fixture: %v\nstmt: %s", err, stmt)
		}
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close fixture db: %v", err)
	}

	state, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer state.Close()

	if got := state.SchemaVersion(); got != latestSchemaMigration {
		t.Fatalf("SchemaVersion() = %q, want %q", got, latestSchemaMigration)
	}
	assertColumnExists(t, ctx, state, "projects", "workspace_id")
	assertColumnExists(t, ctx, state, "sites", "workspace_id")
	assertColumnExists(t, ctx, state, "sites", "name")
	assertColumnExists(t, ctx, state, "sites", "deployment_target")
	assertColumnExists(t, ctx, state, "sites", "routing_policy")
	assertColumnExists(t, ctx, state, "sites", "status")
	assertColumnExists(t, ctx, state, "asset_buckets", "workspace_id")
	assertColumnExists(t, ctx, state, "asset_buckets", "routing_policy")
	assertColumnExists(t, ctx, state, "jobs", "result")
	assertColumnExists(t, ctx, state, "site_deployments", "deployment_target")
	assertColumnExists(t, ctx, state, "site_deployments", "routing_policy")
	assertColumnExists(t, ctx, state, "site_deployments", "resource_failover")
}

func TestOpenReturnsClearSchemaMigrationCheckError(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "broken.sqlite")
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `CREATE TABLE schema_migrations (bad_column TEXT NOT NULL)`); err != nil {
		_ = conn.Close()
		t.Fatalf("create broken schema_migrations: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close fixture db: %v", err)
	}

	_, err = Open(ctx, path)
	if err == nil {
		t.Fatal("Open() error = nil, want migration check error")
	}
	if !strings.Contains(err.Error(), "check schema migration 20260514_0001_projects_workspace_id") {
		t.Fatalf("Open() error = %q, want migration version context", err)
	}
}

func assertColumnExists(t *testing.T, ctx context.Context, state *DB, table, column string) {
	t.Helper()
	rows, err := state.SQL().QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		t.Fatalf("PRAGMA table_info(%s): %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan table_info(%s): %v", table, err)
		}
		if name == column {
			return
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate table_info(%s): %v", table, err)
	}
	t.Fatalf("column %s.%s was not created", table, column)
}

func oldSchemaStatements() []string {
	return []string{
		`CREATE TABLE projects (
			id TEXT PRIMARY KEY,
			created_at TEXT NOT NULL
		);`,
		`CREATE TABLE jobs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			type TEXT NOT NULL,
			status TEXT NOT NULL,
			payload TEXT NOT NULL,
			attempts INTEGER NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE sites (
			id TEXT PRIMARY KEY,
			mode TEXT NOT NULL,
			route_profile TEXT NOT NULL,
			created_at TEXT NOT NULL
		);`,
		`CREATE TABLE site_deployments (
			id TEXT PRIMARY KEY,
			site_id TEXT NOT NULL,
			environment TEXT NOT NULL,
			status TEXT NOT NULL,
			route_profile TEXT NOT NULL,
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
		`CREATE TABLE asset_buckets (
			slug TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT NOT NULL,
			route_profile TEXT NOT NULL,
			allowed_types TEXT NOT NULL,
			max_capacity_bytes INTEGER NOT NULL DEFAULT 0,
			max_file_size_bytes INTEGER NOT NULL DEFAULT 0,
			default_cache_control TEXT NOT NULL,
			status TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
	}
}
