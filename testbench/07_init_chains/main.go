// TEST: Multiple init() functions, blank imports, and init ordering.
// EXPECT: init functions are special — multiple per file, multiple per package.
// gosymdb's `dead` command excludes init, but does indexing handle them at all?
package main

import (
	"fmt"

	// Blank import — triggers init() in the sub-package but no visible call edge.
	_ "testbench/init_chains/sideeffect"
)

var globalState []string

func init() {
	globalState = append(globalState, "main init 1")
	setupHelper() // Call from init — is this edge tracked?
}

func init() {
	globalState = append(globalState, "main init 2")
}

func init() {
	globalState = append(globalState, "main init 3")
	anotherHelper()
}

func setupHelper() {
	globalState = append(globalState, "setup done")
}

func anotherHelper() {
	globalState = append(globalState, "another done")
}

func main() {
	fmt.Println(globalState)
}
