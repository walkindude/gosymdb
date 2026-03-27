// TEST: Functions only called from TestMain.
//
// EXPECT: setup() and teardown() called only from TestMain should show 0 callers
//
//	without --is-test, and >= 1 callers with --is-test.
//
// Probes whether gosymdb indexes test files at all, and whether --is-test
// traverses through TestMain to find its callees.
package testmainpkg

// setup is called only from TestMain — zero static callers in non-test code.
// With --is-test, should show 1 caller (TestMain).
func setup() {}

// teardown is called only from TestMain.
// With --is-test, should show 1 caller (TestMain).
func teardown() {}

// productionFunc is called from both test code and non-test callers below.
// Should always show callers even without --is-test.
func productionFunc() string { return "prod" }

// calledFromMain is called from a non-test function — always visible.
func calledFromMain() string { return "main" }

// Entry point so this can be indexed as a real package (not just tests).
func Run() string {
	return calledFromMain() + productionFunc()
}
