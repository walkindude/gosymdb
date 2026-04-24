package indexer

import (
	"context"
	"database/sql"

	"github.com/walkindude/gosymdb/store/sqlite"
)

// ResetSchema drops all tables and recreates the schema from scratch.
// Used for --force rebuilds. Delegates to the store/sqlite migration system
// which is the single source of truth for schema DDL.
func ResetSchema(db *sql.DB) error {
	return sqlite.ResetSchemaDB(context.Background(), db)
}

// EnsureSchema creates all tables and applies pending migrations.
// Used in incremental index mode (no --force) to preserve existing data.
// Delegates to the store/sqlite migration system.
func EnsureSchema(db *sql.DB) error {
	return sqlite.MigrateDB(context.Background(), db)
}
