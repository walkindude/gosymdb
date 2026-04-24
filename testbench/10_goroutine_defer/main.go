// TEST: go and defer statements create call edges that may be tracked differently.
// EXPECT: Are `go f()` and `defer f()` treated as calls to f?
// What about `go func() { ... }()` anonymous goroutines?
package main

import (
	"fmt"
	"sync"
)

func worker(id int, wg *sync.WaitGroup) {
	defer wg.Done() // defer call — is this an edge to (*sync.WaitGroup).Done?
	fmt.Printf("worker %d\n", id)
	heavyWork(id)
}

func heavyWork(id int) {
	fmt.Printf("heavy work %d\n", id)
}

func cleanup() {
	fmt.Println("cleanup ran")
}

// Deferred closure capturing variables.
func deferredClosure() {
	x := 0
	defer func() {
		fmt.Println("deferred x:", x) // Captures x — is this closure tracked?
		closureHelper()
	}()
	x = 42
}

func closureHelper() {
	fmt.Println("closure helper")
}

// go statement with named function.
func spawnWorkers() {
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go worker(i, &wg) // go call — is this an edge to worker?
	}
	wg.Wait()
}

// go statement with anonymous function.
func spawnAnonymous() {
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(id int) { // Anonymous goroutine.
			defer wg.Done()
			anonymousTarget(id) // Call inside anonymous goroutine.
		}(i)
	}
	wg.Wait()
}

func anonymousTarget(id int) {
	fmt.Printf("anon target %d\n", id)
}

// Stacked defers — order matters at runtime, does the indexer capture all?
func stackedDefers() {
	defer fmt.Println("first deferred (runs last)")
	defer fmt.Println("second deferred")
	defer cleanup()
	fmt.Println("main body")
}

// Defer with method value.
type Resource struct{ name string }

func (r *Resource) Close() { fmt.Println("closing", r.name) }

func useResource() {
	r := &Resource{name: "db"}
	defer r.Close() // Method value defer.
	fmt.Println("using", r.name)
}

func main() {
	spawnWorkers()
	spawnAnonymous()
	deferredClosure()
	stackedDefers()
	useResource()
}
