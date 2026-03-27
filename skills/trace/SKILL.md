---
description: "Full profile of a Go symbol — definition, callers, callees, blast radius in one call. Use BEFORE reading source files."
---

Trace a Go symbol using gosymdb. This replaces reading multiple files to understand what something does, what calls it, and what it calls.

Symbol: $ARGUMENTS

## Setup (do this silently — don't ask the user)

1. **Check binary**: run `which gosymdb` or `gosymdb version --json`.
   - If missing, install it. Try in order:
     a. `go install github.com/walkindude/gosymdb@latest` (if Go is available)
     b. Download from GitHub releases: `gh release download --repo walkindude/gosymdb --pattern 'gosymdb_*_{os}_{arch}.tar.gz' --output - | tar xz -C /usr/local/bin gosymdb` (detect os/arch from `uname`)
     c. If neither works, tell the user how to install manually.

2. **Check index**: `gosymdb agent-context` — read `env.db`.
   - If `env.db` is empty: run `gosymdb index --root .` to build the index.
   - If `env.stale_packages` is non-empty: run `gosymdb index --root . --force` to rebuild.

## Query

3. Run `gosymdb trace --symbol <query> --json`.
   - If the symbol isn't found, the `hint` field will suggest alternatives.
   - If ambiguous, add `--pkg <prefix>` to disambiguate.

4. Present the results as a structured summary:
   - **Definition**: kind, file:line, signature
   - **Callers** (N): list with file:line
   - **Callees** (N): list (group by package if many)
   - **Blast radius**: total transitive callers, max depth reached
   - If `callers_count=0` and kind is a method, check if it's an interface method — suggest `gosymdb implementors --iface <type> --json`.

5. Only THEN read the source file if the user needs to see the actual code.

Do not grep. Do not read files to answer questions gosymdb can answer.
