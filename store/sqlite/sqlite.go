// Package sqlite provides a SQLite implementation of store.Store.
//
// Stability: gosymdb is v0.x. This package is publicly exported so that
// consumers can construct a SQLite-backed Store from Go code, not to provide
// a stable library API. Breaking changes may land on any minor release
// until v1.0.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/walkindude/gosymdb/store"
)

// SQLiteStore is the SQLite implementation of store.Store.
type SQLiteStore struct {
	db *sql.DB
}

// Open opens (or creates) a SQLite database at path and returns a Store.
// The caller must call Close when done.
func Open(path string) (*SQLiteStore, error) {
	db, err := sql.Open(DriverName, path)
	if err != nil {
		return nil, fmt.Errorf("sqlite open %q: %w", path, err)
	}
	return &SQLiteStore{db: db}, nil
}

// UnwrapDB returns the underlying *sql.DB. Used in tests to run raw queries
// for verification without making the internal DB part of the public API.
func UnwrapDB(s store.Store) *sql.DB {
	return s.(*SQLiteStore).db
}

// Close implements store.SchemaStore.
func (s *SQLiteStore) Close() error { return s.db.Close() }

// Migrate implements store.SchemaStore.
func (s *SQLiteStore) Migrate(ctx context.Context) error {
	return migrate(ctx, s.db)
}

// ResetSchema implements store.SchemaStore.
func (s *SQLiteStore) ResetSchema(ctx context.Context) error {
	return resetSchema(ctx, s.db)
}

// SchemaVersion implements store.SchemaStore.
func (s *SQLiteStore) SchemaVersion(ctx context.Context) (int, error) {
	return currentVersion(ctx, s.db)
}

// ---- WriteStore ----

// UpsertModule implements store.WriteStore.
func (s *SQLiteStore) UpsertModule(ctx context.Context, moduleRoot string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO modules(root) VALUES (?)`, moduleRoot)
	return err
}

// PurgeModule implements store.WriteStore.
func (s *SQLiteStore) PurgeModule(ctx context.Context, moduleRoot string) error {
	for _, tbl := range []string{"symbols", "calls", "unresolved_calls", "implements", "package_files", "type_refs"} {
		if _, err := s.db.ExecContext(ctx,
			"DELETE FROM "+tbl+" WHERE module_root = ?", moduleRoot); err != nil {
			return fmt.Errorf("purge %s: %w", tbl, err)
		}
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM package_meta WHERE module_root = ?`, moduleRoot); err != nil {
		return fmt.Errorf("purge package_meta: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM modules WHERE root = ?`, moduleRoot); err != nil {
		return fmt.Errorf("purge modules: %w", err)
	}
	return nil
}

// IndexBatch implements store.WriteStore.
func (s *SQLiteStore) IndexBatch(ctx context.Context, moduleRoot string) (store.IndexBatch, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}

	insertSymbol, err := tx.PrepareContext(ctx, `
INSERT INTO symbols(module_root,package_path,package_name,file_path,name,kind,recv,signature,fqname,exported,line,col)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("prepare insertSymbol: %w", err)
	}

	insertCall, err := tx.PrepareContext(ctx, `
INSERT INTO calls(module_root,package_path,file_path,line,col,from_fqname,to_fqname,callee_expr,kind)
VALUES (?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("prepare insertCall: %w", err)
	}

	insertUnresolved, err := tx.PrepareContext(ctx, `
INSERT INTO unresolved_calls(module_root,package_path,file_path,line,col,from_fqname,expr,reason)
VALUES (?,?,?,?,?,?,?,?)`)
	if err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("prepare insertUnresolved: %w", err)
	}

	insertImpl, err := tx.PrepareContext(ctx, `
INSERT INTO implements(module_root,iface_pkg,iface_fqname,impl_pkg,impl_fqname,is_pointer)
VALUES (?,?,?,?,?,?)`)
	if err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("prepare insertImpl: %w", err)
	}

	insertTypeRef, err := tx.PrepareContext(ctx, `
INSERT INTO type_refs(module_root,package_path,file_path,line,col,from_fqname,to_fqname,ref_kind,expr)
VALUES (?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("prepare insertTypeRef: %w", err)
	}

	return &sqliteBatch{
		tx:               tx,
		insertSymbol:     insertSymbol,
		insertCall:       insertCall,
		insertUnresolved: insertUnresolved,
		insertImpl:       insertImpl,
		insertTypeRef:    insertTypeRef,
	}, nil
}

// UpsertPackageMeta implements store.WriteStore.
func (s *SQLiteStore) UpsertPackageMeta(ctx context.Context, moduleRoot, packagePath, indexedAt string) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO package_meta(module_root, package_path, indexed_at)
VALUES (?, ?, ?)
ON CONFLICT(module_root, package_path) DO UPDATE SET indexed_at = excluded.indexed_at`,
		moduleRoot, packagePath, indexedAt)
	return err
}

// UpsertPackageFile implements store.WriteStore.
func (s *SQLiteStore) UpsertPackageFile(ctx context.Context, moduleRoot, packagePath, filePath, fileHash string) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO package_files(module_root, package_path, file_path, file_hash)
VALUES (?, ?, ?, ?)
ON CONFLICT(module_root, package_path, file_path) DO UPDATE SET file_hash = excluded.file_hash`,
		moduleRoot, packagePath, filePath, fileHash)
	return err
}

// UpdatePackageFilesHash implements store.WriteStore.
func (s *SQLiteStore) UpdatePackageFilesHash(ctx context.Context, moduleRoot, packagePath, filesHash string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE package_meta SET files_hash = ? WHERE module_root = ? AND package_path = ?`,
		filesHash, moduleRoot, packagePath)
	return err
}

// InsertIndexMeta implements store.WriteStore.
func (s *SQLiteStore) InsertIndexMeta(ctx context.Context, meta store.IndexMeta) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO index_meta(tool_version, go_version, indexed_at, root, warnings, indexed_commit)
VALUES (?, ?, ?, ?, ?, ?)`,
		meta.ToolVersion, meta.GoVersion, meta.IndexedAt, meta.Root, meta.Warnings, meta.IndexedCommit)
	return err
}

// ---- sqliteBatch ----

type sqliteBatch struct {
	tx               *sql.Tx
	insertSymbol     *sql.Stmt
	insertCall       *sql.Stmt
	insertUnresolved *sql.Stmt
	insertImpl       *sql.Stmt
	insertTypeRef    *sql.Stmt
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (b *sqliteBatch) InsertSymbol(s store.Symbol) error {
	_, err := b.insertSymbol.Exec(
		s.ModuleRoot, s.PackagePath, s.PackageName, s.FilePath,
		s.Name, s.Kind, s.Recv, s.Signature, s.FQName,
		boolToInt(s.Exported), s.Line, s.Col,
	)
	return err
}

func (b *sqliteBatch) InsertCall(c store.Call) error {
	_, err := b.insertCall.Exec(
		c.ModuleRoot, c.PackagePath, c.FilePath, c.Line, c.Col,
		c.FromFQName, c.ToFQName, c.CalleeExpr, c.Kind,
	)
	return err
}

func (b *sqliteBatch) InsertRef(c store.Call) error {
	ref := c
	ref.Kind = "ref"
	return b.InsertCall(ref)
}

func (b *sqliteBatch) InsertUnresolved(u store.UnresolvedCall) error {
	_, err := b.insertUnresolved.Exec(
		u.ModuleRoot, u.PackagePath, u.FilePath, u.Line, u.Col,
		u.FromFQName, u.Expr, u.Reason,
	)
	return err
}

func (b *sqliteBatch) InsertImplements(imp store.Implements) error {
	_, err := b.insertImpl.Exec(
		imp.ModuleRoot, imp.IfacePkg, imp.IfaceFQName,
		imp.ImplPkg, imp.ImplFQName, boolToInt(imp.IsPointer),
	)
	return err
}

func (b *sqliteBatch) InsertTypeRef(tr store.TypeRef) error {
	_, err := b.insertTypeRef.Exec(
		tr.ModuleRoot, tr.PackagePath, tr.FilePath, tr.Line, tr.Col,
		tr.FromFQName, tr.ToFQName, tr.RefKind, tr.Expr,
	)
	return err
}

func (b *sqliteBatch) Commit() error   { return b.tx.Commit() }
func (b *sqliteBatch) Rollback() error { return b.tx.Rollback() }

// ---- ReadStore stubs — not yet implemented. ----

func (s *SQLiteStore) IndexedModuleRoots(_ context.Context) ([]string, error) { return nil, nil }
func (s *SQLiteStore) ModuleRootForPackage(_ context.Context, _ string) (string, error) {
	return "", nil
}

// Ensure SQLiteStore implements store.Store at compile time.
var _ store.Store = (*SQLiteStore)(nil)
