// TEST: Function references in local variable declarations and assignments.
//
// EXPECT: A function referenced on the RHS of a local var declaration or assignment
//
//	should produce a ref edge, just like a function passed as an argument.
//
// BUG-006: When a function is assigned to a local variable (short decl `h := fn`,
//
//	long decl `var h func(...) = fn`, or post-declaration assignment `h = fn`),
//	the ref edge to `fn` is NOT recorded. This means `fn` has 0 callers even
//	though it is clearly referenced and used in the function body.
//
// Control case: passing a function directly as an argument DOES create a ref edge.
//
// Root cause hypothesis: The call-edge and func-ref walkers handle function arguments,
//
//	composite literals, and return statements, but do not walk the RHS of local
//	assignment statements (ast.AssignStmt) for function-typed identifiers.
package main

import "fmt"

// assignedViaShortDecl is assigned to a local var using `:=`.
// BUG-006: expected 1 caller, actual 0.
func assignedViaShortDecl(s string) { fmt.Println(s) }

// assignedViaLongDecl is assigned to a local var using `var h func(...) = fn`.
// BUG-006: expected 1 caller, actual 0.
func assignedViaLongDecl(s string) { fmt.Println(s) }

// assignedAfterDecl is assigned to a var declared separately: `var h func(...); h = fn`.
// BUG-006: expected 1 caller, actual 0.
func assignedAfterDecl(s string) { fmt.Println(s) }

// passedAsArg is passed directly as a function argument.
// CONTROL: this should have 1 caller (the ref is tracked correctly).
func passedAsArg(s string) { fmt.Println(s) }

// namedFuncTypeTarget is assigned to a named function type variable.
// BUG-006: expected 1 caller, actual 0.
func namedFuncTypeTarget(s string) { fmt.Println(s) }

type Handler func(string)

func run(h func(string), s string) { h(s) }

func main() {
	h1 := assignedViaShortDecl // short decl — ref NOT tracked (BUG-006)
	h1("a")

	var h2 func(string) = assignedViaLongDecl // long decl — ref NOT tracked (BUG-006)
	h2("b")

	var h3 func(string)
	h3 = assignedAfterDecl // post-declaration assign — NOT tracked (BUG-006)
	h3("c")

	run(passedAsArg, "d") // direct arg — ref IS tracked (control)

	var h4 Handler = namedFuncTypeTarget // named func type assign — NOT tracked (BUG-006)
	h4("e")
}
