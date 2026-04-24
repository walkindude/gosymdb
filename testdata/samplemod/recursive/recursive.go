// Package recursive exercises self-calling functions so tests can confirm
// that a function calling itself is NOT considered dead code, and that
// functions reachable transitively are also not dead.
package recursive

// Fib computes the nth Fibonacci number recursively.
// It calls itself AND scale — both must NOT appear in dead-code results.
func Fib(n int) int {
	if n <= 1 {
		return scale(n)
	}
	return Fib(n-1) + Fib(n-2)
}

// scale is an unexported helper called by Fib — reachable, not dead.
func scale(n int) int {
	return n * 2
}

// neverCalled is unexported and not reachable from anywhere — dead.
func neverCalled() string {
	return "dead"
}
