// TEST: go:linkname creates symbol aliases invisible to normal analysis.
// EXPECT: The linked function appears to have no callers/callees through
// normal resolution. gosymdb likely can't follow linkname directives.
package main

import (
	"fmt"
	"unsafe"
)

// actualImpl is the real function that does work.
func actualImpl(x int) int {
	return x * 2
}

// go:linkname links a local name to a symbol in another package (or same).
// This is used extensively in the Go runtime and some libraries.
//
//go:linkname runtimeNanotime runtime.nanotime
func runtimeNanotime() int64

// Expose unexported runtime function via linkname.
//
//go:linkname fastrand runtime.cheaprand
func fastrand() uint32

// go:nosplit and go:noescape affect compilation but not symbol resolution.
//
//go:nosplit
func tightLoop(n int) int {
	sum := 0
	for i := 0; i < n; i++ {
		sum += i
	}
	return sum
}

// go:noinline prevents the function from being inlined — affects call graph
// in practice (inlined calls may or may not appear as edges).
//
//go:noinline
func neverInlined(x int) int {
	return x + 1
}

// Blank import side effect — the init() in the imported package runs,
// but there's no visible call edge.
// (Can't demonstrate cross-module blank import in single file, but
// the go:linkname above serves the same "invisible edge" purpose.)

func main() {
	fmt.Println(actualImpl(21))
	fmt.Println(runtimeNanotime())
	fmt.Println(fastrand())
	fmt.Println(tightLoop(100))
	fmt.Println(neverInlined(41))
	_ = unsafe.Sizeof(0) // keep unsafe import
}
