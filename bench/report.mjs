#!/usr/bin/env node
// bench/report.mjs — aggregate per-trial JSON into a summary.
// Usage: node bench/report.mjs --run-id <id> [--json]
//
// Reports mean / stddev / median / p95 for wall_ns, total_alloc_bytes,
// max_rss_bytes across trials 1..N-1 (trial 0 is the cold run, reported
// separately).

import fs from 'node:fs';
import path from 'node:path';
import url from 'node:url';

const HERE = path.dirname(url.fileURLToPath(import.meta.url));
const RESULTS = path.join(HERE, 'results');

function parseArgs(argv) {
  const out = { runId: null, json: false };
  for (let i = 2; i < argv.length; i++) {
    const a = argv[i];
    if (a === '--run-id') out.runId = argv[++i];
    else if (a === '--json') out.json = true;
    else {
      console.error(`unknown arg: ${a}`);
      process.exit(2);
    }
  }
  return out;
}

function stats(arr) {
  if (arr.length === 0) return null;
  const sorted = [...arr].sort((a, b) => a - b);
  const mean = arr.reduce((s, x) => s + x, 0) / arr.length;
  const variance = arr.reduce((s, x) => s + (x - mean) ** 2, 0) / arr.length;
  const stddev = Math.sqrt(variance);
  const median = sorted[Math.floor(sorted.length / 2)];
  const p95 = sorted[Math.min(sorted.length - 1, Math.floor(sorted.length * 0.95))];
  const min = sorted[0];
  const max = sorted[sorted.length - 1];
  return { n: arr.length, mean, stddev, median, p95, min, max };
}

function fmtMs(ns) {
  return (ns / 1e6).toFixed(0) + 'ms';
}
function fmtMB(b) {
  return (b / 1024 / 1024).toFixed(1) + 'MB';
}
function fmtPct(n, d) {
  if (d === 0) return 'n/a';
  return ((n / d) * 100).toFixed(1) + '%';
}

const { runId, json: emitJSON } = parseArgs(process.argv);
if (!runId) {
  console.error('required: --run-id <id>');
  process.exit(2);
}

const runDir = path.join(RESULTS, runId);
if (!fs.existsSync(runDir)) {
  console.error(`no such run: ${runDir}`);
  process.exit(1);
}

const meta = JSON.parse(fs.readFileSync(path.join(runDir, 'meta.json'), 'utf8'));
const corpora = JSON.parse(fs.readFileSync(path.join(runDir, 'corpora.selected.json'), 'utf8'));

const report = { run_id: runId, meta, corpora: [] };

for (const c of corpora) {
  const corpusDir = path.join(runDir, c.name);
  if (!fs.existsSync(corpusDir)) continue;
  const trials = fs
    .readdirSync(corpusDir)
    .filter((f) => f.startsWith('trial-') && f.endsWith('.json'))
    .sort()
    .map((f) => JSON.parse(fs.readFileSync(path.join(corpusDir, f), 'utf8')));

  const ok = trials.filter((t) => !t.failed);
  const cold = ok[0];
  const warm = ok.slice(1);

  const warmStats = {
    wall_ns: stats(warm.map((t) => t.wall_ns)),
    total_alloc_bytes: stats(warm.map((t) => t.total_alloc_bytes)),
    max_rss_bytes: stats(warm.map((t) => t.max_rss_bytes).filter((x) => x > 0)),
    num_gc: stats(warm.map((t) => t.num_gc)),
  };

  const reprSample = ok[ok.length - 1] ?? cold;

  report.corpora.push({
    name: c.name,
    ref: c.ref,
    trials_ok: ok.length,
    trials_failed: trials.length - ok.length,
    quality: reprSample
      ? {
          modules: reprSample.modules,
          symbols: reprSample.symbols,
          calls: reprSample.calls,
          unresolved: reprSample.unresolved,
          unresolved_ratio:
            reprSample.calls > 0 ? reprSample.unresolved / (reprSample.calls + reprSample.unresolved) : 0,
          type_refs: reprSample.type_refs,
          db_size_bytes: reprSample.db_size_bytes,
        }
      : null,
    cold: cold
      ? {
          wall_ns: cold.wall_ns,
          total_alloc_bytes: cold.total_alloc_bytes,
          max_rss_bytes: cold.max_rss_bytes,
        }
      : null,
    warm: warmStats,
  });
}

if (emitJSON) {
  process.stdout.write(JSON.stringify(report, null, 2) + '\n');
  process.exit(0);
}

// Human-readable report.
console.log(`# gosymdb indexing benchmark — run ${runId}`);
console.log();
console.log(`tool: ${meta.tool_version}   go: ${meta.go_version}`);
console.log(`host: ${meta.hw_model ?? '?'}   ${meta.cpu_brand ?? '?'}`);
if (meta.cpu_logical_cores > 0) {
  const coreBits = [
    `${meta.cpu_logical_cores} logical`,
    meta.cpu_performance_cores > 0 ? `${meta.cpu_performance_cores}P + ${meta.cpu_efficiency_cores}E` : null,
  ].filter(Boolean).join(' / ');
  const ram = meta.ram_bytes > 0 ? `${(meta.ram_bytes / 1024 / 1024 / 1024).toFixed(0)} GB RAM` : '';
  console.log(`cores: ${coreBits}   ${ram}   os: ${meta.host_os} ${meta.os_version ?? ''}`);
}
console.log(`trials/corpus: ${meta.trials}   cold-mode: ${meta.cold ? 'yes' : 'no'}`);
console.log();

console.log('## Throughput (warm trials; trial 0 excluded)');
console.log();
console.log('corpus       n  wall mean±stddev        median       p95          alloc (mean)    maxRSS (mean)');
console.log('-----------  -  --------------------   ----------   ----------   -------------   -------------');
for (const c of report.corpora) {
  const w = c.warm;
  if (!w.wall_ns) {
    console.log(`${c.name.padEnd(11)}  0  (no warm trials)`);
    continue;
  }
  const name = c.name.padEnd(11);
  const n = String(w.wall_ns.n).padStart(1);
  const walls = `${fmtMs(w.wall_ns.mean)}±${fmtMs(w.wall_ns.stddev)}`.padEnd(20);
  const med = fmtMs(w.wall_ns.median).padEnd(10);
  const p95 = fmtMs(w.wall_ns.p95).padEnd(10);
  const alloc = fmtMB(w.total_alloc_bytes.mean).padEnd(13);
  const rss = w.max_rss_bytes ? fmtMB(w.max_rss_bytes.mean) : 'n/a';
  console.log(`${name}  ${n}  ${walls}   ${med}   ${p95}   ${alloc}   ${rss}`);
}

console.log();
console.log('## Cold run (trial 0)');
console.log();
console.log('corpus       wall          alloc         maxRSS');
console.log('-----------  -----------   -----------   -----------');
for (const c of report.corpora) {
  if (!c.cold) continue;
  const name = c.name.padEnd(11);
  const wall = fmtMs(c.cold.wall_ns).padEnd(11);
  const alloc = fmtMB(c.cold.total_alloc_bytes).padEnd(11);
  const rss = c.cold.max_rss_bytes > 0 ? fmtMB(c.cold.max_rss_bytes) : 'n/a';
  console.log(`${name}  ${wall}   ${alloc}   ${rss}`);
}

console.log();
console.log('## Index quality (last trial)');
console.log();
console.log('corpus       modules  symbols    calls       unresolved  unres%   type_refs   db size');
console.log('-----------  -------  --------   ---------   ---------   ------   ---------   ---------');
for (const c of report.corpora) {
  const q = c.quality;
  if (!q) continue;
  const name = c.name.padEnd(11);
  const mods = String(q.modules).padStart(7);
  const syms = String(q.symbols).padStart(8);
  const calls = String(q.calls).padStart(9);
  const unres = String(q.unresolved).padStart(9);
  const unresPct = fmtPct(q.unresolved, q.calls + q.unresolved).padStart(6);
  const refs = String(q.type_refs).padStart(9);
  const db = fmtMB(q.db_size_bytes).padStart(9);
  console.log(`${name}  ${mods}  ${syms}   ${calls}   ${unres}   ${unresPct}   ${refs}   ${db}`);
}
console.log();
