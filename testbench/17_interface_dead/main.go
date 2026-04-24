// TEST: dead command must NOT report interface implementation methods as dead.
// EXPECT: methods on concrete types that satisfy an interface are NOT dead,
//
//	even when they are called exclusively through the interface variable
//	(no direct static call edge exists to the concrete method).
//
// WHY IT'S HARD: gosymdb records call edges to interface method signatures,
//
//	not to the concrete method. The concrete method has 0 callers in the
//	calls table. Without the implements join in the dead query, concrete
//	methods that only serve interface contracts appear dead incorrectly.
package main

import "fmt"

// --- Doer interface with two methods ---

type Doer interface {
	Do(s string) string
	Tag() string
}

// RealDoer satisfies Doer via pointer receiver.
type RealDoer struct{ label string }

func (r *RealDoer) Do(s string) string { return r.label + ": " + s }
func (r *RealDoer) Tag() string        { return r.label }

// StubDoer satisfies Doer via pointer receiver.
type StubDoer struct{}

func (s *StubDoer) Do(str string) string { return "stub:" + str }
func (s *StubDoer) Tag() string          { return "stub" }

// Processor satisfies Doer via value receiver.
type Processor struct{ prefix string }

func (p Processor) Do(s string) string { return p.prefix + s }
func (p Processor) Tag() string        { return p.prefix }

// --- Dead code (genuinely unused) ---

// newStubDoer is never called: it should appear as dead.
func newStubDoer() *StubDoer { return &StubDoer{} }

// --- Entry point: calls through interface only ---

func run(d Doer) {
	fmt.Println(d.Tag(), d.Do("hello"))
}

func main() {
	run(&RealDoer{label: "real"})
	run(&StubDoer{})
	run(Processor{prefix: "proc:"})
}
