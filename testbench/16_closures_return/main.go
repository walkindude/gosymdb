// TEST: Closures that return closures ("closure factories").
//
// EXPECT: Functions called *inside* a returned closure should have call edges
//
//	attributed to the anonymous closure literal that calls them, which in
//	turn is nested inside the factory function.
//
// HARD: The returned closure is stored in a local variable and called indirectly
//
//	(`add5(3)` at the call site). gosymdb cannot resolve `add5` to the specific
//	closure returned by makeAdder — that is a dynamic dispatch limitation.
//	However, the call from the closure body to innerTarget/step IS static and
//	should be tracked (the closure body is a concrete, lexically visible call site).
//
// This module confirms gosymdb handles closure-factory call edges correctly.
package main

import "fmt"

// innerTarget is called inside the anonymous closure returned by makeAdder.
// EXPECT: 1 caller (the anonymous closure inside makeAdder).
func innerTarget(x, y int) int { return x + y }

// step is called inside the anonymous closure returned by makeCounter.
// EXPECT: 1 caller (the anonymous closure inside makeCounter).
func step(n int) int { return n + 1 }

// makeAdder returns a closure that calls innerTarget.
// EXPECT: 1 caller (main).
func makeAdder(x int) func(int) int {
	return func(y int) int {
		return innerTarget(x, y)
	}
}

// makeCounter returns a closure that calls step. Demonstrates multi-level capture.
// EXPECT: 1 caller (main).
func makeCounter(start int) func() int {
	count := start
	return func() int {
		count = step(count)
		return count
	}
}

// doubleFactory returns a closure that itself returns a closure (two levels deep).
// baseOp is called inside the innermost closure.
// EXPECT: baseOp has 1 caller (the innermost anonymous closure).
func baseOp(a, b int) int { return a * b }

func doubleFactory(multiplier int) func(int) func(int) int {
	return func(base int) func(int) int {
		return func(n int) int {
			return baseOp(base*multiplier, n)
		}
	}
}

func main() {
	add5 := makeAdder(5) // add5 is a func value — indirect call, unresolvable
	fmt.Println(add5(3))

	counter := makeCounter(0)
	fmt.Println(counter())
	fmt.Println(counter())

	triple := doubleFactory(3)
	double3 := triple(2)
	fmt.Println(double3(4))
}
