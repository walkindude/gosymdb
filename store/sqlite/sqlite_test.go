package sqlite_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/walkindude/gosymdb/indexer"
	"github.com/walkindude/gosymdb/store"
	"github.com/walkindude/gosymdb/store/sqlite"
)

func openTest(t *testing.T) store.Store {
	t.Helper()
	s, err := sqlite.Open(filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

var ctx = context.Background()

// ---- SchemaStore ----

func TestMigrateCreatesAllTables(t *testing.T) {
	s := openTest(t)
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	tables := []string{
		"modules", "symbols", "calls", "unresolved_calls",
		"implements", "package_meta", "package_files",
		"type_refs", "index_meta", "schema_version",
	}
	db := sqlite.UnwrapDB(s)
	for _, tbl := range tables {
		var n int
		err := db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", tbl,
		).Scan(&n)
		if err != nil || n == 0 {
			t.Errorf("table %q missing after Migrate", tbl)
		}
	}
}

func TestMigrateIdempotent(t *testing.T) {
	s := openTest(t)
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
}

func TestSchemaVersionAfterFreshMigrate(t *testing.T) {
	s := openTest(t)
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	v, err := s.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if v < 4 {
		t.Errorf("expected SchemaVersion >= 4 after fresh migrate, got %d", v)
	}
}

func TestSchemaVersionEmptyDB(t *testing.T) {
	s := openTest(t)
	v, err := s.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("SchemaVersion on empty DB: %v", err)
	}
	if v != 0 {
		t.Errorf("expected 0 for empty DB, got %d", v)
	}
}

func TestResetSchemaDropsAndRecreatess(t *testing.T) {
	s := openTest(t)
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	// Insert a row so we can verify reset deletes data.
	db := sqlite.UnwrapDB(s)
	if _, err := db.ExecContext(ctx, `INSERT INTO modules(root) VALUES ('test/root')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := s.ResetSchema(ctx); err != nil {
		t.Fatalf("ResetSchema: %v", err)
	}
	var n int
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM modules`).Scan(&n)
	if n != 0 {
		t.Errorf("expected 0 rows in modules after ResetSchema, got %d", n)
	}
	// Schema version must still be valid after reset.
	v, _ := s.SchemaVersion(ctx)
	if v < 4 {
		t.Errorf("expected SchemaVersion >= 4 after ResetSchema, got %d", v)
	}
}

func TestBootstrapExistingDB(t *testing.T) {
	// Simulate a DB that was created before schema_version existed:
	// create the schema manually using the old EnsureSchema-style DDL,
	// then call Migrate() and verify schema_version is created and populated.
	s := openTest(t)
	db := sqlite.UnwrapDB(s)

	// Create tables without schema_version (old-style bootstrap).
	oldDDL := []string{
		`PRAGMA journal_mode = WAL`,
		`CREATE TABLE IF NOT EXISTS modules (id INTEGER PRIMARY KEY, root TEXT NOT NULL UNIQUE)`,
		`CREATE TABLE IF NOT EXISTS symbols (id INTEGER PRIMARY KEY, module_root TEXT NOT NULL, package_path TEXT NOT NULL, package_name TEXT NOT NULL, file_path TEXT NOT NULL, name TEXT NOT NULL, kind TEXT NOT NULL, recv TEXT NOT NULL, signature TEXT NOT NULL, fqname TEXT NOT NULL, exported INTEGER NOT NULL, line INTEGER NOT NULL, col INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS calls (id INTEGER PRIMARY KEY, module_root TEXT NOT NULL, package_path TEXT NOT NULL, file_path TEXT NOT NULL, line INTEGER NOT NULL, col INTEGER NOT NULL, from_fqname TEXT NOT NULL, to_fqname TEXT NOT NULL, callee_expr TEXT NOT NULL, kind TEXT NOT NULL DEFAULT 'call')`,
		`CREATE TABLE IF NOT EXISTS unresolved_calls (id INTEGER PRIMARY KEY, module_root TEXT NOT NULL, package_path TEXT NOT NULL, file_path TEXT NOT NULL, line INTEGER NOT NULL, col INTEGER NOT NULL, from_fqname TEXT NOT NULL, expr TEXT NOT NULL, reason TEXT NOT NULL DEFAULT 'unresolved')`,
		`CREATE TABLE IF NOT EXISTS implements (id INTEGER PRIMARY KEY, module_root TEXT NOT NULL, iface_pkg TEXT NOT NULL, iface_fqname TEXT NOT NULL, impl_pkg TEXT NOT NULL, impl_fqname TEXT NOT NULL, is_pointer INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS package_meta (id INTEGER PRIMARY KEY, module_root TEXT NOT NULL, package_path TEXT NOT NULL, indexed_at TEXT NOT NULL, files_hash TEXT NOT NULL DEFAULT '', UNIQUE(module_root, package_path))`,
		`CREATE TABLE IF NOT EXISTS package_files (id INTEGER PRIMARY KEY, module_root TEXT NOT NULL, package_path TEXT NOT NULL, file_path TEXT NOT NULL, file_hash TEXT NOT NULL, UNIQUE(module_root, package_path, file_path))`,
		`CREATE TABLE IF NOT EXISTS type_refs (id INTEGER PRIMARY KEY, module_root TEXT NOT NULL, package_path TEXT NOT NULL, file_path TEXT NOT NULL, line INTEGER NOT NULL, col INTEGER NOT NULL, from_fqname TEXT NOT NULL, to_fqname TEXT NOT NULL, ref_kind TEXT NOT NULL, expr TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE IF NOT EXISTS index_meta (id INTEGER PRIMARY KEY, tool_version TEXT NOT NULL, go_version TEXT NOT NULL, indexed_at TEXT NOT NULL, root TEXT NOT NULL, warnings INTEGER NOT NULL DEFAULT 0, indexed_commit TEXT NOT NULL DEFAULT '')`,
	}
	for _, stmt := range oldDDL {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("old DDL %q: %v", stmt, err)
		}
	}

	// Insert some data to verify it survives migration.
	if _, err := db.ExecContext(ctx, `INSERT INTO modules(root) VALUES ('surviving/root')`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Migrate should detect existing schema, create schema_version, and NOT destroy data.
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate on existing DB: %v", err)
	}

	// Data survives.
	var n int
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM modules WHERE root = 'surviving/root'`).Scan(&n)
	if n != 1 {
		t.Error("existing data was lost during bootstrap migration")
	}

	// schema_version table exists and has entries.
	v, err := s.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("SchemaVersion after bootstrap: %v", err)
	}
	if v < 4 {
		t.Errorf("expected SchemaVersion >= 4 after bootstrap, got %d", v)
	}
}

// ---- WriteStore ----

func TestUpsertModule(t *testing.T) {
	s := openTest(t)
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := s.UpsertModule(ctx, "example.com/foo"); err != nil {
		t.Fatalf("UpsertModule: %v", err)
	}
	// Second call must not error (idempotent).
	if err := s.UpsertModule(ctx, "example.com/foo"); err != nil {
		t.Fatalf("UpsertModule second call: %v", err)
	}
	db := sqlite.UnwrapDB(s)
	var n int
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM modules WHERE root = 'example.com/foo'`).Scan(&n)
	if n != 1 {
		t.Errorf("expected 1 module row, got %d", n)
	}
}

func TestPurgeModule(t *testing.T) {
	s := openTest(t)
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	root := "example.com/purge-me"
	if err := s.UpsertModule(ctx, root); err != nil {
		t.Fatalf("UpsertModule: %v", err)
	}
	// Insert rows into every table that PurgeModule should clean.
	db := sqlite.UnwrapDB(s)
	db.ExecContext(ctx, `INSERT INTO symbols(module_root,package_path,package_name,file_path,name,kind,recv,signature,fqname,exported,line,col) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		root, root+"/pkg", "pkg", "f.go", "Foo", "func", "", "func()", root+"/pkg.Foo", 1, 1, 0)
	db.ExecContext(ctx, `INSERT INTO calls(module_root,package_path,file_path,line,col,from_fqname,to_fqname,callee_expr,kind) VALUES (?,?,?,?,?,?,?,?,?)`,
		root, root+"/pkg", "f.go", 1, 0, root+"/pkg.Bar", root+"/pkg.Foo", "Foo", "call")
	db.ExecContext(ctx, `INSERT INTO package_meta(module_root,package_path,indexed_at) VALUES (?,?,?)`,
		root, root+"/pkg", time.Now().UTC().Format(time.RFC3339))

	if err := s.PurgeModule(ctx, root); err != nil {
		t.Fatalf("PurgeModule: %v", err)
	}

	for _, tbl := range []string{"symbols", "calls", "package_meta", "modules"} {
		var n int
		db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+tbl+" WHERE module_root = ? OR root = ?", root, root).Scan(&n)
		if n != 0 {
			t.Errorf("PurgeModule: %d rows remain in %q for module %q", n, tbl, root)
		}
	}
}

func TestIndexBatchCommit(t *testing.T) {
	s := openTest(t)
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	root := "example.com/batch"
	if err := s.UpsertModule(ctx, root); err != nil {
		t.Fatalf("UpsertModule: %v", err)
	}

	batch, err := s.IndexBatch(ctx, root)
	if err != nil {
		t.Fatalf("IndexBatch: %v", err)
	}

	sym := store.Symbol{
		ModuleRoot: root, PackagePath: root + "/pkg", PackageName: "pkg",
		FilePath: "foo.go", Name: "DoIt", Kind: "func", Recv: "",
		Signature: "func()", FQName: root + "/pkg.DoIt",
		Exported: true, Line: 10, Col: 1,
	}
	if err := batch.InsertSymbol(sym); err != nil {
		t.Fatalf("InsertSymbol: %v", err)
	}

	call := store.Call{
		ModuleRoot: root, PackagePath: root + "/pkg", FilePath: "foo.go",
		Line: 11, Col: 2,
		FromFQName: root + "/pkg.Main", ToFQName: root + "/pkg.DoIt",
		CalleeExpr: "DoIt", Kind: "call",
	}
	if err := batch.InsertCall(call); err != nil {
		t.Fatalf("InsertCall: %v", err)
	}

	if err := batch.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	db := sqlite.UnwrapDB(s)
	var symCount, callCount int
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM symbols WHERE module_root = ?`, root).Scan(&symCount)
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM calls WHERE module_root = ?`, root).Scan(&callCount)
	if symCount != 1 {
		t.Errorf("expected 1 symbol after Commit, got %d", symCount)
	}
	if callCount != 1 {
		t.Errorf("expected 1 call after Commit, got %d", callCount)
	}
}

func TestIndexBatchRollback(t *testing.T) {
	s := openTest(t)
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	root := "example.com/rollback"
	if err := s.UpsertModule(ctx, root); err != nil {
		t.Fatalf("UpsertModule: %v", err)
	}

	batch, err := s.IndexBatch(ctx, root)
	if err != nil {
		t.Fatalf("IndexBatch: %v", err)
	}
	batch.InsertSymbol(store.Symbol{
		ModuleRoot: root, PackagePath: root + "/pkg", PackageName: "pkg",
		FilePath: "f.go", Name: "X", Kind: "func", FQName: root + "/pkg.X",
		Line: 1,
	})
	if err := batch.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	db := sqlite.UnwrapDB(s)
	var n int
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM symbols WHERE module_root = ?`, root).Scan(&n)
	if n != 0 {
		t.Errorf("expected 0 symbols after Rollback, got %d", n)
	}
}

func TestInsertIndexMeta(t *testing.T) {
	s := openTest(t)
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	meta := store.IndexMeta{
		ToolVersion:   "0.2.0",
		GoVersion:     "go1.26",
		IndexedAt:     time.Now().UTC().Format(time.RFC3339),
		Root:          "example.com/meta",
		Warnings:      2,
		IndexedCommit: "abc123",
	}
	if err := s.InsertIndexMeta(ctx, meta); err != nil {
		t.Fatalf("InsertIndexMeta: %v", err)
	}
	db := sqlite.UnwrapDB(s)
	var toolVer, commit string
	db.QueryRowContext(ctx, `SELECT tool_version, indexed_commit FROM index_meta ORDER BY id DESC LIMIT 1`).Scan(&toolVer, &commit)
	if toolVer != "0.2.0" {
		t.Errorf("tool_version: got %q, want %q", toolVer, "0.2.0")
	}
	if commit != "abc123" {
		t.Errorf("indexed_commit: got %q, want %q", commit, "abc123")
	}
}

// ---- ReadStore — callers / callees / blast-radius ----

// openIndexed opens a fresh store, migrates it, and indexes testdata/samplemod
// (without test files). Returns the populated Store; caller must Close it via
// t.Cleanup (already registered inside openTest).
func openIndexed(t *testing.T, withTests bool) store.Store {
	t.Helper()
	s := openTest(t)
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	moduleRoot, err := filepath.Abs(filepath.Join("..", "..", "testdata", "samplemod"))
	if err != nil {
		t.Fatalf("abs samplemod: %v", err)
	}
	db := sqlite.UnwrapDB(s)
	if err := indexer.ResetSchema(db); err != nil {
		t.Fatalf("ResetSchema: %v", err)
	}
	if _, _, _, _, err := indexer.IndexModule(db, moduleRoot, false, withTests); err != nil {
		t.Fatalf("IndexModule: %v", err)
	}
	return s
}

// ---- DirectCallers ----

func TestDirectCallersBasic(t *testing.T) {
	s := openIndexed(t, false)
	// alpha.Top is a direct caller of alpha.*Store.AddObservation
	rows, err := s.DirectCallers(ctx, []string{"example.com/samplemod/alpha.*Store.AddObservation"}, "", 200)
	if err != nil {
		t.Fatalf("DirectCallers: %v", err)
	}
	found := false
	for _, r := range rows {
		if strings.Contains(r.From, "alpha.Top") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected alpha.Top as direct caller of AddObservation; got %+v", rows)
	}
}

func TestDirectCallersEmptyTargets(t *testing.T) {
	s := openIndexed(t, false)
	rows, err := s.DirectCallers(ctx, nil, "", 200)
	if err != nil {
		t.Fatalf("DirectCallers nil targets: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows for nil targets; got %d", len(rows))
	}
}

func TestDirectCallersPkgFilter(t *testing.T) {
	s := openIndexed(t, false)
	// Filter to only callers in the beta package.
	rows, err := s.DirectCallers(ctx, []string{"example.com/samplemod/alpha.Top"}, "example.com/samplemod/beta", 200)
	if err != nil {
		t.Fatalf("DirectCallers with pkg: %v", err)
	}
	for _, r := range rows {
		if !strings.HasPrefix(r.From, "example.com/samplemod/beta") {
			t.Errorf("pkg filter: got caller outside beta: %q", r.From)
		}
	}
	// beta.Use calls alpha.Top, so there must be at least one result.
	if len(rows) == 0 {
		t.Error("expected at least one caller in beta of alpha.Top")
	}
}

func TestDirectCallersMultipleTargets(t *testing.T) {
	s := openIndexed(t, false)
	// Both AddObservation and Top should get callers when asked together.
	rows, err := s.DirectCallers(ctx, []string{
		"example.com/samplemod/alpha.*Store.AddObservation",
		"example.com/samplemod/alpha.Top",
	}, "", 200)
	if err != nil {
		t.Fatalf("DirectCallers multi-target: %v", err)
	}
	haveAddObs := false
	haveTop := false
	for _, r := range rows {
		if r.To == "example.com/samplemod/alpha.*Store.AddObservation" {
			haveAddObs = true
		}
		if r.To == "example.com/samplemod/alpha.Top" {
			haveTop = true
		}
	}
	if !haveAddObs {
		t.Error("expected caller of AddObservation in multi-target result")
	}
	if !haveTop {
		t.Error("expected caller of alpha.Top (beta.Use) in multi-target result")
	}
}

// ---- CountDirectCallers ----

func TestCountDirectCallersNonzero(t *testing.T) {
	s := openIndexed(t, false)
	n, err := s.CountDirectCallers(ctx, "example.com/samplemod/alpha.*Store.AddObservation")
	if err != nil {
		t.Fatalf("CountDirectCallers: %v", err)
	}
	if n == 0 {
		t.Error("expected non-zero caller count for AddObservation")
	}
}

func TestCountDirectCallersZeroForDead(t *testing.T) {
	s := openIndexed(t, false)
	n, err := s.CountDirectCallers(ctx, "example.com/samplemod/recursive.neverCalled")
	if err != nil {
		t.Fatalf("CountDirectCallers dead: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 callers for neverCalled; got %d", n)
	}
}

// ---- FuzzyCallTargets ----

func TestFuzzyCallTargets(t *testing.T) {
	s := openIndexed(t, false)
	// "AddObservation" should appear as a fuzzy hit.
	targets, err := s.FuzzyCallTargets(ctx, "AddObservation")
	if err != nil {
		t.Fatalf("FuzzyCallTargets: %v", err)
	}
	found := false
	for _, tgt := range targets {
		if strings.Contains(tgt, "AddObservation") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected AddObservation in fuzzy targets; got %v", targets)
	}
}

func TestFuzzyCallTargetsExcludesExact(t *testing.T) {
	s := openIndexed(t, false)
	sym := "example.com/samplemod/alpha.*Store.AddObservation"
	targets, err := s.FuzzyCallTargets(ctx, sym)
	if err != nil {
		t.Fatalf("FuzzyCallTargets exact exclusion: %v", err)
	}
	for _, tgt := range targets {
		if tgt == sym {
			t.Errorf("FuzzyCallTargets must not return the exact symbol itself; got %q", tgt)
		}
	}
}

// ---- UnresolvedCallers ----

func TestUnresolvedCallersReturnsRows(t *testing.T) {
	s := openIndexed(t, false)
	// We don't assert a specific count here because unresolved calls are
	// implementation-dependent, but the method must not error.
	_, err := s.UnresolvedCallers(ctx, "example.com/samplemod/alpha.Top", false, 200)
	if err != nil {
		t.Fatalf("UnresolvedCallers: %v", err)
	}
}

// ---- DirectCallees ----

func TestDirectCalleesBasic(t *testing.T) {
	s := openIndexed(t, false)
	rows, err := s.DirectCallees(ctx, store.CalleesOpts{
		Symbol: "example.com/samplemod/alpha.Top",
		Limit:  200,
	})
	if err != nil {
		t.Fatalf("DirectCallees: %v", err)
	}
	found := false
	for _, r := range rows {
		if strings.Contains(r.FQName, "AddObservation") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected AddObservation in callees of alpha.Top; got %+v", rows)
	}
}

func TestDirectCalleesUnique(t *testing.T) {
	s := openIndexed(t, false)
	rows, err := s.DirectCallees(ctx, store.CalleesOpts{
		Symbol: "example.com/samplemod/concurrent.*SafeCounter.Inc",
		Unique: true,
		Limit:  200,
	})
	if err != nil {
		t.Fatalf("DirectCallees unique: %v", err)
	}
	// Unique mode must not have duplicate FQNames.
	seen := map[string]bool{}
	for _, r := range rows {
		if seen[r.FQName] {
			t.Errorf("duplicate callee in unique mode: %q", r.FQName)
		}
		seen[r.FQName] = true
	}
	// sync.*Mutex.Lock and Unlock must be present.
	if !seen["sync.*Mutex.Lock"] {
		t.Errorf("expected sync.*Mutex.Lock in unique callees of SafeCounter.Inc; got %v", seen)
	}
}

func TestDirectCalleesPkgFilter(t *testing.T) {
	s := openIndexed(t, false)
	rows, err := s.DirectCallees(ctx, store.CalleesOpts{
		Symbol: "example.com/samplemod/alpha.Top",
		Pkg:    "example.com/samplemod/alpha",
		Limit:  200,
	})
	if err != nil {
		t.Fatalf("DirectCallees pkg filter: %v", err)
	}
	for _, r := range rows {
		if !strings.HasPrefix(r.FQName, "example.com/samplemod/alpha") {
			t.Errorf("pkg filter: callee outside alpha: %q", r.FQName)
		}
	}
}

// ---- CountCallees ----

func TestCountCalleesNonzero(t *testing.T) {
	s := openIndexed(t, false)
	n, err := s.CountCallees(ctx, store.CalleesOpts{
		Symbol: "example.com/samplemod/alpha.Top",
		Limit:  200,
	})
	if err != nil {
		t.Fatalf("CountCallees: %v", err)
	}
	if n == 0 {
		t.Error("expected non-zero callee count for alpha.Top")
	}
}

func TestCountCalleesUniqueVsAll(t *testing.T) {
	s := openIndexed(t, false)
	all, err := s.CountCallees(ctx, store.CalleesOpts{
		Symbol: "example.com/samplemod/concurrent.*SafeCounter.Inc",
		Limit:  200,
	})
	if err != nil {
		t.Fatalf("CountCallees all: %v", err)
	}
	uniq, err := s.CountCallees(ctx, store.CalleesOpts{
		Symbol: "example.com/samplemod/concurrent.*SafeCounter.Inc",
		Unique: true,
		Limit:  200,
	})
	if err != nil {
		t.Fatalf("CountCallees unique: %v", err)
	}
	// Unique count must be <= all count.
	if uniq > all {
		t.Errorf("unique count (%d) must be <= all count (%d)", uniq, all)
	}
}

// ---- BlastRadius ----

func TestBlastRadiusDirectCallers(t *testing.T) {
	s := openIndexed(t, false)
	rows, err := s.BlastRadius(ctx, store.BlastRadiusOpts{
		Symbol: "example.com/samplemod/alpha.Top",
		Depth:  1,
		Limit:  500,
	})
	if err != nil {
		t.Fatalf("BlastRadius: %v", err)
	}
	found := false
	for _, r := range rows {
		if strings.Contains(r.FQName, "beta.Use") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected beta.Use as depth-1 caller of alpha.Top; got %+v", rows)
	}
}

func TestBlastRadiusTransitive(t *testing.T) {
	s := openIndexed(t, false)
	rows, err := s.BlastRadius(ctx, store.BlastRadiusOpts{
		Symbol: "example.com/samplemod/alpha.*Store.AddObservation",
		Depth:  2,
		Limit:  500,
	})
	if err != nil {
		t.Fatalf("BlastRadius transitive: %v", err)
	}
	depthByFQ := map[string]int{}
	for _, r := range rows {
		depthByFQ[r.FQName] = r.Depth
	}
	// alpha.Top at depth 1, beta.Use at depth 2.
	foundTop := false
	for fq, d := range depthByFQ {
		if strings.Contains(fq, "alpha.Top") && d == 1 {
			foundTop = true
		}
	}
	if !foundTop {
		t.Errorf("expected alpha.Top at depth 1; got %v", depthByFQ)
	}
	foundUse := false
	for fq, d := range depthByFQ {
		if strings.Contains(fq, "beta.Use") && d == 2 {
			foundUse = true
		}
	}
	if !foundUse {
		t.Errorf("expected beta.Use at depth 2; got %v", depthByFQ)
	}
}

func TestBlastRadiusEmptyForDeadCode(t *testing.T) {
	s := openIndexed(t, false)
	rows, err := s.BlastRadius(ctx, store.BlastRadiusOpts{
		Symbol: "example.com/samplemod/recursive.neverCalled",
		Depth:  3,
		Limit:  500,
	})
	if err != nil {
		t.Fatalf("BlastRadius dead code: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected empty blast-radius for neverCalled; got %v", rows)
	}
}

func TestBlastRadiusExcludeTests(t *testing.T) {
	s := openIndexed(t, true)
	// With tests, TestTopAlpha calls alpha.Top.
	withTests, err := s.BlastRadius(ctx, store.BlastRadiusOpts{
		Symbol: "example.com/samplemod/alpha.Top",
		Depth:  3,
		Limit:  500,
	})
	if err != nil {
		t.Fatalf("BlastRadius with tests: %v", err)
	}
	foundTest := false
	for _, r := range withTests {
		if strings.Contains(r.FQName, "TestTopAlpha") {
			foundTest = true
		}
	}
	if !foundTest {
		t.Errorf("expected TestTopAlpha in blast-radius with test files indexed; got %+v", withTests)
	}

	// With ExcludeTests, TestTopAlpha must vanish.
	withoutTests, err := s.BlastRadius(ctx, store.BlastRadiusOpts{
		Symbol:       "example.com/samplemod/alpha.Top",
		Depth:        3,
		ExcludeTests: true,
		Limit:        500,
	})
	if err != nil {
		t.Fatalf("BlastRadius exclude tests: %v", err)
	}
	for _, r := range withoutTests {
		if strings.Contains(r.FQName, "TestTopAlpha") {
			t.Errorf("TestTopAlpha must not appear with ExcludeTests=true; got %+v", withoutTests)
		}
	}
}

func TestBlastRadiusFuzzy(t *testing.T) {
	s := openIndexed(t, false)
	// Fuzzy on "AddObservation" should seed all symbols matching the substring.
	rows, err := s.BlastRadius(ctx, store.BlastRadiusOpts{
		Symbol: "AddObservation",
		Fuzzy:  true,
		Depth:  2,
		Limit:  500,
	})
	if err != nil {
		t.Fatalf("BlastRadius fuzzy: %v", err)
	}
	// alpha.Top and beta.Use should appear in the transitive set.
	found := false
	for _, r := range rows {
		if strings.Contains(r.FQName, "beta.Use") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected beta.Use in fuzzy blast-radius for AddObservation; got %+v", rows)
	}
}

// ---- FindSymbols / CountSymbols ----

func TestFindSymbolsByQuery(t *testing.T) {
	s := openIndexed(t, false)
	result, err := s.FindSymbols(ctx, store.FindOpts{Query: "AddObservation", Limit: 100})
	if err != nil {
		t.Fatalf("FindSymbols: %v", err)
	}
	if len(result.Symbols) == 0 {
		t.Fatal("expected at least one symbol matching 'AddObservation'")
	}
	found := false
	for _, sym := range result.Symbols {
		if strings.Contains(sym.FQName, "AddObservation") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected AddObservation in results; got %+v", result.Symbols)
	}
	if result.TotalMatched == 0 {
		t.Errorf("TotalMatched should be > 0")
	}
}

func TestFindSymbolsByPkg(t *testing.T) {
	s := openIndexed(t, false)
	result, err := s.FindSymbols(ctx, store.FindOpts{Pkg: "example.com/samplemod/alpha", Limit: 100})
	if err != nil {
		t.Fatalf("FindSymbols by pkg: %v", err)
	}
	if len(result.Symbols) == 0 {
		t.Fatal("expected symbols in alpha package")
	}
	for _, sym := range result.Symbols {
		if !strings.HasPrefix(sym.PackagePath, "example.com/samplemod/alpha") {
			t.Errorf("unexpected package %q in results", sym.PackagePath)
		}
	}
}

func TestFindSymbolsByKind(t *testing.T) {
	s := openIndexed(t, false)
	result, err := s.FindSymbols(ctx, store.FindOpts{
		Pkg:   "example.com/samplemod/alpha",
		Kind:  "func",
		Limit: 100,
	})
	if err != nil {
		t.Fatalf("FindSymbols by kind: %v", err)
	}
	for _, sym := range result.Symbols {
		if sym.Kind != "func" && sym.Kind != "method" {
			// kind filter is exact, so only "func" should appear (not "method")
		}
		if sym.Kind != "func" {
			t.Errorf("expected kind=func, got %q for %s", sym.Kind, sym.FQName)
		}
	}
}

func TestFindSymbolsShortName(t *testing.T) {
	s := openIndexed(t, false)
	result, err := s.FindSymbols(ctx, store.FindOpts{Query: "Top", Limit: 100})
	if err != nil {
		t.Fatalf("FindSymbols: %v", err)
	}
	for _, sym := range result.Symbols {
		if sym.Name == "" {
			t.Errorf("Name field empty for %q", sym.FQName)
		}
	}
}

func TestCountSymbols(t *testing.T) {
	s := openIndexed(t, false)
	n, err := s.CountSymbols(ctx, store.FindOpts{Pkg: "example.com/samplemod/alpha"})
	if err != nil {
		t.Fatalf("CountSymbols: %v", err)
	}
	if n == 0 {
		t.Error("expected > 0 symbols in alpha")
	}
}

func TestCountSymbolsAll(t *testing.T) {
	s := openIndexed(t, false)
	// Empty opts → no WHERE filter except 1=1, should return all symbols.
	// But alpha.go has Store, AddObservation, Top, Answer, Global → at least 5.
	n, err := s.CountSymbols(ctx, store.FindOpts{Limit: 100})
	if err != nil {
		t.Fatalf("CountSymbols all: %v", err)
	}
	if n < 5 {
		t.Errorf("expected at least 5 total symbols, got %d", n)
	}
}

// ---- DefSymbol ----

func TestDefSymbolByFQName(t *testing.T) {
	s := openIndexed(t, false)
	results, err := s.DefSymbol(ctx, "example.com/samplemod/alpha.Top", "")
	if err != nil {
		t.Fatalf("DefSymbol by fqname: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for exact fqname, got %d", len(results))
	}
	if results[0].FQName != "example.com/samplemod/alpha.Top" {
		t.Errorf("unexpected fqname %q", results[0].FQName)
	}
	if results[0].Kind != "func" {
		t.Errorf("expected kind=func, got %q", results[0].Kind)
	}
}

func TestDefSymbolByName(t *testing.T) {
	s := openIndexed(t, false)
	results, err := s.DefSymbol(ctx, "Top", "")
	if err != nil {
		t.Fatalf("DefSymbol by name: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result for name 'Top'")
	}
	found := false
	for _, r := range results {
		if strings.Contains(r.FQName, "alpha.Top") {
			found = true
		}
	}
	if !found {
		t.Errorf("alpha.Top not found among results: %+v", results)
	}
}

func TestDefSymbolNotFound(t *testing.T) {
	s := openIndexed(t, false)
	results, err := s.DefSymbol(ctx, "DoesNotExistXYZ", "")
	if err != nil {
		t.Fatalf("DefSymbol not found: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestDefSymbolWithPkg(t *testing.T) {
	s := openIndexed(t, false)
	// "Do" is a method on RealDoer and StubDoer in the iface package.
	results, err := s.DefSymbol(ctx, "Do", "example.com/samplemod/iface")
	if err != nil {
		t.Fatalf("DefSymbol with pkg: %v", err)
	}
	for _, r := range results {
		if !strings.HasPrefix(r.PackagePath, "example.com/samplemod/iface") {
			t.Errorf("got result outside iface pkg: %q", r.PackagePath)
		}
	}
}

// ---- ListPackages ----

func TestListPackages(t *testing.T) {
	s := openIndexed(t, false)
	pkgs, err := s.ListPackages(ctx)
	if err != nil {
		t.Fatalf("ListPackages: %v", err)
	}
	if len(pkgs) == 0 {
		t.Fatal("expected at least one package")
	}
	// Verify alpha is present and has sane counts.
	var alpha *store.PackageRow
	for i := range pkgs {
		if pkgs[i].Path == "example.com/samplemod/alpha" {
			alpha = &pkgs[i]
			break
		}
	}
	if alpha == nil {
		t.Fatalf("alpha package not found in results; got %+v", pkgs)
	}
	if alpha.SymbolCount == 0 {
		t.Error("alpha symbol_count should be > 0")
	}
	if alpha.FuncCount == 0 {
		t.Error("alpha func_count should be > 0 (Top is a func)")
	}
}

func TestListPackagesOrdered(t *testing.T) {
	s := openIndexed(t, false)
	pkgs, err := s.ListPackages(ctx)
	if err != nil {
		t.Fatalf("ListPackages: %v", err)
	}
	for i := 1; i < len(pkgs); i++ {
		if pkgs[i].Path < pkgs[i-1].Path {
			t.Errorf("packages not sorted: %q before %q", pkgs[i-1].Path, pkgs[i].Path)
		}
	}
}

// ---- HealthStats ----

func TestHealthStats(t *testing.T) {
	s := openIndexed(t, false)

	// Insert a fake index_meta row (indexer.IndexModule doesn't insert one).
	db := sqlite.UnwrapDB(s)
	if _, err := db.ExecContext(ctx,
		`INSERT INTO index_meta(tool_version, go_version, indexed_at, root, warnings, indexed_commit)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		"0.test", "go1.26", "2026-01-01T00:00:00Z", "/tmp/samplemod", 0, ""); err != nil {
		t.Fatalf("insert index_meta: %v", err)
	}

	h, err := s.HealthStats(ctx)
	if err != nil {
		t.Fatalf("HealthStats: %v", err)
	}
	if h == nil {
		t.Fatal("expected non-nil HealthResult")
	}
	if h.ToolVersion != "0.test" {
		t.Errorf("tool_version: got %q, want %q", h.ToolVersion, "0.test")
	}
	if h.SymbolCount == 0 {
		t.Error("SymbolCount should be > 0 after indexing samplemod")
	}
}

func TestHealthStatsNoMeta(t *testing.T) {
	s := openTest(t)
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	// No index_meta rows → must return an error.
	_, err := s.HealthStats(ctx)
	if err == nil {
		t.Error("expected error when index_meta is empty, got nil")
	}
}

// ---- FindImplementors ----

func TestFindImplementorsIface(t *testing.T) {
	s := openIndexed(t, false)
	// iface.Doer is implemented by RealDoer and StubDoer.
	rows, err := s.FindImplementors(ctx, store.ImplementorsOpts{
		Iface: "example.com/samplemod/iface.Doer",
		Limit: 100,
	})
	if err != nil {
		t.Fatalf("FindImplementors: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected at least one implementor of iface.Doer")
	}
	for _, r := range rows {
		if !strings.Contains(r.Iface, "Doer") {
			t.Errorf("unexpected iface %q in results", r.Iface)
		}
	}
}

func TestFindImplementorsType(t *testing.T) {
	s := openIndexed(t, false)
	// Search by type: what interfaces does RealDoer implement?
	rows, err := s.FindImplementors(ctx, store.ImplementorsOpts{
		Type:  "example.com/samplemod/iface.RealDoer",
		Limit: 100,
	})
	if err != nil {
		t.Fatalf("FindImplementors by type: %v", err)
	}
	// May be empty if no implements rows for RealDoer, that's fine.
	// The call must succeed without error.
	_ = rows
}

func TestFindImplementorsMissingOpts(t *testing.T) {
	s := openIndexed(t, false)
	_, err := s.FindImplementors(ctx, store.ImplementorsOpts{})
	if err == nil {
		t.Error("expected error when neither Iface nor Type is set")
	}
}

// ---- FindReferences / CountReferences ----

func TestFindReferences(t *testing.T) {
	s := openIndexed(t, false)
	// typerefs package has type_assert/type_switch/composite_lit refs to iface.RealDoer.
	result, err := s.FindReferences(ctx, store.ReferencesOpts{
		Symbol: "example.com/samplemod/iface.RealDoer",
		Limit:  200,
	})
	if err != nil {
		t.Fatalf("FindReferences: %v", err)
	}
	if len(result.Refs) == 0 {
		t.Fatal("expected at least one type reference to iface.RealDoer")
	}
	for _, r := range result.Refs {
		if r.ToFQName != "example.com/samplemod/iface.RealDoer" {
			t.Errorf("unexpected ToFQName %q", r.ToFQName)
		}
	}
}

func TestFindReferencesByKind(t *testing.T) {
	s := openIndexed(t, false)
	result, err := s.FindReferences(ctx, store.ReferencesOpts{
		Symbol:  "example.com/samplemod/iface.RealDoer",
		RefKind: "type_assert",
		Limit:   200,
	})
	if err != nil {
		t.Fatalf("FindReferences by ref_kind: %v", err)
	}
	for _, r := range result.Refs {
		if r.RefKind != "type_assert" {
			t.Errorf("expected ref_kind=type_assert, got %q", r.RefKind)
		}
	}
}

func TestCountReferences(t *testing.T) {
	s := openIndexed(t, false)
	n, err := s.CountReferences(ctx, store.ReferencesOpts{
		Symbol: "example.com/samplemod/iface.RealDoer",
	})
	if err != nil {
		t.Fatalf("CountReferences: %v", err)
	}
	if n == 0 {
		t.Error("expected > 0 references to iface.RealDoer")
	}
}

func TestCountReferencesZero(t *testing.T) {
	s := openIndexed(t, false)
	n, err := s.CountReferences(ctx, store.ReferencesOpts{
		Symbol: "example.com/samplemod/doesnotexist.Nope",
	})
	if err != nil {
		t.Fatalf("CountReferences zero: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 references, got %d", n)
	}
}

// ---- Phase 3: dead, trace, hint, HasFileTracking, stale ----

// seedPhase3 opens a fresh store, migrates it, and inserts minimal fixture
// data for all Phase 3 ReadStore tests. Returns the store and module root.
func seedPhase3(t *testing.T) (store.Store, string) {
	t.Helper()
	s := openTest(t)
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("seedPhase3 Migrate: %v", err)
	}
	root := "example.com/p3"
	pkg := root + "/alpha"
	deadPkg := root + "/dead"

	if err := s.UpsertModule(ctx, root); err != nil {
		t.Fatalf("seedPhase3 UpsertModule: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)

	// index_meta with known commit
	if err := s.InsertIndexMeta(ctx, store.IndexMeta{
		ToolVersion: "test", GoVersion: "go1.26",
		IndexedAt: now, Root: root, IndexedCommit: "deadbeef",
	}); err != nil {
		t.Fatalf("seedPhase3 InsertIndexMeta: %v", err)
	}

	// package_meta + package_files (file path intentionally non-existent on disk)
	if err := s.UpsertPackageMeta(ctx, root, pkg, now); err != nil {
		t.Fatalf("seedPhase3 UpsertPackageMeta: %v", err)
	}
	if err := s.UpsertPackageFile(ctx, root, pkg, "/nonexistent/foo.go", "aabbccdd"); err != nil {
		t.Fatalf("seedPhase3 UpsertPackageFile: %v", err)
	}
	if err := s.UpdatePackageFilesHash(ctx, root, pkg, "hashABC"); err != nil {
		t.Fatalf("seedPhase3 UpdatePackageFilesHash: %v", err)
	}

	batch, err := s.IndexBatch(ctx, root)
	if err != nil {
		t.Fatalf("seedPhase3 IndexBatch: %v", err)
	}
	// calledFunc — has a caller, must NOT be in dead
	_ = batch.InsertSymbol(store.Symbol{
		ModuleRoot: root, PackagePath: pkg, PackageName: "alpha",
		FilePath: "/src/alpha/foo.go", Name: "calledFunc", Kind: "func",
		FQName: pkg + ".calledFunc", Exported: false, Line: 10, Col: 1,
	})
	// deadFunc — no caller, unexported func → must appear in DeadSymbols
	_ = batch.InsertSymbol(store.Symbol{
		ModuleRoot: root, PackagePath: deadPkg, PackageName: "dead",
		FilePath: "/src/dead/bar.go", Name: "deadFunc", Kind: "func",
		FQName: deadPkg + ".deadFunc", Exported: false, Line: 5, Col: 1,
	})
	// ExportedDead — exported, no caller → appears only with IncludeExported
	_ = batch.InsertSymbol(store.Symbol{
		ModuleRoot: root, PackagePath: deadPkg, PackageName: "dead",
		FilePath: "/src/dead/bar.go", Name: "ExportedDead", Kind: "func",
		FQName: deadPkg + ".ExportedDead", Exported: true, Line: 20, Col: 1,
	})
	// call edge: alpha.Caller → alpha.calledFunc
	_ = batch.InsertCall(store.Call{
		ModuleRoot: root, PackagePath: pkg, FilePath: "/src/alpha/foo.go",
		Line: 15, Col: 5,
		FromFQName: pkg + ".Caller", ToFQName: pkg + ".calledFunc",
		CalleeExpr: "calledFunc", Kind: "call",
	})
	// implements row: alpha.MyImpl satisfies alpha.MyIface
	_ = batch.InsertImplements(store.Implements{
		ModuleRoot: root,
		IfacePkg:   pkg, IfaceFQName: pkg + ".MyIface",
		ImplPkg: pkg, ImplFQName: pkg + ".MyImpl",
		IsPointer: true,
	})
	if err := batch.Commit(); err != nil {
		t.Fatalf("seedPhase3 batch.Commit: %v", err)
	}
	return s, root
}

// ---- DeadSymbols ----

func TestDeadSymbolsBasic(t *testing.T) {
	s, root := seedPhase3(t)
	deadPkg := root + "/dead"
	res, err := s.DeadSymbols(ctx, store.DeadOpts{Limit: 100})
	if err != nil {
		t.Fatalf("DeadSymbols: %v", err)
	}
	foundDead := false
	for _, sym := range res.Symbols {
		if sym.FQName == deadPkg+".deadFunc" {
			foundDead = true
		}
		if sym.FQName == root+"/alpha.calledFunc" {
			t.Errorf("calledFunc must not appear — it has a caller")
		}
		if sym.FQName == deadPkg+".ExportedDead" {
			t.Errorf("ExportedDead must not appear without IncludeExported")
		}
	}
	if !foundDead {
		t.Errorf("deadFunc not in DeadSymbols results; got %+v", res.Symbols)
	}
	if res.TotalMatched < 1 {
		t.Errorf("TotalMatched should be >= 1, got %d", res.TotalMatched)
	}
}

func TestDeadSymbolsIncludeExported(t *testing.T) {
	s, root := seedPhase3(t)
	deadPkg := root + "/dead"
	res, err := s.DeadSymbols(ctx, store.DeadOpts{Limit: 100, IncludeExported: true})
	if err != nil {
		t.Fatalf("DeadSymbols IncludeExported: %v", err)
	}
	found := false
	for _, sym := range res.Symbols {
		if sym.FQName == deadPkg+".ExportedDead" {
			found = true
		}
	}
	if !found {
		t.Errorf("ExportedDead should appear with IncludeExported=true; got %+v", res.Symbols)
	}
}

func TestDeadSymbolsPkgFilter(t *testing.T) {
	s, root := seedPhase3(t)
	deadPkg := root + "/dead"
	res, err := s.DeadSymbols(ctx, store.DeadOpts{Limit: 100, Pkg: deadPkg})
	if err != nil {
		t.Fatalf("DeadSymbols Pkg filter: %v", err)
	}
	for _, sym := range res.Symbols {
		if !strings.HasPrefix(sym.PackagePath, deadPkg) {
			t.Errorf("pkg filter returned out-of-pkg symbol: %q", sym.FQName)
		}
	}
}

func TestDeadSymbolsKindFilter(t *testing.T) {
	s, _ := seedPhase3(t)
	res, err := s.DeadSymbols(ctx, store.DeadOpts{Limit: 100, Kind: "func"})
	if err != nil {
		t.Fatalf("DeadSymbols Kind filter: %v", err)
	}
	for _, sym := range res.Symbols {
		if sym.Kind != "func" {
			t.Errorf("kind filter returned non-func: %q kind=%q", sym.FQName, sym.Kind)
		}
	}
}

func TestDeadSymbolsLimit(t *testing.T) {
	s, _ := seedPhase3(t)
	res, err := s.DeadSymbols(ctx, store.DeadOpts{Limit: 1})
	if err != nil {
		t.Fatalf("DeadSymbols limit=1: %v", err)
	}
	if len(res.Symbols) > 1 {
		t.Errorf("expected <= 1 symbol with limit=1, got %d", len(res.Symbols))
	}
}

// ---- TraceSymbol ----

func TestTraceSymbolFound(t *testing.T) {
	s, root := seedPhase3(t)
	pkg := root + "/alpha"
	res, err := s.TraceSymbol(ctx, pkg+".calledFunc", 20, 20)
	if err != nil {
		t.Fatalf("TraceSymbol: %v", err)
	}
	if res == nil {
		t.Fatal("TraceSymbol returned nil for known symbol")
	}
	if res.Symbol == nil || res.Symbol.FQName != pkg+".calledFunc" {
		t.Errorf("Symbol.FQName mismatch: %+v", res.Symbol)
	}
	callerFound := false
	for _, c := range res.Callers {
		if c.From == pkg+".Caller" {
			callerFound = true
		}
	}
	if !callerFound {
		t.Errorf("expected Caller in callers; got %+v", res.Callers)
	}
}

func TestTraceSymbolNotFound(t *testing.T) {
	s, root := seedPhase3(t)
	res, err := s.TraceSymbol(ctx, root+"/alpha.NoSuchFunc", 20, 20)
	if err != nil {
		t.Fatalf("TraceSymbol for unknown symbol returned error: %v", err)
	}
	if res != nil {
		t.Errorf("expected nil for unknown symbol; got %+v", res)
	}
}

func TestTraceSymbolBlastTotal(t *testing.T) {
	s, root := seedPhase3(t)
	pkg := root + "/alpha"
	res, err := s.TraceSymbol(ctx, pkg+".calledFunc", 20, 20)
	if err != nil {
		t.Fatalf("TraceSymbol: %v", err)
	}
	if res == nil {
		t.Fatal("nil result")
	}
	if res.BlastTotal < 1 {
		t.Errorf("BlastTotal should be >= 1; got %d", res.BlastTotal)
	}
}

// ---- hint methods ----

func TestResolveSymbolNameFound(t *testing.T) {
	s, root := seedPhase3(t)
	pkg := root + "/alpha"
	names, err := s.ResolveSymbolName(ctx, "calledFunc", "")
	if err != nil {
		t.Fatalf("ResolveSymbolName: %v", err)
	}
	found := false
	for _, n := range names {
		if n == pkg+".calledFunc" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected %q in ResolveSymbolName; got %v", pkg+".calledFunc", names)
	}
}

func TestResolveSymbolNamePkgFilter(t *testing.T) {
	s, root := seedPhase3(t)
	deadPkg := root + "/dead"
	names, err := s.ResolveSymbolName(ctx, "deadFunc", deadPkg)
	if err != nil {
		t.Fatalf("ResolveSymbolName with pkg: %v", err)
	}
	for _, n := range names {
		if !strings.HasPrefix(n, deadPkg) {
			t.Errorf("pkg filter returned out-of-pkg fqname: %q", n)
		}
	}
	if len(names) == 0 {
		t.Errorf("expected at least one result for deadFunc in %s", deadPkg)
	}
}

func TestResolveSymbolNameNotFound(t *testing.T) {
	s, _ := seedPhase3(t)
	names, err := s.ResolveSymbolName(ctx, "NoSuchFuncAtAll", "")
	if err != nil {
		t.Fatalf("ResolveSymbolName not found: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("expected no results for nonexistent name; got %v", names)
	}
}

func TestSymbolHintFound(t *testing.T) {
	s, root := seedPhase3(t)
	pkg := root + "/alpha"
	hints, err := s.SymbolHint(ctx, "calledF")
	if err != nil {
		t.Fatalf("SymbolHint: %v", err)
	}
	found := false
	for _, h := range hints {
		if h == pkg+".calledFunc" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected %q in SymbolHint; got %v", pkg+".calledFunc", hints)
	}
}

func TestSymbolHintNotFound(t *testing.T) {
	s, _ := seedPhase3(t)
	hints, err := s.SymbolHint(ctx, "zzz_no_match_zzz")
	if err != nil {
		t.Fatalf("SymbolHint not found: %v", err)
	}
	if len(hints) != 0 {
		t.Errorf("expected empty hints; got %v", hints)
	}
}

func TestInterfaceDispatchHintFound(t *testing.T) {
	s, root := seedPhase3(t)
	pkg := root + "/alpha"
	// MyImpl implements MyIface; hint for a method on MyImpl should surface MyIface.
	hints, err := s.InterfaceDispatchHint(ctx, pkg+".MyImpl.SomeMethod")
	if err != nil {
		t.Fatalf("InterfaceDispatchHint: %v", err)
	}
	found := false
	for _, h := range hints {
		if h == pkg+".MyIface" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected MyIface in InterfaceDispatchHint; got %v", hints)
	}
}

func TestInterfaceDispatchHintNotFound(t *testing.T) {
	s, _ := seedPhase3(t)
	hints, err := s.InterfaceDispatchHint(ctx, "no/such.Thing.Method")
	if err != nil {
		t.Fatalf("InterfaceDispatchHint not found: %v", err)
	}
	if len(hints) != 0 {
		t.Errorf("expected empty hints; got %v", hints)
	}
}

// ---- HasFileTracking ----

func TestHasFileTrackingTrue(t *testing.T) {
	s, _ := seedPhase3(t)
	has, err := s.HasFileTracking(ctx)
	if err != nil {
		t.Fatalf("HasFileTracking: %v", err)
	}
	if !has {
		t.Error("expected HasFileTracking=true after seeding package_files")
	}
}

func TestHasFileTrackingFalse(t *testing.T) {
	s := openTest(t)
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	has, err := s.HasFileTracking(ctx)
	if err != nil {
		t.Fatalf("HasFileTracking on empty DB: %v", err)
	}
	if has {
		t.Error("expected HasFileTracking=false on empty DB")
	}
}

// ---- Staleness detection ----

func TestIndexedCommit(t *testing.T) {
	s, _ := seedPhase3(t)
	commit, err := s.IndexedCommit(ctx)
	if err != nil {
		t.Fatalf("IndexedCommit: %v", err)
	}
	if commit != "deadbeef" {
		t.Errorf("IndexedCommit: got %q, want %q", commit, "deadbeef")
	}
}

func TestIndexedCommitEmptyDB(t *testing.T) {
	s := openTest(t)
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	commit, err := s.IndexedCommit(ctx)
	if err != nil {
		t.Fatalf("IndexedCommit empty DB: %v", err)
	}
	if commit != "" {
		t.Errorf("expected empty commit on empty DB; got %q", commit)
	}
}

func TestPackageFilesFound(t *testing.T) {
	s, root := seedPhase3(t)
	pkg := root + "/alpha"
	files, err := s.PackageFiles(ctx, root, pkg)
	if err != nil {
		t.Fatalf("PackageFiles: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 package file; got %d: %+v", len(files), files)
	}
	if files[0].FilePath != "/nonexistent/foo.go" {
		t.Errorf("FilePath mismatch: got %q", files[0].FilePath)
	}
	if files[0].FileHash != "aabbccdd" {
		t.Errorf("FileHash mismatch: got %q", files[0].FileHash)
	}
}

func TestPackageFilesEmpty(t *testing.T) {
	s, root := seedPhase3(t)
	files, err := s.PackageFiles(ctx, root, root+"/noexist")
	if err != nil {
		t.Fatalf("PackageFiles missing pkg: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 files for missing pkg; got %d", len(files))
	}
}

func TestStoredFilesHashFound(t *testing.T) {
	s, root := seedPhase3(t)
	pkg := root + "/alpha"
	h, err := s.StoredFilesHash(ctx, root, pkg)
	if err != nil {
		t.Fatalf("StoredFilesHash: %v", err)
	}
	if h != "hashABC" {
		t.Errorf("StoredFilesHash: got %q, want %q", h, "hashABC")
	}
}

func TestStoredFilesHashMissing(t *testing.T) {
	s, root := seedPhase3(t)
	h, err := s.StoredFilesHash(ctx, root, root+"/noexist")
	if err != nil {
		t.Fatalf("StoredFilesHash missing pkg: %v", err)
	}
	if h != "" {
		t.Errorf("expected empty hash for missing pkg; got %q", h)
	}
}

func TestAllPackagePaths(t *testing.T) {
	s, root := seedPhase3(t)
	pkg := root + "/alpha"
	rows, err := s.AllPackagePaths(ctx)
	if err != nil {
		t.Fatalf("AllPackagePaths: %v", err)
	}
	found := false
	for _, r := range rows {
		if r.ModuleRoot == root && r.PackagePath == pkg {
			found = true
		}
	}
	if !found {
		t.Errorf("expected (%s, %s) in AllPackagePaths; got %+v", root, pkg, rows)
	}
}

// ---- indexer store-backed variants ----

func TestStalePackagesStore(t *testing.T) {
	s, root := seedPhase3(t)
	pkg := root + "/alpha"
	stale, err := indexer.StalePackagesStore(s)
	if err != nil {
		t.Fatalf("StalePackagesStore: %v", err)
	}
	found := false
	for _, p := range stale {
		if p == pkg {
			found = true
		}
	}
	if !found {
		t.Errorf("expected %q in stale packages; got %v", pkg, stale)
	}
}

// TestFindSymbolsShortNameStripsPackagePrefix verifies that SymbolRow.Name is just
// the identifier after the package prefix — e.g. "Top" not "alpha.Top".
// Regression test for Codex PR-43 review (shortName in read.go only stripped the
// import-path slash, not the package prefix dot, so Name = "alpha.Top" not "Top").
func TestFindSymbolsShortNameStripsPackagePrefix(t *testing.T) {
	s := openIndexed(t, false)
	res, err := s.FindSymbols(ctx, store.FindOpts{Query: "Top", Pkg: "example.com/samplemod/alpha", Limit: 10})
	if err != nil {
		t.Fatalf("FindSymbols: %v", err)
	}
	if len(res.Symbols) == 0 {
		t.Fatal("no symbols found for Top")
	}
	for _, sym := range res.Symbols {
		if sym.FQName != "example.com/samplemod/alpha.Top" {
			continue
		}
		if sym.Name != "Top" {
			t.Errorf("Name = %q, want %q (shortName must strip package prefix)", sym.Name, "Top")
		}
		return
	}
	t.Error("did not find example.com/samplemod/alpha.Top")
}

// TestFindSymbolsShortNameMethod verifies that method names strip package+type
// context correctly: "*Store.AddObservation" not "alpha.*Store.AddObservation".
func TestFindSymbolsShortNameMethod(t *testing.T) {
	s := openIndexed(t, false)
	res, err := s.FindSymbols(ctx, store.FindOpts{Query: "AddObservation", Pkg: "example.com/samplemod/alpha", Limit: 10})
	if err != nil {
		t.Fatalf("FindSymbols: %v", err)
	}
	for _, sym := range res.Symbols {
		if sym.FQName != "example.com/samplemod/alpha.*Store.AddObservation" {
			continue
		}
		if sym.Name != "*Store.AddObservation" {
			t.Errorf("Name = %q, want %q", sym.Name, "*Store.AddObservation")
		}
		return
	}
	t.Error("did not find example.com/samplemod/alpha.*Store.AddObservation")
}

// TestRefRowHasExprField verifies that RefRow carries an Expr field, as declared
// in the agent_help.go JSON schema. Regression test for Codex PR-43 review.
// The type_refs table stores an expr column; it must be surfaced in RefRow.
func TestRefRowHasExprField(t *testing.T) {
	s := openIndexed(t, false)
	res, err := s.FindReferences(ctx, store.ReferencesOpts{Symbol: "example.com/samplemod/alpha.Store", Limit: 50})
	if err != nil {
		t.Fatalf("FindReferences: %v", err)
	}
	if len(res.Refs) == 0 {
		t.Skip("no refs for alpha.Store — cannot test Expr field population")
	}
	// The Expr field must be present on RefRow — this line will fail to compile
	// until Expr is added to store.RefRow and populated in read.go.
	for _, r := range res.Refs {
		_ = r.Expr // compile guard: Expr must be a field on store.RefRow
	}
}

// TestLegacyBootstrapAppliesMissingColumns verifies that EnsureSchema (called via
// Migrate) correctly adds columns that were added in additive migrations (v2-v4)
// when the database already has the symbols table (legacy bootstrap path) but was
// created without those columns.
//
// Regression test for Codex PR-43 review: the legacy fast-path stamped all
// migrations as applied without running the ALTER TABLE statements.
func TestLegacyBootstrapAppliesMissingColumns(t *testing.T) {
	s := openTest(t)
	db := sqlite.UnwrapDB(s)

	// Simulate a legacy database: create the symbols and calls tables in the
	// OLD schema (without the columns added by v2/v3/v4 migrations).
	legacyDDL := []string{
		`CREATE TABLE symbols (id INTEGER PRIMARY KEY, module_root TEXT, package_path TEXT,
		 package_name TEXT, file_path TEXT, name TEXT, kind TEXT, recv TEXT,
		 signature TEXT, fqname TEXT, exported INTEGER, line INTEGER, col INTEGER)`,
		`CREATE TABLE calls (id INTEGER PRIMARY KEY, module_root TEXT, package_path TEXT,
		 file_path TEXT, line INTEGER, col INTEGER, from_fqname TEXT, to_fqname TEXT,
		 callee_expr TEXT)`,
		// NOTE: calls is missing the 'kind' column (added by migration v2)
		`CREATE TABLE package_meta (id INTEGER PRIMARY KEY, module_root TEXT,
		 package_path TEXT, indexed_at TEXT, UNIQUE(module_root, package_path))`,
		// NOTE: package_meta is missing 'files_hash' (migration v3)
		`CREATE TABLE index_meta (id INTEGER PRIMARY KEY, tool_version TEXT,
		 go_version TEXT, indexed_at TEXT, root TEXT, warnings INTEGER DEFAULT 0)`,
		// NOTE: index_meta is missing 'indexed_commit' (migration v4)
	}
	for _, stmt := range legacyDDL {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("legacy DDL: %v", err)
		}
	}

	// Now run Migrate — must NOT error and must add the missing columns.
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate on legacy DB: %v", err)
	}

	// Verify the columns are now present by attempting to use them.
	checks := []struct {
		query string
		desc  string
	}{
		{`SELECT kind FROM calls LIMIT 1`, "calls.kind (migration v2)"},
		{`SELECT files_hash FROM package_meta LIMIT 1`, "package_meta.files_hash (migration v3)"},
		{`SELECT indexed_commit FROM index_meta LIMIT 1`, "index_meta.indexed_commit (migration v4)"},
	}
	for _, c := range checks {
		if _, err := db.Exec(c.query); err != nil {
			t.Errorf("column missing after Migrate: %s: %v", c.desc, err)
		}
	}
}

func TestMigrateDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "migratedb.sqlite")
	s, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	db := sqlite.UnwrapDB(s)
	if err := sqlite.MigrateDB(ctx, db); err != nil {
		t.Fatalf("MigrateDB: %v", err)
	}
	v, err2 := s.SchemaVersion(ctx)
	if err2 != nil {
		t.Fatalf("SchemaVersion: %v", err2)
	}
	if v < 1 {
		t.Errorf("expected schema version >= 1, got %d", v)
	}
	s.Close()
}
