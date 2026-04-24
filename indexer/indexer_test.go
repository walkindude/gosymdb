package indexer

import (
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/walkindude/gosymdb/store/sqlite"
)

// copyDir recursively copies src to dst, preserving directory structure.
func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copyDir %s -> %s: %v", src, dst, err)
	}
}

func TestIndexModuleSymbolsAndCalls(t *testing.T) {
	moduleRoot, err := filepath.Abs(filepath.Join("..", "testdata", "samplemod"))
	if err != nil {
		t.Fatalf("abs module root: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "symdb.sqlite")
	db, err := sql.Open(sqlite.DriverName, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := ResetSchema(db); err != nil {
		t.Fatalf("reset schema: %v", err)
	}

	symbolCount, callCount, _, _, err := IndexModule(db, moduleRoot, false, false)
	if err != nil {
		t.Fatalf("index module: %v", err)
	}
	if symbolCount == 0 {
		t.Fatalf("expected symbols, got 0")
	}
	if callCount == 0 {
		t.Fatalf("expected calls, got 0")
	}

	expectSymbols := map[string]string{
		"example.com/samplemod/alpha.Store":                 "type",
		"example.com/samplemod/alpha.*Store.AddObservation": "method",
		"example.com/samplemod/alpha.Top":                   "func",
		"example.com/samplemod/alpha.Answer":                "const",
		"example.com/samplemod/alpha.Global":                "var",
		"example.com/samplemod/beta.Use":                    "func",
	}

	rows, err := db.Query(`SELECT fqname, kind FROM symbols`)
	if err != nil {
		t.Fatalf("query symbols: %v", err)
	}
	defer rows.Close()

	got := map[string]string{}
	for rows.Next() {
		var fq, kind string
		if err := rows.Scan(&fq, &kind); err != nil {
			t.Fatalf("scan symbols: %v", err)
		}
		got[fq] = kind
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("symbols rows: %v", err)
	}

	for fq, wantKind := range expectSymbols {
		gotKind, ok := got[fq]
		if !ok {
			t.Fatalf("missing symbol %q", fq)
		}
		if gotKind != wantKind {
			t.Fatalf("symbol %q kind mismatch: got %q want %q", fq, gotKind, wantKind)
		}
	}

	expectEdges := [][2]string{
		{"example.com/samplemod/alpha.Top", "example.com/samplemod/alpha.*Store.AddObservation"},
		{"example.com/samplemod/beta.Use", "example.com/samplemod/alpha.Top"},
	}

	callRows, err := db.Query(`SELECT from_fqname, to_fqname FROM calls`)
	if err != nil {
		t.Fatalf("query calls: %v", err)
	}
	defer callRows.Close()

	edges := map[[2]string]bool{}
	for callRows.Next() {
		var from, to string
		if err := callRows.Scan(&from, &to); err != nil {
			t.Fatalf("scan calls: %v", err)
		}
		edges[[2]string{from, to}] = true
	}
	if err := callRows.Err(); err != nil {
		t.Fatalf("calls rows: %v", err)
	}

	for _, edge := range expectEdges {
		key := [2]string{edge[0], edge[1]}
		if !edges[key] {
			t.Fatalf("missing call edge %q -> %q", edge[0], edge[1])
		}
	}

	// Regression: package-level callback function literals must be indexed.
	rows2, err := db.Query(`SELECT from_fqname FROM calls WHERE to_fqname = ?`, "example.com/samplemod/alpha.Top")
	if err != nil {
		t.Fatalf("query callers for alpha.Top: %v", err)
	}
	defer rows2.Close()

	hasNamedCaller := false
	hasLiteralCaller := false
	for rows2.Next() {
		var from string
		if err := rows2.Scan(&from); err != nil {
			t.Fatalf("scan callers for alpha.Top: %v", err)
		}
		if from == "example.com/samplemod/beta.Use" {
			hasNamedCaller = true
		}
		if strings.Contains(from, "example.com/samplemod/beta.Hooks$lit@") {
			hasLiteralCaller = true
		}
	}
	if err := rows2.Err(); err != nil {
		t.Fatalf("callers rows for alpha.Top: %v", err)
	}
	if !hasNamedCaller {
		t.Fatalf("expected named caller for alpha.Top")
	}
	if !hasLiteralCaller {
		t.Fatalf("expected package-level func literal caller for alpha.Top")
	}
}

// TestBUG001_CompositeLitFuncRefsTracked verifies that function values stored
// in composite literal elements (map values, slice elements) are recorded as
// ref edges, not just function values passed as call arguments.
func TestBUG001_CompositeLitFuncRefsTracked(t *testing.T) {
	moduleRoot, err := filepath.Abs(filepath.Join("..", "testdata", "samplemod"))
	if err != nil {
		t.Fatalf("abs module root: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "symdb.sqlite")
	db, err := sql.Open(sqlite.DriverName, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := ResetSchema(db); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	if _, _, _, _, err := IndexModule(db, moduleRoot, false, false); err != nil {
		t.Fatalf("index module: %v", err)
	}

	// addFn is stored as a map value at package scope — only reachable via composite lit scan.
	var addFnCallers int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM calls WHERE to_fqname = ? AND kind = 'ref'`,
		"example.com/samplemod/reflits.addFn",
	).Scan(&addFnCallers); err != nil {
		t.Fatalf("query addFn callers: %v", err)
	}
	if addFnCallers == 0 {
		t.Error("BUG-001: addFn has 0 ref edges; expected >= 1 (referenced in package-level map literal)")
	}

	// subFn is stored as a slice element inside a function body — only reachable via composite lit scan.
	var subFnCallers int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM calls WHERE to_fqname = ? AND kind = 'ref'`,
		"example.com/samplemod/reflits.subFn",
	).Scan(&subFnCallers); err != nil {
		t.Fatalf("query subFn callers: %v", err)
	}
	if subFnCallers == 0 {
		t.Error("BUG-001: subFn has 0 ref edges; expected >= 1 (referenced in slice literal inside RunFromSlice)")
	}
}

// TestBUG002_VarInitDirectCallsTracked verifies that direct function calls in
// package-level var initializer expressions are recorded as call edges with a
// synthetic from_fqname, even when there is no containing function body.
func TestBUG002_VarInitDirectCallsTracked(t *testing.T) {
	moduleRoot, err := filepath.Abs(filepath.Join("..", "testdata", "samplemod"))
	if err != nil {
		t.Fatalf("abs module root: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "symdb.sqlite")
	db, err := sql.Open(sqlite.DriverName, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := ResetSchema(db); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	if _, _, _, _, err := IndexModule(db, moduleRoot, false, false); err != nil {
		t.Fatalf("index module: %v", err)
	}

	// directInitCall is called in `var InitValue = directInitCall()` at package scope.
	var directCallers int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM calls WHERE to_fqname = ?`,
		"example.com/samplemod/varinit.directInitCall",
	).Scan(&directCallers); err != nil {
		t.Fatalf("query directInitCall callers: %v", err)
	}
	if directCallers == 0 {
		t.Error("BUG-002: directInitCall has 0 call edges; expected >= 1 (called in var InitValue = directInitCall())")
	}

	// initWithArg is called in `var OtherValue = initWithArg(42)` at package scope.
	var initWithArgCallers int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM calls WHERE to_fqname = ?`,
		"example.com/samplemod/varinit.initWithArg",
	).Scan(&initWithArgCallers); err != nil {
		t.Fatalf("query initWithArg callers: %v", err)
	}
	if initWithArgCallers == 0 {
		t.Error("BUG-002: initWithArg has 0 call edges; expected >= 1 (called in var OtherValue = initWithArg(42))")
	}
}

// TestInterfaceKindSeparation verifies that interface types are indexed with
// kind="interface" and concrete struct types are indexed with kind="type",
// not both as "type". This is the fix for the find --kind interface feature.
func TestInterfaceKindSeparation(t *testing.T) {
	moduleRoot, err := filepath.Abs(filepath.Join("..", "testdata", "samplemod"))
	if err != nil {
		t.Fatalf("abs module root: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "symdb.sqlite")
	db, err := sql.Open(sqlite.DriverName, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := ResetSchema(db); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	if _, _, _, _, err := IndexModule(db, moduleRoot, false, false); err != nil {
		t.Fatalf("index module: %v", err)
	}

	// Doer (in package iface) is an interface — must be kind="interface"
	var doerKind string
	if err := db.QueryRow(
		`SELECT kind FROM symbols WHERE fqname = ?`,
		"example.com/samplemod/iface.Doer",
	).Scan(&doerKind); err != nil {
		t.Fatalf("query Doer kind: %v", err)
	}
	if doerKind != "interface" {
		t.Errorf("Doer kind: got %q, want %q", doerKind, "interface")
	}

	// RealDoer (in package iface) is a struct — must be kind="type"
	var realDoerKind string
	if err := db.QueryRow(
		`SELECT kind FROM symbols WHERE fqname = ?`,
		"example.com/samplemod/iface.RealDoer",
	).Scan(&realDoerKind); err != nil {
		t.Fatalf("query RealDoer kind: %v", err)
	}
	if realDoerKind != "type" {
		t.Errorf("RealDoer kind: got %q, want %q", realDoerKind, "type")
	}
}

// TestBUG006_LocalFuncRefsTracked verifies that function values assigned to
// local variables (short decl, long decl, post-decl assign, named func type)
// produce ref edges, just like functions passed directly as call arguments.
func TestBUG006_LocalFuncRefsTracked(t *testing.T) {
	moduleRoot, err := filepath.Abs(filepath.Join("..", "testdata", "samplemod"))
	if err != nil {
		t.Fatalf("abs module root: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "symdb.sqlite")
	db, err := sql.Open(sqlite.DriverName, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := ResetSchema(db); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	if _, _, _, _, err := IndexModule(db, moduleRoot, false, false); err != nil {
		t.Fatalf("index module: %v", err)
	}

	cases := []struct {
		fqname string
		label  string
	}{
		{"example.com/samplemod/localfuncrefs.AssignedViaShortDecl", "short decl h := fn"},
		{"example.com/samplemod/localfuncrefs.AssignedViaLongDecl", "long decl var h func(...) = fn"},
		{"example.com/samplemod/localfuncrefs.AssignedAfterDecl", "post-decl assign h = fn"},
		{"example.com/samplemod/localfuncrefs.AssignedToNamedFuncType", "named func type var h Handler = fn"},
	}

	for _, tc := range cases {
		var count int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM calls WHERE to_fqname = ? AND kind = 'ref'`,
			tc.fqname,
		).Scan(&count); err != nil {
			t.Fatalf("query ref count for %q: %v", tc.fqname, err)
		}
		if count == 0 {
			t.Errorf("BUG-006: %q has 0 ref edges (%s)", tc.fqname, tc.label)
		}
	}

	// Control: PassedAsArg should already have a ref edge (unchanged behaviour).
	var controlCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM calls WHERE to_fqname = ? AND kind = 'ref'`,
		"example.com/samplemod/localfuncrefs.PassedAsArg",
	).Scan(&controlCount); err != nil {
		t.Fatalf("query ref count for PassedAsArg: %v", err)
	}
	if controlCount == 0 {
		t.Errorf("control: PassedAsArg has 0 ref edges (direct argument passing should always work)")
	}
}

// TestBUG007_TestsNoDuplicateSymbols verifies that indexing with withTests=true
// does not produce duplicate symbol rows. Each non-test symbol should appear
// exactly once, and auto-generated test-binary symbols (e.g. *.test.benchmarks)
// must not be indexed.
func TestBUG007_TestsNoDuplicateSymbols(t *testing.T) {
	moduleRoot, err := filepath.Abs(filepath.Join("..", "testbench", "15_testmain"))
	if err != nil {
		t.Fatalf("abs module root: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "symdb_bug007.sqlite")
	db, err := sql.Open(sqlite.DriverName, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := ResetSchema(db); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	if _, _, _, _, err := IndexModule(db, moduleRoot, false, true); err != nil {
		t.Fatalf("index module with tests: %v", err)
	}

	// "setup" is defined once in pkg.go — must appear exactly once in symbols.
	var setupCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM symbols WHERE name = 'setup'`,
	).Scan(&setupCount); err != nil {
		t.Fatalf("query setup count: %v", err)
	}
	if setupCount != 1 {
		t.Errorf("BUG-007: 'setup' symbol count = %d, want 1 (no duplicates)", setupCount)
	}

	// Auto-generated test-binary symbols (package path ending in ".test") must not appear.
	var testBinaryCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM symbols WHERE package_path LIKE '%.test'`,
	).Scan(&testBinaryCount); err != nil {
		t.Fatalf("query test-binary symbols: %v", err)
	}
	if testBinaryCount > 0 {
		t.Errorf("BUG-007: %d auto-generated test-binary symbol(s) leaked into index (package_path LIKE %%.test)", testBinaryCount)
	}
}

func TestCompactRecv(t *testing.T) {
	got := compactRecv("*example.com/samplemod/alpha.Store", "example.com/samplemod/alpha")
	if got != "*Store" {
		t.Fatalf("compactRecv mismatch: got %q want %q", got, "*Store")
	}
}

func TestIndexModuleCapuresUnresolvedCalls(t *testing.T) {
	moduleRoot, err := filepath.Abs(filepath.Join("..", "testdata", "samplemod"))
	if err != nil {
		t.Fatalf("abs module root: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "symdb.sqlite")
	db, err := sql.Open(sqlite.DriverName, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := ResetSchema(db); err != nil {
		t.Fatalf("reset schema: %v", err)
	}

	if _, _, _, _, err := IndexModule(db, moduleRoot, false, false); err != nil {
		t.Fatalf("index module: %v", err)
	}

	// The table must exist; count may be zero for simple code.
	var unresolvedCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM unresolved_calls`).Scan(&unresolvedCount); err != nil {
		t.Fatalf("query unresolved_calls: %v", err)
	}
	if unresolvedCount < 0 {
		t.Fatalf("unexpected negative unresolved count: %d", unresolvedCount)
	}

	// IndexModule does not populate index_meta; only runIndex does.
	// Verify the table exists by checking the query doesn't error.
	var metaCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM index_meta`).Scan(&metaCount); err != nil {
		t.Fatalf("query index_meta: %v", err)
	}
	if metaCount != 0 {
		t.Fatalf("expected 0 index_meta rows from IndexModule, got %d", metaCount)
	}
}

func TestIndexModuleWithTests(t *testing.T) {
	moduleRoot, err := filepath.Abs(filepath.Join("..", "testdata", "samplemod"))
	if err != nil {
		t.Fatalf("abs module root: %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), "symdb_tests.sqlite")
	db, err := sql.Open(sqlite.DriverName, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := ResetSchema(db); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	// withTests=true should not error; symbol count should be >= count without tests.
	symWithTests, _, _, _, err := IndexModule(db, moduleRoot, false, true)
	if err != nil {
		t.Fatalf("index with tests: %v", err)
	}
	if symWithTests == 0 {
		t.Fatalf("expected symbols with --tests, got 0")
	}
}

func TestFuncRefEdgesDetected(t *testing.T) {
	moduleRoot, err := filepath.Abs(filepath.Join("..", "testdata", "samplemod"))
	if err != nil {
		t.Fatalf("abs module root: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "symdb.sqlite")
	db, err := sql.Open(sqlite.DriverName, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := ResetSchema(db); err != nil {
		t.Fatalf("reset schema: %v", err)
	}

	if _, _, _, _, err := IndexModule(db, moduleRoot, false, false); err != nil {
		t.Fatalf("index module: %v", err)
	}

	rows, err := db.Query(`SELECT to_fqname, kind FROM calls WHERE kind = 'ref'`)
	if err != nil {
		t.Fatalf("query ref calls: %v", err)
	}
	defer rows.Close()

	foundAlphaTopRef := false
	for rows.Next() {
		var to, kind string
		if err := rows.Scan(&to, &kind); err != nil {
			t.Fatalf("scan ref calls: %v", err)
		}
		if strings.Contains(to, "alpha.Top") && kind == "ref" {
			foundAlphaTopRef = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("ref calls rows: %v", err)
	}
	if !foundAlphaTopRef {
		t.Fatalf("expected a func-ref edge to alpha.Top (kind='ref'), got none")
	}
}

// TestIncrementalPackageMetaPopulated verifies that IndexModule populates
// the package_meta table with per-package timestamps.
func TestIncrementalPackageMetaPopulated(t *testing.T) {
	moduleRoot, err := filepath.Abs(filepath.Join("..", "testdata", "samplemod"))
	if err != nil {
		t.Fatalf("abs module root: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "symdb.sqlite")
	db, err := sql.Open(sqlite.DriverName, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := EnsureSchema(db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}

	if _, _, _, _, err := IndexModule(db, moduleRoot, false, false); err != nil {
		t.Fatalf("index module: %v", err)
	}

	// Check that package_meta has rows for this module.
	var count int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM package_meta WHERE module_root = ?`,
		moduleRoot,
	).Scan(&count); err != nil {
		t.Fatalf("query package_meta count: %v", err)
	}
	if count == 0 {
		t.Fatalf("expected package_meta rows for module %s, got 0", moduleRoot)
	}

	// Check that known packages are present (samplemod has alpha and beta packages).
	expectedPkgs := []string{
		"example.com/samplemod/alpha",
		"example.com/samplemod/beta",
	}
	for _, pkg := range expectedPkgs {
		var pkgCount int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM package_meta WHERE module_root = ? AND package_path = ?`,
			moduleRoot, pkg,
		).Scan(&pkgCount); err != nil {
			t.Fatalf("query package_meta for %s: %v", pkg, err)
		}
		if pkgCount != 1 {
			t.Errorf("expected 1 package_meta row for %s, got %d", pkg, pkgCount)
		}
	}
}

// TestPurgeModuleCleansAllTables verifies that PurgeModule removes all data
// for a module across symbols, calls, unresolved_calls, implements, and package_meta.
func TestPurgeModuleCleansAllTables(t *testing.T) {
	sampleRoot, err := filepath.Abs(filepath.Join("..", "testdata", "samplemod"))
	if err != nil {
		t.Fatalf("abs sample module root: %v", err)
	}

	testbenchModule, err := filepath.Abs(filepath.Join("..", "testbench", "14_local_func_refs"))
	if err != nil {
		t.Fatalf("abs testbench module root: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "symdb.sqlite")
	db, err := sql.Open(sqlite.DriverName, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := EnsureSchema(db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}

	// Index both modules.
	if _, _, _, _, err := IndexModule(db, sampleRoot, false, false); err != nil {
		t.Fatalf("index sample module: %v", err)
	}
	if _, _, _, _, err := IndexModule(db, testbenchModule, false, false); err != nil {
		t.Fatalf("index testbench module: %v", err)
	}

	// Verify both modules have data.
	var sampleSymbols, testbenchSymbols int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM symbols WHERE module_root = ?`,
		sampleRoot,
	).Scan(&sampleSymbols); err != nil {
		t.Fatalf("query sample symbols: %v", err)
	}
	if sampleSymbols == 0 {
		t.Fatalf("expected symbols for sample module, got 0")
	}

	if err := db.QueryRow(
		`SELECT COUNT(*) FROM symbols WHERE module_root = ?`,
		testbenchModule,
	).Scan(&testbenchSymbols); err != nil {
		t.Fatalf("query testbench symbols: %v", err)
	}
	if testbenchSymbols == 0 {
		t.Fatalf("expected symbols for testbench module, got 0")
	}

	// Purge sample module.
	if err := PurgeModule(db, sampleRoot); err != nil {
		t.Fatalf("purge sample module: %v", err)
	}

	// Verify sample module data is gone.
	var sampleSymbolsAfter int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM symbols WHERE module_root = ?`,
		sampleRoot,
	).Scan(&sampleSymbolsAfter); err != nil {
		t.Fatalf("query sample symbols after purge: %v", err)
	}
	if sampleSymbolsAfter != 0 {
		t.Errorf("expected 0 symbols for sample module after purge, got %d", sampleSymbolsAfter)
	}

	// Verify type_refs for sample module is gone.
	var sampleTypeRefsAfter int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM type_refs WHERE module_root = ?`,
		sampleRoot,
	).Scan(&sampleTypeRefsAfter); err != nil {
		t.Fatalf("query sample type_refs after purge: %v", err)
	}
	if sampleTypeRefsAfter != 0 {
		t.Errorf("expected 0 type_refs for sample module after purge, got %d", sampleTypeRefsAfter)
	}

	// Verify package_meta for sample module is gone.
	var sampleMetaAfter int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM package_meta WHERE module_root = ?`,
		sampleRoot,
	).Scan(&sampleMetaAfter); err != nil {
		t.Fatalf("query sample package_meta after purge: %v", err)
	}
	if sampleMetaAfter != 0 {
		t.Errorf("expected 0 package_meta rows for sample module after purge, got %d", sampleMetaAfter)
	}

	// Verify testbench module data is untouched.
	var testbenchSymbolsAfter int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM symbols WHERE module_root = ?`,
		testbenchModule,
	).Scan(&testbenchSymbolsAfter); err != nil {
		t.Fatalf("query testbench symbols after sample purge: %v", err)
	}
	if testbenchSymbolsAfter != testbenchSymbols {
		t.Errorf("testbench symbols changed after sample purge: before=%d, after=%d", testbenchSymbols, testbenchSymbolsAfter)
	}
}

// TestIncrementalPackageMetaUpdated verifies that package_meta is updated
// (upserted) with the current timestamp when a module is re-indexed.
func TestIncrementalPackageMetaUpdated(t *testing.T) {
	moduleRoot, err := filepath.Abs(filepath.Join("..", "testdata", "samplemod"))
	if err != nil {
		t.Fatalf("abs module root: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "symdb.sqlite")
	db, err := sql.Open(sqlite.DriverName, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := EnsureSchema(db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}

	// Index the module.
	if _, _, _, _, err := IndexModule(db, moduleRoot, false, false); err != nil {
		t.Fatalf("index module first time: %v", err)
	}

	// Get the indexed_at timestamp for one package.
	var indexedAt1 string
	pkgPath := "example.com/samplemod/alpha"
	if err := db.QueryRow(
		`SELECT indexed_at FROM package_meta WHERE module_root = ? AND package_path = ?`,
		moduleRoot, pkgPath,
	).Scan(&indexedAt1); err != nil {
		t.Fatalf("query indexed_at first time: %v", err)
	}

	// Sleep a bit to ensure timestamp will differ (RFC3339 has second precision).
	time.Sleep(1100 * time.Millisecond)

	// Re-index the module.
	if _, _, _, _, err := IndexModule(db, moduleRoot, false, false); err != nil {
		t.Fatalf("index module second time: %v", err)
	}

	// Get the indexed_at timestamp again.
	var indexedAt2 string
	if err := db.QueryRow(
		`SELECT indexed_at FROM package_meta WHERE module_root = ? AND package_path = ?`,
		moduleRoot, pkgPath,
	).Scan(&indexedAt2); err != nil {
		t.Fatalf("query indexed_at second time: %v", err)
	}

	// Verify the timestamp was updated (should be strictly greater).
	t1, err := time.Parse(time.RFC3339, indexedAt1)
	if err != nil {
		t.Fatalf("parse indexed_at1: %v", err)
	}
	t2, err := time.Parse(time.RFC3339, indexedAt2)
	if err != nil {
		t.Fatalf("parse indexed_at2: %v", err)
	}
	if !t2.After(t1) {
		t.Errorf("expected indexed_at to increase after re-index: %s -> %s", t1, t2)
	}
}

// TestFileHashesStoredAtIndexTime verifies that IndexModule populates
// the package_files table and sets a non-empty files_hash on each package_meta row.
func TestFileHashesStoredAtIndexTime(t *testing.T) {
	moduleRoot, err := filepath.Abs(filepath.Join("..", "testdata", "samplemod"))
	if err != nil {
		t.Fatalf("abs module root: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "symdb.sqlite")
	db, err := sql.Open(sqlite.DriverName, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := EnsureSchema(db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}

	if _, _, _, _, err := IndexModule(db, moduleRoot, false, false); err != nil {
		t.Fatalf("index module: %v", err)
	}

	// Assert package_files has rows for this module.
	var fileCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM package_files WHERE module_root = ?`,
		moduleRoot,
	).Scan(&fileCount); err != nil {
		t.Fatalf("query package_files count: %v", err)
	}
	if fileCount == 0 {
		t.Fatalf("expected package_files rows for module %s, got 0", moduleRoot)
	}

	// Assert every package_meta row has a non-empty files_hash.
	rows, err := db.Query(
		`SELECT package_path, files_hash FROM package_meta WHERE module_root = ?`,
		moduleRoot,
	)
	if err != nil {
		t.Fatalf("query package_meta: %v", err)
	}
	defer rows.Close()

	metaCount := 0
	for rows.Next() {
		var pkgPath, filesHash string
		if err := rows.Scan(&pkgPath, &filesHash); err != nil {
			t.Fatalf("scan package_meta: %v", err)
		}
		metaCount++
		if filesHash == "" {
			t.Errorf("package %s has empty files_hash", pkgPath)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("package_meta rows: %v", err)
	}
	if metaCount == 0 {
		t.Fatalf("expected package_meta rows, got 0")
	}
}

// TestIsPackageStaleNotStale verifies that immediately after indexing,
// IsPackageStale returns false for all packages.
func TestIsPackageStaleNotStale(t *testing.T) {
	moduleRoot, err := filepath.Abs(filepath.Join("..", "testdata", "samplemod"))
	if err != nil {
		t.Fatalf("abs module root: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "symdb.sqlite")
	db, err := sql.Open(sqlite.DriverName, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := EnsureSchema(db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}

	if _, _, _, _, err := IndexModule(db, moduleRoot, false, false); err != nil {
		t.Fatalf("index module: %v", err)
	}

	// Query all packages for this module.
	rows, err := db.Query(
		`SELECT package_path FROM package_meta WHERE module_root = ?`,
		moduleRoot,
	)
	if err != nil {
		t.Fatalf("query package_meta: %v", err)
	}
	defer rows.Close()

	var pkgs []string
	for rows.Next() {
		var pkgPath string
		if err := rows.Scan(&pkgPath); err != nil {
			t.Fatalf("scan package_meta: %v", err)
		}
		pkgs = append(pkgs, pkgPath)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("package_meta rows: %v", err)
	}
	if len(pkgs) == 0 {
		t.Fatalf("no packages found in package_meta")
	}

	for _, pkgPath := range pkgs {
		stale, err := IsPackageStale(db, moduleRoot, pkgPath)
		if err != nil {
			t.Fatalf("IsPackageStale(%s): %v", pkgPath, err)
		}
		if stale {
			t.Errorf("IsPackageStale(%s) = true immediately after indexing, want false", pkgPath)
		}
	}
}

// TestIsPackageStaleDetectsModifiedFile verifies that modifying a .go file
// causes IsPackageStale to return true for the affected package.
func TestIsPackageStaleDetectsModifiedFile(t *testing.T) {
	srcRoot, err := filepath.Abs(filepath.Join("..", "testdata", "samplemod"))
	if err != nil {
		t.Fatalf("abs module root: %v", err)
	}

	tmpDir := t.TempDir()
	modRoot := filepath.Join(tmpDir, "samplemod")
	copyDir(t, srcRoot, modRoot)

	dbPath := filepath.Join(tmpDir, "symdb.sqlite")
	db, err := sql.Open(sqlite.DriverName, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := EnsureSchema(db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}

	if _, _, _, _, err := IndexModule(db, modRoot, false, false); err != nil {
		t.Fatalf("index module: %v", err)
	}

	// Modify a .go file in the alpha package.
	alphaFile := filepath.Join(modRoot, "alpha", "alpha.go")
	f, err := os.OpenFile(alphaFile, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open alpha.go: %v", err)
	}
	if _, err := f.WriteString("\n// comment\n"); err != nil {
		f.Close()
		t.Fatalf("write alpha.go: %v", err)
	}
	f.Close()

	stale, err := IsPackageStale(db, modRoot, "example.com/samplemod/alpha")
	if err != nil {
		t.Fatalf("IsPackageStale: %v", err)
	}
	if !stale {
		t.Errorf("IsPackageStale = false after modifying alpha.go, want true")
	}
}

// TestIsPackageStaleDetectsDeletedFile verifies that removing a .go file
// causes IsPackageStale to return true for the affected package.
func TestIsPackageStaleDetectsDeletedFile(t *testing.T) {
	srcRoot, err := filepath.Abs(filepath.Join("..", "testdata", "samplemod"))
	if err != nil {
		t.Fatalf("abs module root: %v", err)
	}

	tmpDir := t.TempDir()
	modRoot := filepath.Join(tmpDir, "samplemod")
	copyDir(t, srcRoot, modRoot)

	dbPath := filepath.Join(tmpDir, "symdb.sqlite")
	db, err := sql.Open(sqlite.DriverName, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := EnsureSchema(db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}

	if _, _, _, _, err := IndexModule(db, modRoot, false, false); err != nil {
		t.Fatalf("index module: %v", err)
	}

	// Delete a .go file from the alpha package.
	alphaFile := filepath.Join(modRoot, "alpha", "alpha.go")
	if err := os.Remove(alphaFile); err != nil {
		t.Fatalf("remove alpha.go: %v", err)
	}

	stale, err := IsPackageStale(db, modRoot, "example.com/samplemod/alpha")
	if err != nil {
		t.Fatalf("IsPackageStale: %v", err)
	}
	if !stale {
		t.Errorf("IsPackageStale = false after deleting alpha.go, want true")
	}
}

// TestIsPackageStaleDetectsAddedFile verifies that adding a new .go file
// causes IsPackageStale to return true (caught by file count mismatch).
func TestIsPackageStaleDetectsAddedFile(t *testing.T) {
	srcRoot, err := filepath.Abs(filepath.Join("..", "testdata", "samplemod"))
	if err != nil {
		t.Fatalf("abs module root: %v", err)
	}

	tmpDir := t.TempDir()
	modRoot := filepath.Join(tmpDir, "samplemod")
	copyDir(t, srcRoot, modRoot)

	dbPath := filepath.Join(tmpDir, "symdb.sqlite")
	db, err := sql.Open(sqlite.DriverName, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := EnsureSchema(db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}

	if _, _, _, _, err := IndexModule(db, modRoot, false, false); err != nil {
		t.Fatalf("index module: %v", err)
	}

	// Add a new .go file to the alpha package directory.
	extraFile := filepath.Join(modRoot, "alpha", "extra_added.go")
	if err := os.WriteFile(extraFile, []byte("package alpha\n\nfunc Extra() {}\n"), 0o644); err != nil {
		t.Fatalf("write extra_added.go: %v", err)
	}

	stale, err := IsPackageStale(db, modRoot, "example.com/samplemod/alpha")
	if err != nil {
		t.Fatalf("IsPackageStale: %v", err)
	}
	if !stale {
		t.Errorf("IsPackageStale = false after adding extra_added.go, want true")
	}
}

// TestStalePackages verifies that StalePackages returns an empty slice when
// nothing has changed, and returns the modified package after a change.
func TestStalePackages(t *testing.T) {
	srcRoot, err := filepath.Abs(filepath.Join("..", "testdata", "samplemod"))
	if err != nil {
		t.Fatalf("abs module root: %v", err)
	}

	tmpDir := t.TempDir()
	modRoot := filepath.Join(tmpDir, "samplemod")
	copyDir(t, srcRoot, modRoot)

	dbPath := filepath.Join(tmpDir, "symdb.sqlite")
	db, err := sql.Open(sqlite.DriverName, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := EnsureSchema(db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}

	if _, _, _, _, err := IndexModule(db, modRoot, false, false); err != nil {
		t.Fatalf("index module: %v", err)
	}

	// Immediately after indexing, no packages should be stale.
	stale, err := StalePackages(db)
	if err != nil {
		t.Fatalf("StalePackages: %v", err)
	}
	if len(stale) != 0 {
		t.Errorf("StalePackages returned %d stale package(s) immediately after indexing, want 0: %v", len(stale), stale)
	}

	// Modify a file in the beta package.
	betaFile := filepath.Join(modRoot, "beta", "beta.go")
	f, err := os.OpenFile(betaFile, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open beta.go: %v", err)
	}
	if _, err := f.WriteString("\n// stale\n"); err != nil {
		f.Close()
		t.Fatalf("write beta.go: %v", err)
	}
	f.Close()

	stale, err = StalePackages(db)
	if err != nil {
		t.Fatalf("StalePackages after modification: %v", err)
	}

	found := false
	for _, pkg := range stale {
		if pkg == "example.com/samplemod/beta" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("StalePackages did not include example.com/samplemod/beta after modification; got %v", stale)
	}
}

// TestBUG009_PurgeModuleCleansImplMethods verifies that PurgeModule removes
// all impl_methods rows for the purged module. Previously, impl_methods was
// omitted from the purge, so stale interface-method rows survived incremental
// reindexes and caused DeadSymbols to suppress methods forever even after they
// no longer participated in any interface contract.
func TestBUG009_PurgeModuleCleansImplMethods(t *testing.T) {
	sampleRoot, err := filepath.Abs(filepath.Join("..", "testdata", "samplemod"))
	if err != nil {
		t.Fatalf("abs sample module root: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "symdb_bug009.sqlite")
	db, err := sql.Open(sqlite.DriverName, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := EnsureSchema(db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}

	// Index the module — samplemod/iface has RealDoer and StubDoer implementing
	// the Doer interface, so impl_methods rows will be created.
	if _, _, _, _, err := IndexModule(db, sampleRoot, false, false); err != nil {
		t.Fatalf("index module: %v", err)
	}

	// Confirm impl_methods has rows attributed to this module.
	var beforeCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM impl_methods WHERE module_root = ?`,
		sampleRoot,
	).Scan(&beforeCount); err != nil {
		t.Fatalf("query impl_methods before purge: %v", err)
	}
	if beforeCount == 0 {
		t.Fatalf("expected impl_methods rows before purge (samplemod/iface implements Doer), got 0")
	}

	// Purge the module.
	if err := PurgeModule(db, sampleRoot); err != nil {
		t.Fatalf("purge module: %v", err)
	}

	// impl_methods for this module must now be empty.
	var afterCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM impl_methods WHERE module_root = ?`,
		sampleRoot,
	).Scan(&afterCount); err != nil {
		t.Fatalf("query impl_methods after purge: %v", err)
	}
	if afterCount != 0 {
		t.Errorf("BUG-009: impl_methods still has %d row(s) after PurgeModule — stale impl suppression will produce incorrect dead-code results", afterCount)
	}
}

// TestTypeRefsExtracted verifies that the type_refs table is populated with
// the expected ref kinds from the typerefs test fixture.
func TestTypeRefsExtracted(t *testing.T) {
	moduleRoot, err := filepath.Abs(filepath.Join("..", "testdata", "samplemod"))
	if err != nil {
		t.Fatalf("abs module root: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "symdb.sqlite")
	db, err := sql.Open(sqlite.DriverName, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := ResetSchema(db); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	if _, _, _, _, err := IndexModule(db, moduleRoot, false, false); err != nil {
		t.Fatalf("index module: %v", err)
	}

	// Verify each expected ref_kind for iface.RealDoer
	target := "example.com/samplemod/iface.RealDoer"
	expectedKinds := map[string]string{
		"type_assert":   "example.com/samplemod/typerefs.UseTypeAssert",
		"type_switch":   "example.com/samplemod/typerefs.UseTypeSwitch",
		"composite_lit": "example.com/samplemod/typerefs.UseCompositeLit",
		"embed":         "example.com/samplemod/typerefs.EmbedDoer",
	}

	rows, err := db.Query(
		`SELECT from_fqname, ref_kind FROM type_refs WHERE to_fqname = ?`,
		target,
	)
	if err != nil {
		t.Fatalf("query type_refs: %v", err)
	}
	defer rows.Close()

	gotKinds := map[string]string{} // ref_kind → from_fqname
	for rows.Next() {
		var fromFQ, kind string
		if err := rows.Scan(&fromFQ, &kind); err != nil {
			t.Fatalf("scan type_refs: %v", err)
		}
		gotKinds[kind] = fromFQ
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("type_refs rows: %v", err)
	}

	for kind, expectedFrom := range expectedKinds {
		gotFrom, ok := gotKinds[kind]
		if !ok {
			t.Errorf("missing type_ref kind=%q for %s", kind, target)
			continue
		}
		if gotFrom != expectedFrom {
			t.Errorf("type_ref kind=%q: got from=%q, want %q", kind, gotFrom, expectedFrom)
		}
	}
}
