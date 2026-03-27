// TEST: Generics create multiple instantiations of the same function.
// EXPECT: How does gosymdb represent generic functions? One symbol or many?
// Are call edges through type parameters tracked correctly?
package main

import (
	"cmp"
	"fmt"
)

// Generic function — one source symbol, multiple runtime instantiations.
func Max[T cmp.Ordered](a, b T) T {
	if a > b {
		return a
	}
	return b
}

// Generic type with methods.
type Pair[A, B any] struct {
	First  A
	Second B
}

func (p Pair[A, B]) Swap() Pair[B, A] {
	return Pair[B, A]{First: p.Second, Second: p.First}
}

// Generic function calling another generic function.
func MaxOfThree[T cmp.Ordered](a, b, c T) T {
	return Max(Max(a, b), c) // Generic calling generic.
}

// Constrained interface with type sets.
type Number interface {
	~int | ~float64 | ~int64
}

func Sum[T Number](vals []T) T {
	var total T
	for _, v := range vals {
		total += v
	}
	return total
}

// Generic function taking a function parameter — double indirection.
func Map[T, U any](slice []T, fn func(T) U) []U {
	result := make([]U, len(slice))
	for i, v := range slice {
		result[i] = fn(v)
	}
	return result
}

func double(n int) int      { return n * 2 }
func toString(n int) string { return fmt.Sprintf("%d", n) }

// Generic interface satisfaction.
type Container[T any] interface {
	Get() T
	Set(T)
}

type Box[T any] struct{ val T }

func (b *Box[T]) Get() T  { return b.val }
func (b *Box[T]) Set(v T) { b.val = v }

func UseContainer[T any](c Container[T], v T) T {
	c.Set(v)
	return c.Get()
}

// Recursive generic type.
type Tree[T cmp.Ordered] struct {
	Value T
	Left  *Tree[T]
	Right *Tree[T]
}

func (t *Tree[T]) Insert(v T) *Tree[T] {
	if t == nil {
		return &Tree[T]{Value: v}
	}
	if v < t.Value {
		t.Left = t.Left.Insert(v)
	} else {
		t.Right = t.Right.Insert(v)
	}
	return t
}

func main() {
	// Same generic, different type args.
	fmt.Println(Max(1, 2))
	fmt.Println(Max("a", "z"))
	fmt.Println(Max(1.5, 2.5))

	p := Pair[int, string]{1, "hello"}
	fmt.Println(p.Swap())

	fmt.Println(MaxOfThree(1, 2, 3))

	fmt.Println(Sum([]int{1, 2, 3}))
	fmt.Println(Sum([]float64{1.1, 2.2}))

	fmt.Println(Map([]int{1, 2, 3}, double))
	fmt.Println(Map([]int{1, 2, 3}, toString))

	b := &Box[int]{val: 42}
	fmt.Println(UseContainer[int](b, 99))

	tree := (*Tree[int])(nil)
	tree = tree.Insert(5)
	tree = tree.Insert(3)
	tree = tree.Insert(7)
}
