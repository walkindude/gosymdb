package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// migration describes a single schema change.
type migration struct {
	version int
	desc    string
	stmts   []string
}

// migrations is the ordered list of all schema migrations. Each entry is
// applied exactly once and tracked in the schema_version table.
//
// To add a new migration: append an entry with the next version number and
// bump the version number accordingly.
var migrations = []migration{
	{
		version: 1,
		desc:    "initial schema",
		stmts: []string{
			`CREATE TABLE IF NOT EXISTS modules (
				id   INTEGER PRIMARY KEY,
				root TEXT NOT NULL UNIQUE
			)`,
			`CREATE TABLE IF NOT EXISTS symbols (
				id           INTEGER PRIMARY KEY,
				module_root  TEXT NOT NULL,
				package_path TEXT NOT NULL,
				package_name TEXT NOT NULL,
				file_path    TEXT NOT NULL,
				name         TEXT NOT NULL,
				kind         TEXT NOT NULL,
				recv         TEXT NOT NULL,
				signature    TEXT NOT NULL,
				fqname       TEXT NOT NULL,
				exported     INTEGER NOT NULL,
				line         INTEGER NOT NULL,
				col          INTEGER NOT NULL
			)`,
			`CREATE INDEX IF NOT EXISTS idx_symbols_name   ON symbols(name)`,
			`CREATE INDEX IF NOT EXISTS idx_symbols_fqname ON symbols(fqname)`,
			`CREATE INDEX IF NOT EXISTS idx_symbols_kind   ON symbols(kind)`,
			`CREATE TABLE IF NOT EXISTS calls (
				id           INTEGER PRIMARY KEY,
				module_root  TEXT NOT NULL,
				package_path TEXT NOT NULL,
				file_path    TEXT NOT NULL,
				line         INTEGER NOT NULL,
				col          INTEGER NOT NULL,
				from_fqname  TEXT NOT NULL,
				to_fqname    TEXT NOT NULL,
				callee_expr  TEXT NOT NULL,
				kind         TEXT NOT NULL DEFAULT 'call'
			)`,
			`CREATE INDEX IF NOT EXISTS idx_calls_to   ON calls(to_fqname)`,
			`CREATE INDEX IF NOT EXISTS idx_calls_from ON calls(from_fqname)`,
			`CREATE TABLE IF NOT EXISTS unresolved_calls (
				id           INTEGER PRIMARY KEY,
				module_root  TEXT NOT NULL,
				package_path TEXT NOT NULL,
				file_path    TEXT NOT NULL,
				line         INTEGER NOT NULL,
				col          INTEGER NOT NULL,
				from_fqname  TEXT NOT NULL,
				expr         TEXT NOT NULL,
				reason       TEXT NOT NULL DEFAULT 'unresolved'
			)`,
			`CREATE INDEX IF NOT EXISTS idx_unresolved_expr ON unresolved_calls(expr)`,
			`CREATE TABLE IF NOT EXISTS implements (
				id           INTEGER PRIMARY KEY,
				module_root  TEXT NOT NULL,
				iface_pkg    TEXT NOT NULL,
				iface_fqname TEXT NOT NULL,
				impl_pkg     TEXT NOT NULL,
				impl_fqname  TEXT NOT NULL,
				is_pointer   INTEGER NOT NULL DEFAULT 0
			)`,
			`CREATE INDEX IF NOT EXISTS idx_implements_iface ON implements(iface_fqname)`,
			`CREATE INDEX IF NOT EXISTS idx_implements_impl  ON implements(impl_fqname)`,
			`CREATE TABLE IF NOT EXISTS package_meta (
				id           INTEGER PRIMARY KEY,
				module_root  TEXT NOT NULL,
				package_path TEXT NOT NULL,
				indexed_at   TEXT NOT NULL,
				files_hash   TEXT NOT NULL DEFAULT '',
				UNIQUE(module_root, package_path)
			)`,
			`CREATE INDEX IF NOT EXISTS idx_package_meta_module ON package_meta(module_root)`,
			`CREATE TABLE IF NOT EXISTS package_files (
				id           INTEGER PRIMARY KEY,
				module_root  TEXT NOT NULL,
				package_path TEXT NOT NULL,
				file_path    TEXT NOT NULL,
				file_hash    TEXT NOT NULL,
				UNIQUE(module_root, package_path, file_path)
			)`,
			`CREATE TABLE IF NOT EXISTS type_refs (
				id           INTEGER PRIMARY KEY,
				module_root  TEXT NOT NULL,
				package_path TEXT NOT NULL,
				file_path    TEXT NOT NULL,
				line         INTEGER NOT NULL,
				col          INTEGER NOT NULL,
				from_fqname  TEXT NOT NULL,
				to_fqname    TEXT NOT NULL,
				ref_kind     TEXT NOT NULL,
				expr         TEXT NOT NULL DEFAULT ''
			)`,
			`CREATE INDEX IF NOT EXISTS idx_type_refs_to   ON type_refs(to_fqname)`,
			`CREATE INDEX IF NOT EXISTS idx_type_refs_from ON type_refs(from_fqname)`,
			`CREATE INDEX IF NOT EXISTS idx_type_refs_kind ON type_refs(ref_kind)`,
			`CREATE TABLE IF NOT EXISTS index_meta (
				id             INTEGER PRIMARY KEY,
				tool_version   TEXT NOT NULL,
				go_version     TEXT NOT NULL,
				indexed_at     TEXT NOT NULL,
				root           TEXT NOT NULL,
				warnings       INTEGER NOT NULL DEFAULT 0,
				indexed_commit TEXT NOT NULL DEFAULT ''
			)`,
		},
	},
	{
		version: 2,
		desc:    "add calls.kind column",
		// Already included in v1 DDL as DEFAULT 'call'. This migration exists
		// only so that existing DBs created before v1 had the column can have
		// it added. The ALTER is a no-op on fresh DBs because IF NOT EXISTS
		// is not available for ALTER TABLE in SQLite — we catch the error.
		stmts: []string{
			`ALTER TABLE calls ADD COLUMN kind TEXT NOT NULL DEFAULT 'call'`,
		},
	},
	{
		version: 3,
		desc:    "add package_meta.files_hash column",
		stmts: []string{
			`ALTER TABLE package_meta ADD COLUMN files_hash TEXT NOT NULL DEFAULT ''`,
		},
	},
	{
		version: 4,
		desc:    "add index_meta.indexed_commit column",
		stmts: []string{
			`ALTER TABLE index_meta ADD COLUMN indexed_commit TEXT NOT NULL DEFAULT ''`,
		},
	},
	{
		version: 5,
		desc:    "add impl_methods table for method-precise dead suppression",
		stmts: []string{
			`CREATE TABLE IF NOT EXISTS impl_methods (
				id          INTEGER PRIMARY KEY,
				module_root TEXT NOT NULL,
				impl_fqname TEXT NOT NULL
			)`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_impl_methods_fqname ON impl_methods(impl_fqname)`,
		},
	},
	{
		version: 6,
		desc:    "add iface_methods table for method-precise interface dispatch hints",
		stmts: []string{
			`CREATE TABLE IF NOT EXISTS iface_methods (
				id           INTEGER PRIMARY KEY,
				module_root  TEXT NOT NULL,
				iface_fqname TEXT NOT NULL,
				method_name  TEXT NOT NULL
			)`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_iface_methods ON iface_methods(iface_fqname, method_name)`,
			`CREATE INDEX IF NOT EXISTS idx_iface_methods_iface ON iface_methods(iface_fqname)`,
		},
	},
}

// schemaVersionTable DDL — created before any migration runs.
const schemaVersionDDL = `CREATE TABLE IF NOT EXISTS schema_version (
	version    INTEGER NOT NULL,
	applied_at TEXT NOT NULL
)`

// currentVersion returns the highest applied migration version, or 0.
func currentVersion(ctx context.Context, db *sql.DB) (int, error) {
	// schema_version may not exist yet on brand-new or legacy DBs.
	var exists int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='schema_version'`,
	).Scan(&exists)
	if err != nil {
		return 0, err
	}
	if exists == 0 {
		return 0, nil
	}
	var v sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_version`).Scan(&v); err != nil {
		return 0, err
	}
	if !v.Valid {
		return 0, nil
	}
	return int(v.Int64), nil
}

// tablesExist returns true if the core tables already exist (legacy DB).
func tablesExist(ctx context.Context, db *sql.DB) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='symbols'`,
	).Scan(&n)
	return n > 0, err
}

// migrate applies all pending migrations inside individual transactions.
func migrate(ctx context.Context, db *sql.DB) error {
	// WAL mode must be set outside any transaction.
	if _, err := db.ExecContext(ctx, `PRAGMA journal_mode = WAL`); err != nil {
		return fmt.Errorf("set WAL mode: %w", err)
	}

	// Ensure schema_version table exists first (outside any migration tx).
	if _, err := db.ExecContext(ctx, schemaVersionDDL); err != nil {
		return fmt.Errorf("create schema_version: %w", err)
	}

	ver, err := currentVersion(ctx, db)
	if err != nil {
		return fmt.Errorf("current version: %w", err)
	}

	// Legacy bootstrap: tables exist but schema_version is empty.
	// Run all migrations normally — applyMigration uses CREATE TABLE IF NOT
	// EXISTS (no-op) and tolerates "duplicate column name" from ALTER TABLE,
	// so it is safe to apply against a pre-existing schema. This ensures new
	// migrations (e.g. impl_methods in v5) are actually executed rather than
	// silently stamped as applied without creating the table.
	if ver == 0 {
		legacy, err := tablesExist(ctx, db)
		if err != nil {
			return fmt.Errorf("check legacy schema: %w", err)
		}
		if legacy {
			for _, m := range migrations {
				if err := applyMigration(ctx, db, m); err != nil {
					return fmt.Errorf("legacy migration v%d (%s): %w", m.version, m.desc, err)
				}
			}
			return nil
		}
	}

	// Apply pending migrations in order.
	for _, m := range migrations {
		if m.version <= ver {
			continue
		}
		if err := applyMigration(ctx, db, m); err != nil {
			return fmt.Errorf("migration v%d (%s): %w", m.version, m.desc, err)
		}
	}
	return nil
}

// applyMigration runs a single migration inside a transaction.
func applyMigration(ctx context.Context, db *sql.DB, m migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, stmt := range m.stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			// For ALTER TABLE ADD COLUMN, SQLite returns an error if the
			// column already exists. Treat "duplicate column name" as a no-op
			// so migrations 2-4 are safe to apply on DBs that already have
			// those columns from v1 DDL.
			if strings.Contains(err.Error(), "duplicate column name") {
				continue
			}
			tx.Rollback()
			return fmt.Errorf("stmt %q: %w", stmt, err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_version(version, applied_at) VALUES (?, datetime('now'))`, m.version,
	); err != nil {
		tx.Rollback()
		return fmt.Errorf("stamp version: %w", err)
	}
	return tx.Commit()
}

// ResetSchemaDB drops all tables and recreates from migrations. Operates on a
// raw *sql.DB — use this when you don't have a SQLiteStore (e.g. from indexer).
func ResetSchemaDB(ctx context.Context, db *sql.DB) error {
	return resetSchema(ctx, db)
}

// MigrateDB applies pending migrations to a raw *sql.DB. Operates on a
// raw *sql.DB — use this when you don't have a SQLiteStore (e.g. from indexer).
func MigrateDB(ctx context.Context, db *sql.DB) error {
	return migrate(ctx, db)
}

// resetSchema drops all tables (including schema_version) and re-runs migrate.
func resetSchema(ctx context.Context, db *sql.DB) error {
	drops := []string{
		`DROP TABLE IF EXISTS schema_version`,
		`DROP TABLE IF EXISTS iface_methods`,
		`DROP TABLE IF EXISTS impl_methods`,
		`DROP TABLE IF EXISTS index_meta`,
		`DROP TABLE IF EXISTS type_refs`,
		`DROP TABLE IF EXISTS package_files`,
		`DROP TABLE IF EXISTS package_meta`,
		`DROP TABLE IF EXISTS implements`,
		`DROP TABLE IF EXISTS unresolved_calls`,
		`DROP TABLE IF EXISTS calls`,
		`DROP TABLE IF EXISTS symbols`,
		`DROP TABLE IF EXISTS modules`,
	}
	for _, stmt := range drops {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("drop: %w", err)
		}
	}
	return migrate(ctx, db)
}
