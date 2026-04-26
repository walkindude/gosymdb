# AGENTS.md — gosymdb

Documentation for AI agents (and anyone else) reading this repo.

## What gosymdb is

gosymdb is a Go-specific symbol and call-graph database backed by SQLite. It uses Go's own type-checker (`go/packages`, `go/types`) to build a persistent index of one or more Go modules, then answers structured queries:

- `find` — search symbols by name, package, file, or kind
- `def` — exact single-symbol lookup
- `callers` / `callees` — direct or transitive call edges
- `blast-radius` — full transitive impact (with test/prod split)
- `dead` — symbols with no callers
- `implementors` — interface ↔ type satisfaction
- `references` — where a type appears (assertions, switches, literals, conversions)
- `packages`, `health`, `agent-context`, `version` — meta queries

Every query supports `--json` for structured output. The full API is enumerated in one shot by `gosymdb agent-context`, which prints a JSON document with command signatures, flags, env block, and parsing notes.

## How the database is found

`gosymdb` walks parent directories looking for `gosymdb.sqlite`, the same way Go tools walk for `go.mod`. Pass `--db <path>` to override. If no database is found, commands return `error_code: no_database` with a hint to run `gosymdb index --root <module>` first.

## The freshness loop

The index goes stale the moment source files change. Every JSON response carries an `env.stale_packages` array listing packages whose source has drifted from what's indexed. The check uses a git fast-path (one `git diff` against the commit stamped at index time) with a per-file FNV-64a hash fallback when git isn't usable.

Pass `--auto-reindex` to any query command to re-index stale modules in place before the query runs. The check itself is cheap enough to run on every call (sub-second on Kubernetes-scale codebases).

## MCP integration

gosymdb exposes `gosymdb cli-bridge-manifest` — a single subcommand that prints the canonical [cli-bridge](https://github.com/walkindude/cli-bridge) spec for this binary. When cli-bridge is installed and gosymdb's spec is registered, each command becomes an MCP tool named `gosymdb_<command>` (e.g. `gosymdb_callers`, `gosymdb_blast_radius`). Setup steps are in the [main README's MCP section](./README.md#use-with-claude-code-mcp).

Spec drift across gosymdb version bumps is handled automatically by cli-bridge's [auto-refresh on startup](https://github.com/walkindude/cli-bridge/blob/master/AGENTS.md#the-manifest-convention) once the convention is in place.

## Known limitations

gosymdb is a static symbolic index, not a runtime tracer:

- Interface dispatch is not recorded as a direct call edge. The `callers` response includes a `hint` field flagging this when relevant. Use `implementors` to find concrete types, then `callers`/`callees` against the concrete method.
- Reflection (`reflect.Value.Call`, method-value lookups) is not statically resolved.
- `go:linkname`, plugin loading, and runtime dispatch are not modeled.
- Build tags affect what `go/packages` loads. Symbols behind tags not active for the host won't be indexed.

## Related tools in this family

- [walkindude/cli-bridge](https://github.com/walkindude/cli-bridge) — the MCP server that turns `gosymdb cli-bridge-manifest` into the registered MCP tools listed above.
- [walkindude/cairn](https://github.com/walkindude/cairn) — a separate tool for notes-to-future-me across stateless agent sessions. Also follows the cli-bridge manifest convention.

These are listed as factual references — gosymdb works without them, and they're independent projects.
