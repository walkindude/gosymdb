# gosymdb-testbench

Adversarial test suite for gosymdb — a local Go symbol and call-edge index built on SQLite.

`CATALOG.json` is the source of truth for all bugs and limitations. It must be kept updated and internally consistent.

## For agents: quick start

```bash
# 1. Run the full testbench (indexes into fresh temp DB automatically):
go test ./internal/cmd/... -run TestBench -v -count=1

# 2. Read structured results:
cat testbench/CATALOG.json
```

## Repo structure

```
.
├── CLAUDE.md              # You are here. Repo context for agents.
├── CATALOG.json           # Machine-readable catalog of all bugs and limitations.
│                          # This is the source of truth. Update after every test run.
├── 01_reflect_calls/      # Each directory is an independent Go module.
│   ├── go.mod
│   └── main.go
├── 02_func_values/
├── ...
└── 20_iface_embedding/
```

**Test assertions** live in `../internal/cmd/testbench_test.go` — a Go test that indexes all
modules into a fresh temp DB and runs parallel subtests. Each module has a `benchNN`
function registered in `TestBench`.

## Conventions

- **Module naming**: `NN_snake_case/` — number prefix keeps ordering stable.
- **Each module** is a standalone `go.mod` with a `main` package. No external dependencies.
- **Comments** in each `main.go` explain: what the test targets, what we expect, and why it's hard.
- **CATALOG.json** is the canonical catalog. Every bug gets `BUG-NNN`, every limitation gets `LIM-NNN`.

## How to add a new test module

1. Create `NN_new_name/go.mod` and `NN_new_name/main.go`.
2. Add a `benchNN` function to `../internal/cmd/testbench_test.go` and register it in `TestBench`.
3. Add the module entry and any findings to `CATALOG.json`.
4. Run `go test ./internal/cmd/... -run TestBench -v -count=1` to verify.

## What gosymdb handles well

Generics, embedding promotion (3+ levels), init() chains, go/defer call edges,
closures inside deferred/goroutine contexts, type switch branch resolution,
unicode identifiers, method expressions/values as refs, var-init calls (when
inside named functions).

## What's broken or limited

See `CATALOG.json` for the full structured catalog. Summary:

### Bugs (open)
- **BUG-004**: Chained call expressions (`f()()()`) produce unresolved entries.

### Bugs (fixed — 2026-03-19/20)
- **BUG-001/002**: Package-level scope callers — fixed by `init$var:<varname>` synthetic caller.
- **BUG-003**: Ref edges attributed to package instead of enclosing function — fixed.
- **BUG-005**: No interface dispatch hint — fixed.
- **BUG-006**: Local func ref assignments not tracked — fixed.
- **BUG-007**: --tests flag duplicate symbols — fixed.
- **BUG-008**: dead reports interface impls as dead — fixed.

### Limitations (inherent)
- **LIM-001**: Reflect calls invisible (inherent to static analysis).
- **LIM-002**: Build tags restrict index to current GOOS/GOARCH.
- **LIM-003**: go:linkname aliases not resolved to their targets.
- **LIM-004**: Interface dispatch callers invisible. Tests verify correct behavior: 0 callers + hint present.
