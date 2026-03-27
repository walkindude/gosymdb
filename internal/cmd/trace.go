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

func newTraceCmd() *cobra.Command {
	var symbol string
	var pkg string
	var callerLimit int
	var calleeLimit int

	cmd := &cobra.Command{
		Use:           "trace",
		Short:         "Single-shot overview: symbol def + direct callers + direct callees + blast-radius total",
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
			return execTrace(rs, db, symbol, pkg, callerLimit, calleeLimit, asJSON, dbPath)
		},
	}

	setJSONHelpFunc(cmd, "gosymdb trace")

	cmd.Flags().StringVar(&symbol, "symbol", "", "fqname or short name to trace (required)")
	cmd.Flags().StringVar(&pkg, "pkg", "", "disambiguate short names by package path prefix")
	cmd.Flags().IntVar(&callerLimit, "caller-limit", 20, "max direct callers to return")
	cmd.Flags().IntVar(&calleeLimit, "callee-limit", 20, "max direct callees to return")

	return cmd
}

func execTrace(rs store.ReadStore, db *sql.DB, symbol, pkg string, callerLimit, calleeLimit int, asJSON bool, dbPath string) error {
	if strings.TrimSpace(symbol) == "" {
		return errors.New("--symbol is required")
	}

	if autoReindex {
		checkAndAutoReindex(db, false, false)
	}

	ctx := context.Background()

	// Resolve short names via the store.
	var resolveNote string
	if !strings.Contains(symbol, "/") {
		names, err := rs.ResolveSymbolName(ctx, symbol, pkg)
		if err == nil {
			switch len(names) {
			case 0:
				// no match, keep symbol unchanged
			case 1:
				resolveNote = "resolved short name '" + symbol + "' to '" + names[0] + "'"
				symbol = names[0]
			default:
				resolveNote = "ambiguous short name '" + symbol + "' matches: " + strings.Join(names, " | ") + " — use exact fqname or --pkg to disambiguate"
			}
		}
	}

	res, err := rs.TraceSymbol(ctx, symbol, callerLimit, calleeLimit)
	if err != nil {
		return fmt.Errorf("trace: %w", err)
	}

	if asJSON {
		type symJSON struct {
			FQName      string `json:"fqname"`
			Name        string `json:"name"`
			Kind        string `json:"kind"`
			File        string `json:"file"`
			Line        int    `json:"line"`
			Col         int    `json:"col"`
			Signature   string `json:"signature"`
			PackagePath string `json:"package_path"`
		}
		type callEdge struct {
			FQName string `json:"fqname"`
			Name   string `json:"name"`
			File   string `json:"file"`
			Line   int    `json:"line"`
			Col    int    `json:"col"`
			Kind   string `json:"kind"`
		}
		type calleeEdge struct {
			FQName      string `json:"fqname"`
			Name        string `json:"name"`
			File        string `json:"file"`
			Line        int    `json:"line"`
			Col         int    `json:"col"`
			Kind        string `json:"kind"`
			PackagePath string `json:"package_path"`
		}

		payload := map[string]any{
			"env": collectEnv(dbPath),
		}

		if res != nil {
			callers := make([]callEdge, 0, len(res.Callers))
			for _, c := range res.Callers {
				callers = append(callers, callEdge{
					FQName: c.From,
					Name:   shortName(c.From),
					File:   c.File,
					Line:   c.Line,
					Col:    c.Col,
					Kind:   c.Kind,
				})
			}
			callees := make([]calleeEdge, 0, len(res.Callees))
			for _, c := range res.Callees {
				callees = append(callees, calleeEdge{
					FQName:      c.FQName,
					Name:        shortName(c.FQName),
					File:        c.File,
					Line:        c.Line,
					Col:         c.Col,
					Kind:        c.Kind,
					PackagePath: c.PackagePath,
				})
			}
			payload["callers"] = callers
			payload["callers_count"] = len(callers)
			payload["callees"] = callees
			payload["callees_count"] = len(callees)
			payload["blast_radius"] = map[string]any{
				"total":             res.BlastTotal,
				"max_depth_reached": res.BlastDepth,
				"truncated":         res.BlastTrunc,
			}
			sym := res.Symbol
			payload["symbol"] = symJSON{
				FQName:      sym.FQName,
				Name:        sym.Name,
				Kind:        sym.Kind,
				File:        sym.File,
				Line:        sym.Line,
				Col:         sym.Col,
				Signature:   sym.Signature,
				PackagePath: sym.PackagePath,
			}
		} else {
			// Symbol not found — emit empty arrays + hint.
			payload["callers"] = []callEdge{}
			payload["callers_count"] = 0
			payload["callees"] = []calleeEdge{}
			payload["callees_count"] = 0
			payload["blast_radius"] = map[string]any{
				"total":             0,
				"max_depth_reached": 0,
				"truncated":         false,
			}
			hints, _ := rs.SymbolHint(ctx, symbol)
			if len(hints) > 0 {
				payload["hint"] = "Exact fqname mismatch. Similar: " + strings.Join(hints, " | ") + ". Use exact fqname or --fuzzy."
			} else {
				payload["hint"] = "Symbol not in index. Run: gosymdb find --q " + symbol + " --json"
			}
		}
		if resolveNote != "" {
			payload["resolved"] = resolveNote
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		return enc.Encode(payload)
	}

	// Plain-text output.
	if resolveNote != "" {
		fmt.Printf("# %s\n\n", resolveNote)
	}
	if res == nil {
		fmt.Printf("symbol not found: %s\n", symbol)
		hints, _ := rs.SymbolHint(ctx, symbol)
		if len(hints) > 0 {
			fmt.Printf("hint: Exact fqname mismatch. Similar: %s\n", strings.Join(hints, " | "))
		}
		return nil
	}

	sym := res.Symbol
	fmt.Printf("symbol:  %s  [%s]  %s:%d\n", sym.FQName, sym.Kind, sym.File, sym.Line)
	if sym.Signature != "" {
		fmt.Printf("         %s\n", sym.Signature)
	}
	fmt.Println()

	fmt.Printf("callers (%d):\n", len(res.Callers))
	for _, c := range res.Callers {
		fmt.Printf("  %-60s %s:%d\n", c.From, c.File, c.Line)
	}
	fmt.Println()

	fmt.Printf("callees (%d):\n", len(res.Callees))
	for _, c := range res.Callees {
		fmt.Printf("  %-60s %s:%d\n", c.FQName, c.File, c.Line)
	}
	fmt.Println()

	const brDepth = 5
	fmt.Printf("blast-radius: %d total callers (depth <= %d)\n", res.BlastTotal, brDepth)
	return nil
}
