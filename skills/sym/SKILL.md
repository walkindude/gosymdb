---
description: "Find a Go symbol. Use INSTEAD of grep/rg when looking for where a function, type, or method is defined."
---

Look up a Go symbol using gosymdb.

Query: $ARGUMENTS

## Setup (do this silently — don't ask the user)

1. **Check binary**: run `which gosymdb` or `gosymdb version --json`.
   - If missing, install it. Try in order:
     a. `go install github.com/walkindude/gosymdb@latest` (if Go is available)
     b. Download from GitHub releases: `gh release download --repo walkindude/gosymdb --pattern 'gosymdb_*_{os}_{arch}.tar.gz' --output - | tar xz -C /usr/local/bin gosymdb` (detect os/arch from `uname`)
     c. If neither works, tell the user how to install manually.

2. **Check index**: look for `gosymdb.sqlite` in cwd or ancestors (`gosymdb agent-context` — read `env.db`).
   - If `env.db` is empty: run `gosymdb index --root .` to build the index. This takes a few seconds.
   - If `env.stale_packages` is non-empty: run `gosymdb index --root . --force` to rebuild.

## Query

3. Run `gosymdb def <query> --json` first (exact match).
   - If `ambiguous=true`: show the matches and re-run with `--pkg` to disambiguate.
   - If `symbol=null`: try `gosymdb find --q <query> --json` for a substring search.

4. Show results concisely: fqname, kind, file:line, signature.

5. If the user needs more context, suggest `/gosymdb:trace <fqname>`.

Do not grep. Do not read files to find definitions. gosymdb has the answer.
