# Contributing to gosymdb

## Setup

With [mise](https://mise.jdx.dev/) (recommended — one command gets go, golangci-lint, lefthook):

```bash
git clone https://github.com/walkindude/gosymdb
cd gosymdb
mise install
lefthook install
```

Without mise:

1. Install Go 1.26+
2. Install golangci-lint v2.11.4 (see `.golangci.yml`)
3. Install lefthook: `go install github.com/evilmartians/lefthook@latest`
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

## Tests

Bug fixes should land with a regression test. For indexer/analyzer changes, add a minimal module under `testbench/` that exercises the case — see existing modules for the pattern.

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
