// Test: Interface embedding and method set promotion.
//
// What we test:
//   - An Outer interface embeds an Inner interface.
//   - A concrete type (ConcreteOuter) implements both.
//   - gosymdb should find ConcreteOuter as an implementor of BOTH Inner and Outer.
//   - When called through Outer, the concrete Read() should show 0 static callers
//     (interface dispatch — LIM-004 expected).
//   - The `implementors` command should return ConcreteOuter for both interfaces.
//
// Why it's hard:
//   - Most tools track direct interface satisfaction (type implements Outer directly).
//     Promoted method sets from embedded interfaces require recursive method set
//     analysis. gosymdb may only record direct implementations.
//
// Expected:
//   - ConcreteOuter: implements Inner (promoted via Outer embedding)
//   - ConcreteOuter: implements Outer (direct)
//   - ConcreteOuter.Read(): 0 static callers (dispatch through Outer) + hint
//   - ConcreteOuter.Write(): 0 static callers (dispatch through Outer) + hint

package main

// Inner is a simple interface.
type Inner interface {
	Read() int
}

// Outer embeds Inner and adds Write.
type Outer interface {
	Inner
	Write(v int)
}

// ConcreteOuter implements the full Outer interface (including Inner via promotion).
type ConcreteOuter struct {
	val int
}

func (c *ConcreteOuter) Read() int {
	return c.val
}

func (c *ConcreteOuter) Write(v int) {
	c.val = v
}

// useOuter calls through the Outer interface — dispatch is invisible to static analysis.
func useOuter(o Outer) {
	o.Write(1)
	_ = o.Read()
}

// useInner calls through the Inner interface — also invisible.
func useInner(i Inner) {
	_ = i.Read()
}

// directTarget is called directly so we can verify direct callers still work.
func directTarget() int {
	return 99
}

func caller() int {
	return directTarget()
}

func main() {
	c := &ConcreteOuter{}
	useOuter(c)
	useInner(c)
	caller()
}
