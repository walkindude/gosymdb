//go:build cgo_sqlite

package sqlite

import _ "github.com/mattn/go-sqlite3"

// DriverName is the database/sql driver name for the CGo SQLite implementation.
// Build with: go build -tags cgo_sqlite
const DriverName = "sqlite3"
