// Package iface exercises interface dispatch so tests can verify that:
//   - Methods called only through an interface appear as dead in the call graph
//     (the tool cannot trace interface-dispatch calls at index time).
//   - Functions that are genuinely unreachable also appear as dead.
package iface

// Doer is the interface under test.
type Doer interface {
	Do() string
	Tag() string
}

// RealDoer implements Doer. Its methods are called only via the Doer interface,
// so the call graph will record no direct callers — they look "dead".
type RealDoer struct{}

func (r *RealDoer) Do() string  { return "real" }
func (r *RealDoer) Tag() string { return "RealDoer" }

// StubDoer is a second implementation, used in tests only (via Run).
type StubDoer struct{ val string }

func (s *StubDoer) Do() string  { return s.val }
func (s *StubDoer) Tag() string { return "StubDoer" }

// Run calls through the Doer interface — direct call edges to Do/Tag are NOT recorded.
func Run(d Doer) string {
	return d.Do() + "/" + d.Tag()
}

// NewRealDoer is exported and called from outside — NOT dead.
func NewRealDoer() Doer {
	return &RealDoer{}
}

// newStubDoer is unexported and has no callers — genuinely dead.
func newStubDoer(v string) *StubDoer {
	return &StubDoer{val: v}
}

// extraHelper is an unexported method on RealDoer that is NOT part of the Doer
// interface and has no callers. It must appear as dead even though RealDoer
// implements Doer. The dead suppression must be method-precise, not type-wide.
func (r *RealDoer) extraHelper() string { return "extra" }
