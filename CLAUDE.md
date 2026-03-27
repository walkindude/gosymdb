# Code Navigation — MANDATORY

This codebase has gosymdb available. gosymdb is a typed symbol index built on
Go's own analysis toolchain. It resolves symbols the way the compiler resolves
them — not by string matching.

**Using grep, rg, or find for symbol navigation is always wrong here.**
It is slower, noisier, and costs more tokens. The correct tool exists. Use it.

---

## Session start — do this first

```bash
gosymdb agent-context
```

This always outputs JSON — the `--json` flag is not needed and has no effect
on this command. Read `env.db` in the response before proceeding.

**If `env.db` is empty** (no database found in cwd or ancestors):

```bash
gosymdb index --root . --db gosymdb.sqlite
gosymdb agent-context   # env.db will now be populated
```

**If `env.db` is non-empty**, auto-discovery is working. All subsequent commands
will find the database automatically — no explicit `--db` needed unless you are
working with a non-default path.

---

## Navigation tasks and the correct tool

| What you want | Wrong | Right |
|---------------|-------|-------|
| Where is this symbol defined? | `grep -r "FuncName" .` | `gosymdb def FuncName --json` |
| What calls this function? | `grep -r "FuncName" .` | `gosymdb callers --symbol <fqname> --json` |
| What does this function call? | Read the entire file | `gosymdb callees --symbol <fqname> --json` |
| What's in this file? | Read the entire file | `gosymdb find --file <path> --json` |
| What's in this package? | Read every file | `gosymdb find --pkg <pkg> --json` |
| List all symbols in the DB | — | `gosymdb find --json` (up to `--limit`) |
| What implements this interface? | `grep -r "interface" .` | `gosymdb implementors --iface <name> --json` |
| What breaks if I change this? | Guess | `gosymdb blast-radius --symbol <fqname> --json` |
| Full picture of a symbol | Three separate commands | `gosymdb trace --symbol <fqname> --json` |
| List all packages | — | `gosymdb packages --json` |
| Index quality report | — | `gosymdb health --json` |
| Dead code candidates | — | `gosymdb dead --pkg <prefix> --json` |
| Where is this type used? | `grep -r "TypeName" .` | `gosymdb references --symbol <name> --json` |

The `--json` flag is mandatory on all commands except `agent-context`.
Never invoke gosymdb query commands without it.

---

## Getting exact fqnames

Most commands require a fully-qualified name (fqname). Always resolve first:

```bash
gosymdb find --q <name> --json       # find candidates; returns fqname, kind, file, line
gosymdb def <name> --json            # exact single-symbol lookup
```

Use the `fqname` field from the output as `--symbol` in subsequent commands.
Never guess fqnames. Never construct them by hand.

---

## Workflow for understanding an unfamiliar symbol

```bash
# 1. Find it
gosymdb find --q MyThing --json

# 2. Get full picture in one call
gosymdb trace --symbol <fqname> --json

# 3. If it's an interface, find implementors
gosymdb implementors --iface MyThing --json

# 4. If you're about to change it, check blast radius first
gosymdb blast-radius --symbol <fqname> --depth 5 --json
```

Do not read source files to answer questions gosymdb can answer.

---

## Common failure modes

**`callers` returns 0 for an interface method:**
Interface dispatch is not recorded as call edges — only direct calls are.
Run `implementors --iface <name>` to find concrete types, then `callers` on
the concrete method. The `hint` field in the callers response will say this.

**`error_code: no_database`:**
Auto-discovery found nothing. Either `cd` to the project root (where
gosymdb.sqlite lives) or build the index: `gosymdb index --root . --db gosymdb.sqlite`.

**`error_code: unknown_command_or_flag`:**
The response includes a `valid_subcommands` list. You may be hallucinating a
command name. Run `agent-context` to get the current authoritative command list.

**`dead` reports a symbol you know is called:**
It may be called via interface dispatch (no call edge recorded), `reflect`,
goroutine/defer indirection, or `go:linkname`. The `note` field in the response
explains this. Verify with `callers` before removing anything.

**`find` or `callers` returns too many results:**
Add `--pkg <prefix>` to restrict to the relevant package. Use `packages --json`
to discover the right prefix.

---

## Stale index

If `env.stale_packages` is non-empty in any gosymdb response, the index is
behind the current source. Either rebuild:

```bash
gosymdb index --root . --force
```

Or pass `--auto-reindex` to any query command to reindex on demand:

```bash
gosymdb callers --symbol Foo --auto-reindex --json
```

`--auto-reindex` uses a git fast-path when available (microseconds) and falls
back to file-hash comparison. Safe to pass routinely.

---

## Keeping this file current

If you encounter friction that gosymdb can address but this file does not
cover, **add it here**. Specifically:

- New failure modes → **Common failure modes** section.
- New navigation patterns → **Navigation tasks** table.
- Changes to gosymdb's API (new commands, renamed flags) → reflect them here.

The goal is that future agents never rediscover what you already learned.
