// Package varinit exercises function calls that appear directly in package-level
// variable initializer expressions (outside any function body).
// Used by TestBUG002_VarInitDirectCallsTracked.
package varinit

// directInitCall is called directly in a var initializer — no containing function.
var InitValue = directInitCall()

func directInitCall() string { return "initialized" }

// initWithArg is also called directly in a var initializer.
var OtherValue = initWithArg(42)

func initWithArg(n int) int { return n * 2 }

// UseValues ensures the vars are referenced so the compiler doesn't complain.
func UseValues() (string, int) { return InitValue, OtherValue }
