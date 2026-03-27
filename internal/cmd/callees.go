package cmd

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/walkindude/gosymdb/store"
	"github.com/walkindude/gosymdb/store/sqlite"

	"github.com/spf13/cobra"
)

func newCalleesCmd() *cobra.Command {
	var symbol string
	var limit int
	var fuzzy bool
	var pkg string
	var includeUnresolved bool
	var unique bool
	var countOnly bool

	cmd := &cobra.Command{
		Use:           "callees",
		Short:         "List all symbols called by a symbol (what does X call?)",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			dbPath, _ := cmd.Root().PersistentFlags().GetString("db")
			asJSON, _ := cmd.Root().PersistentFlags().GetBool("json")
			rs, err := sqlite.Open(dbPath)
			if err != nil {
				return err
			}
			defer rs.Close()
			db, err := sql.Open(sqlite.DriverName, dbPath)
			if err != nil {
				return err
			}
			defer db.Close()
			return execCallees(rs, db, symbol, limit, fuzzy, pkg, includeUnresolved, unique, countOnly, asJSON, dbPath)
		},
	}

	setJSONHelpFunc(cmd, "gosymdb callees")

	cmd.Flags().StringVar(&symbol, "symbol", "", "caller fqname, exact match by default (required)")
	cmd.Flags().IntVar(&limit, "limit", 200, "row limit")
	cmd.Flags().BoolVar(&fuzzy, "fuzzy", false, "also match symbols containing --symbol as a substring (LIKE)")
	cmd.Flags().StringVar(&pkg, "pkg", "", "restrict callees to this package path prefix")
	cmd.Flags().BoolVar(&includeUnresolved, "include-unresolved", true, "show unresolved outbound calls (external packages, func literals)")
	cmd.Flags().BoolVar(&unique, "unique", false, "deduplicate: show each distinct callee once")
	cmd.Flags().BoolVar(&countOnly, "count", false, "print only the callees count as an integer (no JSON envelope)")

	return cmd
}

type calleeRow struct {
	To          string `json:"to"`
	Name        string `json:"name"`
	File        string `json:"file,omitempty"`
	Line        int    `json:"line,omitempty"`
	Col         int    `json:"col,omitempty"`
	Kind        string `json:"kind"`
	PackagePath string `json:"package_path,omitempty"`
}

type calleesUnresolvedRow struct {
	Expr string `json:"expr"`
	File string `json:"file,omitempty"`
	Line int    `json:"line,omitempty"`
	Col  int    `json:"col,omitempty"`
}

// queryUnresolvedCallees fetches unresolved outbound calls for the given symbol.
func queryUnresolvedCallees(db *sql.DB, symbol string, fuzzy, unique bool, limit int) ([]calleesUnresolvedRow, error) {
	fromClauseBare := `from_fqname = ?`
	fromArgs := []any{symbol}
	if fuzzy {
		fromClauseBare = `(from_fqname = ? OR from_fqname LIKE ?)`
		fromArgs = []any{symbol, "%" + symbol + "%"}
	}

	var result []calleesUnresolvedRow
	if unique {
		uq := `SELECT DISTINCT expr FROM unresolved_calls WHERE ` + fromClauseBare + ` ORDER BY expr LIMIT ?`
		uargs := append(slices.Clone(fromArgs), limit)
		urows, err := db.Query(uq, uargs...)
		if err != nil {
			return nil, err
		}
		defer urows.Close()
		for urows.Next() {
			var r calleesUnresolvedRow
			if err := urows.Scan(&r.Expr); err != nil {
				return nil, err
			}
			result = append(result, r)
		}
		if err := urows.Err(); err != nil {
			return nil, err
		}
	} else {
		uq := `SELECT expr, file_path, line, col FROM unresolved_calls WHERE ` + fromClauseBare + ` ORDER BY file_path, line LIMIT ?`
		uargs := append(slices.Clone(fromArgs), limit)
		urows, err := db.Query(uq, uargs...)
		if err != nil {
			return nil, err
		}
		defer urows.Close()
		for urows.Next() {
			var r calleesUnresolvedRow
			if err := urows.Scan(&r.Expr, &r.File, &r.Line, &r.Col); err != nil {
				return nil, err
			}
			result = append(result, r)
		}
		if err := urows.Err(); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func execCallees(rs store.ReadStore, db *sql.DB, symbol string, limit int, fuzzy bool, pkg string, includeUnresolved, unique, countOnly, asJSON bool, dbPath string) error {
	if strings.TrimSpace(symbol) == "" {
		return errors.New("--symbol is required")
	}

	if autoReindex {
		checkAndAutoReindex(db, false, false)
	}

	var resolveNote string
	symbol, resolveNote = resolveSymbolInput(db, symbol, pkg)

	ctx := context.Background()
	opts := store.CalleesOpts{
		Symbol: symbol,
		Fuzzy:  fuzzy,
		Pkg:    pkg,
		Unique: unique,
		Limit:  limit,
	}

	if countOnly {
		n, err := rs.CountCallees(ctx, opts)
		if err != nil {
			return err
		}
		fmt.Println(n)
		return nil
	}

	callees := make([]calleeRow, 0)
	storeRows, err := rs.DirectCallees(ctx, opts)
	if err != nil {
		return err
	}
	for _, sr := range storeRows {
		r := calleeRow{
			To:          sr.FQName,
			File:        sr.File,
			Line:        sr.Line,
			Col:         sr.Col,
			Kind:        sr.Kind,
			PackagePath: sr.PackagePath,
		}
		r.Name = shortName(r.To)
		if asJSON {
			callees = append(callees, r)
		} else {
			if unique {
				fmt.Printf("%s  [%s]\n", r.To, r.Kind)
			} else {
				marker := ""
				if r.Kind == "ref" {
					marker = "  [func-ref]"
				}
				fmt.Printf("%s -> %s\t%s:%d:%d%s\n", symbol, r.To, r.File, r.Line, r.Col, marker)
			}
		}
	}

	unresolved := make([]calleesUnresolvedRow, 0)
	if includeUnresolved {
		urows, err := queryUnresolvedCallees(db, symbol, fuzzy, unique, limit)
		if err != nil {
			return err
		}
		for _, r := range urows {
			if asJSON {
				unresolved = append(unresolved, r)
			} else {
				if unique {
					fmt.Printf("~> %s  [unresolved]\n", r.Expr)
				} else {
					fmt.Printf("%s ~> %s\t%s:%d:%d  [unresolved]\n", symbol, r.Expr, r.File, r.Line, r.Col)
				}
			}
		}
	}

	if asJSON {
		payload := map[string]any{
			"callees":          callees,
			"callees_count":    len(callees),
			"unresolved":       unresolved,
			"unresolved_count": len(unresolved),
			"env":              collectEnv(dbPath),
		}
		if resolveNote != "" {
			payload["resolved"] = resolveNote
		}
		if len(callees) == 0 && len(unresolved) == 0 {
			if h := symbolHint(db, symbol); h != "" {
				payload["hint"] = h
			}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		return enc.Encode(payload)
	}
	if resolveNote != "" {
		fmt.Printf("# %s\n", resolveNote)
	}
	return nil
}
