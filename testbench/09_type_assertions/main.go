// TEST: Type assertions and type switches create dynamic dispatch paths.
// EXPECT: The concrete type receiving the call is only known at runtime.
// gosymdb should struggle to connect caller to callee through these.
package main

import "fmt"

type Animal interface {
	Speak() string
}

type Dog struct{}

func (d *Dog) Speak() string { return "woof" }
func (d *Dog) Fetch() string { return "fetched!" }

type Cat struct{}

func (c *Cat) Speak() string { return "meow" }
func (c *Cat) Purr() string  { return "purrrr" }

type Fish struct{}

func (f *Fish) Speak() string { return "blub" }
func (f *Fish) Swim() string  { return "swimming" }

// Type switch — each branch calls a different concrete method.
func handleAnimal(a Animal) string {
	switch v := a.(type) {
	case *Dog:
		return v.Fetch() // Only reachable if a is *Dog.
	case *Cat:
		return v.Purr() // Only reachable if a is *Cat.
	case *Fish:
		return v.Swim() // Only reachable if a is *Fish.
	default:
		return v.Speak()
	}
}

// Chained type assertions — increasingly obscure.
func deepAssert(v any) string {
	if a, ok := v.(Animal); ok {
		if d, ok := a.(*Dog); ok {
			return d.Fetch()
		}
	}
	return "unknown"
}

// Type assertion in return position.
func mustDog(a Animal) *Dog {
	return a.(*Dog) // Panic if not *Dog. Static analysis can't verify.
}

// Interface-to-interface assertion.
type FetcherSpeaker interface {
	Animal
	Fetch() string
}

func assertComposite(a Animal) string {
	if fs, ok := a.(FetcherSpeaker); ok {
		return fs.Fetch() + " and " + fs.Speak()
	}
	return "not a fetcher-speaker"
}

func main() {
	animals := []Animal{&Dog{}, &Cat{}, &Fish{}}
	for _, a := range animals {
		fmt.Println(handleAnimal(a))
	}
	fmt.Println(deepAssert(&Dog{}))
	fmt.Println(mustDog(&Dog{}).Fetch())
	fmt.Println(assertComposite(&Dog{}))
}
