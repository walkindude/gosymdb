// Sub-package whose init() is triggered by a blank import in main.
// No explicit call edge exists — the Go runtime calls init implicitly.
package sideeffect

import "fmt"

func init() {
	register("plugin-a")
}

func init() {
	register("plugin-b")
}

func register(name string) {
	fmt.Println("registered:", name)
}

// NeverCalled is exported but nobody calls it. True dead code.
func NeverCalled() {
	fmt.Println("this is actually dead")
}
