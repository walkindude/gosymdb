// Test: //go:build ignore files are excluded from the index.
//
// What we test:
//   - A file with "//go:build ignore" should NOT appear in the index.
//   - Symbols defined only in that file should NOT be findable.
//   - Symbols defined in regular (non-ignored) files SHOULD be in the index.
//
// Why it matters:
//   - Generated files, build-system stubs, and playground snippets often carry
//     "//go:build ignore". If gosymdb indexes them, it pollutes the symbol space
//     with unreachable code and causes false dead-code negatives.
//
// Expected: ignoredFunc NOT found; regularFunc found.

package main

func regularFunc() int {
	return helper()
}

func helper() int {
	return 42
}

func main() {
	regularFunc()
}
