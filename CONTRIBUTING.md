# Contributing to gosymdb

## Setup

1. Clone the repo
2. Ensure Go 1.26+ is installed
3. Install lefthook: `go install github.com/evilmartians/lefthook@latest` (or via your package manager)
4. Run `lefthook install` to set up git hooks

## Development workflow

```bash
# Build
make build

# Run tests
make test

# Run the testbench (integration tests)
make testbench

# Lint
make lint
```

## Test-first discipline

All bug fixes and features must include a test *before* the implementation:

1. Write a failing test (or add a testbench module under `testbench/`)
2. Verify it fails for the right reason
3. Implement the fix/feature
4. Verify the test passes

## Testbench

The `testbench/` directory contains standalone Go modules that exercise specific edge cases. Each module is a numbered directory (`01_reflect_calls`, `02_func_values`, etc.) with its own `go.mod`.

To add a new test module:
1. Create `testbench/NN_descriptive_name/` with a `go.mod` and `.go` files
2. Add assertions in `internal/cmd/testbench_test.go`
3. Update `testbench/CATALOG.json` with the new module entry

## Commit conventions

Use [Conventional Commits](https://www.conventionalcommits.org/). The release
changelog is auto-generated from these prefixes — using the wrong prefix means
your change shows up in the wrong section or gets filtered out entirely.

**Appear in the changelog:**
- `feat:` — new feature or command
- `fix:` — bug fix
- `perf:` — performance improvement (no functional change)
- `refactor:` — code restructuring (no functional change)

**Filtered out of the changelog** (still valid commits):
- `test:` — test-only changes (new testbench modules, assertions)
- `docs:` — documentation only
- `chore:` — maintenance (deps, formatting, config)
- `ci:` — CI/CD changes (workflows, goreleaser, lefthook)
- `style:` — code style (gofmt, whitespace)

Scopes are optional: `fix(indexer):`, `feat(callers):`, etc.

## Pre-commit hooks

Lefthook runs on every commit:
- `gofmt` on staged `.go` files
- `go mod tidy` with drift check
