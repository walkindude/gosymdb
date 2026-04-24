// TEST: Build tags cause different files to be compiled on different platforms.
// EXPECT: gosymdb indexes on one platform — the other platform's functions
// appear as dead code or are missing entirely.
package main

import "fmt"

func main() {
	fmt.Println(platformSpecific())
	fmt.Println(shared())
}

func shared() string {
	return "shared logic: " + helperForPlatform()
}
