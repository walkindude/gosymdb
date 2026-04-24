// TEST: Function calls in package-level variable initializers.
// EXPECT: These calls happen at program startup, before main().
// Are they captured as call edges? Who is the "caller"?
package main

import (
	"fmt"
	"os"
	"strconv"
)

// Direct function call in var initializer.
var defaultPort = getDefaultPort()

func getDefaultPort() int {
	if p := os.Getenv("PORT"); p != "" {
		n, _ := strconv.Atoi(p)
		return n
	}
	return 8080
}

// Method call in var initializer.
type Config struct {
	Host string
	Port int
}

var globalConfig = newConfig()

func newConfig() *Config {
	return &Config{
		Host: getHost(),
		Port: getDefaultPort(), // Second call to same function.
	}
}

func getHost() string {
	if h := os.Getenv("HOST"); h != "" {
		return h
	}
	return "localhost"
}

// Chained calls in var init.
var computed = transform(generate(42))

func generate(seed int) []int {
	result := make([]int, seed)
	for i := range result {
		result[i] = i * i
	}
	return result
}

func transform(input []int) int {
	sum := 0
	for _, v := range input {
		sum += v
	}
	return sum
}

// Function literal in var init.
var lazy = func() string {
	return secretInit() // Call inside anonymous function assigned to var.
}()

func secretInit() string {
	return "initialized"
}

// Var init ordering dependency — b depends on a.
var a = initA()
var b = initB(a)

func initA() int      { return 10 }
func initB(x int) int { return x * 2 }

func main() {
	fmt.Println(defaultPort)
	fmt.Println(globalConfig)
	fmt.Println(computed)
	fmt.Println(lazy)
	fmt.Println(a, b)
}
