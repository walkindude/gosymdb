package cmd

// TestBench is the sole testbench runner (replaces the former run_tests.sh).
// Benefits: parallel module execution, proper JSON parsing, no CWD issues,
// no process-spawn overhead (~5s vs ~63s).
//
// Run:
//   go test -run TestBench -v -count=1
//
// This file mirrors the assertions in run_tests.sh 1:1. Each bash section
// (01_reflect_calls, 02_func_values, …) becomes a parallel subtest.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/walkindude/gosymdb/indexer"
	"github.com/walkindude/gosymdb/store/sqlite"
)

// stdoutMu serializes stdout captures. The exec functions write JSON to
// os.Stdout; we redirect it through a pipe to capture the output. This is
// inherently process-global, so concurrent subtests must serialize captures.
// The lock is held for microseconds per capture — not a bottleneck.
var stdoutMu sync.Mutex

// benchCapture calls fn (which writes JSON to stdout) and returns the parsed map.
// Thread-safe via stdoutMu.
func benchCapture(t *testing.T, fn func()) map[string]any {
	t.Helper()
	stdoutMu.Lock()
	defer stdoutMu.Unlock()

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

// jint extracts an integer from a JSON map (handles float64 from encoding/json).
func jint(m map[string]any, key string) int {
	v, ok := m[key]
	if !ok {
		return -1
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return -1
}

// jstr extracts a string from a JSON map.
func jstr(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// jcontains checks if the JSON map, re-serialized, contains a substring.
func jcontains(m map[string]any, substr string) bool {
	b, _ := json.Marshal(m)
	return strings.Contains(string(b), substr)
}

// benchEnv holds shared state for all testbench subtests.
type benchEnv struct {
	rs            *sqlite.SQLiteStore
	db            *sql.DB
	dbPath        string
	testbenchRoot string // absolute path to testbench/ dir (for bench history)
}

func TestBench(t *testing.T) {
	started := time.Now()

	testbenchRoot, err := filepath.Abs(filepath.Join("..", "..", "testbench"))
	if err != nil {
		t.Fatalf("abs testbench: %v", err)
	}

	// Index all testbench modules into a shared temp DB.
	dbPath := filepath.Join(t.TempDir(), "testbench.sqlite")
	db, err := sql.Open(sqlite.DriverName, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := indexer.ResetSchema(db); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	modules, err := indexer.DiscoverModules(testbenchRoot)
	if err != nil {
		t.Fatalf("discover modules: %v", err)
	}
	var totalSymbols, totalCalls, totalUnresolved int
	for _, mod := range modules {
		s, c, u, _, err := indexer.IndexModule(db, mod, false, false)
		if err != nil {
			t.Fatalf("index %s: %v", mod, err)
		}
		totalSymbols += s
		totalCalls += c
		totalUnresolved += u
	}
	db.Close()

	rs, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { rs.Close() })
	db2, err := sql.Open(sqlite.DriverName, dbPath)
	if err != nil {
		t.Fatalf("open db2: %v", err)
	}
	t.Cleanup(func() { db2.Close() })

	env := &benchEnv{rs: rs, db: db2, dbPath: dbPath, testbenchRoot: testbenchRoot}

	t.Run("01_reflect_calls", func(t *testing.T) { t.Parallel(); bench01(t, env) })
	t.Run("02_func_values", func(t *testing.T) { t.Parallel(); bench02(t, env) })
	t.Run("03_interface_dispatch", func(t *testing.T) { t.Parallel(); bench03(t, env) })
	t.Run("04_generics", func(t *testing.T) { t.Parallel(); bench04(t, env) })
	t.Run("05_linkname", func(t *testing.T) { t.Parallel(); bench05(t, env) })
	t.Run("06_embedding_promo", func(t *testing.T) { t.Parallel(); bench06(t, env) })
	t.Run("07_init_chains", func(t *testing.T) { t.Parallel(); bench07(t, env) })
	t.Run("08_build_tags", func(t *testing.T) { t.Parallel(); bench08(t, env) })
	t.Run("09_type_assertions", func(t *testing.T) { t.Parallel(); bench09(t, env) })
	t.Run("10_goroutine_defer", func(t *testing.T) { t.Parallel(); bench10(t, env) })
	t.Run("11_unicode_shadowing", func(t *testing.T) { t.Parallel(); bench11(t, env) })
	t.Run("12_var_init_calls", func(t *testing.T) { t.Parallel(); bench12(t, env) })
	t.Run("13_method_exprs", func(t *testing.T) { t.Parallel(); bench13(t, env) })
	t.Run("14_local_func_refs", func(t *testing.T) { t.Parallel(); bench14(t, env) })
	t.Run("15_testmain", func(t *testing.T) { t.Parallel(); bench15(t, env) })
	t.Run("16_closures_return", func(t *testing.T) { t.Parallel(); bench16(t, env) })
	t.Run("17_interface_dead", func(t *testing.T) { t.Parallel(); bench17(t, env) })
	t.Run("18_build_ignore", func(t *testing.T) { t.Parallel(); bench18(t, env) })
	t.Run("19_named_func_types", func(t *testing.T) { t.Parallel(); bench19(t, env) })
	t.Run("20_iface_embedding", func(t *testing.T) { t.Parallel(); bench20(t, env) })
	t.Run("21_ref_edge_cases", func(t *testing.T) { t.Parallel(); bench21(t, env) })

	// Append bench-history record after all subtests complete.
	// t.Cleanup runs after subtests, so we use it to capture the final state.
	t.Cleanup(func() {
		appendBenchHistory(testbenchRoot, started, len(modules),
			totalSymbols, totalCalls, totalUnresolved, t.Failed())
	})
}

// benchHistoryRecord is one line in testbench/bench_history.jsonl.
type benchHistoryRecord struct {
	TS         string `json:"ts"`
	Commit     string `json:"commit"`
	Modules    int    `json:"modules"`
	Symbols    int    `json:"symbols"`
	Calls      int    `json:"calls"`
	Unresolved int    `json:"unresolved"`
	DurationMS int64  `json:"duration_ms"`
	Failed     bool   `json:"failed"`
}

func appendBenchHistory(testbenchRoot string, started time.Time, modules, symbols, calls, unresolved int, failed bool) {
	commit, _ := gitOut("rev-parse", "--short", "HEAD")

	rec := benchHistoryRecord{
		TS:         started.UTC().Format(time.RFC3339),
		Commit:     strings.TrimSpace(commit),
		Modules:    modules,
		Symbols:    symbols,
		Calls:      calls,
		Unresolved: unresolved,
		DurationMS: time.Since(started).Milliseconds(),
		Failed:     failed,
	}

	histPath := filepath.Join(testbenchRoot, "bench_history.jsonl")
	f, err := os.OpenFile(histPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return // non-fatal — don't fail the test for logging
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(rec)
}

// --- helpers for common query patterns ---

func bCallers(t *testing.T, e *benchEnv, symbol string) map[string]any {
	t.Helper()
	return benchCapture(t, func() {
		if err := execCallers(e.rs, e.db, symbol, 200, false, "", false, 1, false, false, true, e.dbPath); err != nil {
			json.NewEncoder(os.Stdout).Encode(map[string]any{"error": err.Error()})
		}
	})
}

func bCallees(t *testing.T, e *benchEnv, symbol string) map[string]any {
	t.Helper()
	return benchCapture(t, func() {
		if err := execCallees(e.rs, e.db, symbol, 200, false, "", true, false, false, true, e.dbPath); err != nil {
			json.NewEncoder(os.Stdout).Encode(map[string]any{"error": err.Error()})
		}
	})
}

func bFind(t *testing.T, e *benchEnv, q, pkg, kind string) map[string]any {
	t.Helper()
	return benchCapture(t, func() {
		if err := execFind(e.rs, e.dbPath, q, pkg, kind, "", 100, false, true); err != nil {
			json.NewEncoder(os.Stdout).Encode(map[string]any{"error": err.Error()})
		}
	})
}

func bDead(t *testing.T, e *benchEnv, pkg string, inclExported bool) map[string]any {
	t.Helper()
	return benchCapture(t, func() {
		if err := execDead(e.rs, e.db, "", pkg, 1000, inclExported, true, e.dbPath); err != nil {
			json.NewEncoder(os.Stdout).Encode(map[string]any{"error": err.Error()})
		}
	})
}

func bBlast(t *testing.T, e *benchEnv, symbol string) map[string]any {
	t.Helper()
	return benchCapture(t, func() {
		if err := execBlastRadius(e.rs, e.db, symbol, 5, false, "", false, 200, true, e.dbPath); err != nil {
			// Write a JSON error so benchCapture can still parse
			json.NewEncoder(os.Stdout).Encode(map[string]any{"error": err.Error()})
		}
	})
}

func bImpls(t *testing.T, e *benchEnv, iface string) map[string]any {
	t.Helper()
	return benchCapture(t, func() {
		if err := execImplementors(e.rs, e.dbPath, iface, "", 100, true); err != nil {
			json.NewEncoder(os.Stdout).Encode(map[string]any{"error": err.Error()})
		}
	})
}

func callersCount(t *testing.T, e *benchEnv, symbol string) int {
	t.Helper()
	return jint(bCallers(t, e, symbol), "callers_count")
}

// --- module test functions ---

func bench01(t *testing.T, e *benchEnv) {
	// 01: REFLECT CALLS
	cnt := callersCount(t, e, "testbench/reflect_calls.*Service.secretWork")
	if cnt != 0 {
		t.Errorf("reflect-called method: expected 0 callers, got %d", cnt)
	}
	dead := bDead(t, e, "testbench/reflect_calls", true)
	if !jcontains(dead, "hiddenHelper") {
		t.Error("hiddenHelper should be flagged as dead (reflect false positive)")
	}
}

func bench02(t *testing.T, e *benchEnv) {
	// 02: FUNCTION VALUES
	for _, fn := range []string{"add", "multiply", "subtract"} {
		cnt := callersCount(t, e, "testbench/func_values."+fn)
		if cnt == 0 {
			t.Errorf("%s: expected callers >0, got 0", fn)
		}
	}
	if cnt := callersCount(t, e, "testbench/func_values.deepTarget"); cnt == 0 {
		t.Error("deepTarget: expected callers >0")
	}
	for _, fn := range []string{"greet", "farewell"} {
		if cnt := callersCount(t, e, "testbench/func_values."+fn); cnt == 0 {
			t.Errorf("%s: expected callers >0 (passed in slice)", fn)
		}
	}
	// BUG-004: chained calls
	callees := bCallees(t, e, "testbench/func_values.main")
	if !jcontains(callees, "tripleNest") {
		t.Error("tripleNest first call should resolve")
	}
	unresolved := jint(callees, "unresolved_count")
	if unresolved != 0 {
		t.Logf("BUG-004: chained calls unresolved=%d (expected 0, known open bug)", unresolved)
	}
}

func bench03(t *testing.T, e *benchEnv) {
	// 03: INTERFACE DISPATCH (LIM-004)
	for _, method := range []string{"*FileWriter.Write", "*NetWriter.Write", "*Socket.Read", "*Socket.Write"} {
		m := bCallers(t, e, "testbench/interface_dispatch."+method)
		if jint(m, "callers_count") != 0 {
			t.Errorf("%s: expected 0 static callers (LIM-004)", method)
		}
		if jstr(m, "hint") == "" {
			t.Errorf("%s: expected hint for interface dispatch", method)
		}
	}
	m := bCallers(t, e, "testbench/interface_dispatch.*SneakyProcessor.Process")
	if jint(m, "callers_count") != 0 {
		t.Error("SneakyProcessor.Process: expected 0 callers (LIM-004)")
	}
	if jstr(m, "hint") == "" {
		t.Error("SneakyProcessor.Process: expected hint")
	}
	impls := bImpls(t, e, "Writer")
	for _, typ := range []string{"FileWriter", "NetWriter", "Socket"} {
		if !jcontains(impls, typ) {
			t.Errorf("%s should implement Writer", typ)
		}
	}
}

func bench04(t *testing.T, e *benchEnv) {
	// 04: GENERICS
	found := bFind(t, e, "Max", "testbench/generics", "")
	if !jcontains(found, "Max") {
		t.Error("Generic func Max not indexed")
	}
	callees := benchCapture(t, func() {
		execCallees(e.rs, e.db, "testbench/generics.MaxOfThree", 200, true, "", false, false, false, true, e.dbPath)
	})
	if !jcontains(callees, "Max") {
		t.Error("MaxOfThree should call Max")
	}
	if !jcontains(bFind(t, e, "Swap", "testbench/generics", ""), "Swap") {
		t.Error("Generic Pair.Swap not indexed")
	}
	if !jcontains(bFind(t, e, "Insert", "testbench/generics", ""), "Insert") {
		t.Error("Generic Tree.Insert not indexed")
	}
	mapCallees := benchCapture(t, func() {
		execCallees(e.rs, e.db, "testbench/generics.Map", 200, true, "", false, false, false, true, e.dbPath)
	})
	if jint(mapCallees, "callees_count") == 0 {
		t.Error("Generic Map should have callees")
	}
}

func bench05(t *testing.T, e *benchEnv) {
	// 05: LINKNAME
	if !jcontains(bFind(t, e, "runtimeNanotime", "", ""), "runtimeNanotime") {
		t.Error("go:linkname runtimeNanotime not indexed")
	}
	if !jcontains(bFind(t, e, "fastrand", "", ""), "fastrand") {
		t.Error("go:linkname fastrand not indexed")
	}
	if callersCount(t, e, "testbench/linkname.runtimeNanotime") == 0 {
		t.Error("runtimeNanotime should have callers")
	}
	callees := bCallees(t, e, "testbench/linkname.main")
	if !jcontains(callees, "runtimeNanotime") {
		t.Error("main callees should include runtimeNanotime")
	}
	if callersCount(t, e, "testbench/linkname.neverInlined") == 0 {
		t.Error("go:noinline func should have callers")
	}
}

func bench06(t *testing.T, e *benchEnv) {
	// 06: EMBEDDING PROMOTION
	callees := bCallees(t, e, "testbench/embedding_promo.main")
	if !jcontains(callees, "DoWork") {
		t.Error("main callees should include DoWork")
	}
	if callersCount(t, e, "testbench/embedding_promo.*Level0.DeepMethod") == 0 {
		t.Error("Level0.DeepMethod should have callers (3-level embedding)")
	}
	if callersCount(t, e, "testbench/embedding_promo.*Middle.Name") == 0 {
		t.Error("Middle.Name should have callers (shadowed embedding)")
	}
	if callersCount(t, e, "testbench/embedding_promo.*Base.Name") != 0 {
		t.Error("Base.Name should have 0 callers (shadowed)")
	}
	impls := bImpls(t, e, "Saver")
	if !jcontains(impls, "Document") {
		t.Error("Document should implement Saver via embedding")
	}
}

func bench07(t *testing.T, e *benchEnv) {
	// 07: INIT CHAINS
	found := bFind(t, e, "init", "testbench/init_chains", "func")
	cnt := jint(found, "count")
	if cnt < 2 {
		t.Errorf("expected >=2 init functions, got %d", cnt)
	}
	if callersCount(t, e, "testbench/init_chains.setupHelper") == 0 {
		t.Error("setupHelper should be called from init")
	}
	if callersCount(t, e, "testbench/init_chains/sideeffect.register") == 0 {
		t.Error("sideeffect.register should be called from init")
	}
	dead := bDead(t, e, "testbench/init_chains", true)
	if !jcontains(dead, "NeverCalled") {
		t.Error("NeverCalled should be flagged as dead")
	}
}

func bench08(t *testing.T, e *benchEnv) {
	// 08: BUILD TAGS — platform-specific: only current GOOS functions are indexed.
	current := map[string]string{
		"windows": "windowsOnly",
		"linux":   "linuxOnly",
		"darwin":  "darwinOnly",
	}
	for goos, sym := range current {
		found := bFind(t, e, sym, "", "")
		if goos == runtime.GOOS {
			if !jcontains(found, sym) {
				t.Errorf("%s should be indexed on %s", sym, runtime.GOOS)
			}
		} else {
			if jint(found, "count") != 0 {
				t.Errorf("%s should NOT be indexed on %s", sym, runtime.GOOS)
			}
		}
	}
}

func bench09(t *testing.T, e *benchEnv) {
	// 09: TYPE ASSERTIONS
	for _, method := range []string{"*Dog.Fetch", "*Cat.Purr", "*Fish.Swim"} {
		if callersCount(t, e, "testbench/type_assertions."+method) == 0 {
			t.Errorf("%s should have callers (via type switch)", method)
		}
	}
	impls := bImpls(t, e, "Animal")
	for _, typ := range []string{"Dog", "Cat", "Fish"} {
		if !jcontains(impls, typ) {
			t.Errorf("%s should implement Animal", typ)
		}
	}
}

func bench10(t *testing.T, e *benchEnv) {
	// 10: GOROUTINE & DEFER
	checks := map[string]string{
		"testbench/goroutine_defer.worker":          "go statement",
		"testbench/goroutine_defer.anonymousTarget": "anonymous goroutine",
		"testbench/goroutine_defer.cleanup":         "defer",
		"testbench/goroutine_defer.closureHelper":   "deferred closure",
		"testbench/goroutine_defer.*Resource.Close": "defer method value",
	}
	for sym, via := range checks {
		if callersCount(t, e, sym) == 0 {
			t.Errorf("%s should have callers (via %s)", sym, via)
		}
	}
}

func bench11(t *testing.T, e *benchEnv) {
	// 11: UNICODE & SHADOWING
	found := bFind(t, e, "rocess", "testbench/unicode_shadowing", "")
	if jint(found, "count") < 2 {
		t.Error("expected >=2 symbols matching 'rocess' (Latin + Cyrillic)")
	}
	found = bFind(t, e, "elper", "testbench/unicode_shadowing", "")
	if jint(found, "count") < 2 {
		t.Error("expected >=2 symbols matching 'elper' (Greek + Latin)")
	}
	if callersCount(t, e, "testbench/unicode_shadowing.Process") == 0 {
		t.Error("Latin Process() should have callers")
	}
	if !jcontains(bFind(t, e, "len", "testbench/unicode_shadowing", "func"), "len") {
		t.Error("user-defined len() should be indexed")
	}
	if !jcontains(bFind(t, e, "cap", "testbench/unicode_shadowing", "func"), "cap") {
		t.Error("user-defined cap() should be indexed")
	}
}

func bench12(t *testing.T, e *benchEnv) {
	// 12: VAR INIT CALLS
	for _, sym := range []string{
		"testbench/var_init_calls.getDefaultPort",
		"testbench/var_init_calls.getHost",
		"testbench/var_init_calls.secretInit",
		"testbench/var_init_calls.initA",
		"testbench/var_init_calls.initB",
	} {
		if callersCount(t, e, sym) == 0 {
			t.Errorf("%s should have callers", sym)
		}
	}
}

func bench13(t *testing.T, e *benchEnv) {
	// 13: METHOD EXPRESSIONS
	if callersCount(t, e, "testbench/method_exprs.*Calculator.Add") == 0 {
		t.Error("Calculator.Add should have callers (method value + expression)")
	}
	if callersCount(t, e, "testbench/method_exprs.*Calculator.Multiply") == 0 {
		t.Error("Calculator.Multiply should have callers (method expression in slice)")
	}
	blast := bBlast(t, e, "testbench/method_exprs.*Calculator.Add")
	summary, _ := blast["summary"].(map[string]any)
	total := 0
	if summary != nil {
		total = jint(summary, "total")
	}
	if total < 1 {
		t.Errorf("blast-radius of Calculator.Add: expected >=1, got %d", total)
	}
	m := bCallers(t, e, "testbench/method_exprs.*Calculator.Add")
	if !jcontains(m, "ref") {
		t.Error("method expression/value should be tracked as ref kind")
	}
}

func bench14(t *testing.T, e *benchEnv) {
	// 14: LOCAL FUNC REFS (BUG-006)
	for _, sym := range []string{
		"testbench/local_func_refs.assignedViaShortDecl",
		"testbench/local_func_refs.assignedViaLongDecl",
		"testbench/local_func_refs.assignedAfterDecl",
		"testbench/local_func_refs.namedFuncTypeTarget",
		"testbench/local_func_refs.passedAsArg",
	} {
		if callersCount(t, e, sym) == 0 {
			short := sym[strings.LastIndex(sym, ".")+1:]
			t.Errorf("%s should have callers (BUG-006)", short)
		}
	}
}

func bench15(t *testing.T, e *benchEnv) {
	// 15: TESTMAIN (LIM-005)
	found := bFind(t, e, "TestMain", "testbench/testmain", "")
	if jcontains(found, "testmain.TestMain") {
		t.Error("TestMain should NOT be in standard index (LIM-005)")
	}
	if callersCount(t, e, "testbench/testmain.setup") != 0 {
		t.Error("setup should have 0 callers without --tests (LIM-005)")
	}

	// Separate DB with --tests for BUG-007 verification.
	// Must close the indexing connection before re-opening via sqlite store.
	testMainRoot, err := filepath.Abs(filepath.Join("..", "..", "testbench", "15_testmain"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	tmPath := filepath.Join(t.TempDir(), "testmain_tests.sqlite")
	tmDB, err := sql.Open(sqlite.DriverName, tmPath)
	if err != nil {
		t.Fatalf("open tmDB: %v", err)
	}
	if err := indexer.ResetSchema(tmDB); err != nil {
		tmDB.Close()
		t.Fatalf("reset tmDB: %v", err)
	}
	if _, _, _, _, err := indexer.IndexModule(tmDB, testMainRoot, false, true); err != nil {
		tmDB.Close()
		t.Fatalf("index testmain with --tests: %v", err)
	}
	tmDB.Close()

	tmRS, err := sqlite.Open(tmPath)
	if err != nil {
		t.Fatalf("sqlite.Open tmPath: %v", err)
	}
	defer tmRS.Close()
	tmDB2, _ := sql.Open(sqlite.DriverName, tmPath)
	defer tmDB2.Close()
	tmEnv := &benchEnv{rs: tmRS, db: tmDB2, dbPath: tmPath}

	cnt := callersCount(t, tmEnv, "testbench/testmain.setup")
	if cnt == 0 {
		t.Error("setup should have callers when indexed with --tests")
	}
	found2 := bFind(t, tmEnv, "setup", "testbench/testmain", "")
	if jint(found2, "count") != 1 {
		t.Errorf("setup should be indexed exactly once with --tests (BUG-007), got %d", jint(found2, "count"))
	}
}

func bench16(t *testing.T, e *benchEnv) {
	// 16: CLOSURES RETURNING CLOSURES
	for _, sym := range []string{
		"testbench/closures_return.innerTarget",
		"testbench/closures_return.step",
		"testbench/closures_return.baseOp",
		"testbench/closures_return.makeAdder",
	} {
		if callersCount(t, e, sym) == 0 {
			short := sym[strings.LastIndex(sym, ".")+1:]
			t.Errorf("%s should have callers", short)
		}
	}
}

func bench17(t *testing.T, e *benchEnv) {
	// 17: INTERFACE DEAD CODE FALSE POSITIVES
	dead := bDead(t, e, "testbench/interface_dead", true)

	// These must NOT appear as dead (called via interface)
	for _, sym := range []string{"RealDoer.Do", "RealDoer.Tag", "StubDoer.Do", "StubDoer.Tag", "Processor.Do", "Processor.Tag"} {
		if jcontains(dead, sym) {
			t.Errorf("%s should NOT be reported as dead (interface impl)", sym)
		}
	}
	// newStubDoer IS genuinely dead
	if !jcontains(dead, "newStubDoer") {
		t.Error("newStubDoer should be dead (genuinely unused)")
	}
}

func bench18(t *testing.T, e *benchEnv) {
	// 18: BUILD IGNORE EXCLUSION
	syms := bFind(t, e, "", "testbench/build_ignore", "")
	if !jcontains(syms, "regularFunc") {
		t.Error("regularFunc should be indexed (no build tag)")
	}
	if jcontains(syms, "ignoredFunc") {
		t.Error("ignoredFunc should NOT be indexed (//go:build ignore)")
	}
}

func bench19(t *testing.T, e *benchEnv) {
	// 19: NAMED FUNCTION TYPES AND STRUCT FUNCTION FIELDS
	if callersCount(t, e, "testbench/named_func_types.myHandler") == 0 {
		t.Error("myHandler should have callers (assigned to named func type var)")
	}
	if callersCount(t, e, "testbench/named_func_types.myHandle") == 0 {
		t.Error("myHandle should have callers (struct literal field)")
	}
	cnt := callersCount(t, e, "testbench/named_func_types.called")
	if cnt != 2 {
		t.Errorf("called should have 2 callers (myHandler + myHandle), got %d", cnt)
	}
}

func bench20(t *testing.T, e *benchEnv) {
	// 20: INTERFACE EMBEDDING AND METHOD SET PROMOTION
	impls := bImpls(t, e, "Inner")
	if !jcontains(impls, "ConcreteOuter") {
		t.Error("ConcreteOuter should implement Inner (promoted via Outer embedding)")
	}
	impls = bImpls(t, e, "Outer")
	if !jcontains(impls, "ConcreteOuter") {
		t.Error("ConcreteOuter should implement Outer")
	}
	// Read: 0 callers + hint
	m := bCallers(t, e, "testbench/iface_embedding.*ConcreteOuter.Read")
	if jint(m, "callers_count") != 0 {
		t.Error("*ConcreteOuter.Read: expected 0 callers (LIM-004)")
	}
	if jstr(m, "hint") == "" {
		t.Error("*ConcreteOuter.Read: expected hint")
	}
	// Write: 0 callers + hint
	m = bCallers(t, e, "testbench/iface_embedding.*ConcreteOuter.Write")
	if jint(m, "callers_count") != 0 {
		t.Error("*ConcreteOuter.Write: expected 0 callers (LIM-004)")
	}
	if jstr(m, "hint") == "" {
		t.Error("*ConcreteOuter.Write: expected hint")
	}
	// directTarget: has callers
	if callersCount(t, e, "testbench/iface_embedding.directTarget") == 0 {
		t.Error("directTarget should have callers")
	}
}

func bench21(t *testing.T, e *benchEnv) {
	// 21: REF EDGE CASES — untested function reference contexts
	// controlArg is passed as a call argument (known-working path)
	if callersCount(t, e, "testbench/ref_edge_cases.controlArg") == 0 {
		t.Error("controlArg should have callers (passed as argument — control)")
	}
	// returnTarget is returned as a function value from factory()
	if callersCount(t, e, "testbench/ref_edge_cases.returnTarget") == 0 {
		t.Error("returnTarget should have callers (returned as func value — ReturnStmt)")
	}
	// sendTarget is sent on a channel via ch <- sendTarget
	if callersCount(t, e, "testbench/ref_edge_cases.sendTarget") == 0 {
		t.Error("sendTarget should have callers (sent on channel — SendStmt)")
	}
}

func init() {
	// Suppress log output from indexer during tests.
	_ = fmt.Sprint // keep fmt import
}
