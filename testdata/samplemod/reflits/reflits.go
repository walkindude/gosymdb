// Package reflits exercises function references inside composite literals.
// Used by TestBUG001_CompositeLitFuncRefsTracked to verify that gosymdb
// records ref edges for function values stored in map values and slice elements,
// not just function values passed as call arguments.
package reflits

// addFn is referenced ONLY as a map value at package scope.
// It is never passed as a call argument, so the existing arg-ref pass misses it.
func addFn(a, b int) int { return a + b }

// subFn is referenced ONLY as a slice element inside a function body.
// It is never passed as a call argument.
func subFn(a, b int) int { return a - b }

type binOp func(int, int) int

// Package-level map whose values are function references.
// addFn should be reachable via the composite lit scanner.
var ops = map[string]binOp{
	"add": addFn,
}

// RunFromMap looks up a function from the package-level map and calls it.
func RunFromMap(name string, a, b int) int {
	if fn, ok := ops[name]; ok {
		return fn(a, b)
	}
	return 0
}

// RunFromSlice uses subFn inside a slice literal.
// subFn should be reachable via the composite lit scanner.
func RunFromSlice() int {
	fns := []binOp{subFn}
	return fns[0](1, 2)
}
