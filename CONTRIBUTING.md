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

### Adversarial regression tests

Some fixtures under `testbench/` encode bugs found during adversarial review — the aliases/generics cases in `testbench/alias_generic_consistency/` (bench22) and the package-filter leak in `testbench/blast_pkg_filter/` (bench23) are the current examples, each cross-referenced to `docs/adversarial/round-20260328.md`. These are **release gates**, not incidental examples. Do not remove, weaken, or rewrite them unless the underlying behavior is intentionally changing — in which case update the adversarial report in the same commit so the change is auditable.

### Contract tests

Two tests in `internal/cmd/` guard the agent-facing contract against drift:

- `TestCLIBridgeManifestMatchesCobra` — keeps `cli_bridge_spec.json` in sync with the Cobra command surface (command names, flag names, and declared globals). Adding a Cobra command? Add it to the manifest, or add it to the `excludedFromManifest` allowlist with a reason.
- `TestAgentContextMatchesCobra` — keeps `agent-context`'s command list in sync with Cobra. Adding a Cobra command? Append it to `agentContextOrder`, or add it to `excludedFromAgentContext` with a reason.

If either test fails after a command/flag change, the message names the missing side — update it there instead of silencing the test.

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
