// TEST: Reflect-based calls are invisible to static analysis.
// EXPECT: gosymdb should show secretWork as dead code (0 callers),
// but it IS called at runtime via reflect. False dead-code positive.
package main

import (
	"fmt"
	"reflect"
)

type Service struct{}

// secretWork is called only via reflect — no static call edge exists.
func (s *Service) secretWork() string {
	return "called via reflect"
}

// hiddenHelper is called by secretWork's reflect cousin.
func hiddenHelper() string {
	return "I exist"
}

// reflectCall invokes a method by name string — completely opaque to static analysis.
func reflectCall(obj any, method string) any {
	v := reflect.ValueOf(obj)
	m := v.MethodByName(method)
	if !m.IsValid() {
		panic("no such method: " + method)
	}
	results := m.Call(nil)
	return results[0].Interface()
}

// Double indirection: the method name is computed.
func computedReflectCall(obj any, prefix string) any {
	name := prefix + "Work" // "secret" + "Work" = "secretWork"
	return reflectCall(obj, name)
}

func main() {
	s := &Service{}
	// Static analysis sees: main -> computedReflectCall -> reflectCall
	// Static analysis MISSES: reflectCall -> Service.secretWork
	result := computedReflectCall(s, "secret")
	fmt.Println(result)
}
