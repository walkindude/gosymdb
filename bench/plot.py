"""Render benchmark plots for bench/results/launch-baseline/.

Reads per-trial JSON + sloc.json, aggregates warm trials (1..N-1) by median,
writes PNGs into bench/results/launch-baseline/plots/.

Run with: /tmp/bench-plots-venv/bin/python3 bench/plot.py
"""

import json
import statistics
from pathlib import Path

import matplotlib.pyplot as plt
import numpy as np


REPO = Path(__file__).resolve().parents[1]
RUN_DIR = REPO / "bench" / "results" / "launch-baseline"
PLOTS_DIR = RUN_DIR / "plots"
PLOTS_DIR.mkdir(parents=True, exist_ok=True)

# Corpus order (roughly by size) and colors — consistent across all panels so the
# eye can track a given corpus between subplots.
CORPORA = ["kubernetes", "cockroach", "terraform", "istio"]
COLORS = {
    "kubernetes": "#326CE5",  # k8s blue
    "cockroach":  "#6933FF",  # crdb purple-ish
    "terraform":  "#7B42BC",  # hashi purple
    "istio":      "#466BB0",  # istio blue
}


def load_trials(corpus: str) -> list[dict]:
    """Load all trial JSONs for a corpus, sorted by trial index."""
    trials = []
    for p in sorted((RUN_DIR / corpus).glob("trial-*.json")):
        trials.append(json.loads(p.read_text()))
    return trials


def warm(trials: list[dict]) -> list[dict]:
    """Trial 0 is the cold/first run; 1..N-1 are the warm distribution."""
    return [t for t in trials if t["trial"] != 0]


def median(values):
    return statistics.median(values)


def aggregate() -> dict:
    """Per-corpus median of warm trials, plus SLOC join."""
    sloc_raw = json.loads((RUN_DIR / "sloc.json").read_text())["corpora"]
    out = {}
    for corpus in CORPORA:
        trials = warm(load_trials(corpus))
        out[corpus] = {
            "wall_s":             median(t["wall_ns"] for t in trials) / 1e9,
            "max_rss_mb":         median(t["max_rss_bytes"] for t in trials) / 1e6,
            "sys_mb":             median(t["sys_bytes"] for t in trials) / 1e6,
            "heap_alloc_mb":      median(t["heap_alloc_bytes"] for t in trials) / 1e6,
            "total_alloc_gb":     median(t["total_alloc_bytes"] for t in trials) / 1e9,
            "db_size_mb":         median(t["db_size_bytes"] for t in trials) / 1e6,
            "symbols_k":          median(t["symbols"] for t in trials) / 1e3,
            "calls_k":            median(t["calls"] for t in trials) / 1e3,
            "unresolved_pct":     100 * median(t["unresolved"] for t in trials) / median(t["calls"] for t in trials),
            "modules":            int(median(t["modules"] for t in trials)),
            "sloc_m":             sloc_raw[corpus]["non_test_lines"] / 1e6,
            "sloc_total_m":       sloc_raw[corpus]["total_lines"] / 1e6,
            "files":              sloc_raw[corpus]["non_test_files"],
        }
    return out


def bar_panel(ax, data, key, *, ylabel, value_fmt="{:.1f}", title=None):
    values = [data[c][key] for c in CORPORA]
    colors = [COLORS[c] for c in CORPORA]
    bars = ax.bar(CORPORA, values, color=colors, edgecolor="white", linewidth=0.5)
    ax.set_ylabel(ylabel, fontsize=9)
    if title:
        ax.set_title(title, fontsize=10, fontweight="bold", pad=6)
    ax.tick_params(axis="x", labelsize=8)
    ax.tick_params(axis="y", labelsize=8)
    ax.grid(axis="y", linestyle=":", linewidth=0.5, alpha=0.5)
    ax.set_axisbelow(True)
    for spine in ("top", "right"):
        ax.spines[spine].set_visible(False)
    # Value labels above bars
    ymax = max(values) if values else 1
    for bar, v in zip(bars, values):
        ax.text(
            bar.get_x() + bar.get_width() / 2,
            bar.get_height() + ymax * 0.02,
            value_fmt.format(v),
            ha="center", va="bottom", fontsize=8,
        )
    # Headroom so labels fit
    ax.set_ylim(0, ymax * 1.18)


def grouped_memory_panel(ax, data):
    """Three memory metrics side-by-side per corpus: max RSS, Sys, Heap live."""
    metrics = [
        ("Max RSS (OS peak)", "max_rss_mb", "#d62728"),
        ("Sys (Go runtime)",  "sys_mb",     "#ff7f0e"),
        ("Heap live (end)",   "heap_alloc_mb", "#2ca02c"),
    ]
    x = np.arange(len(CORPORA))
    width = 0.25
    for i, (label, key, color) in enumerate(metrics):
        values = [data[c][key] for c in CORPORA]
        offset = (i - 1) * width
        bars = ax.bar(x + offset, values, width, label=label, color=color,
                      edgecolor="white", linewidth=0.3)
        for bar, v in zip(bars, values):
            ax.text(
                bar.get_x() + bar.get_width() / 2,
                bar.get_height() + max(values) * 0.02,
                f"{v:.0f}",
                ha="center", va="bottom", fontsize=7,
            )
    ax.set_xticks(x)
    ax.set_xticklabels(CORPORA, fontsize=8)
    ax.set_ylabel("memory (MB)", fontsize=9)
    ax.set_title("Memory — three views", fontsize=10, fontweight="bold", pad=6)
    ax.legend(fontsize=7, frameon=False, loc="upper right")
    ax.grid(axis="y", linestyle=":", linewidth=0.5, alpha=0.5)
    ax.set_axisbelow(True)
    for spine in ("top", "right"):
        ax.spines[spine].set_visible(False)
    ymax = max(data[c]["max_rss_mb"] for c in CORPORA)
    ax.set_ylim(0, ymax * 1.22)


def make_dashboard(data: dict, out_path: Path, *, meta: dict):
    fig, axes = plt.subplots(3, 3, figsize=(14, 11))
    fig.patch.set_facecolor("white")

    # Row 1 — corpus-level size + speed
    bar_panel(axes[0][0], data, "sloc_m",
              ylabel="non-test Go LOC (M)",
              value_fmt="{:.2f}M",
              title="Source size — SLOC (non-test, non-vendor)")
    bar_panel(axes[0][1], data, "wall_s",
              ylabel="wall time (s)",
              value_fmt="{:.1f}s",
              title="Indexing wall time")
    bar_panel(axes[0][2], data, "db_size_mb",
              ylabel="SQLite DB (MB)",
              value_fmt="{:.0f} MB",
              title="Index database size")

    # Row 2 — memory (three views) + total alloc + throughput
    grouped_memory_panel(axes[1][0], data)
    bar_panel(axes[1][1], data, "total_alloc_gb",
              ylabel="cumulative allocations (GB)",
              value_fmt="{:.1f} GB",
              title="Total lifetime allocations")
    # Derived: throughput = SLOC / wall_s
    throughput = {c: {"throughput_kloc_s": (data[c]["sloc_m"] * 1000) / data[c]["wall_s"]} for c in CORPORA}
    bar_panel(axes[1][2], throughput, "throughput_kloc_s",
              ylabel="kLOC / second",
              value_fmt="{:.0f}",
              title="Throughput (non-test kLOC / s)")

    # Row 3 — index contents
    bar_panel(axes[2][0], data, "symbols_k",
              ylabel="symbols (thousands)",
              value_fmt="{:.0f}k",
              title="Symbols indexed")
    bar_panel(axes[2][1], data, "calls_k",
              ylabel="call edges (thousands)",
              value_fmt="{:.0f}k",
              title="Call edges recorded")
    bar_panel(axes[2][2], data, "unresolved_pct",
              ylabel="unresolved / calls (%)",
              value_fmt="{:.1f}%",
              title="Unresolved call ratio")

    # Super-title with run metadata
    subtitle = (
        f"{meta['tool_version']} · {meta['hw_model']} {meta['cpu_brand']} "
        f"({meta['cpu_physical_cores']}c) · "
        f"{meta['ram_bytes'] // (1024**3)} GB RAM · "
        f"{meta['go_version']} · warm trials median · "
        f"run {meta['run_id']} ({meta['started_at'][:10]})"
    )
    fig.suptitle("gosymdb — indexing benchmark", fontsize=14, fontweight="bold", y=0.995)
    fig.text(0.5, 0.965, subtitle, ha="center", va="top", fontsize=9, color="#555")

    plt.tight_layout(rect=(0, 0, 1, 0.955))
    fig.savefig(out_path, dpi=160, bbox_inches="tight", facecolor="white")
    plt.close(fig)
    print(f"wrote {out_path}")


def make_hero(data: dict, out_path: Path, *, meta: dict):
    """A single compact chart: SLOC vs wall time as a scatter with labels,
    plus throughput annotation. Makes the 'bigger codebases take longer,
    at roughly constant throughput' story in one image."""
    fig, ax = plt.subplots(figsize=(9, 6))
    fig.patch.set_facecolor("white")

    xs = [data[c]["sloc_m"] for c in CORPORA]
    ys = [data[c]["wall_s"] for c in CORPORA]
    colors = [COLORS[c] for c in CORPORA]
    sizes = [data[c]["symbols_k"] * 4 for c in CORPORA]  # symbol count as dot size

    ax.scatter(xs, ys, s=sizes, c=colors, alpha=0.85, edgecolor="white", linewidth=1.5)
    # Manual per-label offsets so terraform/istio don't collide in the bottom-left.
    # terraform sits above and to the right; istio sits level but further right
    # so the two labels stack horizontally instead of fighting for the y-axis.
    label_offsets = {
        "kubernetes": (12, 0, "left", "center"),
        "cockroach":  (12, 0, "left", "center"),
        "terraform":  (12, 8, "left", "bottom"),
        "istio":      (12, -2, "left", "top"),
    }
    for c, x, y in zip(CORPORA, xs, ys):
        throughput = (data[c]["sloc_m"] * 1000) / data[c]["wall_s"]
        label = f"{c}\n{data[c]['wall_s']:.1f}s · {throughput:.0f} kLOC/s"
        dx, dy, ha, va = label_offsets[c]
        ax.annotate(label, xy=(x, y), xytext=(dx, dy), textcoords="offset points",
                    fontsize=9, ha=ha, va=va)

    # Fit a through-origin reference line for eyeball throughput comparison.
    max_x = max(xs) * 1.35
    mean_throughput = np.mean([ys[i] / xs[i] for i in range(len(xs))])
    ref_xs = np.linspace(0, max_x, 50)
    ref_ys = ref_xs * mean_throughput
    ax.plot(ref_xs, ref_ys, linestyle="--", color="#aaa", linewidth=1,
            label=f"mean wall/SLOC (all corpora)")

    ax.set_xlabel("non-test Go LOC (millions)", fontsize=10)
    ax.set_ylabel("indexing wall time (seconds)", fontsize=10)
    ax.grid(linestyle=":", linewidth=0.5, alpha=0.5)
    ax.set_axisbelow(True)
    for spine in ("top", "right"):
        ax.spines[spine].set_visible(False)
    ax.set_xlim(0, max_x)
    ax.set_ylim(0, max(ys) * 1.2)
    ax.legend(loc="lower right", fontsize=9, frameon=False)

    fig.suptitle("Indexing time scales with source size", fontsize=14, fontweight="bold", y=0.98)
    subtitle = (
        f"{meta['hw_model']} · {meta['cpu_brand']} · "
        f"warm trials median · dot size ∝ symbols indexed"
    )
    fig.text(0.5, 0.935, subtitle, ha="center", va="top", fontsize=9, color="#555")

    plt.tight_layout(rect=(0, 0, 1, 0.91))
    fig.savefig(out_path, dpi=160, bbox_inches="tight", facecolor="white")
    plt.close(fig)
    print(f"wrote {out_path}")


def main():
    meta = json.loads((RUN_DIR / "meta.json").read_text())
    data = aggregate()
    # Pretty-print aggregated table to stderr for inspection.
    print(f"{'corpus':<12} {'SLOC(M)':>8} {'wall(s)':>8} {'maxRSS(MB)':>11} {'sys(MB)':>8} {'DB(MB)':>8} {'syms(k)':>8} {'calls(k)':>9} {'unres%':>7}")
    for c in CORPORA:
        d = data[c]
        print(f"{c:<12} {d['sloc_m']:>8.2f} {d['wall_s']:>8.1f} {d['max_rss_mb']:>11.0f} {d['sys_mb']:>8.0f} {d['db_size_mb']:>8.0f} {d['symbols_k']:>8.1f} {d['calls_k']:>9.1f} {d['unresolved_pct']:>6.1f}")
    make_dashboard(data, PLOTS_DIR / "dashboard.png", meta=meta)
    make_hero(data, PLOTS_DIR / "hero.png", meta=meta)


if __name__ == "__main__":
    main()
