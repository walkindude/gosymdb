// Test: Function reference contexts that may be missed by the func-ref pass.
//
// The indexer's recordRefs handles: CallExpr args, CompositeLit values,
// AssignStmt RHS, and ValueSpec values. This module tests contexts that
// are NOT in that switch statement:
//
//   - ReturnStmt: returning a function value
//   - SendStmt: sending a function value on a channel
//
// Expected:
//   - returnTarget: has callers (returned as func value from factory)
//   - sendTarget: has callers (sent on channel)
//   - controlArg: has callers (passed as argument — known-working control)

package main

// returnTarget is returned as a function value. If the indexer's ref pass
// does not handle *ast.ReturnStmt, this function will show 0 callers.
func returnTarget() int { return 42 }

// factory returns returnTarget as a function value.
func factory() func() int {
	return returnTarget
}

// sendTarget is sent on a channel. If the indexer's ref pass does not
// handle *ast.SendStmt, this function will show 0 callers.
func sendTarget() int { return 99 }

// sender sends sendTarget on a channel.
func sender(ch chan func() int) {
	ch <- sendTarget
}

// controlArg is passed as a function argument (known-working path).
func controlArg() int { return 1 }

func useFunc(f func() int) int { return f() }

func main() {
	fn := factory()
	_ = fn()

	ch := make(chan func() int, 1)
	sender(ch)
	recv := <-ch
	_ = recv()

	_ = useFunc(controlArg)
}
