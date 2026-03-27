package cmd

import (
	"database/sql"
	"strings"
)

// resolveSymbolInput tries to resolve a short symbol name to a fully-qualified name.
// If input already contains "/" it is treated as a fqname and returned unchanged.
// Otherwise the symbols table is queried by name. If exactly one match is found it is
// returned as the resolved fqname and note is set. If multiple matches are found, note
// describes the ambiguity and input is returned unchanged.
func resolveSymbolInput(db *sql.DB, input, pkg string) (fqname string, note string) {
	if strings.Contains(input, "/") {
		return input, ""
	}
	q := `SELECT fqname FROM symbols WHERE name = ?`
	args := []any{input}
	if pkg != "" {
		q += ` AND package_path LIKE ?`
		args = append(args, pkg+"%")
	}
	q += ` ORDER BY fqname LIMIT 5`
	rows, err := db.Query(q, args...)
	if err != nil {
		return input, ""
	}
	defer rows.Close()
	var matches []string
	for rows.Next() {
		var f string
		if rows.Scan(&f) == nil {
			matches = append(matches, f)
		}
	}
	if err := rows.Err(); err != nil {
		return input, ""
	}
	switch len(matches) {
	case 0:
		return input, ""
	case 1:
		return matches[0], "resolved short name '" + input + "' to '" + matches[0] + "'"
	default:
		return input, "ambiguous short name '" + input + "' matches: " + strings.Join(matches, " | ") + " — use exact fqname or --pkg to disambiguate"
	}
}

// symbolHint returns a diagnostic hint when an exact --symbol lookup returns
// 0 results. It looks for similar fqnames in the index and tells the agent
// what to do next. Returns "" if no hint is needed or the query fails.
func symbolHint(db *sql.DB, symbol string) string {
	rows, err := db.Query(`SELECT fqname FROM symbols WHERE fqname LIKE ? LIMIT 3`, "%"+symbol+"%")
	if err != nil {
		return ""
	}
	defer rows.Close()
	var matches []string
	for rows.Next() {
		var f string
		if rows.Scan(&f) == nil {
			matches = append(matches, f)
		}
	}
	if err := rows.Err(); err != nil {
		return ""
	}
	if len(matches) > 0 {
		return "Exact fqname mismatch. Similar: " + strings.Join(matches, " | ") + ". Use exact fqname or --fuzzy."
	}
	return "Symbol not in index. Run: gosymdb find --q " + symbol + " --json"
}

// interfaceDispatchHint returns a hint when the symbol is a method that
// implements one or more interfaces recorded in the implements table.
// This is useful when callers returns 0: the method may be called via interface
// dispatch rather than direct call, so the static call graph has no edges.
//
// symbol is expected to be a fully-qualified method name such as:
//
//	some/pkg.*FileWriter.Write
//	some/pkg.FileWriter.Write
//
// The function extracts the type fqname (everything before the last ".") and
// queries the implements table. It then filters the result to only interfaces
// whose method set includes the queried method name (from iface_methods table),
// preventing misleading hints for embedded-interface patterns.
// Returns "" if there are no matches or on error.
func interfaceDispatchHint(db *sql.DB, symbol string) string {
	// Extract the type fqname and method name from the last "." segment.
	// Example: "some/pkg.*FileWriter.Write" → typeFQN = "some/pkg.*FileWriter", methodName = "Write"
	lastDot := strings.LastIndex(symbol, ".")
	if lastDot <= 0 {
		return ""
	}
	typeFQN := symbol[:lastDot]
	methodName := symbol[lastDot+1:]

	// The impl_fqname in the implements table stores bare type names without a
	// pointer-receiver "*" prefix (e.g. "pkg.FileWriter", not "pkg.*FileWriter").
	// Build a version without the leading "*" on the type name component so we
	// can match both pointer and value receiver methods.
	// typeFQN looks like "some/pkg.*FileWriter"; bareFQN = "some/pkg.FileWriter".
	bareFQN := strings.Replace(typeFQN, ".*", ".", 1)

	rows, err := db.Query(`
SELECT DISTINCT iface_fqname
FROM implements
WHERE impl_fqname = ? OR impl_fqname = ? OR impl_fqname LIKE ?
LIMIT 10`,
		typeFQN, bareFQN, "%"+bareFQN)
	if err != nil {
		return ""
	}
	defer rows.Close()

	var candidates []string
	for rows.Next() {
		var fq string
		if rows.Scan(&fq) == nil {
			candidates = append(candidates, fq)
		}
	}
	if rows.Err() != nil || len(candidates) == 0 {
		return ""
	}

	// Filter candidates to only interfaces that have the queried method in their
	// method set. We use the iface_methods table populated at index time.
	// Fail-open: if iface_methods has no entry for a candidate (e.g. the interface
	// is from an external unindexed package), keep it in the hint.
	var ifaces []string
	for _, ifaceFQN := range candidates {
		var n int
		// Check if any methods are known for this interface.
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM iface_methods WHERE iface_fqname = ?`, ifaceFQN,
		).Scan(&n); err != nil || n == 0 {
			// No data in iface_methods — keep it (fail-open for external interfaces).
			ifaces = append(ifaces, ifaceFQN)
			continue
		}
		// We have method data: only include if the specific method is present.
		var has int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM iface_methods WHERE iface_fqname = ? AND method_name = ?`,
			ifaceFQN, methodName,
		).Scan(&has); err == nil && has > 0 {
			ifaces = append(ifaces, ifaceFQN)
		}
	}

	if len(ifaces) == 0 {
		return ""
	}

	return "This method may be called via interface dispatch. Try: gosymdb implementors --iface " +
		ifaces[0] + " --json  (implements: " + strings.Join(ifaces, ", ") + ")"
}
