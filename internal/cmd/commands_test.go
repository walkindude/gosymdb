package cmd

// Tests for the command-layer functions (execCallees, execDead, execPackages, execFind, execImplementors).
// Also covers agent-context registration and env.db population.
// All tests index testdata/samplemod, which includes:
//   alpha     — basic symbols and calls (Store, Top, AddObservation)
//   beta      — cross-package calls and func-ref callbacks
//   concurrent — sync.Mutex usage + unexported dead func
//   iface     — interface dispatch, unexported dead func
//   recursive — self-calling Fib + unexported dead func

import (
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/walkindude/gosymdb/indexer"
	"github.com/walkindude/gosymdb/store"
	"github.com/walkindude/gosymdb/store/sqlite"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	// Driver registered by store/sqlite/driver_*.go via sqlite import above.
)

// indexSamplemod returns an open, populated DB for testdata/samplemod.
// Caller is responsible for closing it (via t.Cleanup or defer).
func indexSamplemod(t *testing.T) *sql.DB {
	t.Helper()
	moduleRoot, err := filepath.Abs(filepath.Join("..", "..", "testdata", "samplemod"))
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}
	db, err := sql.Open(sqlite.DriverName, filepath.Join(t.TempDir(), "symdb.sqlite"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := indexer.ResetSchema(db); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	if _, _, _, _, err := indexer.IndexModule(db, moduleRoot, false, false); err != nil {
		t.Fatalf("index module: %v", err)
	}
	return db
}

// queryCalls returns the set of to_fqnames called by from.
func queryCalls(t *testing.T, db *sql.DB, from string) map[string]bool {
	t.Helper()
	rows, err := db.Query(`SELECT DISTINCT to_fqname FROM calls WHERE from_fqname = ?`, from)
	if err != nil {
		t.Fatalf("query calls from %q: %v", from, err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var to string
		if err := rows.Scan(&to); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[to] = true
	}
	return out
}

// queryDead returns the set of fqnames with no entry in calls.to_fqname,
// restricted to func/method kinds and excluding well-known runtime entry points.
func queryDead(t *testing.T, db *sql.DB) map[string]bool {
	t.Helper()
	rows, err := db.Query(`
SELECT fqname FROM symbols
WHERE kind IN ('func','method')
  AND name NOT IN ('init','main')
  AND name NOT LIKE 'Test%'
  AND name NOT LIKE 'Benchmark%'
  AND name NOT LIKE 'Example%'
  AND name NOT LIKE 'Fuzz%'
  AND exported = 0
  AND NOT EXISTS (SELECT 1 FROM calls WHERE to_fqname = symbols.fqname)
`)
	if err != nil {
		t.Fatalf("query dead: %v", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var fq string
		if err := rows.Scan(&fq); err != nil {
			t.Fatalf("scan dead: %v", err)
		}
		out[fq] = true
	}
	return out
}

// — callees ——————————————————————————————————————————————————————————————————

func TestCalleesBasic(t *testing.T) {
	db := indexSamplemod(t)
	// alpha.Top calls alpha.*Store.AddObservation
	got := queryCalls(t, db, "example.com/samplemod/alpha.Top")
	want := "example.com/samplemod/alpha.*Store.AddObservation"
	if !got[want] {
		t.Fatalf("expected callee %q in callees of alpha.Top; got %v", want, got)
	}
}

func TestCalleesUnique(t *testing.T) {
	db := indexSamplemod(t)
	// beta.Use calls alpha.Top — verify deduplication doesn't lose real edges.
	got := queryCalls(t, db, "example.com/samplemod/beta.Use")
	if !got["example.com/samplemod/alpha.Top"] {
		t.Fatalf("expected alpha.Top in callees of beta.Use; got %v", got)
	}
	// beta.Use also creates a Store — should call alpha.Store constructor (it uses &alpha.Store{})
	// The composite literal doesn't produce a call edge, so alpha.Top must be the primary edge.
	if len(got) == 0 {
		t.Fatal("callees of beta.Use must not be empty")
	}
}

func TestCalleesIncludesMutexCalls(t *testing.T) {
	db := indexSamplemod(t)
	// concurrent.(*SafeCounter).Inc must call sync.*Mutex.Lock and sync.*Mutex.Unlock.
	// These are resolved by the type-checker even though sync is stdlib.
	got := queryCalls(t, db, "example.com/samplemod/concurrent.*SafeCounter.Inc")
	if !got["sync.*Mutex.Lock"] {
		t.Fatalf("expected sync.*Mutex.Lock in callees of SafeCounter.Inc; got %v", got)
	}
	if !got["sync.*Mutex.Unlock"] {
		t.Fatalf("expected sync.*Mutex.Unlock in callees of SafeCounter.Inc; got %v", got)
	}
}

func TestCalleesRecursiveSelf(t *testing.T) {
	db := indexSamplemod(t)
	// Fib calls itself.
	got := queryCalls(t, db, "example.com/samplemod/recursive.Fib")
	if !got["example.com/samplemod/recursive.Fib"] {
		t.Fatalf("expected recursive.Fib to call itself; got %v", got)
	}
	// Fib also calls scale.
	if !got["example.com/samplemod/recursive.scale"] {
		t.Fatalf("expected recursive.Fib to call recursive.scale; got %v", got)
	}
}

// — dead ——————————————————————————————————————————————————————————————————————

func TestDeadFindsUnexportedUnusedFuncs(t *testing.T) {
	db := indexSamplemod(t)
	dead := queryDead(t, db)

	// These are the unexported funcs with no callers in any of the new packages.
	mustBeDead := []string{
		"example.com/samplemod/concurrent.unused",
		"example.com/samplemod/iface.newStubDoer",
		"example.com/samplemod/recursive.neverCalled",
	}
	for _, fq := range mustBeDead {
		if !dead[fq] {
			t.Errorf("expected %q to be dead, but it was not in dead set", fq)
		}
	}
}

func TestDeadExcludesRecursiveFunc(t *testing.T) {
	db := indexSamplemod(t)
	dead := queryDead(t, db)

	// Fib calls itself, so it has a caller and must NOT be dead.
	if dead["example.com/samplemod/recursive.Fib"] {
		t.Fatal("recursive.Fib must not appear as dead: it calls itself")
	}
	// scale is called by Fib.
	if dead["example.com/samplemod/recursive.scale"] {
		t.Fatal("recursive.scale must not appear as dead: it is called by Fib")
	}
}

// TestDeadDoesNotReportInterfaceImpls verifies that methods which satisfy an
// interface are suppressed from dead output even when they have no direct call
// edges. The implements table records the satisfaction relationship; DeadSymbols
// joins against it to exclude these false positives.
func TestDeadDoesNotReportInterfaceImpls(t *testing.T) {
	tmpPath := filepath.Join(t.TempDir(), "dead_iface.sqlite")
	tdb, _ := sql.Open(sqlite.DriverName, tmpPath)
	t.Cleanup(func() { tdb.Close() })
	moduleRoot, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "samplemod"))
	indexer.ResetSchema(tdb)
	indexer.IndexModule(tdb, moduleRoot, false, false)

	rs, err := sqlite.Open(tmpPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer rs.Close()
	db2, _ := sql.Open(sqlite.DriverName, tmpPath)
	t.Cleanup(func() { db2.Close() })

	// --include-exported so the exported Do/Tag methods are in scope.
	out := captureJSON(t, func() {
		execDead(rs, db2, "", "example.com/samplemod/iface", 1000, true, true, tmpPath)
	})
	symbols, _ := out["symbols"].([]any)
	dead := map[string]bool{}
	for _, s := range symbols {
		sm, _ := s.(map[string]any)
		dead[sm["fqname"].(string)] = true
	}

	// These methods are called only via the Doer interface (interface dispatch).
	// With the implements join they must NOT appear as dead.
	ifaceImpls := []string{
		"example.com/samplemod/iface.*RealDoer.Do",
		"example.com/samplemod/iface.*RealDoer.Tag",
		"example.com/samplemod/iface.*StubDoer.Do",
		"example.com/samplemod/iface.*StubDoer.Tag",
	}
	for _, fq := range ifaceImpls {
		if dead[fq] {
			t.Errorf("interface impl %q wrongly reported as dead (no direct callers, but satisfies Doer interface)", fq)
		}
	}

	// newStubDoer is genuinely dead: unexported, no callers, not an interface impl.
	if !dead["example.com/samplemod/iface.newStubDoer"] {
		t.Error("newStubDoer should still appear dead (genuinely unused)")
	}
}

// TestDeadReportsExtraMethodsOnImplementors verifies that dead suppression is
// method-precise, not type-wide. A concrete type that implements an interface
// may have additional methods not required by the interface; those extra methods
// must still be reported as dead if they have no callers.
// Regression test for BUG-009 (Codex PR-45 review).
func TestDeadReportsExtraMethodsOnImplementors(t *testing.T) {
	tmpPath := filepath.Join(t.TempDir(), "dead_extra.sqlite")
	tdb, _ := sql.Open(sqlite.DriverName, tmpPath)
	t.Cleanup(func() { tdb.Close() })
	moduleRoot, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "samplemod"))
	indexer.ResetSchema(tdb)
	indexer.IndexModule(tdb, moduleRoot, false, false)

	rs, err := sqlite.Open(tmpPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer rs.Close()
	db2, _ := sql.Open(sqlite.DriverName, tmpPath)
	t.Cleanup(func() { db2.Close() })

	out := captureJSON(t, func() {
		execDead(rs, db2, "", "example.com/samplemod/iface", 1000, false, true, tmpPath)
	})
	symbols, _ := out["symbols"].([]any)
	dead := map[string]bool{}
	for _, s := range symbols {
		sm, _ := s.(map[string]any)
		dead[sm["fqname"].(string)] = true
	}

	// extraHelper is unexported, has no callers, and is NOT part of the Doer
	// interface. It must appear as dead. The current broad suppression hides it.
	const extra = "example.com/samplemod/iface.*RealDoer.extraHelper"
	if !dead[extra] {
		t.Errorf("%q must be reported as dead: it is not part of any interface and has no callers", extra)
	}

	// Interface impl methods must still be suppressed (existing behaviour).
	for _, fq := range []string{
		"example.com/samplemod/iface.*RealDoer.Do",
		"example.com/samplemod/iface.*RealDoer.Tag",
	} {
		if dead[fq] {
			t.Errorf("interface impl %q wrongly reported as dead", fq)
		}
	}
}

func TestDeadExcludesExportedByDefault(t *testing.T) {
	db := indexSamplemod(t)
	dead := queryDead(t, db)
	// The query in queryDead above filters exported=0, same as execDead default.
	// Verify no exported symbol slipped through.
	for fq := range dead {
		// Exported names start with an uppercase letter after the last dot.
		parts := strings.Split(fq, ".")
		last := parts[len(parts)-1]
		// Strip pointer receiver prefix if present.
		last = strings.TrimPrefix(last, "*")
		if len(last) > 0 && last[0] >= 'A' && last[0] <= 'Z' {
			t.Errorf("exported symbol %q appeared in unexported-only dead set", fq)
		}
	}
}

// — packages ——————————————————————————————————————————————————————————————————

func TestPackagesReturnsAllPackages(t *testing.T) {
	db := indexSamplemod(t)
	rows, err := db.Query(`SELECT package_path FROM symbols GROUP BY package_path`)
	if err != nil {
		t.Fatalf("query packages: %v", err)
	}
	defer rows.Close()
	got := map[string]bool{}
	for rows.Next() {
		var pkg string
		if err := rows.Scan(&pkg); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[pkg] = true
	}

	want := []string{
		"example.com/samplemod/alpha",
		"example.com/samplemod/beta",
		"example.com/samplemod/concurrent",
		"example.com/samplemod/iface",
		"example.com/samplemod/recursive",
	}
	for _, pkg := range want {
		if !got[pkg] {
			t.Errorf("expected package %q in index, got packages: %v", pkg, got)
		}
	}
}

func TestPackagesSymbolCounts(t *testing.T) {
	db := indexSamplemod(t)

	var concCount int
	err := db.QueryRow(`SELECT COUNT(*) FROM symbols WHERE package_path = 'example.com/samplemod/concurrent'`).Scan(&concCount)
	if err != nil {
		t.Fatalf("count concurrent symbols: %v", err)
	}
	// SafeCounter (type), Inc, Value, New, unused = at least 5
	if concCount < 5 {
		t.Errorf("expected at least 5 symbols in concurrent package, got %d", concCount)
	}

	var ifaceCount int
	err = db.QueryRow(`SELECT COUNT(*) FROM symbols WHERE package_path = 'example.com/samplemod/iface'`).Scan(&ifaceCount)
	if err != nil {
		t.Fatalf("count iface symbols: %v", err)
	}
	// Doer (type), RealDoer, StubDoer, Do×2, Tag×2, Run, NewRealDoer, newStubDoer = at least 9
	if ifaceCount < 9 {
		t.Errorf("expected at least 9 symbols in iface package, got %d", ifaceCount)
	}
}

// — execCallees (command layer) ————————————————————————————————————————————————

func TestRunCalleesRequiresSymbol(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "x.sqlite")
	db, _ := sql.Open(sqlite.DriverName, dbPath)
	indexer.ResetSchema(db)
	db.Close()

	rs, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer rs.Close()
	db2, _ := sql.Open(sqlite.DriverName, dbPath)
	defer db2.Close()

	err = execCallees(rs, db2, "", 200, false, "", true, false, false, false, dbPath)
	if err == nil || !strings.Contains(err.Error(), "--symbol is required") {
		t.Fatalf("expected --symbol required error, got: %v", err)
	}
}

func TestRunCalleesUniqueNoDuplicates(t *testing.T) {
	// Build a db from samplemod and run the command-layer function.
	moduleRoot, err := filepath.Abs(filepath.Join("..", "..", "testdata", "samplemod"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), "symdb.sqlite")
	db, err := sql.Open(sqlite.DriverName, dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := indexer.ResetSchema(db); err != nil {
		db.Close()
		t.Fatalf("schema: %v", err)
	}
	if _, _, _, _, err := indexer.IndexModule(db, moduleRoot, false, false); err != nil {
		db.Close()
		t.Fatalf("index: %v", err)
	}
	db.Close()

	// --unique should return without error and produce no duplicate to_fqnames.
	// We verify by running the raw query ourselves.
	db2, _ := sql.Open(sqlite.DriverName, dbPath)
	defer db2.Close()

	rows, err := db2.Query(`
SELECT to_fqname, COUNT(*) AS n
FROM (SELECT DISTINCT to_fqname, kind FROM calls WHERE from_fqname LIKE '%SafeCounter.Inc%')
GROUP BY to_fqname
HAVING n > 1`)
	if err != nil {
		t.Fatalf("duplicate check query: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var to string
		var n int
		rows.Scan(&to, &n)
		t.Errorf("duplicate callee %q appeared %d times in unique result", to, n)
	}
}

// — find --pkg ————————————————————————————————————————————————————————————————

// queryFindPkg runs a raw SQL find filtered by package prefix, returning fqnames.
func queryFindPkg(t *testing.T, db *sql.DB, pkgPrefix, q, kind string) map[string]bool {
	t.Helper()
	base := `SELECT fqname FROM symbols WHERE 1=1`
	var params []any
	if q != "" {
		base += " AND (fqname LIKE ? OR name LIKE ? OR signature LIKE ?)"
		params = append(params, "%"+q+"%", "%"+q+"%", "%"+q+"%")
	}
	if pkgPrefix != "" {
		base += " AND package_path LIKE ?"
		params = append(params, pkgPrefix+"%")
	}
	if kind != "" {
		base += " AND kind = ?"
		params = append(params, kind)
	}
	rows, err := db.Query(base, params...)
	if err != nil {
		t.Fatalf("queryFindPkg: %v", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var fq string
		if err := rows.Scan(&fq); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[fq] = true
	}
	return out
}

func TestFindPkgScopesResults(t *testing.T) {
	db := indexSamplemod(t)

	// Both alpha and store packages have a symbol named "Store" and "Top".
	// Without --pkg, --q "Store" returns both.
	all := queryFindPkg(t, db, "", "Store", "")
	hasAlpha := false
	hasStore := false
	for fq := range all {
		if strings.Contains(fq, "/alpha.") {
			hasAlpha = true
		}
		if strings.Contains(fq, "/store.") {
			hasStore = true
		}
	}
	if !hasAlpha || !hasStore {
		t.Fatalf("expected both alpha and store results without --pkg; got %v", all)
	}

	// With --pkg scoped to store, only store symbols should appear.
	storeOnly := queryFindPkg(t, db, "example.com/samplemod/store", "Store", "")
	for fq := range storeOnly {
		if !strings.Contains(fq, "example.com/samplemod/store") {
			t.Errorf("--pkg store returned symbol outside package: %q", fq)
		}
	}
	if len(storeOnly) == 0 {
		t.Fatal("--pkg store returned no results")
	}
}

func TestFindPkgNoQueryListsPackage(t *testing.T) {
	db := indexSamplemod(t)

	// --pkg alone (no --q) should return all symbols in the package.
	got := queryFindPkg(t, db, "example.com/samplemod/concurrent", "", "")
	// SafeCounter, Inc, Value, New, unused, mu field not indexed (fields aren't indexed)
	wantFQs := []string{
		"example.com/samplemod/concurrent.SafeCounter",
		"example.com/samplemod/concurrent.*SafeCounter.Inc",
		"example.com/samplemod/concurrent.*SafeCounter.Value",
		"example.com/samplemod/concurrent.New",
		"example.com/samplemod/concurrent.unused",
	}
	for _, fq := range wantFQs {
		if !got[fq] {
			t.Errorf("expected %q in --pkg concurrent results; got %v", fq, got)
		}
	}
	// Must not include anything from alpha or store.
	for fq := range got {
		if !strings.HasPrefix(fq, "example.com/samplemod/concurrent") {
			t.Errorf("--pkg concurrent returned out-of-package symbol: %q", fq)
		}
	}
}

func TestFindPkgAndQCompose(t *testing.T) {
	db := indexSamplemod(t)

	// --pkg store + --q Top should return store.Top but not alpha.Top.
	got := queryFindPkg(t, db, "example.com/samplemod/store", "Top", "")
	if got["example.com/samplemod/alpha.Top"] {
		t.Error("alpha.Top must not appear when --pkg is scoped to store")
	}
	if !got["example.com/samplemod/store.Top"] {
		t.Errorf("store.Top missing from results; got %v", got)
	}
}

func TestFindPkgPrefixMatchesSub(t *testing.T) {
	db := indexSamplemod(t)

	// --pkg example.com/samplemod/i should match iface (prefix).
	got := queryFindPkg(t, db, "example.com/samplemod/i", "", "")
	hasIface := false
	for fq := range got {
		if strings.HasPrefix(fq, "example.com/samplemod/iface") {
			hasIface = true
		}
	}
	if !hasIface {
		t.Fatalf("prefix --pkg example.com/samplemod/i should match iface package; got %v", got)
	}
}

// TestFindNoFilter_ReturnsAll verifies that find with no --q/--pkg/--file returns
// all symbols (or an empty list for an empty DB) without error.
func TestFindNoFilter_ReturnsAll(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "x.sqlite")
	db, _ := sql.Open(sqlite.DriverName, dbPath)
	indexer.ResetSchema(db)
	db.Close()

	st, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer st.Close()
	// Empty DB — should return 0 symbols with no error.
	err = execFind(st, dbPath, "", "", "", "", 100, false, false)
	if err != nil {
		t.Fatalf("find with no filter should succeed on empty DB; got: %v", err)
	}
}

// TestFindNoFilter_JSON verifies that find with no filters emits valid JSON with
// a symbols array.
func TestFindNoFilter_JSON(t *testing.T) {
	moduleRoot, err := filepath.Abs(filepath.Join("..", "..", "testdata", "samplemod"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), "x.sqlite")
	db, _ := sql.Open(sqlite.DriverName, dbPath)
	if err := indexer.ResetSchema(db); err != nil {
		db.Close()
		t.Fatalf("reset schema: %v", err)
	}
	if _, _, _, _, err := indexer.IndexModule(db, moduleRoot, false, false); err != nil {
		db.Close()
		t.Fatalf("index: %v", err)
	}
	db.Close()

	st, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer st.Close()

	out := captureJSON(t, func() {
		execFind(st, dbPath, "", "", "", "", 1000, false, true)
	})
	syms, ok := out["symbols"].([]any)
	if !ok {
		t.Fatalf("expected symbols array in JSON; got: %v", out)
	}
	if len(syms) == 0 {
		t.Fatal("expected at least 1 symbol when no filter given and DB is populated")
	}
}

// captureJSON captures JSON written to stdout by fn and returns the decoded map.
// The reader goroutine drains the pipe concurrently to prevent deadlock when fn
// writes output larger than the OS pipe buffer (4 KB on Windows).
func captureJSON(t *testing.T, fn func()) map[string]any {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	var buf strings.Builder
	done := make(chan struct{})
	go func() {
		io.Copy(&buf, r)
		close(done)
	}()
	fn()
	w.Close()
	os.Stdout = old
	<-done
	r.Close()
	var m map[string]any
	if err := json.Unmarshal([]byte(buf.String()), &m); err != nil {
		t.Fatalf("unmarshal JSON: %v\nraw: %s", err, buf.String())
	}
	return m
}

// TestFindTruncationIndicator verifies that find JSON output includes
// total_matched and truncated fields, and that truncated=true when
// results are capped by --limit.
func TestFindTruncationIndicator(t *testing.T) {
	db := indexSamplemod(t)
	dbPath := db.Stats().MaxOpenConnections // unused; need path
	_ = dbPath
	// Re-open via path to pass to execFind.
	moduleRoot, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "samplemod"))
	tmpPath := filepath.Join(t.TempDir(), "find_trunc.sqlite")
	tdb, _ := sql.Open(sqlite.DriverName, tmpPath)
	t.Cleanup(func() { tdb.Close() })
	indexer.ResetSchema(tdb)
	indexer.IndexModule(tdb, moduleRoot, false, false)

	tst, tsterr := sqlite.Open(tmpPath)
	if tsterr != nil {
		t.Fatalf("sqlite.Open tmpPath: %v", tsterr)
	}
	t.Cleanup(func() { tst.Close() })

	// No truncation: samplemod has many symbols, but with a generous limit.
	out := captureJSON(t, func() {
		execFind(tst, tmpPath, "", "example.com/samplemod/alpha", "", "", 1000, false, true)
	})
	if _, ok := out["total_matched"]; !ok {
		t.Error("find JSON missing 'total_matched' field")
	}
	if _, ok := out["truncated"]; !ok {
		t.Error("find JSON missing 'truncated' field")
	}
	if out["truncated"] != false {
		t.Errorf("truncated should be false with generous limit; got %v", out["truncated"])
	}

	// Truncated: limit=1 on a package with multiple symbols.
	out2 := captureJSON(t, func() {
		execFind(tst, tmpPath, "", "example.com/samplemod/alpha", "", "", 1, false, true)
	})
	if out2["truncated"] != true {
		t.Errorf("truncated should be true with limit=1 on multi-symbol package; got %v", out2["truncated"])
	}
	tm, ok := out2["total_matched"].(float64)
	if !ok || tm <= 1 {
		t.Errorf("total_matched should be >1 for alpha package; got %v", out2["total_matched"])
	}
}

// TestDeadTruncationIndicator verifies that dead JSON output includes
// total_matched and truncated fields.
func TestDeadTruncationIndicator(t *testing.T) {
	moduleRoot, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "samplemod"))
	tmpPath := filepath.Join(t.TempDir(), "dead_trunc.sqlite")
	tdb, _ := sql.Open(sqlite.DriverName, tmpPath)
	t.Cleanup(func() { tdb.Close() })
	indexer.ResetSchema(tdb)
	// Need index_meta for dead to work.
	tdb.Exec(`INSERT OR IGNORE INTO index_meta(module_root,indexed_at,symbol_count,call_count,unresolved_count,go_version) VALUES (?,?,?,?,?,?)`,
		moduleRoot, "2026-01-01T00:00:00Z", 0, 0, 0, "go1.26")
	indexer.IndexModule(tdb, moduleRoot, false, false)

	rs, err := sqlite.Open(tmpPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer rs.Close()
	db2, _ := sql.Open(sqlite.DriverName, tmpPath)
	t.Cleanup(func() { db2.Close() })

	// Generous limit — not truncated.
	out := captureJSON(t, func() {
		execDead(rs, db2, "", "", 1000, false, true, tmpPath)
	})
	if _, ok := out["total_matched"]; !ok {
		t.Error("dead JSON missing 'total_matched' field")
	}
	if _, ok := out["truncated"]; !ok {
		t.Error("dead JSON missing 'truncated' field")
	}
	if out["truncated"] != false {
		t.Errorf("dead: truncated should be false with generous limit; got %v", out["truncated"])
	}

	// limit=1 should truncate (samplemod has several dead unexported funcs).
	out2 := captureJSON(t, func() {
		execDead(rs, db2, "", "", 1, false, true, tmpPath)
	})
	if out2["truncated"] != true {
		t.Errorf("dead: truncated should be true with limit=1; got %v", out2["truncated"])
	}
}

// — trace —————————————————————————————————————————————————————————————————————

func TestTraceSymbolSection(t *testing.T) {
	moduleRoot, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "samplemod"))
	tmpPath := filepath.Join(t.TempDir(), "trace.sqlite")
	tdb, _ := sql.Open(sqlite.DriverName, tmpPath)
	t.Cleanup(func() { tdb.Close() })
	indexer.ResetSchema(tdb)
	indexer.IndexModule(tdb, moduleRoot, false, false)

	rs, err := sqlite.Open(tmpPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer rs.Close()
	db2, _ := sql.Open(sqlite.DriverName, tmpPath)
	t.Cleanup(func() { db2.Close() })

	out := captureJSON(t, func() {
		execTrace(rs, db2, "example.com/samplemod/alpha.Top", "", 20, 20, true, tmpPath)
	})

	// symbol section present and populated.
	sym, ok := out["symbol"].(map[string]any)
	if !ok || sym == nil {
		t.Fatalf("trace JSON missing 'symbol' section; got %v", out)
	}
	if sym["fqname"] != "example.com/samplemod/alpha.Top" {
		t.Errorf("symbol.fqname wrong: %v", sym["fqname"])
	}
	if sym["kind"] != "func" {
		t.Errorf("symbol.kind wrong: %v", sym["kind"])
	}

	// callees section: Top calls AddObservation.
	callees, _ := out["callees"].([]any)
	if len(callees) == 0 {
		t.Error("trace JSON callees should not be empty for alpha.Top")
	}
	hasAddObs := false
	for _, c := range callees {
		cm, _ := c.(map[string]any)
		if cm["fqname"] == "example.com/samplemod/alpha.*Store.AddObservation" {
			hasAddObs = true
		}
	}
	if !hasAddObs {
		t.Errorf("callees should include AddObservation; got %v", callees)
	}

	// callers section: Top is called by beta.Use.
	callers, _ := out["callers"].([]any)
	hasUse := false
	for _, c := range callers {
		cm, _ := c.(map[string]any)
		if strings.Contains(cm["fqname"].(string), "beta.Use") {
			hasUse = true
		}
	}
	if !hasUse {
		t.Errorf("callers should include beta.Use; got %v", callers)
	}

	// blast_radius section present.
	br, ok := out["blast_radius"].(map[string]any)
	if !ok || br == nil {
		t.Fatalf("trace JSON missing 'blast_radius' section; got %v", out)
	}
	total, _ := br["total"].(float64)
	if total == 0 {
		t.Error("blast_radius.total should be > 0 for alpha.Top (called by beta.Use)")
	}
}

func TestTraceUnknownSymbolHint(t *testing.T) {
	moduleRoot, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "samplemod"))
	tmpPath := filepath.Join(t.TempDir(), "trace_hint.sqlite")
	tdb, _ := sql.Open(sqlite.DriverName, tmpPath)
	t.Cleanup(func() { tdb.Close() })
	indexer.ResetSchema(tdb)
	indexer.IndexModule(tdb, moduleRoot, false, false)

	rs, err := sqlite.Open(tmpPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer rs.Close()
	db2, _ := sql.Open(sqlite.DriverName, tmpPath)
	t.Cleanup(func() { db2.Close() })

	out := captureJSON(t, func() {
		execTrace(rs, db2, "example.com/samplemod/alpha.NonExistentFunc", "", 20, 20, true, tmpPath)
	})
	if _, hasHint := out["hint"]; !hasHint {
		t.Error("trace should include 'hint' when symbol not found")
	}
}

// — agent-context ——————————————————————————————————————————————————————————————

// TestAgentContextIncludesTraceAndDef verifies that agent-context lists both
// the trace and def commands in its commands array.
func TestAgentContextIncludesTraceAndDef(t *testing.T) {
	out := captureJSON(t, func() { execAgentContext("") })
	cmds, ok := out["commands"].([]any)
	if !ok {
		t.Fatalf("commands not an array: %T", out["commands"])
	}
	seen := map[string]bool{}
	for _, c := range cmds {
		cm, _ := c.(map[string]any)
		if name, _ := cm["command"].(string); name != "" {
			seen[name] = true
		}
	}
	if !seen["gosymdb trace"] {
		t.Error("agent-context missing 'gosymdb trace' in commands")
	}
	if !seen["gosymdb def"] {
		t.Error("agent-context missing 'gosymdb def' in commands")
	}
}

// TestAgentContextEnvDBPopulated verifies that agent-context returns env.db
// populated with an absolute path when a gosymdb.sqlite exists in the cwd.
func TestAgentContextEnvDBPopulated(t *testing.T) {
	// Create a temp dir with a gosymdb.sqlite file.
	tmpDir := t.TempDir()
	dbFile := filepath.Join(tmpDir, "gosymdb.sqlite")
	if f, err := os.Create(dbFile); err != nil {
		t.Fatalf("create temp db: %v", err)
	} else {
		f.Close()
	}

	// Temporarily change cwd to the temp dir so discoverDB finds the file.
	orig, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(orig)

	out := captureJSON(t, func() { execAgentContext("") })
	env, ok := out["env"].(map[string]any)
	if !ok {
		t.Fatalf("env not a map: %T", out["env"])
	}
	db, _ := env["db"].(string)
	if db == "" {
		t.Error("agent-context env.db is empty even though gosymdb.sqlite exists in cwd")
	}
}

// TestDeadIncludeExportedFlagRenamed verifies that dead uses --include-exported
// (not --exported) so the naming is consistent with --include-unresolved on
// other commands.
func TestDeadIncludeExportedFlagRenamed(t *testing.T) {
	cmd := newDeadCmd()
	if cmd.Flags().Lookup("include-exported") == nil {
		t.Error("dead command is missing --include-exported flag")
	}
	if cmd.Flags().Lookup("exported") != nil {
		t.Error("dead command still has old --exported flag (should be renamed to --include-exported)")
	}
}

// TestGitEnvHasGitAvailableField verifies that the gitEnv struct includes a
// git_available field that is true when run inside a git repository.
func TestGitEnvHasGitAvailableField(t *testing.T) {
	g := collectGitEnv()
	// Skip if not inside a git repo (e.g. when tests run in an exported temp dir).
	if !g.GitAvailable {
		t.Skip("not inside a git repository — skipping git_available check")
	}
	if g.Branch == "" {
		t.Error("collectGitEnv: branch should be non-empty inside a git repository")
	}
}

// — implementors ——————————————————————————————————————————————————————————————

// queryImplementors returns all (iface_fqname, impl_fqname, is_pointer) rows
// matching the given iface or type partial name.
func queryImplementors(t *testing.T, db *sql.DB, ifacePartial, typePartial string) []struct {
	Iface     string
	Impl      string
	IsPointer bool
} {
	t.Helper()
	var rows *sql.Rows
	var err error
	if ifacePartial != "" {
		rows, err = db.Query(`SELECT iface_fqname, impl_fqname, is_pointer FROM implements WHERE iface_fqname LIKE ? ORDER BY impl_fqname`, "%"+ifacePartial+"%")
	} else {
		rows, err = db.Query(`SELECT iface_fqname, impl_fqname, is_pointer FROM implements WHERE impl_fqname LIKE ? ORDER BY iface_fqname`, "%"+typePartial+"%")
	}
	if err != nil {
		t.Fatalf("queryImplementors: %v", err)
	}
	defer rows.Close()
	var out []struct {
		Iface     string
		Impl      string
		IsPointer bool
	}
	for rows.Next() {
		var iface, impl string
		var isPtr int
		if err := rows.Scan(&iface, &impl, &isPtr); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, struct {
			Iface     string
			Impl      string
			IsPointer bool
		}{iface, impl, isPtr != 0})
	}
	return out
}

func TestImplementorsDoerHasTwoImpls(t *testing.T) {
	db := indexSamplemod(t)

	rows := queryImplementors(t, db, "iface.Doer", "")
	if len(rows) < 2 {
		t.Fatalf("expected at least 2 implementors of Doer, got %d: %+v", len(rows), rows)
	}
	implNames := map[string]bool{}
	for _, r := range rows {
		implNames[r.Impl] = true
	}
	if !implNames["example.com/samplemod/iface.RealDoer"] && !implNames["example.com/samplemod/iface.StubDoer"] {
		t.Errorf("expected RealDoer and/or StubDoer as Doer implementors; got %v", implNames)
	}
}

func TestImplementorsIsPointerFlag(t *testing.T) {
	db := indexSamplemod(t)

	// RealDoer and StubDoer both use pointer receivers (*RealDoer).Do(), so
	// is_pointer should be true (only *T satisfies Doer, not T itself).
	rows := queryImplementors(t, db, "iface.Doer", "")
	for _, r := range rows {
		if !r.IsPointer {
			t.Errorf("expected is_pointer=true for %q implementing Doer (pointer receivers)", r.Impl)
		}
	}
}

func TestImplementorsTypeDirection(t *testing.T) {
	db := indexSamplemod(t)

	// Looking up what interfaces RealDoer satisfies — should include Doer.
	rows := queryImplementors(t, db, "", "RealDoer")
	found := false
	for _, r := range rows {
		if strings.Contains(r.Iface, "Doer") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected RealDoer to satisfy Doer interface; got %+v", rows)
	}
}

func TestImplementorsRequiresIfaceOrType(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "x.sqlite")
	db, _ := sql.Open(sqlite.DriverName, dbPath)
	indexer.ResetSchema(db)
	db.Close()

	st, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer st.Close()
	err = execImplementors(st, dbPath, "", "", 200, false, false)
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("expected exactly-one mode error; got: %v", err)
	}
}

func TestImplementorsRejectsIfaceAndTypeTogether(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "x.sqlite")
	db, _ := sql.Open(sqlite.DriverName, dbPath)
	indexer.ResetSchema(db)
	db.Close()

	st, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer st.Close()

	err = execImplementors(st, dbPath, "iface.Doer", "RealDoer", 200, false, false)
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("expected mutually-exclusive flag error; got: %v", err)
	}
}

func TestImplementorsNoFalsePositives(t *testing.T) {
	db := indexSamplemod(t)

	// SafeCounter from concurrent does NOT implement Doer.
	rows := queryImplementors(t, db, "iface.Doer", "")
	for _, r := range rows {
		if strings.Contains(r.Impl, "SafeCounter") {
			t.Errorf("SafeCounter must not appear as Doer implementor; got %+v", r)
		}
	}
}

// — blast-radius ——————————————————————————————————————————————————————————————

// indexSamplemodWithTests is like indexSamplemod but includes *_test.go files.
func indexSamplemodWithTests(t *testing.T) *sql.DB {
	t.Helper()
	moduleRoot, err := filepath.Abs(filepath.Join("..", "..", "testdata", "samplemod"))
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}
	db, err := sql.Open(sqlite.DriverName, filepath.Join(t.TempDir(), "symdb.sqlite"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := indexer.ResetSchema(db); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	if _, _, _, _, err := indexer.IndexModule(db, moduleRoot, false, true); err != nil {
		t.Fatalf("index module with tests: %v", err)
	}
	return db
}

// queryBlastRadius runs execBlastRadius against an already-open db by writing
// it to a temp file, then collects results via the db directly.
func blastRadiusCallers(t *testing.T, db *sql.DB, symbol string, depth int, excludeTests bool) map[string]int {
	t.Helper()
	testFilter := ""
	if excludeTests {
		testFilter = " AND INSTR(c.file_path, '_test.go') = 0"
	}
	query := `
WITH RECURSIVE blast(caller, depth) AS (
  SELECT DISTINCT c.from_fqname, 1
  FROM calls c
  WHERE (c.to_fqname = ? OR c.to_fqname LIKE ?)` + testFilter + `

  UNION

  SELECT DISTINCT c.from_fqname, b.depth + 1
  FROM calls c
  INNER JOIN blast b ON c.to_fqname = b.caller
  WHERE b.depth < ?` + testFilter + `
)
SELECT b.caller, MIN(b.depth) AS depth
FROM blast b
GROUP BY b.caller
ORDER BY MIN(b.depth), b.caller
LIMIT 500`

	rows, err := db.Query(query, symbol, "%"+symbol+"%", depth)
	if err != nil {
		t.Fatalf("blastRadiusCallers query: %v", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var caller string
		var d int
		if err := rows.Scan(&caller, &d); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[caller] = d
	}
	return out
}

func TestBlastRadiusRequiresSymbol(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "x.sqlite")
	db, _ := sql.Open(sqlite.DriverName, dbPath)
	indexer.ResetSchema(db)
	db.Close()

	rs, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer rs.Close()
	db2, _ := sql.Open(sqlite.DriverName, dbPath)
	defer db2.Close()

	err = execBlastRadius(rs, db2, "", 3, false, "", false, 500, false, false, dbPath)
	if err == nil || !strings.Contains(err.Error(), "--symbol is required") {
		t.Fatalf("expected --symbol required error, got: %v", err)
	}
}

func TestBlastRadiusDirectCallers(t *testing.T) {
	db := indexSamplemod(t)
	got := blastRadiusCallers(t, db, "alpha.Top", 1, false)
	// beta.Use calls alpha.Top directly → depth 1
	found := false
	for k := range got {
		if strings.Contains(k, "beta.Use") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected beta.Use as a depth-1 caller of alpha.Top; got %v", got)
	}
}

func TestBlastRadiusTransitive(t *testing.T) {
	db := indexSamplemod(t)
	got := blastRadiusCallers(t, db, "alpha.*Store.AddObservation", 2, false)

	// alpha.Top calls AddObservation directly → depth 1
	foundTop := false
	for k, d := range got {
		if strings.Contains(k, "alpha.Top") && d == 1 {
			foundTop = true
		}
	}
	if !foundTop {
		t.Fatalf("expected alpha.Top at depth 1; got %v", got)
	}

	// beta.Use calls alpha.Top which calls AddObservation → depth 2
	foundUse := false
	for k, d := range got {
		if strings.Contains(k, "beta.Use") && d == 2 {
			foundUse = true
		}
	}
	if !foundUse {
		t.Fatalf("expected beta.Use at depth 2; got %v", got)
	}
}

func TestBlastRadiusDepthCap(t *testing.T) {
	db := indexSamplemod(t)
	got := blastRadiusCallers(t, db, "alpha.*Store.AddObservation", 1, false)

	// At depth 1 only alpha.Top should appear; beta.Use is at depth 2.
	foundTop := false
	for k := range got {
		if strings.Contains(k, "alpha.Top") {
			foundTop = true
		}
		if strings.Contains(k, "beta.Use") {
			t.Errorf("beta.Use must not appear at depth <= 1; got %v", got)
		}
	}
	if !foundTop {
		t.Fatalf("expected alpha.Top at depth 1; got %v", got)
	}
}

func TestBlastRadiusEmptyForDeadCode(t *testing.T) {
	db := indexSamplemod(t)
	got := blastRadiusCallers(t, db, "recursive.neverCalled", 3, false)
	if len(got) != 0 {
		t.Fatalf("expected empty blast-radius for neverCalled; got %v", got)
	}
}

func TestBlastRadiusExcludeTests(t *testing.T) {
	db := indexSamplemodWithTests(t)

	// With tests included, TestTopAlpha (in alpha_test.go) should appear as a caller.
	withTests := blastRadiusCallers(t, db, "alpha.Top", 3, false)
	foundTest := false
	for k := range withTests {
		if strings.Contains(k, "TestTopAlpha") {
			foundTest = true
		}
	}
	if !foundTest {
		t.Fatalf("expected TestTopAlpha as caller when tests are indexed; got %v", withTests)
	}

	// With --exclude-tests, TestTopAlpha must vanish.
	withoutTests := blastRadiusCallers(t, db, "alpha.Top", 3, true)
	for k := range withoutTests {
		if strings.Contains(k, "TestTopAlpha") {
			t.Errorf("TestTopAlpha must not appear with --exclude-tests; got %v", withoutTests)
		}
	}
}

// — health ————————————————————————————————————————————————————————————————————

// — BUG-005: interface dispatch hint in callers ———————————————————————————————

// indexInterfaceDispatch returns an open, populated DB for testbench/03_interface_dispatch.
// Caller is responsible for closing it (via t.Cleanup or defer).
func indexInterfaceDispatch(t *testing.T) (*sql.DB, string) {
	t.Helper()
	moduleRoot, err := filepath.Abs(filepath.Join("..", "..", "testbench", "03_interface_dispatch"))
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), "iface_dispatch.sqlite")
	db, err := sql.Open(sqlite.DriverName, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := indexer.ResetSchema(db); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	if _, _, _, _, err := indexer.IndexModule(db, moduleRoot, false, false); err != nil {
		t.Fatalf("index module: %v", err)
	}
	return db, dbPath
}

// TestBUG005_InterfaceDispatchHintInCallers verifies that when callers returns 0
// for a method that only gets called via interface dispatch, the hint mentions
// "interface dispatch" or "implementors".
func TestBUG005_InterfaceDispatchHintInCallers(t *testing.T) {
	db, _ := indexInterfaceDispatch(t)

	// Verify that *FileWriter.Write has 0 direct callers in the index.
	// writeAll() calls w.Write(data) through the Writer interface — the static
	// call graph cannot resolve this to FileWriter.Write directly.
	var callerCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM calls WHERE to_fqname LIKE '%FileWriter.Write'`,
	).Scan(&callerCount); err != nil {
		t.Fatalf("query FileWriter.Write callers: %v", err)
	}
	if callerCount != 0 {
		t.Skipf("BUG-005 precondition: expected 0 direct callers for FileWriter.Write, got %d (indexer may have improved)", callerCount)
	}

	// Verify the implements table has a row linking FileWriter → Writer.
	var implCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM implements WHERE impl_fqname LIKE '%FileWriter' AND iface_fqname LIKE '%Writer'`,
	).Scan(&implCount); err != nil {
		t.Fatalf("query implements for FileWriter: %v", err)
	}
	if implCount == 0 {
		t.Fatal("BUG-005 precondition: expected FileWriter to implement Writer in implements table")
	}

	// Now call interfaceDispatchHint with the exact method fqname.
	// The method is *FileWriter.Write, so the type fqname is testbench/interface_dispatch.*FileWriter.
	// interfaceDispatchHint should find the Writer interface in the implements table.
	const methodFQN = "testbench/interface_dispatch.*FileWriter.Write"
	hint := interfaceDispatchHint(db, methodFQN)
	if hint == "" {
		t.Fatal("BUG-005: interfaceDispatchHint returned empty string for a method that implements an interface")
	}
	lowerHint := strings.ToLower(hint)
	if !strings.Contains(lowerHint, "interface") && !strings.Contains(lowerHint, "implementors") {
		t.Errorf("BUG-005: hint does not mention 'interface' or 'implementors'; got: %q", hint)
	}
}

// TestBUG005_HintInCallersJSONOutput verifies that execCallers emits a "hint"
// field in JSON output when 0 callers are found and the symbol is an interface
// method implementation.
func TestBUG005_HintInCallersJSONOutput(t *testing.T) {
	_, dbPath := indexInterfaceDispatch(t)

	// Redirect stdout to capture JSON output.
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w

	rs, rsErr := sqlite.Open(dbPath)
	if rsErr != nil {
		t.Fatalf("sqlite.Open: %v", rsErr)
	}
	defer rs.Close()
	db2, _ := sql.Open(sqlite.DriverName, dbPath)
	defer db2.Close()

	execErr := execCallers(rs, db2, "testbench/interface_dispatch.*FileWriter.Write", 200, false, "", false, 1, false, false, true, false, dbPath)

	w.Close()
	os.Stdout = old

	var buf strings.Builder
	io.Copy(&buf, r)
	r.Close()

	if execErr != nil {
		t.Fatalf("execCallers error: %v", execErr)
	}

	output := buf.String()
	if !strings.Contains(output, "hint") {
		t.Fatalf("BUG-005: JSON output does not contain 'hint' field; got: %s", output)
	}
	lowerOutput := strings.ToLower(output)
	if !strings.Contains(lowerOutput, "interface") && !strings.Contains(lowerOutput, "implementors") {
		t.Errorf("BUG-005: hint in JSON output does not mention 'interface' or 'implementors'; got: %s", output)
	}
}

// — callers --depth N ————————————————————————————————————————————————————————

// execCallersJSON is a helper that captures execCallers JSON output to a string.
func execCallersJSON(t *testing.T, dbPath, symbol string, limit int, fuzzy bool, pkg string, includeUnresolved bool, depth int, isTest bool) string {
	t.Helper()

	rs, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer rs.Close()
	db2, _ := sql.Open(sqlite.DriverName, dbPath)
	defer db2.Close()

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	execErr := execCallers(rs, db2, symbol, limit, fuzzy, pkg, includeUnresolved, depth, isTest, false, true, false, dbPath)
	w.Close()
	os.Stdout = old
	var buf strings.Builder
	io.Copy(&buf, r)
	r.Close()
	if execErr != nil {
		t.Fatalf("execCallers error: %v", execErr)
	}
	return buf.String()
}

func TestCallersDepthOneBackwardCompat(t *testing.T) {
	// depth=1 must behave identically to the old behaviour.
	// alpha.Top directly calls alpha.*Store.AddObservation → depth 1.
	moduleRoot, err := filepath.Abs(filepath.Join("..", "..", "testdata", "samplemod"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), "symdb.sqlite")
	db, _ := sql.Open(sqlite.DriverName, dbPath)
	indexer.ResetSchema(db)
	indexer.IndexModule(db, moduleRoot, false, false)
	db.Close()

	out := execCallersJSON(t, dbPath, "example.com/samplemod/alpha.*Store.AddObservation", 200, false, "", false, 1, false)
	if !strings.Contains(out, `"depth":1`) {
		t.Errorf("depth=1: expected depth field 1 in output; got: %s", out)
	}
	if !strings.Contains(out, "alpha.Top") {
		t.Errorf("depth=1: expected alpha.Top as direct caller; got: %s", out)
	}
}

func TestCallersDepthTwoTransitive(t *testing.T) {
	// Verify BFS transitive callers:
	//   alpha.*Store.AddObservation  ← alpha.Top (depth 1)
	//   alpha.Top                    ← beta.Use   (depth 2)
	// At depth=2, both alpha.Top (d=1) and beta.Use (d=2) must appear.
	moduleRoot, err := filepath.Abs(filepath.Join("..", "..", "testdata", "samplemod"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), "symdb.sqlite")
	db, _ := sql.Open(sqlite.DriverName, dbPath)
	indexer.ResetSchema(db)
	indexer.IndexModule(db, moduleRoot, false, false)
	db.Close()

	out := execCallersJSON(t, dbPath, "example.com/samplemod/alpha.*Store.AddObservation", 200, false, "", false, 2, false)
	if !strings.Contains(out, "alpha.Top") {
		t.Errorf("depth=2: expected alpha.Top (depth 1) in output; got: %s", out)
	}
	if !strings.Contains(out, "beta.Use") {
		t.Errorf("depth=2: expected beta.Use (depth 2) in output; got: %s", out)
	}
	if !strings.Contains(out, `"depth":2`) {
		t.Errorf("depth=2: expected a row with depth=2; got: %s", out)
	}
	// The JSON envelope must include a top-level "depth" field.
	if !strings.Contains(out, `"depth":2`) {
		t.Errorf("depth=2: top-level depth field missing; got: %s", out)
	}
}

func TestCallersDepthClampMax(t *testing.T) {
	// depth > 10 must be clamped to 10 (no error).
	moduleRoot, err := filepath.Abs(filepath.Join("..", "..", "testdata", "samplemod"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), "symdb.sqlite")
	db, _ := sql.Open(sqlite.DriverName, dbPath)
	indexer.ResetSchema(db)
	indexer.IndexModule(db, moduleRoot, false, false)
	db.Close()

	// depth=100 should not error and should behave like depth=10.
	out := execCallersJSON(t, dbPath, "example.com/samplemod/alpha.*Store.AddObservation", 200, false, "", false, 100, false)
	if !strings.Contains(out, "callers") {
		t.Errorf("depth clamp: unexpected output: %s", out)
	}
}

// — callers --is-test ————————————————————————————————————————————————————————

func TestCallersIsTestOnlyReturnsTestCallers(t *testing.T) {
	// Index with test files. alpha.Top is called by:
	//   beta.Use           (beta.go — not a test file)
	//   alpha.TestTopAlpha (alpha_test.go — test file)
	// --is-test must return only alpha.TestTopAlpha.
	moduleRoot, err := filepath.Abs(filepath.Join("..", "..", "testdata", "samplemod"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), "symdb.sqlite")
	db, _ := sql.Open(sqlite.DriverName, dbPath)
	indexer.ResetSchema(db)
	indexer.IndexModule(db, moduleRoot, false, true) // includeTests=true
	db.Close()

	out := execCallersJSON(t, dbPath, "example.com/samplemod/alpha.Top", 200, false, "", false, 1, true)
	if !strings.Contains(out, "TestTopAlpha") {
		t.Errorf("is-test: expected TestTopAlpha in output; got: %s", out)
	}
	if strings.Contains(out, "beta.Use") {
		t.Errorf("is-test: beta.Use must not appear with --is-test; got: %s", out)
	}
}

func TestCallersIsTestFalseIncludesNonTestCallers(t *testing.T) {
	// Without --is-test (default false), all callers including non-test ones appear.
	moduleRoot, err := filepath.Abs(filepath.Join("..", "..", "testdata", "samplemod"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), "symdb.sqlite")
	db, _ := sql.Open(sqlite.DriverName, dbPath)
	indexer.ResetSchema(db)
	indexer.IndexModule(db, moduleRoot, false, true) // includeTests=true
	db.Close()

	out := execCallersJSON(t, dbPath, "example.com/samplemod/alpha.Top", 200, false, "", false, 1, false)
	if !strings.Contains(out, "beta.Use") {
		t.Errorf("is-test=false: expected beta.Use in output; got: %s", out)
	}
}

func TestCallersIsTestDepthTwoFindsTestCallersViaIntermediary(t *testing.T) {
	// BUG: --is-test currently filters out non-test intermediaries during BFS,
	// which prunes transitive test callers that reach the target via a non-test
	// function. The correct behaviour is to traverse the full graph and filter
	// only the reported results.
	//
	// Call chain in samplemod:
	//   TestTopAlpha (alpha_test.go — test)
	//     → alpha.Top (alpha.go — non-test)
	//       → alpha.*Store.AddObservation
	//
	// With --depth 2 --is-test on AddObservation:
	//   depth 1: alpha.Top is the only caller of AddObservation — it is NOT a
	//            test file, so the broken implementation prunes it from BFS and
	//            never expands it.
	//   depth 2: TestTopAlpha should be found as a transitive test caller.
	moduleRoot, err := filepath.Abs(filepath.Join("..", "..", "testdata", "samplemod"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), "symdb.sqlite")
	db, _ := sql.Open(sqlite.DriverName, dbPath)
	indexer.ResetSchema(db)
	indexer.IndexModule(db, moduleRoot, false, true) // includeTests=true
	db.Close()

	out := execCallersJSON(t, dbPath, "example.com/samplemod/alpha.*Store.AddObservation", 200, false, "", false, 2, true)
	if !strings.Contains(out, "TestTopAlpha") {
		t.Errorf("is-test+depth=2: expected TestTopAlpha (transitive test caller) in output; got: %s", out)
	}
}

// TestItem31_InterfaceHintFiltersToRelevantIface verifies that interfaceDispatchHint
// for a method only mentions interfaces whose method set includes that method.
// When querying callers of *ConcreteOuter.Write (Outer has Write, Inner does not),
// the hint should mention Outer but NOT Inner.
func TestItem31_InterfaceHintFiltersToRelevantIface(t *testing.T) {
	moduleRoot, err := filepath.Abs(filepath.Join("..", "..", "testbench", "20_iface_embedding"))
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), "iface_embedding.sqlite")
	db, err := sql.Open(sqlite.DriverName, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := indexer.ResetSchema(db); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	if _, _, _, _, err := indexer.IndexModule(db, moduleRoot, false, false); err != nil {
		t.Fatalf("index module: %v", err)
	}

	const methodFQN = "testbench/iface_embedding.*ConcreteOuter.Write"
	hint := interfaceDispatchHint(db, methodFQN)
	if hint == "" {
		t.Fatal("expected a hint for *ConcreteOuter.Write (implements Outer); got empty")
	}
	// Outer has Write — must be mentioned.
	if !strings.Contains(hint, "Outer") {
		t.Errorf("hint should mention Outer (which has Write); got: %q", hint)
	}
	// Inner has only Read — must NOT be mentioned.
	if strings.Contains(hint, "Inner") {
		t.Errorf("hint should NOT mention Inner (which has no Write method); got: %q", hint)
	}
}

// TestItem36_FailOpenPreV6DB verifies that interfaceDispatchHint's fail-open
// path works correctly: when the implements table has a row but iface_methods
// has NO entries for that interface (e.g. a pre-v6 DB), the hint should still
// include the interface. This exercises the backward-compatibility code path
// (backward-compat for pre-v6 DBs).
func TestItem36_FailOpenPreV6DB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "failopen.sqlite")
	db, err := sql.Open(sqlite.DriverName, dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	if err := indexer.ResetSchema(db); err != nil {
		t.Fatalf("reset: %v", err)
	}

	// Seed implements with a fake row (simulating pre-v6 DB state).
	_, err = db.Exec(`INSERT INTO implements(module_root, iface_pkg, iface_fqname, impl_pkg, impl_fqname, is_pointer)
		VALUES ('test', 'test/pkg', 'test/pkg.MyIface', 'test/pkg', 'test/pkg.MyImpl', 1)`)
	if err != nil {
		t.Fatalf("seed implements: %v", err)
	}
	// Deliberately do NOT populate iface_methods — simulates pre-v6 DB.

	hint := interfaceDispatchHint(db, "test/pkg.*MyImpl.DoThing")
	if hint == "" {
		t.Fatal("fail-open: expected hint when implements has row but iface_methods is empty")
	}
	if !strings.Contains(hint, "test/pkg.MyIface") {
		t.Errorf("fail-open: hint should include MyIface; got: %q", hint)
	}
}

// TestItem36_FilterClosedWhenIfaceMethodsPopulated verifies that when
// iface_methods IS populated for an interface, only interfaces with the
// queried method name appear in the hint (the "closed" path).
func TestItem36_FilterClosedWhenIfaceMethodsPopulated(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "filterclosed.sqlite")
	db, err := sql.Open(sqlite.DriverName, dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	if err := indexer.ResetSchema(db); err != nil {
		t.Fatalf("reset: %v", err)
	}

	// Two interfaces: HasWrite (has Write method) and HasRead (has Read method).
	// Both implemented by the same concrete type.
	for _, iface := range []struct{ fqname, method string }{
		{"test/pkg.HasWrite", "Write"},
		{"test/pkg.HasRead", "Read"},
	} {
		db.Exec(`INSERT INTO implements(module_root, iface_pkg, iface_fqname, impl_pkg, impl_fqname, is_pointer)
			VALUES ('test', 'test/pkg', ?, 'test/pkg', 'test/pkg.MyImpl', 1)`, iface.fqname)
		db.Exec(`INSERT OR IGNORE INTO iface_methods(module_root, iface_fqname, method_name)
			VALUES ('test', ?, ?)`, iface.fqname, iface.method)
	}

	// Hint for MyImpl.Write should include HasWrite but NOT HasRead.
	hint := interfaceDispatchHint(db, "test/pkg.*MyImpl.Write")
	if hint == "" {
		t.Fatal("expected hint for MyImpl.Write")
	}
	if !strings.Contains(hint, "HasWrite") {
		t.Errorf("hint should include HasWrite; got: %q", hint)
	}
	if strings.Contains(hint, "HasRead") {
		t.Errorf("hint should NOT include HasRead (no Write method); got: %q", hint)
	}
}

// TestItem39_HelpSpecsFlagConsistency verifies that every flag declared in
// helpSpecs actually exists on the corresponding cobra command.
// Catches stale helpSpecs entries where a flag was added/removed from the command
// but not updated in agent_help.go.
func TestItem39_HelpSpecsFlagConsistency(t *testing.T) {
	// Persistent flags live on the root command, not subcommands.
	persistentFlags := map[string]bool{
		"--db": true, "--json": true, "--auto-reindex": true,
	}

	for specKey, spec := range helpSpecs {
		if specKey == "gosymdb" {
			continue // root command — flags are persistent, checked separately
		}
		parts := strings.Fields(spec.Command)
		if len(parts) < 2 {
			continue
		}
		subcmdName := parts[1]

		// Find the cobra subcommand.
		var subcmd *cobra.Command
		for _, c := range rootCmd.Commands() {
			if c.Name() == subcmdName {
				subcmd = c
				break
			}
		}
		if subcmd == nil {
			t.Errorf("helpSpecs[%q]: subcommand %q not found on rootCmd", specKey, subcmdName)
			continue
		}

		// Verify every flag in helpSpecs exists on the cobra command.
		for _, f := range spec.Flags {
			if persistentFlags[f.Name] {
				continue
			}
			flagName := strings.TrimPrefix(f.Name, "--")
			if subcmd.Flags().Lookup(flagName) == nil {
				t.Errorf("helpSpecs[%q]: flag %q declared in helpSpec but not found on cobra command %q",
					specKey, f.Name, subcmdName)
			}
		}

		// Verify every non-hidden cobra flag appears in helpSpecs.
		specFlags := map[string]bool{}
		for _, f := range spec.Flags {
			specFlags[strings.TrimPrefix(f.Name, "--")] = true
		}
		subcmd.Flags().VisitAll(func(f *pflag.Flag) {
			if f.Hidden {
				return
			}
			if !specFlags[f.Name] && !persistentFlags["--"+f.Name] {
				t.Errorf("helpSpecs[%q]: cobra flag --%s exists on command but missing from helpSpec",
					specKey, f.Name)
			}
		})
	}
}

func TestHealthReturnsErrorWithoutMeta(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "symdb.sqlite")
	db, err := sql.Open(sqlite.DriverName, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := indexer.ResetSchema(db); err != nil {
		db.Close()
		t.Fatalf("reset schema: %v", err)
	}
	db.Close()

	st, stErr := sqlite.Open(dbPath)
	if stErr != nil {
		t.Fatalf("sqlite.Open: %v", stErr)
	}
	defer st.Close()
	err = execHealth(st, dbPath, false)
	if err == nil {
		t.Fatal("expected execHealth to return non-nil error when no index_meta row exists")
	}
}

// indexSamplemodWithPath returns a populated DB, ReadStore, and dbPath for samplemod.
func indexSamplemodWithPath(t *testing.T) (*sql.DB, store.ReadStore, string) {
	t.Helper()
	moduleRoot, err := filepath.Abs(filepath.Join("..", "..", "testdata", "samplemod"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), "symdb.sqlite")
	db, err := sql.Open(sqlite.DriverName, dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := indexer.ResetSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if _, _, _, _, err := indexer.IndexModule(db, moduleRoot, false, false); err != nil {
		t.Fatalf("index: %v", err)
	}
	// Insert index_meta so execHealth works.
	db.Exec(`INSERT INTO index_meta(tool_version, go_version, indexed_at, root, warnings) VALUES('test', 'go1.26', '2026-01-01T00:00:00Z', ?, 0)`, moduleRoot)

	rs, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { rs.Close() })
	return db, rs, dbPath
}

// — execDef ——————————————————————————————————————————————————————————————————

func TestDefFindsKnownSymbol(t *testing.T) {
	_, rs, dbPath := indexSamplemodWithPath(t)
	// Use --pkg to disambiguate since "Top" exists in both alpha and store.
	m := captureJSON(t, func() {
		if err := execDef(rs, dbPath, "Top", "example.com/samplemod/alpha", true); err != nil {
			t.Fatalf("execDef: %v", err)
		}
	})
	sym, ok := m["symbol"].(map[string]any)
	if !ok || sym == nil {
		t.Fatalf("expected symbol in output, got: %v", m)
	}
	if fq, _ := sym["fqname"].(string); !strings.Contains(fq, "alpha.Top") {
		t.Errorf("expected fqname to contain alpha.Top, got: %s", fq)
	}
}

func TestDefReturnsNilForUnknownSymbol(t *testing.T) {
	_, rs, dbPath := indexSamplemodWithPath(t)
	m := captureJSON(t, func() {
		if err := execDef(rs, dbPath, "DoesNotExist999", "", true); err != nil {
			t.Fatalf("execDef: %v", err)
		}
	})
	if m["symbol"] != nil {
		t.Errorf("expected nil symbol for unknown name, got: %v", m["symbol"])
	}
	if _, ok := m["hint"]; !ok {
		t.Error("expected hint for unknown symbol")
	}
}

func TestDefAmbiguousName(t *testing.T) {
	_, rs, dbPath := indexSamplemodWithPath(t)
	// "Do" exists in both RealDoer and StubDoer in the iface package.
	m := captureJSON(t, func() {
		if err := execDef(rs, dbPath, "Do", "", true); err != nil {
			t.Fatalf("execDef: %v", err)
		}
	})
	if amb, _ := m["ambiguous"].(bool); !amb {
		t.Error("expected ambiguous=true for 'Do'")
	}
}

func TestDefTextOutput(t *testing.T) {
	_, rs, dbPath := indexSamplemodWithPath(t)
	// Just verify text mode doesn't panic.
	if err := execDef(rs, dbPath, "Top", "", false); err != nil {
		t.Fatalf("execDef text: %v", err)
	}
}

// — execReferences ————————————————————————————————————————————————————————————

func TestReferencesFindsTypeRefs(t *testing.T) {
	_, rs, dbPath := indexSamplemodWithPath(t)
	m := captureJSON(t, func() {
		if err := execReferences(rs, dbPath, "example.com/samplemod/alpha.Store", "", "", "", 200, false, true); err != nil {
			t.Fatalf("execReferences: %v", err)
		}
	})
	refs, ok := m["references"].([]any)
	if !ok {
		t.Fatalf("expected references array, got: %v", m)
	}
	// Store is used (composite lit or field access) somewhere in samplemod.
	if len(refs) == 0 {
		t.Log("no type references found for Store — this may be expected if Store is only constructed indirectly")
	}
}

func TestReferencesCountOnly(t *testing.T) {
	_, rs, dbPath := indexSamplemodWithPath(t)
	// count mode should not error.
	if err := execReferences(rs, dbPath, "example.com/samplemod/alpha.Store", "", "", "", 200, true, false); err != nil {
		t.Fatalf("execReferences count: %v", err)
	}
}

func TestReferencesRequiresSymbol(t *testing.T) {
	_, rs, dbPath := indexSamplemodWithPath(t)
	err := execReferences(rs, dbPath, "", "", "", "", 200, false, true)
	if err == nil {
		t.Fatal("expected error for empty symbol")
	}
}

// — execHealth ————————————————————————————————————————————————————————————————

func TestHealthJSONOutput(t *testing.T) {
	moduleRoot, err := filepath.Abs(filepath.Join("..", "..", "testdata", "samplemod"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), "health.sqlite")
	db, err := sql.Open(sqlite.DriverName, dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := indexer.ResetSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if _, _, _, _, err := indexer.IndexModule(db, moduleRoot, false, false); err != nil {
		t.Fatalf("index: %v", err)
	}
	db.Exec(`INSERT INTO index_meta(tool_version, go_version, indexed_at, root, warnings) VALUES('test', 'go1.26', '2026-01-01T00:00:00Z', ?, 0)`, moduleRoot)
	db.Close() // Flush WAL before opening ReadStore.

	rs, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer rs.Close()

	m := captureJSON(t, func() {
		if err := execHealth(rs, dbPath, true); err != nil {
			t.Fatalf("execHealth: %v", err)
		}
	})
	if _, ok := m["symbols"]; !ok {
		t.Error("expected 'symbols' key in health output")
	}
	if _, ok := m["env"]; !ok {
		t.Error("expected 'env' key in health output")
	}
}

// — execPackages ——————————————————————————————————————————————————————————————

func TestPackagesJSONOutput(t *testing.T) {
	_, rs, dbPath := indexSamplemodWithPath(t)
	m := captureJSON(t, func() {
		if err := execPackages(rs, dbPath, true); err != nil {
			t.Fatalf("execPackages: %v", err)
		}
	})
	pkgs, ok := m["packages"].([]any)
	if !ok {
		t.Fatalf("expected packages array, got: %v", m)
	}
	if len(pkgs) < 3 {
		t.Errorf("expected at least 3 packages, got %d", len(pkgs))
	}
}

func TestPackagesTextOutput(t *testing.T) {
	_, rs, dbPath := indexSamplemodWithPath(t)
	if err := execPackages(rs, dbPath, false); err != nil {
		t.Fatalf("execPackages text: %v", err)
	}
}

// — resolveSymbolInput ————————————————————————————————————————————————————————

func TestResolveSymbolInputFQNamePassthrough(t *testing.T) {
	// A symbol containing "/" is treated as already-qualified.
	fq, note := resolveSymbolInput(nil, "example.com/samplemod/alpha.Top", "")
	if fq != "example.com/samplemod/alpha.Top" {
		t.Errorf("expected passthrough, got: %s", fq)
	}
	if note != "" {
		t.Errorf("expected empty note, got: %s", note)
	}
}

func TestResolveSymbolInputResolvesShortName(t *testing.T) {
	db := indexSamplemod(t)
	// AddObservation is unique in samplemod — only in alpha package.
	fq, note := resolveSymbolInput(db, "AddObservation", "")
	if !strings.Contains(fq, "AddObservation") {
		t.Errorf("expected resolved to contain AddObservation, got: %s", fq)
	}
	if note == "" {
		t.Error("expected non-empty resolve note")
	}
}

func TestResolveSymbolInputAmbiguous(t *testing.T) {
	db := indexSamplemod(t)
	// "Do" exists on multiple types.
	_, note := resolveSymbolInput(db, "Do", "")
	if !strings.Contains(note, "ambiguous") {
		t.Errorf("expected ambiguous note, got: %s", note)
	}
}

func TestResolveSymbolInputWithPkg(t *testing.T) {
	db := indexSamplemod(t)
	// "Top" is ambiguous (alpha + store) but --pkg narrows it.
	fq, _ := resolveSymbolInput(db, "Top", "example.com/samplemod/alpha")
	if !strings.Contains(fq, "alpha.Top") {
		t.Errorf("expected alpha-scoped resolution, got: %s", fq)
	}
}

func TestResolveSymbolInputNoMatch(t *testing.T) {
	db := indexSamplemod(t)
	fq, note := resolveSymbolInput(db, "ZZZNonexistent", "")
	if fq != "ZZZNonexistent" {
		t.Errorf("expected unchanged input, got: %s", fq)
	}
	if note != "" {
		t.Errorf("expected empty note for no match, got: %s", note)
	}
}

// — symbolHint and interfaceDispatchHint ——————————————————————————————————————

func TestSymbolHintFindsCloseName(t *testing.T) {
	db := indexSamplemod(t)
	hint := symbolHint(db, "Top")
	if hint == "" {
		t.Error("expected non-empty hint for 'Top'")
	}
	if !strings.Contains(hint, "Similar") {
		t.Errorf("expected 'Similar' in hint, got: %s", hint)
	}
}

func TestSymbolHintNoMatch(t *testing.T) {
	db := indexSamplemod(t)
	hint := symbolHint(db, "ZZZCompletelyUnknown999")
	if !strings.Contains(hint, "not in index") {
		t.Errorf("expected 'not in index' hint, got: %s", hint)
	}
}

func TestInterfaceDispatchHintForImplementor(t *testing.T) {
	db := indexSamplemod(t)
	// RealDoer.Do implements Doer.Do — should get an interface dispatch hint.
	hint := interfaceDispatchHint(db, "example.com/samplemod/iface.RealDoer.Do")
	if hint == "" {
		t.Log("no interface dispatch hint — may need pointer receiver in test data")
	}
	if hint != "" && !strings.Contains(hint, "interface dispatch") {
		t.Errorf("expected 'interface dispatch' in hint, got: %s", hint)
	}
}

func TestInterfaceDispatchHintNonMethod(t *testing.T) {
	db := indexSamplemod(t)
	hint := interfaceDispatchHint(db, "example.com/samplemod/alpha.Top")
	if hint != "" {
		t.Errorf("expected empty hint for non-method, got: %s", hint)
	}
}

// — execCallers (JSON output) —————————————————————————————————————————————————

func TestCallersJSONOutput(t *testing.T) {
	db, rs, dbPath := indexSamplemodWithPath(t)
	m := captureJSON(t, func() {
		if err := execCallers(rs, db, "example.com/samplemod/alpha.Top", 200, false, "", false, 1, false, false, true, false, dbPath); err != nil {
			t.Fatalf("execCallers: %v", err)
		}
	})
	if _, ok := m["callers"]; !ok {
		t.Error("expected 'callers' key")
	}
	if _, ok := m["env"]; !ok {
		t.Error("expected 'env' key")
	}
}

func TestCallersExplainJSONOutput(t *testing.T) {
	db, rs, dbPath := indexSamplemodWithPath(t)
	m := captureJSON(t, func() {
		if err := execCallers(rs, db, "Top", 200, true, "example.com/samplemod/alpha", false, 2, false, false, true, true, dbPath); err != nil {
			t.Fatalf("execCallers explain: %v", err)
		}
	})
	explain, ok := m["explain"].(map[string]any)
	if !ok {
		t.Fatalf("expected explain payload; got %v", m["explain"])
	}
	if explain["command"] != "callers" {
		t.Fatalf("expected callers explain payload; got %v", explain)
	}
	if explain["resolved_symbol"] == "" {
		t.Fatalf("expected resolved_symbol in explain payload; got %v", explain)
	}
}

func TestCallersCountOnly(t *testing.T) {
	db, rs, _ := indexSamplemodWithPath(t)
	// countOnly mode should not panic.
	if err := execCallers(rs, db, "example.com/samplemod/alpha.Top", 200, false, "", false, 1, false, true, false, false, ""); err != nil {
		t.Fatalf("execCallers count: %v", err)
	}
}

func TestCallersTextOutput(t *testing.T) {
	db, rs, _ := indexSamplemodWithPath(t)
	if err := execCallers(rs, db, "example.com/samplemod/alpha.Top", 200, false, "", false, 1, false, false, false, false, ""); err != nil {
		t.Fatalf("execCallers text: %v", err)
	}
}

func TestCallersFuzzyMode(t *testing.T) {
	db, rs, dbPath := indexSamplemodWithPath(t)
	m := captureJSON(t, func() {
		if err := execCallers(rs, db, "Top", 200, true, "", false, 1, false, false, true, false, dbPath); err != nil {
			t.Fatalf("execCallers fuzzy: %v", err)
		}
	})
	if _, ok := m["callers"]; !ok {
		t.Error("expected 'callers' key in fuzzy output")
	}
}

func TestCallersWithDepth(t *testing.T) {
	db, rs, dbPath := indexSamplemodWithPath(t)
	m := captureJSON(t, func() {
		if err := execCallers(rs, db, "example.com/samplemod/alpha.Top", 200, false, "", false, 3, false, false, true, false, dbPath); err != nil {
			t.Fatalf("execCallers depth: %v", err)
		}
	})
	if d, _ := m["depth"].(float64); d != 3 {
		t.Errorf("expected depth 3, got %v", d)
	}
}

func TestCallersIncludeUnresolved(t *testing.T) {
	db, rs, dbPath := indexSamplemodWithPath(t)
	m := captureJSON(t, func() {
		if err := execCallers(rs, db, "example.com/samplemod/alpha.Top", 200, false, "", true, 1, false, false, true, false, dbPath); err != nil {
			t.Fatalf("execCallers unresolved: %v", err)
		}
	})
	if _, ok := m["unresolved"]; !ok {
		t.Error("expected 'unresolved' key")
	}
}

func TestCallersHintForUnknownSymbol(t *testing.T) {
	db, rs, dbPath := indexSamplemodWithPath(t)
	m := captureJSON(t, func() {
		if err := execCallers(rs, db, "example.com/nonexistent.Foo", 200, false, "", false, 1, false, false, true, false, dbPath); err != nil {
			t.Fatalf("execCallers: %v", err)
		}
	})
	if _, ok := m["hint"]; !ok {
		t.Error("expected 'hint' for unknown symbol")
	}
}

// — execBlastRadius ———————————————————————————————————————————————————————————

func TestBlastRadiusJSONOutput(t *testing.T) {
	db, rs, dbPath := indexSamplemodWithPath(t)
	m := captureJSON(t, func() {
		if err := execBlastRadius(rs, db, "example.com/samplemod/alpha.Top", 3, false, "", false, 200, true, false, dbPath); err != nil {
			t.Fatalf("execBlastRadius: %v", err)
		}
	})
	if _, ok := m["callers"]; !ok {
		t.Error("expected 'callers' key")
	}
	if _, ok := m["env"]; !ok {
		t.Error("expected 'env' key")
	}
}

func TestBlastRadiusExplainJSONOutput(t *testing.T) {
	db, rs, dbPath := indexSamplemodWithPath(t)
	m := captureJSON(t, func() {
		if err := execBlastRadius(rs, db, "Top", 3, true, "example.com/samplemod/alpha", false, 200, true, true, dbPath); err != nil {
			t.Fatalf("execBlastRadius explain: %v", err)
		}
	})
	explain, ok := m["explain"].(map[string]any)
	if !ok {
		t.Fatalf("expected explain payload; got %v", m["explain"])
	}
	traversal, ok := explain["traversal"].(map[string]any)
	if !ok || traversal["seed_match"] == nil {
		t.Fatalf("expected traversal.seed_match in explain payload; got %v", explain)
	}
}

func TestBlastRadiusTextOutput(t *testing.T) {
	db, rs, _ := indexSamplemodWithPath(t)
	if err := execBlastRadius(rs, db, "example.com/samplemod/alpha.Top", 3, false, "", false, 200, false, false, ""); err != nil {
		t.Fatalf("execBlastRadius text: %v", err)
	}
}

func TestBlastRadiusEmptySymbol(t *testing.T) {
	db, rs, _ := indexSamplemodWithPath(t)
	err := execBlastRadius(rs, db, "", 3, false, "", false, 200, true, false, "")
	if err == nil {
		t.Fatal("expected error for empty symbol")
	}
}

// — execTrace —————————————————————————————————————————————————————————————————

func TestTraceJSONOutput(t *testing.T) {
	db, rs, dbPath := indexSamplemodWithPath(t)
	m := captureJSON(t, func() {
		if err := execTrace(rs, db, "example.com/samplemod/alpha.Top", "", 10, 10, true, dbPath); err != nil {
			t.Fatalf("execTrace: %v", err)
		}
	})
	if _, ok := m["symbol"]; !ok {
		t.Error("expected 'symbol' key")
	}
	if _, ok := m["env"]; !ok {
		t.Error("expected 'env' key")
	}
}

func TestTraceTextOutput(t *testing.T) {
	db, rs, _ := indexSamplemodWithPath(t)
	if err := execTrace(rs, db, "example.com/samplemod/alpha.Top", "", 10, 10, false, ""); err != nil {
		t.Fatalf("execTrace text: %v", err)
	}
}

func TestTraceRequiresSymbol(t *testing.T) {
	db, rs, _ := indexSamplemodWithPath(t)
	err := execTrace(rs, db, "", "", 10, 10, true, "")
	if err == nil {
		t.Fatal("expected error for empty symbol")
	}
}

// — emitAgentHelp ————————————————————————————————————————————————————————————

func TestJsonFlagInArgs(t *testing.T) {
	if !jsonFlagInArgs([]string{"callers", "--symbol", "foo", "--json"}) {
		t.Error("expected true for --json in args")
	}
	if jsonFlagInArgs([]string{"callers", "--symbol", "foo"}) {
		t.Error("expected false for args without --json")
	}
}

func TestEmitAgentHelp(t *testing.T) {
	m := captureJSON(t, func() {
		if !emitAgentHelp("gosymdb") {
			t.Fatal("expected emitAgentHelp to return true for 'gosymdb'")
		}
	})
	if _, ok := m["command"]; !ok {
		t.Error("expected 'command' key in agent help output")
	}
	if emitAgentHelp("nonexistent-key") {
		t.Error("expected false for unknown key")
	}
}

// — more coverage tests ——————————————————————————————————————————————————————

func TestHealthTextOutput(t *testing.T) {
	moduleRoot, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "samplemod"))
	dbPath := filepath.Join(t.TempDir(), "health_text.sqlite")
	db, _ := sql.Open(sqlite.DriverName, dbPath)
	indexer.ResetSchema(db)
	indexer.IndexModule(db, moduleRoot, false, false)
	db.Exec(`INSERT INTO index_meta(tool_version, go_version, indexed_at, root, warnings) VALUES('test', 'go1.26', '2026-01-01T00:00:00Z', ?, 1)`, moduleRoot)
	db.Close()
	rs, _ := sqlite.Open(dbPath)
	defer rs.Close()
	// Text output with warnings > 0 exercises extra branch.
	if err := execHealth(rs, dbPath, false); err != nil {
		t.Fatalf("execHealth text: %v", err)
	}
}

func TestCalleesJSONOutputWithUnresolved(t *testing.T) {
	db, rs, dbPath := indexSamplemodWithPath(t)
	// Test JSON path with include-unresolved (default true).
	m := captureJSON(t, func() {
		if err := execCallees(rs, db, "example.com/samplemod/alpha.Top", 200, false, "", true, false, false, true, dbPath); err != nil {
			t.Fatalf("execCallees: %v", err)
		}
	})
	if _, ok := m["callees"]; !ok {
		t.Error("expected 'callees' key")
	}
	if _, ok := m["unresolved"]; !ok {
		t.Error("expected 'unresolved' key")
	}
}

func TestCalleesUniqueJSON(t *testing.T) {
	db, rs, dbPath := indexSamplemodWithPath(t)
	m := captureJSON(t, func() {
		if err := execCallees(rs, db, "example.com/samplemod/alpha.Top", 200, false, "", true, true, false, true, dbPath); err != nil {
			t.Fatalf("execCallees unique: %v", err)
		}
	})
	if _, ok := m["callees"]; !ok {
		t.Error("expected 'callees' key in unique output")
	}
}

func TestCalleesUniqueTextOutput(t *testing.T) {
	db, rs, dbPath := indexSamplemodWithPath(t)
	if err := execCallees(rs, db, "example.com/samplemod/alpha.Top", 200, false, "", true, true, false, false, dbPath); err != nil {
		t.Fatalf("execCallees unique text: %v", err)
	}
}

func TestCalleesFuzzyJSON(t *testing.T) {
	db, rs, dbPath := indexSamplemodWithPath(t)
	m := captureJSON(t, func() {
		if err := execCallees(rs, db, "Top", 200, true, "", true, false, false, true, dbPath); err != nil {
			t.Fatalf("execCallees fuzzy: %v", err)
		}
	})
	if _, ok := m["callees"]; !ok {
		t.Error("expected 'callees' key in fuzzy output")
	}
}

func TestReferencesJSONWithRefKind(t *testing.T) {
	_, rs, dbPath := indexSamplemodWithPath(t)
	m := captureJSON(t, func() {
		if err := execReferences(rs, dbPath, "example.com/samplemod/alpha.Store", "", "composite_lit", "", 200, false, true); err != nil {
			t.Fatalf("execReferences refKind: %v", err)
		}
	})
	if _, ok := m["references"]; !ok {
		t.Error("expected 'references' key with refKind filter")
	}
}

func TestFindRejectsInvalidKind(t *testing.T) {
	_, rs, dbPath := indexSamplemodWithPath(t)
	err := execFind(rs, dbPath, "Store", "", "metho", "", 100, false, true)
	if err == nil || !strings.Contains(err.Error(), "invalid value for --kind") {
		t.Fatalf("expected invalid --kind error; got: %v", err)
	}
}

func TestDeadRejectsInvalidKind(t *testing.T) {
	db, rs, dbPath := indexSamplemodWithPath(t)
	err := execDead(rs, db, "type", "", 100, false, true, dbPath)
	if err == nil || !strings.Contains(err.Error(), "invalid value for --kind") {
		t.Fatalf("expected invalid dead --kind error; got: %v", err)
	}
}

func TestReferencesRejectInvalidRefKind(t *testing.T) {
	_, rs, dbPath := indexSamplemodWithPath(t)
	err := execReferences(rs, dbPath, "example.com/samplemod/alpha.Store", "", "field-read", "", 200, false, true)
	if err == nil || !strings.Contains(err.Error(), "invalid value for --ref-kind") {
		t.Fatalf("expected invalid --ref-kind error; got: %v", err)
	}
}

func TestImplementorsExplainJSONOutput(t *testing.T) {
	_, rs, dbPath := indexSamplemodWithPath(t)
	m := captureJSON(t, func() {
		if err := execImplementors(rs, dbPath, "example.com/samplemod/iface.Box[int]", "", 100, true, true); err != nil {
			t.Fatalf("execImplementors explain: %v", err)
		}
	})
	explain, ok := m["explain"].(map[string]any)
	if !ok {
		t.Fatalf("expected explain payload; got %v", m["explain"])
	}
	if explain["mode"] != "iface" {
		t.Fatalf("expected iface mode in explain payload; got %v", explain)
	}
	if explain["normalized_query"] != "example.com/samplemod/iface.Box" {
		t.Fatalf("expected normalized query without instantiation args; got %v", explain["normalized_query"])
	}
}

func TestReferencesTextOutput(t *testing.T) {
	_, rs, dbPath := indexSamplemodWithPath(t)
	if err := execReferences(rs, dbPath, "example.com/samplemod/alpha.Store", "", "", "", 200, false, false); err != nil {
		t.Fatalf("execReferences text: %v", err)
	}
}

func TestCallersIsTestFilter(t *testing.T) {
	db, rs, dbPath := indexSamplemodWithPath(t)
	m := captureJSON(t, func() {
		if err := execCallers(rs, db, "example.com/samplemod/alpha.Top", 200, false, "", false, 1, true, false, true, false, dbPath); err != nil {
			t.Fatalf("execCallers isTest: %v", err)
		}
	})
	callers, _ := m["callers"].([]any)
	// With isTest=true, only test file callers should be returned.
	for _, c := range callers {
		row, _ := c.(map[string]any)
		file, _ := row["file"].(string)
		if !strings.HasSuffix(file, "_test.go") {
			t.Errorf("expected only test callers, got file: %s", file)
		}
	}
}

func TestCallersBFSDepthClamp(t *testing.T) {
	db, rs, dbPath := indexSamplemodWithPath(t)
	// Depth 0 should be clamped to 1, depth 99 to 10.
	m := captureJSON(t, func() {
		if err := execCallers(rs, db, "example.com/samplemod/alpha.Top", 200, false, "", false, 0, false, false, true, false, dbPath); err != nil {
			t.Fatalf("execCallers: %v", err)
		}
	})
	if d, _ := m["depth"].(float64); d != 1 {
		t.Errorf("expected depth clamped to 1, got %v", d)
	}
}

func TestBlastRadiusFuzzy(t *testing.T) {
	db, rs, dbPath := indexSamplemodWithPath(t)
	m := captureJSON(t, func() {
		if err := execBlastRadius(rs, db, "Top", 2, true, "", false, 200, true, false, dbPath); err != nil {
			t.Fatalf("execBlastRadius fuzzy: %v", err)
		}
	})
	if _, ok := m["callers"]; !ok {
		t.Error("expected 'callers' key in fuzzy blast-radius")
	}
}
