---
description: "Check what breaks before changing a Go symbol. Use BEFORE any refactor, rename, or signature change."
---

Assess the impact of changing a Go symbol using gosymdb blast-radius.

Symbol to change: $ARGUMENTS

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

3. Resolve the exact fqname: `gosymdb def <query> --json`

4. Run blast-radius: `gosymdb blast-radius --symbol <fqname> --depth 5 --json`

5. Present:
   - **Target**: fqname, file:line
   - **Total impact**: N transitive callers (N test, N non-test)
   - **Direct callers** (depth 1): list with file:line
   - **Transitive callers** (depth 2+): grouped by package
   - If `truncated=true`: note the real impact is larger.
   - If 0 callers and it's a method: check `gosymdb implementors` — interface dispatch doesn't appear in call edges.

6. Advise:
   - Small blast radius (< 10 non-test): safe to change with targeted updates.
   - Large blast radius: suggest migration strategy or deprecation path.
   - Always list test files that cover the symbol.

Do not grep for callers. gosymdb has the complete call graph.
