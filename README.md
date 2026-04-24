# gosymdb

[![CI](https://github.com/walkindude/gosymdb/actions/workflows/ci.yml/badge.svg)](https://github.com/walkindude/gosymdb/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/walkindude/gosymdb)](https://goreportcard.com/report/github.com/walkindude/gosymdb)
[![Go Reference](https://pkg.go.dev/badge/github.com/walkindude/gosymdb.svg)](https://pkg.go.dev/github.com/walkindude/gosymdb)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](./LICENSE)
[![MCP](https://img.shields.io/badge/MCP-compatible-8A2BE2)](https://modelcontextprotocol.io)

A Go symbol and call-graph database backed by SQLite. Index any Go module and query symbols, callers, callees, blast radius, dead code, interface implementors, and type references — all with structured JSON output designed for programmatic consumption.

## Install

**One command** (macOS/Linux):

```bash
curl -sSL https://raw.githubusercontent.com/walkindude/gosymdb/master/install.sh | sh
```

Windows (PowerShell):

```powershell
iwr -useb https://raw.githubusercontent.com/walkindude/gosymdb/master/install.ps1 | iex
```

The installer downloads the binary, verifies its checksum, and — if Claude Code is installed — generates the [cli-bridge](https://github.com/walkindude/cli-bridge) spec so gosymdb registers as a first-class MCP tool (see below).

Or with the Go toolchain:

```bash
go install github.com/walkindude/gosymdb@latest
```

Or with [Nix](https://nixos.org/) flakes:

```bash
nix profile install github:walkindude/gosymdb
# or for a one-off run:
nix run github:walkindude/gosymdb -- --help
# or drop into a dev shell with go + gopls:
nix develop github:walkindude/gosymdb
```

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


## License

Apache 2.0 — see [LICENSE](LICENSE).
