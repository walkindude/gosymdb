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

Requires Go 1.26+ on your machine, since this path builds gosymdb from source. The other install methods in this section (Homebrew, scripted installer, Nix, Linux packages) ship pre-compiled binaries and have no Go dependency at install or runtime.

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

gosymdb exposes a self-describing manifest (`gosymdb cli-bridge-manifest`) that the [cli-bridge plugin](https://github.com/walkindude/cli-bridge) turns into first-class MCP tools. Once set up, agents can call `gosymdb_callers`, `gosymdb_blast-radius`, `gosymdb_implementors` etc. as MCP tools — typed inputs, structured outputs, no `grep` fallback.

This is the recommended path for agentic use. Agents lose track of CLI tools mentioned only in `CLAUDE.md` once the conversation gets long; MCP tools live in the agent's tool registry and don't decay under context pressure. (For background on why, see [cli-bridge's "The problem" section](https://github.com/walkindude/cli-bridge#the-problem).)

**Three steps:**

1. `go install github.com/walkindude/gosymdb@latest` (or use the installer above; install.sh auto-generates the cli-bridge spec when Claude Code is detected, in which case skip step 3).
2. Install cli-bridge — either as a plugin (`/plugin marketplace add walkindude/cli-bridge && /plugin install cli-bridge@cli-bridge`) or standalone (`npm i -g cli-bridge && claude mcp add cli-bridge -s user -- cli-bridge`).
3. Inside Claude Code: `/cli-bridge:register gosymdb` (uses the canonical `cli-bridge-manifest` subcommand — no `--help` scraping).

Restart Claude Code and all `gosymdb_*` tools appear in the MCP tool list. After upgrading the gosymdb binary, cli-bridge [auto-refreshes](https://github.com/walkindude/cli-bridge/blob/master/AGENTS.md#the-manifest-convention) the spec on its next startup — no manual re-register.

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

## Freshness loop

The index goes stale the moment you edit a file. gosymdb closes the loop on its own.

Every JSON response carries an `env` envelope that always includes `env.stale_packages` — the list of packages whose source has drifted from what's indexed. The agent sees it on every query, regardless of the command:

```bash
$ gosymdb find --q Store --json | jq '.env.stale_packages'
["github.com/you/repo/store"]
```

Pass `--auto-reindex` to any query command and stale modules get re-indexed in place before the query runs. The staleness check itself is cheap enough to run on every call: a git fast-path uses `git diff` against the commit stamped at index time (one subprocess for the whole repo, sub-second on Kubernetes-scale codebases), with a per-file FNV-64a hash fallback for when git isn't usable.

```bash
gosymdb callers --symbol 'github.com/you/repo/store.Open' --auto-reindex --json
```

The shipped [`CLAUDE.md`](./CLAUDE.md) wires this up for agents: watch `env.stale_packages`, rebuild or pass `--auto-reindex`. A silent failure mode (querying a stale index) becomes a self-correcting feedback loop the agent doesn't have to be reminded about.

## Known limitations

gosymdb is a static symbolic index, not a runtime tracer. Some things it cannot know:

- **Interface dispatch is not recorded as a direct call edge.** Use `implementors` to find concrete types, then run `callers`/`callees` against the concrete method. Caller responses include a `hint` field that flags this when relevant.
- **Reflection** (`reflect.Value.Call`, method-value lookups) is not statically resolved.
- **`go:linkname`, plugin loading, and runtime dispatch** are not modeled.
- **Build tags affect what `go/packages` loads.** Symbols behind tags not active for your host won't be indexed.
- **Generated code** is indexed only when it exists on disk and is part of the loaded package set — run your generator before indexing.
- **CGO is off by default.** Pass `--cgo` to `index` when indexing CGO-dependent modules.
- **Test files are excluded by default.** Pass `--tests` to include `*_test.go` symbols and the calls they contain.
- **Query results depend on index freshness.** See [Freshness loop](#freshness-loop) — `env.stale_packages` flags drift on every response, and `--auto-reindex` self-heals before the query runs.

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


## API stability

During v0.x, the supported contract is:

- **Command names** — `index`, `find`, `def`, `callers`, `callees`, `blast-radius`, `dead`, `implementors`, `references`, `packages`, `health`, `agent-context`, `cli-bridge-manifest`, `version`.
- **Flag names and types** — both command-local flags and the global `--db`, `--json`, `--auto-reindex`.
- **JSON output shape** — field names, nesting, and error envelopes (`error_code`, `hint`, `recovery`) as emitted when `--json` is set.
- **`env` block** on every JSON response: always `env.db` and `env.stale_packages`; `env.git` when `GOSYMDB_ENV_GIT=1`.

Breaking changes to any of the above bump the minor version and are called out in release notes. Additive changes (new commands, new fields, new optional flags) are non-breaking and can land in patch releases. Tests consuming gosymdb's JSON should be resilient to unknown fields.

## For library users

gosymdb is primarily a CLI. A few Go packages are publicly exported — `store`, `store/sqlite`, `indexer` — to make it possible to plug in alternative `store.Store` backends or drive the indexer from your own code. They are **not** intended as a stable library API.

While gosymdb is in v0.x, these packages may change in any minor release: signatures, types, or whole packages can be renamed, moved, or removed. If you import them, pin an exact version and expect to read the changelog on every bump.

## License

Apache 2.0 — see [LICENSE](LICENSE).
