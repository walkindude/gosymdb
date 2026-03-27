package cmd

import (
	"encoding/json"
	"os"
)

type agentHelp struct {
	Command       string      `json:"command"`
	Summary       string      `json:"summary"`
	Usage         string      `json:"usage"`
	Description   string      `json:"description,omitempty"`
	Flags         []agentFlag `json:"flags,omitempty"`
	Subcommands   []subCmd    `json:"subcommands,omitempty"`
	Prerequisites []string    `json:"prerequisites,omitempty"`
	Examples      []string    `json:"examples,omitempty"`
	Next          []string    `json:"next,omitempty"`
	Notes         []string    `json:"notes,omitempty"`
	OutputSchema  string      `json:"output_schema,omitempty"`
	ErrorContract string      `json:"error_contract,omitempty"`
}

type subCmd struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type agentFlag struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Default     string `json:"default"`
	Description string `json:"description"`
}

// errorContract is the shared error contract emitted on every command spec.
const errorContract = `Exit 1. When --json is set, errors are written to stdout as JSON: {"error":"...","error_code":"...","flag":"...","recovery":"...","hint":"..."}. error_code values: missing_required_flag, no_database, unknown_command_or_flag. Empty results return empty array/zero counts, not an error.`

var helpSpecs = map[string]agentHelp{
	"gosymdb": {
		Command: "gosymdb",
		Summary: "Go symbol and call-graph index. Index modules, find symbols, traverse call graph, detect dead code.",
		Usage:   "gosymdb <command> [--db <path>] [--json]",
		Subcommands: []subCmd{
			{Name: "agent-context", Description: "Full API reference in one JSON payload."},
			{Name: "index", Description: "Build/update symbol+call index from Go source."},
			{Name: "find", Description: "Search symbols by name, package, kind, or file."},
			{Name: "def", Description: "Exact single-symbol lookup; returns one result or ambiguity list."},
			{Name: "callers", Description: "Direct or transitive callers of a symbol (--depth N)."},
			{Name: "callees", Description: "What a symbol calls."},
			{Name: "blast-radius", Description: "Transitive callers at every depth."},
			{Name: "dead", Description: "Symbols with no callers."},
			{Name: "trace", Description: "Single-shot: symbol def + direct callers + direct callees + blast-radius total."},
			{Name: "packages", Description: "All indexed packages with symbol counts."},
			{Name: "health", Description: "Index quality: counts, warnings, unresolved ratio."},
			{Name: "implementors", Description: "Types implementing an interface, or interfaces a type satisfies."},
			{Name: "references", Description: "Where a type is used: assertions, conversions, literals, embeds, field access."},
			{Name: "version", Description: "Print gosymdb version and schema version."},
		},
		Notes: []string{
			"--db: auto-discovered by walking parent directories when omitted; explicit --db always wins.",
			`All --json responses include a top-level env key: {"ts":unix_epoch,"cwd":"","os":"","arch":"","db":"abs_path","git":{"git_available":true,"branch":"","is_worktree":false,"ahead_behind":"ahead/behind","worktree_root":"","dirty_count":0,"staged_count":0,"last_fetch_age":null}}`,
			"env.db is the absolute path of the database in use; empty string when no database is found in cwd or ancestors.",
			"env.git.git_available is false when cwd is not inside a git repository (all other git fields will be zero/empty).",
			"env.git.last_fetch_age is null when the repo has never been fetched (not -1).",
			`All symbol-bearing --json outputs include both fqname (full qualified) and name (short name after package path). Examples: fqname=github.com/walkindude/gosymdb/cmd.Execute → name=Execute; fqname=testbench/method_exprs.*Calculator.Add → name=*Calculator.Add.`,
			"find/def/callers/callees/dead/blast-radius all include package_path in JSON output.",
			"kind=interface is now distinct from kind=type in the index.",
			`Errors with --json: stdout JSON {"error":"...","error_code":"...","hint":"...","recovery":"..."}. error_code: missing_required_flag | no_database | unknown_command_or_flag. unknown_command_or_flag includes a valid_subcommands list — you may be hallucinating that flag/command.`,
			`--count flag on callers/callees/find: prints just the integer count to stdout (no JSON, no env). Shell usage: count=$(gosymdb callers --symbol Foo --count); [ "$count" -ge 1 ]`,
			`Recommended JSON parser for shell pipelines: jq. Examples: gosymdb callers --symbol Foo --json | jq '.callers_count'; gosymdb find --q Bar --json | jq '[.symbols[].name]'; gosymdb health --json | jq '{symbols,calls,unresolved}'`,
		},
		Next: []string{
			"gosymdb agent-context",
		},
	},
	"gosymdb agent-context": {
		Command: "gosymdb agent-context",
		Summary: "Full gosymdb API reference + current env snapshot. Run once at session start.",
		Usage:   "gosymdb agent-context",
		Examples: []string{
			"gosymdb agent-context",
		},
		OutputSchema: `{"commands":[...],"env":{"ts":0,"cwd":"","os":"","arch":"","db":"","git":{"git_available":false,"branch":"","is_worktree":false,"ahead_behind":"","worktree_root":"","dirty_count":0,"staged_count":0,"last_fetch_age":null}}}`,
	},
	"gosymdb index": {
		Command: "gosymdb index",
		Summary: "Walk a directory tree, find all go.mod files, build a SQLite symbol+call index.",
		Usage:   "gosymdb index [flags]",
		Flags: []agentFlag{
			{Name: "--root", Type: "string", Default: ".", Description: "Root dir to scan for go.mod files."},
			{Name: "--db", Type: "string", Default: "gosymdb.sqlite", Description: "Output SQLite path."},
			{Name: "--force", Type: "bool", Default: "false", Description: "Full rebuild (default: incremental)."},
			{Name: "--tests", Type: "bool", Default: "false", Description: "Include *_test.go files."},
			{Name: "--cgo", Type: "bool", Default: "false", Description: "Set CGO_ENABLED=1."},
		},
		Examples: []string{
			"gosymdb index --root . --db ./repo.sqlite --force",
		},
		Next: []string{
			"gosymdb health --db <db> --json",
			"gosymdb packages --db <db> --json",
			"gosymdb find --db <db> --q <name> --json",
		},
		Notes: []string{
			"Modules with load/type errors are indexed partially; warnings count appears in health output.",
			"Interface types are stored as kind=interface; concrete types as kind=type.",
			"Progress lines emitted to stderr: [N/total] indexing <module> ..., then loaded K package(s), then done: symbols/calls/unresolved.",
		},
		ErrorContract: errorContract,
	},
	"gosymdb find": {
		Command: "gosymdb find",
		Summary: "Search symbols by name/package/file. All filters are optional; omitting all returns every symbol (up to --limit).",
		Usage:   "gosymdb find [--q <text>] [--pkg <prefix>] [--file <path>] [--kind <kind>] [flags]",
		Flags: []agentFlag{
			{Name: "--db", Type: "string", Default: "gosymdb.sqlite", Description: "SQLite path."},
			{Name: "--q", Type: "string", Default: "", Description: "Substring match on fqname/name/signature (optional)."},
			{Name: "--pkg", Type: "string", Default: "", Description: "Filter by package path prefix (optional)."},
			{Name: "--kind", Type: "string", Default: "", Description: "func|method|type|interface|var|const — interface is now a distinct kind."},
			{Name: "--file", Type: "string", Default: "", Description: "Filter by file path substring (optional)."},
			{Name: "--count", Type: "bool", Default: "false", Description: "Print only the integer count to stdout (no JSON envelope)."},
			{Name: "--limit", Type: "int", Default: "100", Description: "Row limit."},
			{Name: "--json", Type: "bool", Default: "false", Description: "JSON output."},
		},
		Prerequisites: []string{"gosymdb index"},
		Examples: []string{
			"gosymdb find --db ./repo.sqlite --json",
			"gosymdb find --db ./repo.sqlite --q Store --kind type --json",
			"gosymdb find --db ./repo.sqlite --pkg github.com/org/repo/util --kind interface --json",
			"gosymdb find --db ./repo.sqlite --file internal/auth.go --json",
		},
		Next: []string{
			"gosymdb def <name> --db <db> --json",
			"gosymdb callers --db <db> --symbol <fqname> --json",
			"gosymdb callees --db <db> --symbol <fqname> --json",
			"gosymdb blast-radius --db <db> --symbol <fqname> --json",
		},
		Notes: []string{
			"Use exact fqname from output as --symbol for callers/callees/blast-radius to avoid false positives.",
			"--pkg prefix-matches: foo/bar matches foo/bar and foo/bar/sub.",
			"--kind interface returns only named interface types; --kind type returns only concrete types.",
			"--file <path> lists all symbols defined in that file — useful for understanding a file's surface before reading it.",
			"truncated=true means total_matched > --limit; raise --limit or add --pkg/--kind to narrow results.",
			"No filters: returns all symbols in the DB (capped by --limit). Useful for inspecting a fresh index.",
		},
		OutputSchema:  `{"symbols":[{"fqname":"string","name":"string","kind":"string","file":"string","line":0,"col":0,"signature":"string","package_path":"string"}],"count":0,"total_matched":0,"truncated":false}`,
		ErrorContract: errorContract,
	},
	"gosymdb def": {
		Command: "gosymdb def",
		Summary: "Exact single-symbol lookup. Returns one result or an ambiguity list with a --pkg hint.",
		Usage:   "gosymdb def <name> [--pkg <prefix>] [--json]",
		Flags: []agentFlag{
			{Name: "--db", Type: "string", Default: "gosymdb.sqlite", Description: "SQLite path."},
			{Name: "--pkg", Type: "string", Default: "", Description: "Disambiguate by package path prefix when name matches multiple symbols."},
			{Name: "--json", Type: "bool", Default: "false", Description: "JSON output."},
		},
		Prerequisites: []string{"gosymdb index"},
		Examples: []string{
			"gosymdb def Store --json",
			"gosymdb def Store --pkg github.com/org/repo/storage --json",
			"gosymdb def github.com/org/repo/storage.Store --json",
		},
		Next: []string{
			"gosymdb callers --db <db> --symbol <fqname> --json",
			"gosymdb callees --db <db> --symbol <fqname> --json",
		},
		Notes: []string{
			"Tries exact fqname first, then exact name match.",
			"ambiguous=true when name matches multiple symbols — use --pkg to narrow.",
			"Returns symbol=null with a hint when not found.",
		},
		OutputSchema:  `{"symbol":{"fqname":"","name":"","kind":"","file":"","line":0,"col":0,"signature":"","package_path":""},"ambiguous":false,"matches":[...],"hint":"string (present when ambiguous or not found)"}`,
		ErrorContract: errorContract,
	},
	"gosymdb callers": {
		Command: "gosymdb callers",
		Summary: "Direct or transitive callers of a symbol (BFS up to --depth N).",
		Usage:   "gosymdb callers --symbol <fqname> [flags]",
		Flags: []agentFlag{
			{Name: "--db", Type: "string", Default: "gosymdb.sqlite", Description: "SQLite path."},
			{Name: "--symbol", Type: "string", Default: "", Description: "fqname or short name (auto-resolved if unambiguous). Get exact fqname from find/def. Required."},
			{Name: "--depth", Type: "int", Default: "1", Description: "Transitive caller depth (1=direct only, max 10). Each row includes a depth field."},
			{Name: "--fuzzy", Type: "bool", Default: "false", Description: "Also match symbols containing --symbol as a substring (LIKE)."},
			{Name: "--pkg", Type: "string", Default: "", Description: "Restrict callers to this package path prefix."},
			{Name: "--is-test", Type: "bool", Default: "false", Description: "Only return callers from test files (*_test.go)."},
			{Name: "--include-unresolved", Type: "bool", Default: "false", Description: "Also return unresolved references."},
			{Name: "--count", Type: "bool", Default: "false", Description: "Print only the integer count to stdout (no JSON envelope). For shell: count=$(gosymdb callers --symbol X --count)."},
			{Name: "--limit", Type: "int", Default: "200", Description: "Row limit (applies per BFS hop)."},
			{Name: "--json", Type: "bool", Default: "false", Description: "JSON output."},
		},
		Prerequisites: []string{"gosymdb index", "gosymdb find or def (to get exact fqname)"},
		Examples: []string{
			"gosymdb callers --symbol example.com/pkg.(*Store).Save --json",
			"gosymdb callers --symbol example.com/pkg.(*Store).Save --depth 3 --json",
			"gosymdb callers --symbol Send --fuzzy --pkg example.com/client --is-test --json",
		},
		Next: []string{
			"gosymdb blast-radius --db <db> --symbol <fqname> --json",
			"gosymdb callees --db <db> --symbol <fqname> --json",
		},
		Notes: []string{
			"Short names (no '/') are auto-resolved: if exactly one symbol matches, it is used and a 'resolved' note is returned.",
			"0 results? Check hint — may suggest interface dispatch via implementors.",
			"kind: call=direct call, ref=function value reference.",
			"--depth > 1: uses BFS; each row has depth=N. Use blast-radius for full transitive analysis with test/prod split.",
		},
		OutputSchema:  `{"callers":[{"from":"string","name":"string","to":"string","file":"string","line":0,"col":0,"kind":"call|ref","depth":1}],"callers_count":0,"depth":1,"unresolved":[{"from":"string","expr":"string","file":"string","line":0,"col":0}],"unresolved_count":0,"resolved":"string (only present when short name was auto-resolved)","hint":"string (only present when count=0)"}`,
		ErrorContract: errorContract,
	},
	"gosymdb blast-radius": {
		Command: "gosymdb blast-radius",
		Summary: "Transitive callers of a symbol — full impact at every depth, with test/prod split.",
		Usage:   "gosymdb blast-radius --symbol <fqname> [flags]",
		Flags: []agentFlag{
			{Name: "--db", Type: "string", Default: "gosymdb.sqlite", Description: "SQLite path."},
			{Name: "--symbol", Type: "string", Default: "", Description: "Exact full fqname as stored in index (e.g. example.com/pkg.(*T).Method). Get it from find. Required."},
			{Name: "--fuzzy", Type: "bool", Default: "false", Description: "Also match symbols containing --symbol as a substring (LIKE)."},
			{Name: "--depth", Type: "int", Default: "3", Description: "Max traversal depth (cap: 10)."},
			{Name: "--pkg", Type: "string", Default: "", Description: "Restrict results to callers in this package path prefix."},
			{Name: "--exclude-tests", Type: "bool", Default: "false", Description: "Omit test callers from results and traversal."},
			{Name: "--limit", Type: "int", Default: "500", Description: "Row limit."},
			{Name: "--json", Type: "bool", Default: "false", Description: "JSON output."},
		},
		Prerequisites: []string{"gosymdb index", "gosymdb find (to get exact fqname)"},
		Examples: []string{
			"gosymdb blast-radius --symbol example.com/pkg.(*Store).Save --json",
			"gosymdb blast-radius --symbol Send --fuzzy --depth 5 --exclude-tests --json",
		},
		Next: []string{
			"gosymdb callers --db <db> --symbol <fqname> --json",
			"gosymdb callees --db <db> --symbol <fqname> --json",
		},
		Notes: []string{
			"Short names (no '/') are auto-resolved: if exactly one symbol matches, it is used and a 'resolved' note is returned.",
			"0 results? Check the hint field — it lists similar fqnames or tells you to run find.",
			"Each caller reported at shallowest reachable depth. summary.truncated=true means --limit hit.",
		},
		OutputSchema:  `{"target":"string","callers":[{"fqname":"string","name":"string","package":"string","file":"string","line":0,"depth":0,"is_test":false}],"summary":{"total":0,"test":0,"non_test":0,"max_depth_reached":0,"truncated":false},"resolved":"string (only present when short name was auto-resolved)","hint":"string (only present when callers empty)"}`,
		ErrorContract: errorContract,
	},
	"gosymdb callees": {
		Command: "gosymdb callees",
		Summary: "What does X call? (one hop, inverse of callers).",
		Usage:   "gosymdb callees --symbol <fqname> [flags]",
		Flags: []agentFlag{
			{Name: "--db", Type: "string", Default: "gosymdb.sqlite", Description: "SQLite path."},
			{Name: "--symbol", Type: "string", Default: "", Description: "fqname or short name (auto-resolved if unambiguous). Get exact fqname from find. Required."},
			{Name: "--fuzzy", Type: "bool", Default: "false", Description: "Also match symbols containing --symbol as a substring (LIKE)."},
			{Name: "--pkg", Type: "string", Default: "", Description: "Restrict callees to this package path prefix."},
			{Name: "--include-unresolved", Type: "bool", Default: "true", Description: "Include external/stdlib calls (default true)."},
			{Name: "--unique", Type: "bool", Default: "false", Description: "One row per callee instead of per call site."},
			{Name: "--count", Type: "bool", Default: "false", Description: "Print only the integer count to stdout (no JSON envelope)."},
			{Name: "--limit", Type: "int", Default: "200", Description: "Row limit."},
			{Name: "--json", Type: "bool", Default: "false", Description: "JSON output."},
		},
		Prerequisites: []string{"gosymdb index", "gosymdb find (to get exact fqname)"},
		Examples: []string{
			"gosymdb callees --symbol example.com/pkg.(*Store).Save --unique --json",
			"gosymdb callees --symbol applyOp --fuzzy --pkg example.com/internal --json",
		},
		Next: []string{
			"gosymdb callers --db <db> --symbol <to-fqname> --json",
			"gosymdb blast-radius --db <db> --symbol <to-fqname> --json",
		},
		Notes: []string{
			"Short names (no '/') are auto-resolved: if exactly one symbol matches, it is used and a 'resolved' note is returned.",
			"0 results? Check the hint field — it lists similar fqnames or tells you to run find.",
			"--include-unresolved=true by default (external/stdlib calls only appear here).",
			"kind: call=direct, ref=function value. --unique for dependency view.",
			"package_path in each row is the callee's package (from index; empty for external symbols).",
		},
		OutputSchema:  `{"callees":[{"to":"string","name":"string","file":"string","line":0,"col":0,"kind":"call|ref","package_path":"string"}],"callees_count":0,"unresolved":[{"expr":"string","file":"string","line":0,"col":0}],"unresolved_count":0,"resolved":"string (only present when short name was auto-resolved)","hint":"string (only present when count=0)"}`,
		ErrorContract: errorContract,
	},
	"gosymdb dead": {
		Command: "gosymdb dead",
		Summary: "Symbols with no callers (dead code candidates). Unexported only by default.",
		Usage:   "gosymdb dead [flags]",
		Flags: []agentFlag{
			{Name: "--db", Type: "string", Default: "gosymdb.sqlite", Description: "SQLite path."},
			{Name: "--kind", Type: "string", Default: "", Description: "func|method (default: both)."},
			{Name: "--pkg", Type: "string", Default: "", Description: "Filter by package path prefix."},
			{Name: "--include-exported", Type: "bool", Default: "false", Description: "Include exported symbols (default: unexported only)."},
			{Name: "--limit", Type: "int", Default: "200", Description: "Row limit."},
			{Name: "--json", Type: "bool", Default: "false", Description: "JSON output."},
		},
		Prerequisites: []string{"gosymdb index"},
		Examples: []string{
			"gosymdb dead --pkg github.com/org/repo/internal --json",
		},
		Next: []string{
			"gosymdb callers --db <db> --symbol <fqname> --json",
		},
		Notes: []string{
			"Auto-excludes init, main, Test*/Benchmark*/Example*/Fuzz* functions.",
			"Interface impls appear dead (no recorded callers via dispatch); verify before removing.",
			"truncated=true means total_matched > --limit; raise --limit or add --pkg/--kind to narrow results.",
		},
		OutputSchema:  `{"symbols":[{"fqname":"string","name":"string","kind":"string","package":"string","file":"string","line":0,"col":0,"signature":"string"}],"count":0,"total_matched":0,"truncated":false,"note":"string"}`,
		ErrorContract: errorContract,
	},
	"gosymdb trace": {
		Command: "gosymdb trace",
		Summary: "Single-shot overview of a symbol: def + direct callers + direct callees + blast-radius total. Reduces 3 round-trips to 1.",
		Usage:   "gosymdb trace --symbol <fqname|short> [--pkg <prefix>] [--caller-limit N] [--callee-limit N] [--json]",
		Flags: []agentFlag{
			{Name: "--symbol", Type: "string", Default: "", Description: "fqname or short name to trace (required)."},
			{Name: "--pkg", Type: "string", Default: "", Description: "Disambiguate short name by package path prefix."},
			{Name: "--caller-limit", Type: "int", Default: "20", Description: "Max direct callers to return."},
			{Name: "--callee-limit", Type: "int", Default: "20", Description: "Max direct callees to return."},
			{Name: "--db", Type: "string", Default: "gosymdb.sqlite", Description: "SQLite path."},
			{Name: "--json", Type: "bool", Default: "false", Description: "JSON output."},
		},
		Prerequisites: []string{"gosymdb index"},
		Examples: []string{
			"gosymdb trace --symbol github.com/walkindude/gosymdb/indexer.IndexModule --json",
			"gosymdb trace --symbol IndexModule --pkg github.com/walkindude/gosymdb/indexer --json",
		},
		Next: []string{
			"gosymdb callers --db <db> --symbol <fqname> --depth 5 --json",
			"gosymdb blast-radius --db <db> --symbol <fqname> --json",
		},
		Notes: []string{
			"trace is a convenience command — equivalent to running def + callers --depth 1 + callees + blast-radius --depth 5 in one call.",
			"callers and callees lists are capped; use dedicated commands for full results.",
			"blast_radius.total is the transitive count at depth <= 5; truncated=true when >= 1000.",
			"symbol section is absent (and hint is set) when the fqname is not in the index.",
		},
		OutputSchema:  `{"symbol":{"fqname":"string","name":"string","kind":"string","file":"string","line":0,"col":0,"signature":"string","package_path":"string"},"callers":[{"fqname":"string","name":"string","file":"string","line":0,"col":0,"kind":"call|ref"}],"callers_count":0,"callees":[{"fqname":"string","name":"string","file":"string","line":0,"col":0,"kind":"call|ref","package_path":"string"}],"callees_count":0,"blast_radius":{"total":0,"max_depth_reached":0,"truncated":false},"resolved":"string (only present when short name auto-resolved)","hint":"string (only present when symbol not found)"}`,
		ErrorContract: errorContract,
	},
	"gosymdb packages": {
		Command: "gosymdb packages",
		Summary: "All indexed packages with symbol and function counts.",
		Usage:   "gosymdb packages [flags]",
		Flags: []agentFlag{
			{Name: "--db", Type: "string", Default: "gosymdb.sqlite", Description: "SQLite path."},
			{Name: "--json", Type: "bool", Default: "false", Description: "JSON output."},
		},
		Prerequisites: []string{"gosymdb index"},
		Examples: []string{
			"gosymdb packages --json",
		},
		Next: []string{
			"gosymdb find --db <db> --pkg <package-path> --json",
			"gosymdb dead --db <db> --pkg <package-path-prefix> --json",
		},
		OutputSchema:  `[{"path":"string","symbol_count":0,"func_count":0,"exported_count":0}]`,
		ErrorContract: errorContract,
	},
	"gosymdb health": {
		Command: "gosymdb health",
		Summary: "Index quality report: symbol/call counts, load warnings, and unresolved-call ratio.",
		Usage:   "gosymdb health [flags]",
		Flags: []agentFlag{
			{Name: "--db", Type: "string", Default: "gosymdb.sqlite", Description: "SQLite path."},
			{Name: "--json", Type: "bool", Default: "false", Description: "JSON output."},
		},
		Prerequisites: []string{"gosymdb index"},
		Examples: []string{
			"gosymdb health --json",
		},
		Next: []string{
			"gosymdb packages --db <db> --json",
		},
		Notes: []string{
			"warnings>0 means some packages failed to load; index is partial — re-run with --cgo or fix errors.",
			"unresolved_ratio>20% suggests CGO deps or generated code; try --cgo or narrowing --root.",
		},
		OutputSchema:  `{"root":"string","indexed_at":"string","tool_version":"string","go_version":"string","symbols":0,"calls":0,"unresolved":0,"unresolved_ratio":0.0,"warnings":0,"top_unresolved":[{"expr":"string","count":0}]}`,
		ErrorContract: errorContract,
	},
	"gosymdb implementors": {
		Command: "gosymdb implementors",
		Summary: "Types implementing an interface, or interfaces a type satisfies. Bridges the gap callers/callees can't cross.",
		Usage:   "gosymdb implementors (--iface <partial> | --type <partial>) [flags]",
		Flags: []agentFlag{
			{Name: "--db", Type: "string", Default: "gosymdb.sqlite", Description: "SQLite path."},
			{Name: "--iface", Type: "string", Default: "", Description: "Partial interface fqname → returns implementing types. Required unless --type given."},
			{Name: "--type", Type: "string", Default: "", Description: "Partial type fqname → returns satisfied interfaces. Required unless --iface given."},
			{Name: "--limit", Type: "int", Default: "200", Description: "Row limit."},
			{Name: "--json", Type: "bool", Default: "false", Description: "JSON output."},
		},
		Prerequisites: []string{"gosymdb index"},
		Examples: []string{
			"gosymdb implementors --iface ClockedRepo --json",
			"gosymdb implementors --type GitRepo --json",
		},
		Next: []string{
			"gosymdb callees --db <db> --symbol <impl-method> --json",
			"gosymdb callers --db <db> --symbol <iface-method> --json",
		},
		Notes: []string{
			"is_pointer=true means only *T satisfies the interface (pointer receivers).",
			"Both flags match via LIKE. Cross-module satisfaction not checked.",
		},
		OutputSchema:  `{"implementors":[{"iface":"string","impl":"string","is_pointer":false}],"count":0}`,
		ErrorContract: errorContract,
	},
	"gosymdb references": {
		Command: "gosymdb references",
		Summary: "Find where a type is used: type assertions, type switches, composite literals, conversions, field accesses, embeds.",
		Usage:   "gosymdb references --symbol <fqname|short> [flags]",
		Flags: []agentFlag{
			{Name: "--symbol", Type: "string", Default: "", Description: "Type fqname or short name (required). Get exact fqname from find/def."},
			{Name: "--pkg", Type: "string", Default: "", Description: "Filter by package_path prefix."},
			{Name: "--ref-kind", Type: "string", Default: "", Description: "Filter: type_assert, type_switch, composite_lit, conversion, field_access, embed."},
			{Name: "--from", Type: "string", Default: "", Description: "Filter by from_fqname substring."},
			{Name: "--limit", Type: "int", Default: "200", Description: "Row limit."},
			{Name: "--count", Type: "bool", Default: "false", Description: "Print only the integer count to stdout."},
			{Name: "--db", Type: "string", Default: "gosymdb.sqlite", Description: "SQLite path."},
			{Name: "--json", Type: "bool", Default: "false", Description: "JSON output."},
		},
		Prerequisites: []string{"gosymdb index"},
		Examples: []string{
			"gosymdb references --symbol example.com/pkg.Store --json",
			"gosymdb references --symbol Store --ref-kind composite_lit --json",
			"gosymdb references --symbol Store --count",
			"gosymdb references --symbol Store --from HandleRequest --json",
		},
		Next: []string{
			"gosymdb def <type-name> --db <db> --json",
			"gosymdb implementors --db <db> --type <fqname> --json",
			"gosymdb callers --db <db> --symbol <from-fqname> --json",
		},
		Notes: []string{
			"Short names (no '/') are auto-resolved: if exactly one type matches, it is used.",
			"ref_kind values: type_assert, type_switch, composite_lit, conversion, field_access, embed.",
			"Field accesses track the receiver type, not the field type — 'where is this struct's fields read/written?'",
			"Embeds track which types embed a given type as an unnamed field.",
			"Not tracked (too noisy): var declarations, parameter types, return types.",
		},
		OutputSchema:  `{"references":[{"from":"string","from_name":"string","to":"string","to_name":"string","ref_kind":"string","file":"string","line":0,"col":0,"expr":"string","package_path":"string"}],"count":0,"total_matched":0,"truncated":false,"resolved":"string (only when short name resolved)","hint":"string (only when count=0)"}`,
		ErrorContract: errorContract,
	},
	"gosymdb version": {
		Command: "gosymdb version",
		Summary: "Print gosymdb version and schema version.",
		Usage:   "gosymdb version [--json]",
		Flags: []agentFlag{
			{Name: "--json", Type: "bool", Default: "false", Description: "JSON output."},
		},
		Examples: []string{
			"gosymdb version --json",
		},
		OutputSchema:  `{"version":"0.1.0","schema_version":1}`,
		ErrorContract: errorContract,
	},
}

// emitAgentHelp writes the spec for key to stdout as compact JSON.
func emitAgentHelp(key string) bool {
	spec, ok := helpSpecs[key]
	if !ok {
		return false
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(spec)
	return true
}

// jsonFlagInArgs returns true if --json or -json appears in args.
func jsonFlagInArgs(args []string) bool {
	for _, a := range args {
		if a == "--json" || a == "-json" {
			return true
		}
	}
	return false
}
