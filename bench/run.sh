#!/usr/bin/env bash
# bench/run.sh — raw-indexing-throughput harness for gosymdb.
#
# Usage:
#   bench/run.sh [--trials N] [--corpora name1,name2,...] [--cold]
#                [--gosymdb /path/to/gosymdb] [--run-id <id>]
#
# For each corpus × trial, wipes the DB and runs `gosymdb index --bench-json`
# wrapped in `/usr/bin/time -l` (macOS) so we capture both in-process Go
# memstats AND the OS-level max RSS. Writes per-trial JSON to
# bench/results/<run-id>/<corpus>/trial-<N>.json.
#
# Trial 0 is reported as a "cold" run (module cache may be cold, filesystem
# cache may be cold) and is not aggregated into the warm distribution.
# Run `bench/report.mjs --run-id <id>` afterwards to aggregate.
#
# Requires: git, go, /usr/bin/time, jq.

set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$HERE/.." && pwd)"
CHECKOUTS="$HERE/checkouts"
RESULTS="$HERE/results"

TRIALS=5
CORPORA_FILTER=""
COLD=0
GOSYMDB_BIN=""
RUN_ID=""
CORPORA_FILE=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --trials) TRIALS="$2"; shift 2 ;;
    --corpora) CORPORA_FILTER="$2"; shift 2 ;;
    --corpora-file) CORPORA_FILE="$2"; shift 2 ;;
    --cold) COLD=1; shift ;;
    --gosymdb) GOSYMDB_BIN="$2"; shift 2 ;;
    --run-id) RUN_ID="$2"; shift 2 ;;
    -h|--help) sed -n '1,30p' "$0"; exit 0 ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
done

if [[ -z "$GOSYMDB_BIN" ]]; then
  # Prefer a freshly-built dev binary over whatever's on PATH.
  GOSYMDB_BIN="$REPO_ROOT/gosymdb"
  if [[ ! -x "$GOSYMDB_BIN" ]]; then
    echo "building gosymdb..." >&2
    ( cd "$REPO_ROOT" && go build -o gosymdb ./ )
  fi
fi

if [[ ! -x "$GOSYMDB_BIN" ]]; then
  echo "gosymdb binary not found or not executable: $GOSYMDB_BIN" >&2
  exit 1
fi

# Probe that the binary supports --bench-json (hidden but present).
if ! "$GOSYMDB_BIN" index --bench-json --help >/dev/null 2>&1; then
  : # --help exits 0 regardless; real probe below
fi

if [[ -z "$RUN_ID" ]]; then
  RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)"
fi

RUN_DIR="$RESULTS/$RUN_ID"
mkdir -p "$RUN_DIR"
mkdir -p "$CHECKOUTS"

TOOL_VERSION="$("$GOSYMDB_BIN" version 2>/dev/null | head -1 || echo unknown)"
GO_VERSION="$(go version | awk '{print $3}')"
HOST_OS="$(uname -s)"
HOST_ARCH="$(uname -m)"

# Hardware fingerprint (macOS specifics; values are "unknown"/-1 on other OSes).
CPU_BRAND="$(sysctl -n machdep.cpu.brand_string 2>/dev/null || echo unknown)"
CPU_PHYS="$(sysctl -n hw.physicalcpu 2>/dev/null || echo -1)"
CPU_LOGICAL="$(sysctl -n hw.logicalcpu 2>/dev/null || echo -1)"
CPU_PERF_CORES="$(sysctl -n hw.perflevel0.logicalcpu 2>/dev/null || echo -1)"
CPU_EFF_CORES="$(sysctl -n hw.perflevel1.logicalcpu 2>/dev/null || echo -1)"
RAM_BYTES="$(sysctl -n hw.memsize 2>/dev/null || echo -1)"
HW_MODEL="$(sysctl -n hw.model 2>/dev/null || echo unknown)"
OS_VERSION="$(sw_vers -productVersion 2>/dev/null || uname -r)"

cat > "$RUN_DIR/meta.json" <<EOF
{
  "run_id": "$RUN_ID",
  "trials": $TRIALS,
  "cold": $COLD,
  "tool_version": "$TOOL_VERSION",
  "go_version": "$GO_VERSION",
  "host_os": "$HOST_OS",
  "host_arch": "$HOST_ARCH",
  "os_version": "$OS_VERSION",
  "hw_model": "$HW_MODEL",
  "cpu_brand": "$CPU_BRAND",
  "cpu_physical_cores": $CPU_PHYS,
  "cpu_logical_cores": $CPU_LOGICAL,
  "cpu_performance_cores": $CPU_PERF_CORES,
  "cpu_efficiency_cores": $CPU_EFF_CORES,
  "ram_bytes": $RAM_BYTES,
  "started_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
}
EOF

echo "run: $RUN_ID" >&2
echo "trials/corpus: $TRIALS  cold: $COLD  gosymdb: $GOSYMDB_BIN ($TOOL_VERSION)" >&2

# Select corpora.
CORPORA_JSON="${CORPORA_FILE:-$HERE/corpora.json}"
if [[ -n "$CORPORA_FILTER" ]]; then
  FILTER="$CORPORA_FILTER" python3 -c '
import json, os
with open(os.environ["CORPORA_JSON"]) as f: data = json.load(f)
wanted = set(os.environ["FILTER"].split(","))
out = [c for c in data if c["name"] in wanted]
print(json.dumps(out))
' > "$RUN_DIR/corpora.selected.json"
else
  cp "$CORPORA_JSON" "$RUN_DIR/corpora.selected.json"
fi

# Sync checkouts. Shallow clone to the pinned ref.
CORPORA_JSON="$RUN_DIR/corpora.selected.json" CHECKOUTS="$CHECKOUTS" python3 - <<'PY' > "$RUN_DIR/_sync.sh"
import json, os, shlex
with open(os.environ["CORPORA_JSON"]) as f: corpora = json.load(f)
for c in corpora:
    name = c["name"]; repo = c["repo"]; ref = c["ref"]
    dest = os.path.join(os.environ.get("CHECKOUTS", ""), name)
    if repo == "skip":
        print(f'echo "{name}: skip clone (local target)" >&2')
        continue
    print(f'if [ ! -e {shlex.quote(dest)}/.git ]; then')
    print(f'  echo "cloning {name} @ {ref}..." >&2')
    print(f'  git clone --depth 1 --branch {shlex.quote(ref)} {shlex.quote(repo)} {shlex.quote(dest)}')
    print(f'else')
    print(f'  echo "{name} already cloned, skipping" >&2')
    print(f'fi')
PY
CHECKOUTS="$CHECKOUTS" bash "$RUN_DIR/_sync.sh"
rm "$RUN_DIR/_sync.sh"

maybe_purge() {
  if [[ $COLD -eq 1 ]]; then
    if sudo -n true 2>/dev/null; then
      echo "  purging filesystem cache..." >&2
      sudo purge
    else
      echo "  --cold requested but sudo is not passwordless; skipping purge (numbers will be warm)" >&2
    fi
  fi
}

# For each corpus, run N trials. Trial 0 is cold; trials 1..N-1 are warm.
while read -r CORPUS; do
  NAME="$(echo "$CORPUS" | jq -r .name)"
  REF="$(echo "$CORPUS" | jq -r .ref)"
  CORPUS_DIR="$CHECKOUTS/$NAME"
  CORPUS_RESULTS="$RUN_DIR/$NAME"
  mkdir -p "$CORPUS_RESULTS"

  echo "=== $NAME @ $REF ===" >&2

  # Pre-warm module cache once, so trial timings don't include go mod download.
  # Errors are non-fatal; some corpora have weird module setups.
  echo "  pre-warming module cache..." >&2
  ( cd "$CORPUS_DIR" && go mod download ./... 2>/dev/null ) || true

  for ((t=0; t<TRIALS; t++)); do
    TRIAL_DB="/tmp/gosymdb-bench-$NAME-$t.sqlite"
    TRIAL_OUT="$CORPUS_RESULTS/trial-$t.json"
    TRIAL_TIME="$CORPUS_RESULTS/trial-$t.time"
    rm -f "$TRIAL_DB" "$TRIAL_DB-wal" "$TRIAL_DB-shm"

    if [[ $t -eq 0 ]]; then
      maybe_purge
    fi

    echo "  trial $t..." >&2
    # /usr/bin/time -l on macOS prints resource stats to stderr.
    /usr/bin/time -l "$GOSYMDB_BIN" index \
      --root "$CORPUS_DIR" \
      --db "$TRIAL_DB" \
      --bench-json \
      > "$TRIAL_OUT.raw" 2> "$TRIAL_TIME" || {
        echo "  trial $t FAILED; see $TRIAL_TIME" >&2
        echo '{"failed":true}' > "$TRIAL_OUT"
        continue
      }

    # Extract the final line of stdout (the bench JSON).
    tail -n 1 "$TRIAL_OUT.raw" > "$TRIAL_OUT.benchjson"

    # Parse /usr/bin/time -l output.
    MAX_RSS="$(grep -E 'maximum resident set size' "$TRIAL_TIME" | awk '{print $1}' || echo -1)"
    REAL_S="$(grep -E ' *[0-9.]+ +real' "$TRIAL_TIME" | awk '{print $1}' || echo -1)"
    USER_S="$(grep -E ' *[0-9.]+ +user' "$TRIAL_TIME" | awk '{print $1}' || echo -1)"
    SYS_S="$(grep -E ' *[0-9.]+ +sys' "$TRIAL_TIME" | awk '{print $1}' || echo -1)"

    jq --arg trial "$t" \
       --arg corpus "$NAME" \
       --arg ref "$REF" \
       --argjson maxrss "${MAX_RSS:-0}" \
       --argjson real "${REAL_S:-0}" \
       --argjson userT "${USER_S:-0}" \
       --argjson sysT "${SYS_S:-0}" \
       '. + {trial:($trial|tonumber), corpus:$corpus, corpus_ref:$ref, max_rss_bytes:$maxrss, wall_s_outer:$real, user_s:$userT, sys_s:$sysT}' \
       "$TRIAL_OUT.benchjson" > "$TRIAL_OUT"
    rm "$TRIAL_OUT.raw" "$TRIAL_OUT.benchjson"

    WALL_MS="$(jq -r '.wall_ns / 1e6 | floor' "$TRIAL_OUT")"
    SYMS="$(jq -r '.symbols' "$TRIAL_OUT")"
    UNRES="$(jq -r '.unresolved' "$TRIAL_OUT")"
    RSS_MB="$(awk -v b="$MAX_RSS" 'BEGIN { printf "%.0f", b/1024/1024 }')"
    echo "    ${WALL_MS}ms  syms=${SYMS}  unresolved=${UNRES}  maxRSS=${RSS_MB}MB" >&2
  done
done < <(jq -c '.[]' "$RUN_DIR/corpora.selected.json")

echo "" >&2
echo "all done. aggregate with:" >&2
echo "  node $HERE/report.mjs --run-id $RUN_ID" >&2
