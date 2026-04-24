// TEST: Method expressions vs method values — two ways to reference methods.
// EXPECT: T.Method (expression) and t.Method (value) create different AST nodes.
// Does gosymdb track both as references to the same underlying method?
package main

import "fmt"

type Calculator struct {
	value int
}

func (c *Calculator) Add(n int) *Calculator {
	c.value += n
	return c
}

func (c *Calculator) Multiply(n int) *Calculator {
	c.value *= n
	return c
}

func (c *Calculator) Result() int {
	return c.value
}

func main() {
	c := &Calculator{}

	// --- Method value: bound to receiver ---
	addMethod := c.Add // Method value — receiver is captured.
	addMethod(5)       // Calls c.Add(5). Is this tracked as a call to Add?

	// --- Method expression: receiver becomes first parameter ---
	addExpr := (*Calculator).Add // Method expression — explicit receiver.
	addExpr(c, 10)               // Calls c.Add(10). Different AST, same target.

	// --- Passing method expression as function value ---
	applyOp(c, (*Calculator).Multiply, 3)

	// --- Passing method value as function value ---
	applyBound(c.Add, 7)

	fmt.Println(c.Result())

	// --- Method expression in slice ---
	ops := []func(*Calculator, int) *Calculator{
		(*Calculator).Add,
		(*Calculator).Multiply,
	}
	for _, op := range ops {
		op(c, 2) // Indirect call via method expression in slice.
	}

	fmt.Println(c.Result())

	// --- Method value in map ---
	boundOps := map[string]func(int) *Calculator{
		"add": c.Add,
		"mul": c.Multiply,
	}
	boundOps["add"](100) // Indirect call via method value in map.

	fmt.Println(c.Result())
}

func applyOp(c *Calculator, op func(*Calculator, int) *Calculator, n int) {
	op(c, n) // Calls through method expression parameter.
}

func applyBound(op func(int) *Calculator, n int) {
	op(n) // Calls through method value parameter.
}
