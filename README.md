# gosymdb

[![CI](https://github.com/walkindude/gosymdb/actions/workflows/ci.yml/badge.svg)](https://github.com/walkindude/gosymdb/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/walkindude/gosymdb?style=flat)](https://goreportcard.com/report/github.com/walkindude/gosymdb)
[![Go Reference](https://pkg.go.dev/badge/github.com/walkindude/gosymdb.svg)](https://pkg.go.dev/github.com/walkindude/gosymdb)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](./LICENSE)
[![MCP](https://img.shields.io/badge/MCP-compatible-8A2BE2)](https://modelcontextprotocol.io)

A Go symbol and call-graph database backed by SQLite. Index any Go module and query symbols, callers, callees, blast radius, dead code, interface implementors, and type references — all with structured JSON output designed for programmatic consumption.

## What this is

gosymdb is a local, Go-specific symbolic query CLI for coding agents. It uses Go's own package/type analysis (`go/packages`, `go/types` — the same loader the compiler uses) to build a persistent SQLite index, then answers repeatable JSON queries such as:

- where is this symbol defined?
- what calls this function?
- what does this function call?
- what breaks if I change this signature?
- what implements this interface?
- where is this type used?
- which symbols are never called?

## What this is not

- **Not semantic search.** Queries are symbolic, not natural-language or embedding-based.
- **Not multi-language.** Go only.
- **Not hosted.** Everything runs locally. No telemetry.
- **Not a replacement for `gopls`.** It complements `gopls` — see [gosymdb and gopls](#gosymdb-and-gopls).
- **Not a replacement for Sourcegraph, Augment, or Cursor.** Those solve broader multi-language, team-scale, or IDE-native problems.

## Install

### Homebrew (macOS / Linux)

```bash
brew tap walkindude/tap
brew install --cask gosymdb
```

The cask strips macOS Gatekeeper's quarantine flag post-install so the binary runs on first invocation without `xattr` gymnastics.

### Go toolchain

```bash
go install github.com/walkindude/gosymdb@latest
```

Requires Go 1.26+.

### Linux packages (deb / rpm / apk)

Download `.deb`, `.rpm`, or `.apk` for `amd64` / `arm64` from the [latest GitHub release](https://github.com/walkindude/gosymdb/releases/latest).

### Nix

```bash
nix profile install github:walkindude/gosymdb
# or one-off:
nix run github:walkindude/gosymdb -- --help
# or drop into a dev shell with go + gopls:
nix develop github:walkindude/gosymdb
```

### Scripted installer (macOS / Linux / Windows)

```bash
curl -sSL https://raw.githubusercontent.com/walkindude/gosymdb/master/install.sh | sh
```

Windows (PowerShell):

```powershell
iwr -useb https://raw.githubusercontent.com/walkindude/gosymdb/master/install.ps1 | iex
```

Both installers download the binary, verify its SHA-256 checksum against the release's `checksums.txt`, and — if Claude Code is installed — generate the [cli-bridge](https://github.com/walkindude/cli-bridge) spec so gosymdb registers as a first-class MCP tool (see below).

## Quick start

```bash
# 1. Index a Go module
cd ~/your-go-project
gosymdb index --root .

# 2. Query it
gosymdb find --q Store --json
gosymdb callers --symbol 'github.com/you/repo/pkg.MyFunc' --json
gosymdb blast-radius --symbol 'github.com/you/repo/pkg.MyFunc' --depth 5 --json
```

## Use with Claude Code (MCP)

gosymdb exposes a self-describing manifest (`gosymdb cli-bridge-manifest`) that the [cli-bridge plugin](https://github.com/walkindude/cli-bridge) turns into first-class MCP tools. Once set up, your agent can call `gosymdb_callers`, `gosymdb_blast-radius`, `gosymdb_implementors` etc. directly — no grep fallbacks, structured output, typed answers.

**Three steps:**

1. `go install github.com/walkindude/gosymdb@latest` (or use the installer above).
2. Inside Claude Code: `/plugin marketplace add walkindude/cli-bridge && /plugin install cli-bridge@cli-bridge`.
3. Inside Claude Code: `/cli-bridge:register gosymdb` (uses the canonical `cli-bridge-manifest` subcommand — no `--help` scraping).

Restart Claude Code and all `gosymdb_*` tools appear in the MCP tool list.

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

## Known limitations

gosymdb is a static symbolic index, not a runtime tracer. Some things it cannot know:

- **Interface dispatch is not recorded as a direct call edge.** Use `implementors` to find concrete types, then run `callers`/`callees` against the concrete method. Caller responses include a `hint` field that flags this when relevant.
- **Reflection** (`reflect.Value.Call`, method-value lookups) is not statically resolved.
- **`go:linkname`, plugin loading, and runtime dispatch** are not modeled.
- **Build tags affect what `go/packages` loads.** Symbols behind tags not active for your host won't be indexed.
- **Generated code** is indexed only when it exists on disk and is part of the loaded package set — run your generator before indexing.
- **CGO is off by default.** Pass `--cgo` to `index` when indexing CGO-dependent modules.
- **Test files are excluded by default.** Pass `--tests` to include `*_test.go` symbols and the calls they contain.
- **Query results depend on index freshness.** Check `env.stale_packages` on any response, or pass `--auto-reindex` to any query command to re-index stale modules on demand (uses a git fast-path when available).

## gosymdb and gopls

gosymdb complements [`gopls`](https://github.com/golang/tools/tree/master/gopls); it is not a replacement.

- `gopls` is the live Language Server view of a workspace — interactive, in-process, built for the IDE inner loop.
- gosymdb is a persistent SQLite query database — offline, scriptable, built for repeatable agent workflows: callers, callees, blast-radius, implementors, dead-code candidates, references, and `--json` output that diffs cleanly across runs.

If you're editing code in an IDE, use `gopls`. If an agent needs to ask structural questions across a long session without maintaining an LSP conversation, use gosymdb.

## Privacy

gosymdb runs **entirely on your machine**. It does not send your source code, symbols, or any data to external servers. There is no telemetry, no phoning home, no network access of any kind. The SQLite database stays on your local disk.

## Agent integration

gosymdb is designed for AI coding agents. Every command produces structured JSON with:

- Fully-qualified symbol names (no ambiguity)
- File paths and line numbers (jump-to-definition)
- Structured errors with `error_code`, `hint`, and `recovery` fields
- Environment envelope on every response (`env.stale_packages` always; `env.git` branch/dirty state when `GOSYMDB_ENV_GIT=1`)
- `agent-context` command for one-shot API discovery

Run `gosymdb agent-context` at the start of any agent session to get the full command reference.

### Environment variables

- `GOSYMDB_ENV_GIT=1` — populate the `env.git` block (branch, dirty count, fetch age, etc.) on every response. Off by default because it costs ~7 subprocess `git` calls per query; turn it on if your agent consumes this info.


## For library users

gosymdb is primarily a CLI. A few Go packages are publicly exported —
`store`, `store/sqlite`, `indexer` — to make it possible to plug in
alternative `store.Store` backends or drive the indexer from your own
code. They are **not** intended as a stable library API.

While gosymdb is in v0.x, these packages may change in any minor
release: signatures, types, or whole packages can be renamed, moved,
or removed. If you import them, pin an exact version and expect to
read the changelog on every bump.

The CLI surface (commands, flags, JSON output shape) is the stable
contract during v0.x; breaking changes there will bump the minor
version and be called out in release notes.

## License

Apache 2.0 — see [LICENSE](LICENSE).
