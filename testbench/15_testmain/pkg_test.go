package testmainpkg

import (
	"os"
	"testing"
)

// TestMain is the test harness entry point.
// setup() and teardown() are only called here — invisible without --is-test.
func TestMain(m *testing.M) {
	setup()
	code := m.Run()
	teardown()
	os.Exit(code)
}

// TestProductionFunc calls productionFunc from a test.
func TestProductionFunc(t *testing.T) {
	result := productionFunc()
	if result == "" {
		t.Fatal("productionFunc returned empty string")
	}
}
