package cmd

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/walkindude/gosymdb/store"
	"github.com/walkindude/gosymdb/store/sqlite"

	"github.com/spf13/cobra"
)

func newCallersCmd() *cobra.Command {
	var symbol string
	var limit int
	var fuzzy bool
	var pkg string
	var includeUnresolved bool
	var depth int
	var isTest bool
	var countOnly bool

	cmd := &cobra.Command{
		Use:           "callers",
		Short:         "Direct callers of a symbol (who calls X?)",
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
			return execCallers(rs, db, symbol, limit, fuzzy, pkg, includeUnresolved, depth, isTest, countOnly, asJSON, dbPath)
		},
	}

	setJSONHelpFunc(cmd, "gosymdb callers")

	cmd.Flags().StringVar(&symbol, "symbol", "", "callee fqname, exact match by default (required)")
	cmd.Flags().IntVar(&limit, "limit", 200, "row limit")
	cmd.Flags().BoolVar(&fuzzy, "fuzzy", false, "also match symbols containing --symbol as a substring (LIKE); falls back to LIKE only when exact match returns nothing")
	cmd.Flags().StringVar(&pkg, "pkg", "", "restrict callers to this package path prefix")
	cmd.Flags().BoolVar(&includeUnresolved, "include-unresolved", false, "also show unresolved call references")
	cmd.Flags().IntVar(&depth, "depth", 1, "transitive caller depth (1 = direct callers only, max 10)")
	cmd.Flags().BoolVar(&isTest, "is-test", false, "only include callers from test files (*_test.go)")
	cmd.Flags().BoolVar(&countOnly, "count", false, "print only the callers count as an integer (no JSON envelope)")

	return cmd
}

type callerRow struct {
	From  string `json:"from"`
	Name  string `json:"name"` // short name of the caller (everything after package path)
	To    string `json:"to"`
	File  string `json:"file"`
	Line  int    `json:"line"`
	Col   int    `json:"col"`
	Kind  string `json:"kind"`
	Depth int    `json:"depth"`
}

type callersUnresolvedRow struct {
	From string `json:"from"`
	Expr string `json:"expr"`
	File string `json:"file"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
}

// buildCallersBFS performs a BFS over the call graph up to `depth` hops.
func buildCallersBFS(rs store.ReadStore, symbol string, fuzzy bool, pkg string, depth, limit int, isTest bool) ([]callerRow, error) {
	ctx := context.Background()

	visited := map[string]bool{symbol: true}
	toVisit := []string{symbol}
	if fuzzy {
		exactCount, _ := rs.CountDirectCallers(ctx, symbol)
		if exactCount == 0 {
			fuzzyTargets, ferr := rs.FuzzyCallTargets(ctx, symbol)
			if ferr == nil {
				for _, s := range fuzzyTargets {
					if !visited[s] {
						visited[s] = true
						toVisit = append(toVisit, s)
					}
				}
			}
		}
	}

	allCallers := make([]callerRow, 0)
	for d := 1; d <= depth && len(toVisit) > 0; d++ {
		storeRows, err := rs.DirectCallers(ctx, toVisit, pkg, limit)
		if err != nil {
			return nil, err
		}
		nextVisit := []string{}
		for _, sr := range storeRows {
			r := callerRow{
				From: sr.From,
				To:   sr.To,
				File: sr.File,
				Line: sr.Line,
				Col:  sr.Col,
				Kind: sr.Kind,
			}
			r.Name = shortName(r.From)
			r.Depth = d
			if !isTest || strings.HasSuffix(r.File, "_test.go") {
				allCallers = append(allCallers, r)
			}
			if !visited[r.From] {
				visited[r.From] = true
				nextVisit = append(nextVisit, r.From)
			}
		}
		toVisit = nextVisit
	}
	return allCallers, nil
}

// formatCallersJSON builds and writes the JSON envelope for callers output.
func formatCallersJSON(db *sql.DB, symbol, resolveNote, dbPath string, allCallers []callerRow, unresolved []callersUnresolvedRow, depth int) error {
	payload := map[string]any{
		"callers":          allCallers,
		"callers_count":    len(allCallers),
		"depth":            depth,
		"unresolved":       unresolved,
		"unresolved_count": len(unresolved),
		"env":              collectEnv(dbPath),
	}
	if resolveNote != "" {
		payload["resolved"] = resolveNote
	}
	if len(allCallers) == 0 && len(unresolved) == 0 {
		if h := interfaceDispatchHint(db, symbol); h != "" {
			payload["hint"] = h
		} else if h := symbolHint(db, symbol); h != "" {
			payload["hint"] = h
		}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	return enc.Encode(payload)
}

func execCallers(rs store.ReadStore, db *sql.DB, symbol string, limit int, fuzzy bool, pkg string, includeUnresolved bool, depth int, isTest bool, countOnly bool, asJSON bool, dbPath string) error {
	if strings.TrimSpace(symbol) == "" {
		return errors.New("--symbol is required")
	}
	if depth < 1 {
		depth = 1
	}
	if depth > 10 {
		depth = 10
	}

	if autoReindex {
		checkAndAutoReindex(db, false, false)
	}

	var resolveNote string
	symbol, resolveNote = resolveSymbolInput(db, symbol, pkg)

	allCallers, err := buildCallersBFS(rs, symbol, fuzzy, pkg, depth, limit, isTest)
	if err != nil {
		return err
	}

	unresolved := make([]callersUnresolvedRow, 0)
	if includeUnresolved {
		ctx := context.Background()
		urows, err := rs.UnresolvedCallers(ctx, symbol, fuzzy, limit)
		if err != nil {
			return err
		}
		for _, ur := range urows {
			r := callersUnresolvedRow{From: ur.From, Expr: ur.Expr, File: ur.File, Line: ur.Line, Col: ur.Col}
			if asJSON {
				unresolved = append(unresolved, r)
			} else {
				fmt.Printf("%s ~> %s\t%s:%d:%d  [unresolved]\n", r.From, r.Expr, r.File, r.Line, r.Col)
			}
		}
	}

	if countOnly {
		fmt.Println(len(allCallers))
		return nil
	}

	if asJSON {
		return formatCallersJSON(db, symbol, resolveNote, dbPath, allCallers, unresolved, depth)
	}

	if resolveNote != "" {
		fmt.Printf("# %s\n", resolveNote)
	}
	for _, r := range allCallers {
		marker := ""
		if r.Kind == "ref" {
			marker = "  [func-ref]"
		}
		depthSuffix := ""
		if depth > 1 {
			depthSuffix = fmt.Sprintf("  [d%d]", r.Depth)
		}
		fmt.Printf("%s -> %s\t%s:%d:%d%s%s\n", r.From, r.To, r.File, r.Line, r.Col, marker, depthSuffix)
	}
	return nil
}
