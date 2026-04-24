// Package concurrent exercises mutex-guarded state so the indexer can track
// sync.Mutex calls as resolved callees of Inc and Value.
package concurrent

import "sync"

// SafeCounter is a thread-safe counter.
type SafeCounter struct {
	mu sync.Mutex
	n  int
}

// Inc increments the counter. Callees must include sync.*Mutex.Lock and Unlock.
func (c *SafeCounter) Inc() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
}

// Value returns the current count.
func (c *SafeCounter) Value() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// New constructs a SafeCounter. Callers of New exist in tests only.
func New() *SafeCounter {
	return &SafeCounter{}
}

// unused is unexported with no callers — a true dead-code candidate.
func unused() int {
	return 42
}
