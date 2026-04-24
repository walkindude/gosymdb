//go:build ignore

// This file should be excluded from the index because of the build tag above.
// ignoredFunc must NOT appear in gosymdb's symbol table.

package main

func ignoredFunc() string {
	return "I should not be indexed"
}
