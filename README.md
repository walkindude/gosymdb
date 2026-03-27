# gosymdb

A Go symbol and call-graph database backed by SQLite. Index any Go module and query symbols, callers, callees, blast radius, dead code, interface implementors, and type references — all with structured JSON output designed for programmatic consumption.

## Install

```bash
go install github.com/walkindude/gosymdb@latest
```

## Quick start

```bash
# Index the current module
gosymdb index --root .

# Find a symbol
gosymdb find --q Store --json

# Full profile: definition + callers + callees + blast radius
gosymdb trace --symbol MyFunc --json

# What breaks if I change this?
gosymdb blast-radius --symbol 'github.com/walkindude/gosymdb/indexer.IndexModule' --depth 5 --json
```

## Commands

| Command | Description |
|---------|-------------|
| `index` | Walk a directory tree, find all `go.mod` files, build a SQLite symbol + call index |
| `find` | Search symbols by name, package, file, or kind |
| `def` | Exact single-symbol lookup |
| `callers` | Direct or transitive callers of a symbol |
| `callees` | What does a function call? |
| `blast-radius` | Full transitive impact analysis with test/prod split |
| `dead` | Symbols with no callers (dead code candidates) |
| `trace` | Single-shot overview: def + callers + callees + blast radius |
| `implementors` | Types implementing an interface, or interfaces a type satisfies |
| `references` | Where a type is used: assertions, switches, composite literals, conversions |
| `packages` | All indexed packages with symbol counts |
| `health` | Index quality report |
| `agent-context` | One-shot API dump for agent bootstrap |
| `version` | Version and schema info |

All query commands support `--json` for structured output.

## How it works

gosymdb uses `go/packages` (the same loader the Go compiler uses) to parse and type-check your code, then walks the AST to extract:

- **Symbols**: functions, methods, types, interfaces, variables, constants
- **Call edges**: direct calls, function-value references
- **Interface satisfaction**: which types implement which interfaces
- **Type references**: assertions, switches, composite literals, conversions, field access, embedding

Everything is stored in a single SQLite file for fast, offline querying.

## Privacy

gosymdb runs **entirely on your machine**. It does not send your source code, symbols, or any data to external servers. There is no telemetry, no phoning home, no network access of any kind. The SQLite database stays on your local disk.

## Agent integration

gosymdb is designed for AI coding agents. Every command produces structured JSON with:

- Fully-qualified symbol names (no ambiguity)
- File paths and line numbers (jump-to-definition)
- Structured errors with `error_code`, `hint`, and `recovery` fields
- Environment envelope (`env.stale_packages`, `env.git`) on every response
- `agent-context` command for one-shot API discovery

Run `gosymdb agent-context` at the start of any agent session to get the full command reference.

### Claude Code plugin

gosymdb ships as a [Claude Code plugin](https://docs.anthropic.com/en/docs/claude-code/plugins) with three skills:

- **sym** — find a Go symbol (replaces grep/rg for definition lookup)
- **trace** — full symbol profile in one call (def + callers + callees + blast radius)
- **impact** — check what breaks before changing a symbol

Install locally:

```bash
claude plugin install --dir /path/to/gosymdb
```

The plugin handles binary installation, index creation, and stale-index detection automatically.

## License

Apache 2.0 — see [LICENSE](LICENSE).
