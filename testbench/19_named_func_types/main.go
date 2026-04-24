// Test: Named function types and struct function fields.
//
// What we test:
//   1. Named function type: `type Handler func(string)` — when `myHandler` is assigned
//      to a var of type Handler and then called via that var, is `myHandler` tracked?
//   2. Struct function field: `type Router struct { Handle func(string) }` — when
//      `myHandle` is stored in Router.Handle and called via r.Handle("foo"), is
//      `myHandle` tracked as a callee?
//
// After BUG-006 was fixed (local var assignments now track func refs), named function
// type assignments should be covered. Struct function fields are a separate pattern:
// the ref is a field value in a struct literal, not a local variable assignment.
//
// Expected:
//   - myHandler: has callers (assigned to Handler var, BUG-006 fix covers this)
//   - myHandle:  may have 0 callers (struct literal field assignment — separate from
//               local var decl; depends on whether struct literal RHS is walked)
//   - called:   has callers (called directly inside myHandler and myHandle)

package main

// --- Named function type ---

// Handler is a named function type.
type Handler func(string)

// called is the direct target we want to track.
func called(s string) {
	_ = s
}

// myHandler is the concrete function assigned to a Handler variable.
func myHandler(s string) {
	called(s)
}

// namedTypeUser assigns myHandler to a Handler var and calls it.
func namedTypeUser() {
	var h Handler = myHandler
	h("hello")
}

// --- Struct function field ---

// Router has a function-typed field.
type Router struct {
	Handle func(string)
}

// myHandle is stored into Router.Handle.
func myHandle(s string) {
	called(s)
}

// structFieldUser builds a Router with Handle = myHandle and calls it.
func structFieldUser() {
	r := Router{Handle: myHandle}
	r.Handle("world")
}

func main() {
	namedTypeUser()
	structFieldUser()
}
