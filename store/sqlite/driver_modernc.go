//go:build !cgo_sqlite

package sqlite

import _ "modernc.org/sqlite"

// DriverName is the database/sql driver name for the pure-Go SQLite implementation.
const DriverName = "sqlite"
