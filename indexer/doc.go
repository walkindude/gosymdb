// Package indexer walks a Go module, type-checks it via go/packages, and
// writes the resulting symbol and call-edge graph to a store.Store.
//
// The indexer is backend-agnostic: it accepts any implementation of
// store.Store, so alternative storage backends (beyond the bundled
// SQLite one) can reuse the same indexing logic.
//
// Stability: gosymdb is v0.x. This package is publicly exported to
// support alternative store.Store implementations, not to provide a
// stable library API. Breaking changes may land on any minor release
// until v1.0.
package indexer
