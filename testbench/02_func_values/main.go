// TEST: Function values, closures, and higher-order functions.
// EXPECT: Call edges through function variables are hard/impossible to resolve.
// Multiple functions share the same call site via a variable.
package main

import "fmt"

func add(a, b int) int      { return a + b }
func multiply(a, b int) int { return a * b }
func subtract(a, b int) int { return a - b }

type binOp func(int, int) int

// apply calls whatever function is in op — static analysis can't know which.
func apply(op binOp, a, b int) int {
	return op(a, b) // Who is the callee here? add? multiply? subtract?
}

// registry: function selection deferred to runtime via map.
var ops = map[string]binOp{
	"add": add,
	"mul": multiply,
	"sub": subtract,
}

func runFromRegistry(name string, a, b int) int {
	return ops[name](a, b) // Even harder — map lookup + indirect call.
}

// Closure capturing a function — triple indirection.
func makeAccumulator(op binOp) func(int) int {
	total := 0
	return func(n int) int {
		total = op(total, n) // Which op? Depends on call site of makeAccumulator.
		return total
	}
}

// Function returned from a function returned from a function.
func tripleNest() func() func() string {
	return func() func() string {
		return func() string {
			return deepTarget()
		}
	}
}

func deepTarget() string { return "found me" }

// Slice of functions — iteration-based indirect call.
func batchApply(fns []func() string) []string {
	var results []string
	for _, fn := range fns {
		results = append(results, fn())
	}
	return results
}

func greet() string    { return "hello" }
func farewell() string { return "goodbye" }

func main() {
	fmt.Println(apply(add, 1, 2))
	fmt.Println(apply(multiply, 3, 4))
	fmt.Println(runFromRegistry("sub", 10, 3))

	acc := makeAccumulator(add)
	fmt.Println(acc(5), acc(3))

	fmt.Println(tripleNest()()())

	fns := []func() string{greet, farewell}
	fmt.Println(batchApply(fns))
}
