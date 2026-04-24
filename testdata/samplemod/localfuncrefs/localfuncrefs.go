// Package localfuncrefs tests BUG-006: function references in local variable
// declarations and assignments should produce ref edges.
package localfuncrefs

import "fmt"

// AssignedViaShortDecl is assigned to a local var using `:=`.
func AssignedViaShortDecl(s string) { fmt.Println(s) }

// AssignedViaLongDecl is assigned to a local var using `var h func(...) = fn`.
func AssignedViaLongDecl(s string) { fmt.Println(s) }

// AssignedAfterDecl is assigned to a var declared separately.
func AssignedAfterDecl(s string) { fmt.Println(s) }

// PassedAsArg is passed directly as a function argument (control case).
func PassedAsArg(s string) { fmt.Println(s) }

// Handler is a named function type.
type Handler func(string)

// AssignedToNamedFuncType is assigned to a named function type variable.
func AssignedToNamedFuncType(s string) { fmt.Println(s) }

func run(h func(string), s string) { h(s) }

// UseAll exercises all assignment forms so ref edges are created (or not, pre-fix).
func UseAll() {
	h1 := AssignedViaShortDecl // short decl — ref NOT tracked pre-fix (BUG-006)
	h1("a")

	var h2 func(string) = AssignedViaLongDecl // long decl — ref NOT tracked pre-fix (BUG-006)
	h2("b")

	var h3 func(string)
	h3 = AssignedAfterDecl // post-decl assign — NOT tracked pre-fix (BUG-006)
	h3("c")

	run(PassedAsArg, "d") // direct arg — ref IS tracked (control)

	var h4 Handler = AssignedToNamedFuncType // named func type assign — NOT tracked pre-fix (BUG-006)
	h4("e")
}
