package sqlite

import (
	"context"
	"fmt"
	"log"
	"slices"
	"strings"

	"github.com/walkindude/gosymdb/store"
)

// Ensure database/sql is not accidentally imported here.
// All raw DB access goes through s.db which is typed *sql.DB but accessed via the field.

// shortName extracts the symbol identifier from a fully-qualified name.
// It strips the import path (up to and including the last "/"), then strips the
// package-name prefix (up to and including the first "."), mirroring cmd.shortName.
// Examples:
//
//	"example.com/pkg/sub.TypeName"   → "TypeName"
//	"example.com/pkg.*T.Method"      → "*T.Method"
//	"example.com/pkg.Func"           → "Func"
func shortName(fqname string) string {
	rest := fqname
	if i := strings.LastIndex(fqname, "/"); i >= 0 {
		rest = fqname[i+1:] // e.g. "sub.TypeName" or "sub.*T.Method"
	}
	if dot := strings.Index(rest, "."); dot >= 0 {
		return rest[dot+1:] // strip "sub." prefix → "TypeName" or "*T.Method"
	}
	return rest
}

func stripInstantiationArgs(name string) string {
	var b strings.Builder
	depth := 0
	for _, r := range name {
		switch r {
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

// ---- FindSymbols / CountSymbols ----

func (s *SQLiteStore) FindSymbols(ctx context.Context, opts store.FindOpts) (store.FindResult, error) {
	where, params := buildFindWhere(opts)

	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}

	// Fetch limit+1 rows to detect truncation without a separate COUNT(*) query.
	// The COUNT(*) is only issued when the result IS truncated (to populate total_matched).
	q := `SELECT fqname, kind, file_path, line, col, signature, package_path FROM symbols` +
		where + " ORDER BY fqname LIMIT ?"
	queryParams := append(append([]any{}, params...), limit+1)

	rows, err := s.db.QueryContext(ctx, q, queryParams...)
	if err != nil {
		return store.FindResult{}, fmt.Errorf("FindSymbols query: %w", err)
	}
	defer rows.Close()

	syms := make([]store.SymbolRow, 0, limit)
	for rows.Next() {
		var r store.SymbolRow
		if err := rows.Scan(&r.FQName, &r.Kind, &r.File, &r.Line, &r.Col, &r.Signature, &r.PackagePath); err != nil {
			return store.FindResult{}, fmt.Errorf("FindSymbols scan: %w", err)
		}
		r.Name = shortName(r.FQName)
		syms = append(syms, r)
	}
	if err := rows.Err(); err != nil {
		return store.FindResult{}, fmt.Errorf("FindSymbols rows: %w", err)
	}

	truncated := len(syms) > limit
	if truncated {
		syms = syms[:limit] // trim the extra probe row
	}

	// Only run COUNT(*) when truncated — avoids full table scan in the common case.
	totalMatched := len(syms)
	if truncated {
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM symbols`+where, params...).Scan(&totalMatched); err != nil {
			return store.FindResult{}, fmt.Errorf("FindSymbols count: %w", err)
		}
	}

	return store.FindResult{Symbols: syms, TotalMatched: totalMatched}, nil
}

func (s *SQLiteStore) CountSymbols(ctx context.Context, opts store.FindOpts) (int, error) {
	where, params := buildFindWhere(opts)
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM symbols`+where, params...).Scan(&n); err != nil {
		return 0, fmt.Errorf("CountSymbols: %w", err)
	}
	return n, nil
}

func buildFindWhere(opts store.FindOpts) (string, []any) {
	where := ` WHERE 1=1`
	var params []any
	if opts.Query != "" {
		where += " AND (fqname LIKE ? OR name LIKE ? OR signature LIKE ?)"
		params = append(params, "%"+opts.Query+"%", "%"+opts.Query+"%", "%"+opts.Query+"%")
	}
	if opts.Pkg != "" {
		where += " AND package_path LIKE ?"
		params = append(params, opts.Pkg+"%")
	}
	if opts.Kind != "" {
		where += " AND kind = ?"
		params = append(params, opts.Kind)
	}
	if opts.File != "" {
		where += " AND file_path LIKE ?"
		params = append(params, "%"+opts.File+"%")
	}
	return where, params
}

// ---- DefSymbol ----

func (s *SQLiteStore) DefSymbol(ctx context.Context, name, pkg string) ([]store.SymbolRow, error) {
	const cols = `SELECT fqname, kind, file_path, line, col, signature, package_path FROM symbols`

	scanRows := func(q string, args ...any) ([]store.SymbolRow, error) {
		rows, err := s.db.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var results []store.SymbolRow
		for rows.Next() {
			var r store.SymbolRow
			if err := rows.Scan(&r.FQName, &r.Kind, &r.File, &r.Line, &r.Col, &r.Signature, &r.PackagePath); err != nil {
				return nil, err
			}
			r.Name = shortName(r.FQName)
			results = append(results, r)
		}
		return results, rows.Err()
	}

	// 1. Exact fqname match.
	q := cols + ` WHERE fqname = ?`
	qargs := []any{name}
	if pkg != "" {
		q += ` AND package_path LIKE ?`
		qargs = append(qargs, pkg+"%")
	}
	results, err := scanRows(q, qargs...)
	if err != nil {
		return nil, fmt.Errorf("DefSymbol fqname lookup: %w", err)
	}
	if len(results) == 0 {
		baseName := stripInstantiationArgs(name)
		if baseName != name {
			qargs = []any{baseName}
			if pkg != "" {
				qargs = append(qargs, pkg+"%")
			}
			results, err = scanRows(q, qargs...)
			if err != nil {
				return nil, fmt.Errorf("DefSymbol normalized fqname lookup: %w", err)
			}
		}
	}

	// 2. Exact name match (may be ambiguous).
	if len(results) == 0 {
		q = cols + ` WHERE name = ?`
		qargs = []any{name}
		if pkg != "" {
			q += ` AND package_path LIKE ?`
			qargs = append(qargs, pkg+"%")
		}
		q += ` ORDER BY fqname LIMIT 20`
		results, err = scanRows(q, qargs...)
		if err != nil {
			return nil, fmt.Errorf("DefSymbol name lookup: %w", err)
		}
		if len(results) == 0 {
			baseName := stripInstantiationArgs(name)
			if baseName != name {
				qargs = []any{baseName}
				if pkg != "" {
					qargs = append(qargs, pkg+"%")
				}
				qargs = append(qargs, 20)
				results, err = scanRows(q, qargs...)
				if err != nil {
					return nil, fmt.Errorf("DefSymbol normalized name lookup: %w", err)
				}
			}
		}
	}

	return results, nil
}

// ---- ListPackages ----

func (s *SQLiteStore) ListPackages(ctx context.Context) ([]store.PackageRow, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT
    package_path,
    COUNT(*) AS symbol_count,
    SUM(CASE WHEN kind IN ('func','method') THEN 1 ELSE 0 END) AS func_count,
    SUM(CASE WHEN exported = 1 THEN 1 ELSE 0 END) AS exported_count
FROM symbols
GROUP BY package_path
ORDER BY package_path
`)
	if err != nil {
		return nil, fmt.Errorf("ListPackages: %w", err)
	}
	defer rows.Close()

	results := make([]store.PackageRow, 0)
	for rows.Next() {
		var r store.PackageRow
		if err := rows.Scan(&r.Path, &r.SymbolCount, &r.FuncCount, &r.ExportedCount); err != nil {
			return nil, fmt.Errorf("ListPackages scan: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// ---- HealthStats ----

func (s *SQLiteStore) HealthStats(ctx context.Context) (*store.HealthResult, error) {
	result := &store.HealthResult{}

	err := s.db.QueryRowContext(ctx,
		`SELECT tool_version, go_version, indexed_at, root, warnings FROM index_meta ORDER BY id DESC LIMIT 1`).
		Scan(&result.ToolVersion, &result.GoVersion, &result.IndexedAt, &result.Root, &result.Warnings)
	if err != nil {
		return nil, fmt.Errorf("no index_meta found (run gosymdb index first): %w", err)
	}

	if err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM symbols),
		(SELECT COUNT(*) FROM calls),
		(SELECT COUNT(*) FROM unresolved_calls),
		(SELECT COUNT(*) FROM type_refs)`).Scan(
		&result.SymbolCount, &result.CallCount, &result.UnresolvedCount, &result.TypeRefCount,
	); err != nil {
		log.Printf("warn: health counts: %v", err)
	}

	if result.UnresolvedCount > 0 {
		urows, err := s.db.QueryContext(ctx,
			`SELECT expr, COUNT(*) AS n FROM unresolved_calls GROUP BY expr ORDER BY n DESC LIMIT 10`)
		if err == nil {
			defer urows.Close()
			for urows.Next() {
				var ue store.UnresolvedExpr
				if urows.Scan(&ue.Expr, &ue.Count) == nil {
					result.TopUnresolved = append(result.TopUnresolved, ue)
				}
			}
		}
	}

	return result, nil
}

// ---- FindImplementors ----

func (s *SQLiteStore) FindImplementors(ctx context.Context, opts store.ImplementorsOpts) ([]store.ImplementorRow, error) {
	if strings.TrimSpace(opts.Iface) == "" && strings.TrimSpace(opts.Type) == "" {
		return nil, fmt.Errorf("FindImplementors: --iface or --type is required")
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 200
	}

	return scanImplementorRows(s, ctx, opts, limit)
}

func scanImplementorRows(s *SQLiteStore, ctx context.Context, opts store.ImplementorsOpts, limit int) ([]store.ImplementorRow, error) {
	var q string
	var args []any
	if opts.Iface != "" {
		ifaceBase := stripInstantiationArgs(opts.Iface)
		q = `
SELECT iface_fqname, impl_fqname, is_pointer
FROM implements
WHERE iface_fqname = ? OR iface_fqname LIKE ? OR iface_fqname = ? OR iface_fqname LIKE ?
ORDER BY impl_fqname
LIMIT ?`
		args = []any{opts.Iface, "%" + opts.Iface + "%", ifaceBase, "%" + ifaceBase + "%", limit}
	} else {
		typeBase := stripInstantiationArgs(opts.Type)
		q = `
SELECT iface_fqname, impl_fqname, is_pointer
FROM implements
WHERE impl_fqname = ? OR impl_fqname LIKE ? OR impl_fqname = ? OR impl_fqname LIKE ?
ORDER BY iface_fqname
LIMIT ?`
		args = []any{opts.Type, "%" + opts.Type + "%", typeBase, "%" + typeBase + "%", limit}
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("FindImplementors query: %w", err)
	}
	defer rows.Close()

	results := make([]store.ImplementorRow, 0)
	for rows.Next() {
		var r store.ImplementorRow
		var isPtr int
		if err := rows.Scan(&r.Iface, &r.Impl, &isPtr); err != nil {
			return nil, fmt.Errorf("FindImplementors scan: %w", err)
		}
		r.IsPointer = isPtr != 0
		results = append(results, r)
	}
	return results, rows.Err()
}

// ---- FindReferences / CountReferences ----

func (s *SQLiteStore) FindReferences(ctx context.Context, opts store.ReferencesOpts) (store.ReferencesResult, error) {
	where, params := buildRefsWhere(opts)

	// Total count for truncation detection.
	var totalMatched int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM type_refs t`+where, params...).Scan(&totalMatched); err != nil {
		log.Printf("warn: references count: %v", err)
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 200
	}
	q := `SELECT t.from_fqname, t.to_fqname, t.ref_kind, t.file_path, t.line, t.col, t.expr, t.package_path
FROM type_refs t` + where + ` ORDER BY t.ref_kind, t.from_fqname, t.file_path, t.line LIMIT ?`
	queryParams := append(append([]any{}, params...), limit)

	rows, err := s.db.QueryContext(ctx, q, queryParams...)
	if err != nil {
		return store.ReferencesResult{}, fmt.Errorf("FindReferences query: %w", err)
	}
	defer rows.Close()

	refs := make([]store.RefRow, 0)
	for rows.Next() {
		var r store.RefRow
		if err := rows.Scan(&r.FromFQName, &r.ToFQName, &r.RefKind, &r.File, &r.Line, &r.Col, &r.Expr, &r.PackagePath); err != nil {
			return store.ReferencesResult{}, fmt.Errorf("FindReferences scan: %w", err)
		}
		refs = append(refs, r)
	}
	if err := rows.Err(); err != nil {
		return store.ReferencesResult{}, fmt.Errorf("FindReferences rows: %w", err)
	}

	return store.ReferencesResult{Refs: refs, TotalMatched: totalMatched}, nil
}

func (s *SQLiteStore) CountReferences(ctx context.Context, opts store.ReferencesOpts) (int, error) {
	where, params := buildRefsWhere(opts)
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM type_refs t`+where, params...).Scan(&n); err != nil {
		return 0, fmt.Errorf("CountReferences: %w", err)
	}
	return n, nil
}

func buildRefsWhere(opts store.ReferencesOpts) (string, []any) {
	where := ` WHERE t.to_fqname = ?`
	params := []any{opts.Symbol}
	if opts.RefKind != "" {
		where += ` AND t.ref_kind = ?`
		params = append(params, opts.RefKind)
	}
	if opts.Pkg != "" {
		where += ` AND t.package_path LIKE ?`
		params = append(params, opts.Pkg+"%")
	}
	if opts.From != "" {
		where += ` AND t.from_fqname LIKE ?`
		params = append(params, "%"+opts.From+"%")
	}
	return where, params
}

// ---- Call graph — callers ----

// DirectCallers implements store.ReadStore.
// It performs a single BFS hop: returns all callers of any symbol in targets,
// optionally filtered to callers whose package_path starts with pkg.
func (s *SQLiteStore) DirectCallers(ctx context.Context, targets []string, pkg string, limit int) ([]store.CallerRow, error) {
	if len(targets) == 0 {
		return nil, nil
	}

	placeholders := strings.Repeat("?,", len(targets))
	placeholders = placeholders[:len(placeholders)-1]
	q := `SELECT from_fqname, to_fqname, file_path, line, col, kind FROM calls WHERE to_fqname IN (` + placeholders + `)`
	args := make([]any, len(targets))
	for i, s := range targets {
		args[i] = s
	}

	if pkg != "" {
		q += ` AND package_path LIKE ?`
		args = append(args, pkg+"%")
	}
	q += ` ORDER BY kind DESC, from_fqname, file_path, line LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []store.CallerRow
	for rows.Next() {
		var r store.CallerRow
		if err := rows.Scan(&r.From, &r.To, &r.File, &r.Line, &r.Col, &r.Kind); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountDirectCallers implements store.ReadStore.
func (s *SQLiteStore) CountDirectCallers(ctx context.Context, symbol string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM calls WHERE to_fqname = ?`, symbol).Scan(&n)
	return n, err
}

// FuzzyCallTargets implements store.ReadStore.
// Returns distinct to_fqnames that contain symbol as a substring but are not
// identical to symbol.
func (s *SQLiteStore) FuzzyCallTargets(ctx context.Context, symbol string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT to_fqname FROM calls WHERE to_fqname LIKE ? AND to_fqname != ?`,
		"%"+symbol+"%", symbol)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// UnresolvedCallers implements store.ReadStore.
func (s *SQLiteStore) UnresolvedCallers(ctx context.Context, symbol string, fuzzy bool, limit int) ([]store.UnresolvedCallerRow, error) {
	q := `SELECT from_fqname, expr, file_path, line, col FROM unresolved_calls WHERE expr = ?`
	args := []any{symbol}
	if fuzzy {
		q += ` OR expr LIKE ?`
		args = append(args, "%"+symbol+"%")
	}
	q += ` ORDER BY from_fqname, file_path, line LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []store.UnresolvedCallerRow
	for rows.Next() {
		var r store.UnresolvedCallerRow
		if err := rows.Scan(&r.From, &r.Expr, &r.File, &r.Line, &r.Col); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---- Call graph — callees ----

// DirectCallees implements store.ReadStore.
func (s *SQLiteStore) DirectCallees(ctx context.Context, opts store.CalleesOpts) ([]store.CalleeRow, error) {
	fromClause := `c.from_fqname = ?`
	fromArgs := []any{opts.Symbol}
	if opts.Fuzzy {
		fromClause = `(c.from_fqname = ? OR c.from_fqname LIKE ?)`
		fromArgs = []any{opts.Symbol, "%" + opts.Symbol + "%"}
	}

	pkgClause := ""
	var pkgArg []any
	if opts.Pkg != "" {
		pkgClause = ` AND c.to_fqname LIKE ?`
		pkgArg = []any{opts.Pkg + "%"}
	}

	var out []store.CalleeRow

	if opts.Unique {
		q := `SELECT DISTINCT c.to_fqname, c.kind, COALESCE(s.package_path,'') FROM calls c LEFT JOIN symbols s ON s.fqname = c.to_fqname WHERE ` + fromClause + pkgClause + ` ORDER BY c.to_fqname LIMIT ?`
		args := append(append(slices.Clone(fromArgs), pkgArg...), opts.Limit)
		rows, err := s.db.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var r store.CalleeRow
			if err := rows.Scan(&r.FQName, &r.Kind, &r.PackagePath); err != nil {
				return nil, err
			}
			out = append(out, r)
		}
		return out, rows.Err()
	}

	q := `SELECT c.to_fqname, c.file_path, c.line, c.col, c.kind, COALESCE(s.package_path,'') FROM calls c LEFT JOIN symbols s ON s.fqname = c.to_fqname WHERE ` + fromClause + pkgClause + ` ORDER BY c.kind DESC, c.to_fqname, c.file_path, c.line LIMIT ?`
	args := append(append(slices.Clone(fromArgs), pkgArg...), opts.Limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r store.CalleeRow
		if err := rows.Scan(&r.FQName, &r.File, &r.Line, &r.Col, &r.Kind, &r.PackagePath); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountCallees implements store.ReadStore.
func (s *SQLiteStore) CountCallees(ctx context.Context, opts store.CalleesOpts) (int, error) {
	fromClause := `c.from_fqname = ?`
	fromArgs := []any{opts.Symbol}
	if opts.Fuzzy {
		fromClause = `(c.from_fqname = ? OR c.from_fqname LIKE ?)`
		fromArgs = []any{opts.Symbol, "%" + opts.Symbol + "%"}
	}

	pkgClause := ""
	var pkgArg []any
	if opts.Pkg != "" {
		pkgClause = ` AND c.to_fqname LIKE ?`
		pkgArg = []any{opts.Pkg + "%"}
	}

	var cq string
	if opts.Unique {
		cq = `SELECT COUNT(DISTINCT c.to_fqname) FROM calls c WHERE ` + fromClause + pkgClause
	} else {
		cq = `SELECT COUNT(*) FROM calls c WHERE ` + fromClause + pkgClause
	}
	cargs := append(slices.Clone(fromArgs), pkgArg...)

	var n int
	if err := s.db.QueryRowContext(ctx, cq, cargs...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// ---- Blast radius ----

// BlastRadius implements store.ReadStore.
func (s *SQLiteStore) BlastRadius(ctx context.Context, opts store.BlastRadiusOpts) ([]store.BlastRadiusRow, error) {
	seedWhere := `c.to_fqname = ?`
	queryArgs := []any{opts.Symbol}
	if opts.Fuzzy {
		seedWhere = `(c.to_fqname = ? OR c.to_fqname LIKE ?)`
		queryArgs = append(queryArgs, "%"+opts.Symbol+"%")
	}

	testFilter := ""
	if opts.ExcludeTests {
		testFilter = " AND INSTR(c.file_path, '_test.go') = 0"
	}

	pkgFilter := ""
	if opts.Pkg != "" {
		pkgFilter = ` AND c.package_path LIKE ?`
	}

	query := `
WITH RECURSIVE blast(caller, depth) AS (
  SELECT DISTINCT c.from_fqname, 1
  FROM calls c
  WHERE ` + seedWhere + testFilter + pkgFilter + `

  UNION

  SELECT DISTINCT c.from_fqname, b.depth + 1
  FROM calls c
  INNER JOIN blast b ON c.to_fqname = b.caller
  WHERE b.depth < ?` + testFilter + pkgFilter + `
)
SELECT b.caller, MIN(b.depth) AS depth, COALESCE(s.file_path, ''), COALESCE(s.line, 0), COALESCE(s.package_path, '')
FROM blast b
LEFT JOIN symbols s ON s.fqname = b.caller
GROUP BY b.caller
ORDER BY MIN(b.depth), b.caller
LIMIT ?`

	if opts.Pkg != "" {
		queryArgs = append(queryArgs, opts.Pkg+"%")
	}
	queryArgs = append(queryArgs, opts.Depth)
	if opts.Pkg != "" {
		queryArgs = append(queryArgs, opts.Pkg+"%")
	}
	queryArgs = append(queryArgs, opts.Limit)

	rows, err := s.db.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []store.BlastRadiusRow
	for rows.Next() {
		var r store.BlastRadiusRow
		if err := rows.Scan(&r.FQName, &r.Depth, &r.File, &r.Line, &r.Package); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---- Phase 3: dead, trace, hint, env, stale ----

// DeadSymbols implements store.ReadStore.
func (s *SQLiteStore) DeadSymbols(ctx context.Context, opts store.DeadOpts) (store.DeadResult, error) {
	where := `
WHERE s.kind IN ('func', 'method')
  AND s.name NOT IN ('init', 'main')
  AND s.name NOT LIKE 'Test%'
  AND s.name NOT LIKE 'Benchmark%'
  AND s.name NOT LIKE 'Example%'
  AND s.name NOT LIKE 'Fuzz%'
  AND NOT EXISTS (SELECT 1 FROM calls c WHERE c.to_fqname = s.fqname)
  AND NOT EXISTS (SELECT 1 FROM impl_methods im WHERE im.impl_fqname = s.fqname)
`
	var params []any
	if !opts.IncludeExported {
		where += "  AND s.exported = 0\n"
	}
	if opts.Kind != "" {
		where += "  AND s.kind = ?\n"
		params = append(params, opts.Kind)
	}
	if opts.Pkg != "" {
		where += "  AND s.package_path LIKE ?\n"
		params = append(params, opts.Pkg+"%")
	}

	var totalMatched int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM symbols s`+where, params...,
	).Scan(&totalMatched); err != nil {
		return store.DeadResult{}, fmt.Errorf("DeadSymbols count: %w", err)
	}

	q := `SELECT s.fqname, s.name, s.kind, s.package_path, s.file_path, s.line, s.col, s.signature FROM symbols s` +
		where + "ORDER BY s.package_path, s.fqname\nLIMIT ?"
	rows, err := s.db.QueryContext(ctx, q, append(params, opts.Limit)...)
	if err != nil {
		return store.DeadResult{}, fmt.Errorf("DeadSymbols query: %w", err)
	}
	defer rows.Close()

	var syms []store.SymbolRow
	for rows.Next() {
		var r store.SymbolRow
		if err := rows.Scan(&r.FQName, &r.Name, &r.Kind, &r.PackagePath, &r.File, &r.Line, &r.Col, &r.Signature); err != nil {
			return store.DeadResult{}, err
		}
		syms = append(syms, r)
	}
	if err := rows.Err(); err != nil {
		return store.DeadResult{}, err
	}
	return store.DeadResult{Symbols: syms, TotalMatched: totalMatched}, nil
}

// TraceSymbol implements store.ReadStore.
// Returns nil if the symbol is not found.
func (s *SQLiteStore) TraceSymbol(ctx context.Context, symbol string, callerLimit, calleeLimit int) (*store.TraceResult, error) {
	// --- symbol lookup ---
	symRows, err := s.db.QueryContext(ctx,
		`SELECT fqname, name, kind, file_path, line, col, signature, package_path
		 FROM symbols WHERE fqname = ? LIMIT 1`, symbol)
	if err != nil {
		return nil, fmt.Errorf("TraceSymbol lookup: %w", err)
	}
	defer symRows.Close()
	if !symRows.Next() {
		return nil, nil // symbol not found
	}
	var sym store.SymbolRow
	if err := symRows.Scan(&sym.FQName, &sym.Name, &sym.Kind, &sym.File, &sym.Line, &sym.Col, &sym.Signature, &sym.PackagePath); err != nil {
		return nil, err
	}
	symRows.Close()

	// --- direct callers ---
	callerRows, err := s.db.QueryContext(ctx,
		`SELECT from_fqname, to_fqname, file_path, line, col, kind FROM calls
		 WHERE to_fqname = ? ORDER BY from_fqname LIMIT ?`,
		symbol, callerLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("TraceSymbol callers: %w", err)
	}
	defer callerRows.Close()
	callers := make([]store.CallerRow, 0)
	for callerRows.Next() {
		var r store.CallerRow
		if err := callerRows.Scan(&r.From, &r.To, &r.File, &r.Line, &r.Col, &r.Kind); err != nil {
			return nil, err
		}
		r.Name = shortName(r.From)
		callers = append(callers, r)
	}
	if err := callerRows.Err(); err != nil {
		return nil, err
	}

	// --- direct callees ---
	calleeRows, err := s.db.QueryContext(ctx,
		`SELECT c.to_fqname, c.file_path, c.line, c.col, c.kind, COALESCE(s2.package_path,'')
		 FROM calls c
		 LEFT JOIN symbols s2 ON s2.fqname = c.to_fqname
		 WHERE c.from_fqname = ? ORDER BY c.to_fqname LIMIT ?`,
		symbol, calleeLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("TraceSymbol callees: %w", err)
	}
	defer calleeRows.Close()
	callees := make([]store.CalleeRow, 0)
	for calleeRows.Next() {
		var r store.CalleeRow
		if err := calleeRows.Scan(&r.FQName, &r.File, &r.Line, &r.Col, &r.Kind, &r.PackagePath); err != nil {
			return nil, err
		}
		r.Name = shortName(r.FQName)
		callees = append(callees, r)
	}
	if err := calleeRows.Err(); err != nil {
		return nil, err
	}

	// --- blast-radius summary (recursive CTE, depth <= 5) ---
	const brDepth = 5
	const brLimit = 1000
	brRows, err := s.db.QueryContext(ctx, `
WITH RECURSIVE blast(caller, depth) AS (
  SELECT DISTINCT from_fqname, 1 FROM calls WHERE to_fqname = ?
  UNION
  SELECT DISTINCT c.from_fqname, b.depth + 1
  FROM calls c INNER JOIN blast b ON c.to_fqname = b.caller
  WHERE b.depth < ?
)
SELECT caller, MIN(depth) FROM blast GROUP BY caller ORDER BY MIN(depth) LIMIT ?`,
		symbol, brDepth, brLimit+1,
	)
	if err != nil {
		return nil, fmt.Errorf("TraceSymbol blast: %w", err)
	}
	defer brRows.Close()
	brTotal := 0
	brMaxDepth := 0
	brTrunc := false
	for brRows.Next() {
		var caller string
		var d int
		if err := brRows.Scan(&caller, &d); err != nil {
			return nil, err
		}
		brTotal++
		if brTotal > brLimit {
			brTrunc = true
			break
		}
		if d > brMaxDepth {
			brMaxDepth = d
		}
	}
	if err := brRows.Err(); err != nil {
		return nil, err
	}

	return &store.TraceResult{
		Symbol:     &sym,
		Callers:    callers,
		Callees:    callees,
		BlastTotal: brTotal,
		BlastDepth: brMaxDepth,
		BlastTrunc: brTrunc,
	}, nil
}

// ResolveSymbolName implements store.ReadStore.
func (s *SQLiteStore) ResolveSymbolName(ctx context.Context, name, pkg string) ([]string, error) {
	q := `SELECT fqname FROM symbols WHERE name = ?`
	args := []any{name}
	if pkg != "" {
		q += ` AND package_path LIKE ?`
		args = append(args, pkg+"%")
	}
	q += ` ORDER BY fqname LIMIT 5`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var f string
		if rows.Scan(&f) == nil {
			out = append(out, f)
		}
	}
	return out, rows.Err()
}

// SymbolHint implements store.ReadStore.
func (s *SQLiteStore) SymbolHint(ctx context.Context, symbol string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT fqname FROM symbols WHERE fqname LIKE ? LIMIT 3`, "%"+symbol+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var f string
		if rows.Scan(&f) == nil {
			out = append(out, f)
		}
	}
	return out, rows.Err()
}

// InterfaceDispatchHint implements store.ReadStore.
func (s *SQLiteStore) InterfaceDispatchHint(ctx context.Context, symbol string) ([]string, error) {
	lastDot := strings.LastIndex(symbol, ".")
	if lastDot <= 0 {
		return nil, nil
	}
	typeFQN := symbol[:lastDot]
	bareFQN := strings.Replace(typeFQN, ".*", ".", 1)

	rows, err := s.db.QueryContext(ctx, `
SELECT DISTINCT iface_fqname
FROM implements
WHERE impl_fqname = ? OR impl_fqname = ? OR impl_fqname LIKE ?
LIMIT 5`,
		typeFQN, bareFQN, "%"+bareFQN)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var fq string
		if rows.Scan(&fq) == nil {
			out = append(out, fq)
		}
	}
	return out, rows.Err()
}

// HasFileTracking implements store.ReadStore.
func (s *SQLiteStore) HasFileTracking(ctx context.Context) (bool, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM package_files LIMIT 1`).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// IndexedCommit implements store.ReadStore.
func (s *SQLiteStore) IndexedCommit(ctx context.Context) (string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT indexed_commit FROM index_meta ORDER BY id DESC LIMIT 1`)
	if err != nil {
		return "", fmt.Errorf("IndexedCommit: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return "", nil
	}
	var commit string
	if err := rows.Scan(&commit); err != nil {
		return "", err
	}
	return commit, rows.Err()
}

// PackageFiles implements store.ReadStore.
func (s *SQLiteStore) PackageFiles(ctx context.Context, moduleRoot, packagePath string) ([]store.PackageFile, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT file_path, file_hash FROM package_files WHERE module_root = ? AND package_path = ?`,
		moduleRoot, packagePath,
	)
	if err != nil {
		return nil, fmt.Errorf("PackageFiles: %w", err)
	}
	defer rows.Close()
	var out []store.PackageFile
	for rows.Next() {
		var pf store.PackageFile
		if err := rows.Scan(&pf.FilePath, &pf.FileHash); err != nil {
			return nil, err
		}
		out = append(out, pf)
	}
	return out, rows.Err()
}

// StoredFilesHash implements store.ReadStore.
func (s *SQLiteStore) StoredFilesHash(ctx context.Context, moduleRoot, packagePath string) (string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT files_hash FROM package_meta WHERE module_root = ? AND package_path = ?`,
		moduleRoot, packagePath,
	)
	if err != nil {
		return "", fmt.Errorf("StoredFilesHash: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return "", nil
	}
	var h string
	if err := rows.Scan(&h); err != nil {
		return "", err
	}
	return h, rows.Err()
}

// AllPackagePaths implements store.ReadStore.
func (s *SQLiteStore) AllPackagePaths(ctx context.Context) ([]store.PackageMetaRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT module_root, package_path FROM package_meta`)
	if err != nil {
		return nil, fmt.Errorf("AllPackagePaths: %w", err)
	}
	defer rows.Close()
	var out []store.PackageMetaRow
	for rows.Next() {
		var r store.PackageMetaRow
		if err := rows.Scan(&r.ModuleRoot, &r.PackagePath); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
