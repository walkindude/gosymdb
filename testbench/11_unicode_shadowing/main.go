// TEST: Unicode identifiers, visual confusables, and scope shadowing.
// EXPECT: gosymdb may conflate visually similar identifiers or mishandle
// unicode normalization. Shadowing may confuse caller/callee resolution.
package main

import "fmt"

// Latin 'a' vs Cyrillic 'а' (U+0430) — visually identical, different symbols!
func Process() string { return "latin Process" }

func Рrocess() string { return "cyrillic Р at start" } // Cyrillic Р (U+0420)

// Combining characters.
func café() string { return "café with combining é" } // e + combining acute
func cafe() string { return "plain cafe" }

// Greek letters commonly confused with Latin.
func Ηelper() string { return "starts with Greek Eta Η" } // Η (U+0397)
func Helper() string { return "starts with Latin H" }

// --- Scope shadowing ---

var x = outerX()

func outerX() int {
	return 1
}

func shadowed() {
	x := innerX() // Shadows package-level x.
	fmt.Println(x)

	{
		x := deeperX() // Shadows again.
		fmt.Println(x)
	}
}

func innerX() int  { return 2 }
func deeperX() int { return 3 }

// Shadowing built-in names.
func len() int { return -1 } // Shadows builtin len!
func cap() int { return -2 } // Shadows builtin cap!

// Function with same name in different scope levels.
func ambiguous() string {
	type local struct{}
	// Method on local type — same "name" as package-level ambiguous.
	_ = local{}
	return "package-level"
}

func main() {
	fmt.Println(Process())
	fmt.Println(Рrocess())
	fmt.Println(café())
	fmt.Println(cafe())
	fmt.Println(Ηelper())
	fmt.Println(Helper())
	fmt.Println(len())
	fmt.Println(cap())
	shadowed()
	fmt.Println(ambiguous())
}
