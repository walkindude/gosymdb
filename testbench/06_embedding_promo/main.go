// TEST: Promoted methods via embedding create implicit call edges.
// EXPECT: When Outer.DoWork() is called, it's actually Inner.DoWork().
// Does gosymdb track the promoted method call to the right target?
package main

import "fmt"

// --- Basic promotion ---

type Inner struct{}

func (i *Inner) DoWork() string {
	return "inner work"
}

func (i *Inner) helper() string {
	return "inner helper"
}

type Outer struct {
	*Inner // Promotes DoWork and helper to Outer.
}

// --- Multi-level embedding ---

type Level0 struct{}

func (l *Level0) DeepMethod() string { return "level0" }

type Level1 struct{ *Level0 }
type Level2 struct{ *Level1 }
type Level3 struct{ *Level2 } // Level3.DeepMethod() goes through 3 embeddings.

// --- Shadowing via embedding ---

type Base struct{}

func (b *Base) Name() string { return "base" }

type Middle struct{ *Base }

func (m *Middle) Name() string { return "middle" } // Shadows Base.Name.

type Top struct{ *Middle }

// Top.Name() calls Middle.Name, NOT Base.Name.
// Does gosymdb resolve this correctly?

// --- Multiple embeddings, same method name ---

type Alpha struct{}

func (a *Alpha) Greet() string { return "alpha" }

type Beta struct{}

func (b *Beta) Greet() string { return "beta" }

type Conflict struct {
	*Alpha
	*Beta
}

// Conflict.Greet() is ambiguous — compile error if called directly.
// But each can be called explicitly: Conflict.Alpha.Greet()

// --- Interface satisfaction via embedding ---

type Saver interface {
	Save() error
}

type SaveMixin struct{}

func (s *SaveMixin) Save() error {
	fmt.Println("saved")
	return nil
}

type Document struct {
	*SaveMixin // Document now satisfies Saver via embedding.
}

func persist(s Saver) error {
	return s.Save() // Calls SaveMixin.Save, but via Document as Saver.
}

func main() {
	o := &Outer{Inner: &Inner{}}
	fmt.Println(o.DoWork()) // Promoted call.
	fmt.Println(o.helper()) // Promoted unexported call.

	l3 := &Level3{&Level2{&Level1{&Level0{}}}}
	fmt.Println(l3.DeepMethod()) // 3-level promotion.

	t := &Top{&Middle{&Base{}}}
	fmt.Println(t.Name()) // Shadowed — calls Middle.Name, not Base.Name.

	c := &Conflict{&Alpha{}, &Beta{}}
	fmt.Println(c.Alpha.Greet()) // Explicit, not ambiguous.
	fmt.Println(c.Beta.Greet())

	d := &Document{&SaveMixin{}}
	persist(d) // Interface + embedding.
}
