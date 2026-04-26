# Working on gosymdb

This is the gosymdb repo. The binary built here is the typed Go symbol index, and running it against this codebase is the canonical way to navigate, since the schema and command surface match the source you're editing.

## What gosymdb does for navigation

gosymdb resolves symbols via Go's `go/packages` and `go/types` (the loader and typechecker the compiler uses), not by string matching. For Go-semantic questions ("who calls X", "what implements Y", "what breaks if I change Z") it returns concrete answers with file paths, line numbers, and fully-qualified names. Grep finds string occurrences; gosymdb finds resolved symbols. The two answer different questions, and either is the right tool depending on what's being asked.

## Session-start bootstrap

```bash
gosymdb agent-context
```

`agent-context` always emits JSON with the full command reference plus an `env` block (cwd, db path, stale_packages, git state). Reading it once at the top of a session removes the need for `--help` calls later.

If `env.db` is empty, no database has been built. The path that fixes this:

```bash
gosymdb index --root . --db gosymdb.sqlite
gosymdb agent-context   # env.db now populated
```

After that, subsequent commands auto-discover the database from cwd or its ancestors; an explicit `--db` flag is only needed for non-default paths.

## Navigation tasks and the corresponding command

| Task | Command |
|------|---------|
| Where is this symbol defined? | `gosymdb def FuncName --json` |
| What calls this function? | `gosymdb callers --symbol <fqname> --json` |
| What does this function call? | `gosymdb callees --symbol <fqname> --json` |
| What's in this file? | `gosymdb find --file <path> --json` |
| What's in this package? | `gosymdb find --pkg <pkg> --json` |
| List all symbols in the DB | `gosymdb find --json` (up to `--limit`) |
| What implements this interface? | `gosymdb implementors --iface <name> --json` |
| What breaks if I change this? | `gosymdb blast-radius --symbol <fqname> --json` |
| List all packages | `gosymdb packages --json` |
| Index quality report | `gosymdb health --json` |
| Dead code candidates | `gosymdb dead --pkg <prefix> --json` |
| Where is this type used? | `gosymdb references --symbol <name> --json` |

`--json` is supported on every query command; the JSON shape is the contract, while the text output is informal and unstable. `agent-context` emits JSON unconditionally and ignores the flag.

## Resolving fqnames

Most commands take a fully-qualified name (`pkg/path.Symbol` or `pkg/path.*Type.Method`). Two reliable ways to obtain one:

```bash
gosymdb find --q <name> --json   # candidates with their fqname, kind, file, line
gosymdb def <name> --json        # exact single-symbol lookup
```

The `fqname` field from those responses is what other commands accept as `--symbol`. Hand-constructed fqnames frequently miss the package path or method-receiver formatting, so the find/def output is the reliable source.

## A typical exploration sequence

```bash
# 1. Find candidates by name.
gosymdb find --q MyThing --json

# 2. Pin down a specific symbol.
gosymdb def MyThing --json

# 3. Look outward (callers) and inward (callees).
gosymdb callers --symbol <fqname> --json
gosymdb callees --symbol <fqname> --json

# 4. If it's an interface, find concrete implementors.
gosymdb implementors --iface MyThing --json

# 5. Before changing it, blast radius surfaces the transitive callers.
gosymdb blast-radius --symbol <fqname> --depth 5 --json
```

For symbol-shaped questions, this sequence is faster and more accurate than reading source files; for code-shape, control-flow, or comment-context questions, source reading remains the right tool.

## Common failure modes

**`callers` returns 0 for an interface method.** Interface dispatch isn't recorded as a call edge — only direct calls are. The `hint` field in the response flags this when relevant. The recovery is `implementors --iface <name>` to find concrete types, then `callers` against the concrete method.

**`error_code: no_database`.** Auto-discovery found no SQLite file in cwd or its ancestors. Either `cd` into the project root (where `gosymdb.sqlite` lives) or build the index with `gosymdb index --root . --db gosymdb.sqlite`.

**`error_code: unknown_command_or_flag`.** The response carries a `valid_subcommands` list. `agent-context` returns the current authoritative command surface, which catches drift between memory and the installed binary.

**`dead` reports a symbol you know is called.** It may be reached via interface dispatch (no call edge recorded), `reflect`, goroutine/defer indirection, or `go:linkname`. The `note` field in the response explains. Running `callers` against the symbol confirms reachability before any deletion.

**`find` or `callers` returns too many results.** `--pkg <prefix>` restricts to a package prefix. `packages --json` lists the available prefixes if the right one isn't obvious.

## Stale-index detection

Every JSON response carries `env.stale_packages`. When non-empty, the indexed source has drifted from disk. Two recovery shapes:

- Rebuild explicitly: `gosymdb index --root . --force`.
- Pass `--auto-reindex` on a query, which runs a stale check (git fast-path with file-hash fallback, sub-second on Kubernetes-scale repos) and reindexes affected modules in place before the query runs. Cheap enough to pass on every call.

## Keeping this file current

If a failure mode comes up that this file doesn't already describe, adding it under "Common failure modes" with the response field that signals it keeps the doc reliable. New navigation patterns belong in the table above. Renamed flags or new commands should land here at the same time the change lands in code, since this file is the contract this repo expects from itself.
