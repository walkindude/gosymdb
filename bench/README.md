# gosymdb indexing benchmark

Raw indexing throughput against the largest public Go codebases. Not a
correctness test (that's `testbench/`); this measures wall time, Go heap
churn, OS-level max RSS, and index quality (unresolved-call ratio) so we
know what we ship and can catch regressions honestly.

## Running

```bash
bench/run.sh --trials 5
node bench/report.mjs --run-id <id-printed-by-run.sh>
```

`run.sh` shallow-clones each corpus into `bench/checkouts/<name>/` on first
use (pinned to a tag in `corpora.json` so results are reproducible across
hosts and over time), pre-warms the module cache, then runs N trials per
corpus. Each trial wipes the SQLite file so we always measure a from-scratch
index build.

### Useful flags

- `--trials N` — trials per corpus (default 5). Trial 0 is treated as a
  cold-cache run and reported separately; trials 1..N-1 form the warm
  distribution.
- `--corpora name1,name2` — restrict to a subset (e.g. `--corpora terraform,istio`
  if you don't want to wait on kubernetes).
- `--cold` — attempts `sudo purge` before trial 0. If sudo isn't
  passwordless, this silently falls back to "whatever the cache happens to
  hold" and notes that in stderr. Don't lie to yourself.
- `--gosymdb <path>` — use a specific binary. Defaults to
  `<repo>/gosymdb`, rebuilding if missing.
- `--run-id <id>` — set the results subdir name (default: UTC timestamp).

## What's measured

Per trial, from the binary itself via `--bench-json` (a hidden flag that
emits a single JSON line after indexing):

- `wall_ns` — elapsed from just before the first `go/packages.Load` to just
  after the `index_meta` row is committed. Shell startup cost is *not*
  included.
- `total_alloc_bytes` — cumulative heap bytes allocated during the run
  (from `runtime.MemStats.TotalAlloc`, baselined at start).
- `heap_alloc_bytes`, `sys_bytes`, `num_gc`, `pause_total_ns`,
  `mallocs`, `frees` — end-of-run snapshot of MemStats.
- `db_size_bytes` — size of the SQLite file after `db.Close()` (so WAL is
  checkpointed into the main file).
- `symbols`, `calls`, `unresolved`, `type_refs`, `modules` — index size.

Wrapped externally by `/usr/bin/time -l` (macOS) for:

- `max_rss_bytes` — peak OS-level resident memory.
- `wall_s_outer`, `user_s`, `sys_s` — cross-check on wall time and CPU
  split.

## Current corpus

Top-of-mind picks for "biggest public Go codebase." These are chosen for
LOC, not complexity — the point is raw throughput:

| corpus | role | ref |
|---|---|---|
| kubernetes | container orchestration | v1.31.0 |
| cockroach | distributed SQL | v24.2.0 |
| moby | docker engine | v27.3.0 |
| terraform | IaC | v1.9.5 |
| istio | service mesh | 1.23.0 |

Pinned refs in `corpora.json`. Bump them deliberately and re-run the
baseline rather than drifting via `main`.

## Honest caveats

- **macOS-only wrapper.** `/usr/bin/time -l` output format differs on
  Linux. Porting is mechanical but hasn't been done yet.
- **Unresolved calls are real.** The `unresolved` column exists precisely
  because Go's call graph can't be fully resolved statically on codebases
  that lean heavily on interfaces, reflect, and generics. Kubernetes in
  particular has a meaningful unresolved ratio. That's the truth; the
  benchmark reports it, and so should any public comparison.
- **Module cache is pre-warmed.** Timings exclude `go mod download` so the
  distribution reflects steady-state indexing, not first-time setup.
- **`sudo purge` is best-effort.** If it can't run passwordless, the cold
  numbers are warm numbers with a misleading label — the runner will say so
  in stderr.
