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

func newBlastRadiusCmd() *cobra.Command {
	var symbol string
	var depth int
	var fuzzy bool
	var pkg string
	var excludeTests bool
	var limit int
	var explain bool

	cmd := &cobra.Command{
		Use:           "blast-radius",
		Short:         "Show full transitive caller impact of a symbol (who is affected, at which depth?)",
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
			return execBlastRadius(rs, db, symbol, depth, fuzzy, pkg, excludeTests, limit, asJSON, explain, dbPath)
		},
	}

	setJSONHelpFunc(cmd, "gosymdb blast-radius")

	cmd.Flags().StringVar(&symbol, "symbol", "", "callee fqname, exact match by default (required)")
	cmd.Flags().IntVar(&depth, "depth", 3, "maximum traversal depth (capped at 10)")
	cmd.Flags().BoolVar(&fuzzy, "fuzzy", false, "also match symbols containing --symbol as a substring (LIKE)")
	cmd.Flags().StringVar(&pkg, "pkg", "", "restrict results to callers in this package path prefix")
	cmd.Flags().BoolVar(&excludeTests, "exclude-tests", false, "skip test callers from results and traversal")
	cmd.Flags().IntVar(&limit, "limit", 500, "maximum rows returned")
	cmd.Flags().BoolVar(&explain, "explain", false, "show normalized inputs and traversal filters")

	return cmd
}

type blastCallerEntry struct {
	FQName  string `json:"fqname"`
	Name    string `json:"name"`
	Package string `json:"package"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Depth   int    `json:"depth"`
	IsTest  bool   `json:"is_test"`
}

type blastSummary struct {
	testCount int
	maxDepth  int
	truncated bool
}

func summarizeBlastCallers(callers []blastCallerEntry, limit int) blastSummary {
	s := blastSummary{truncated: len(callers) == limit}
	for _, c := range callers {
		if c.IsTest {
			s.testCount++
		}
		if c.Depth > s.maxDepth {
			s.maxDepth = c.Depth
		}
	}
	return s
}

func formatBlastRadiusText(symbol, resolveNote string, callers []blastCallerEntry, depth int, s blastSummary) {
	if resolveNote != "" {
		fmt.Printf("# %s\n", resolveNote)
	}
	if len(callers) == 0 {
		fmt.Printf("blast-radius: %s\n\nno callers found\n", symbol)
		return
	}
	fmt.Printf("blast-radius: %s  (depth <= %d)\n\n", symbol, depth)
	byDepth := make(map[int][]blastCallerEntry)
	for _, c := range callers {
		byDepth[c.Depth] = append(byDepth[c.Depth], c)
	}
	for d := 1; d <= s.maxDepth; d++ {
		group, ok := byDepth[d]
		if !ok {
			continue
		}
		fmt.Printf("  [depth %d]\n", d)
		for _, c := range group {
			loc := ""
			if c.File != "" {
				loc = fmt.Sprintf("%s:%d", c.File, c.Line)
			}
			fmt.Printf("    %-60s %s\n", c.FQName, loc)
		}
		fmt.Println()
	}
	suffix := ""
	if s.truncated {
		suffix = "  (truncated)"
	}
	fmt.Printf("%d callers  (%d test, %d production)  max depth %d%s\n",
		len(callers), s.testCount, len(callers)-s.testCount, s.maxDepth, suffix)
}

func execBlastRadius(rs store.ReadStore, db *sql.DB, symbol string, depth int, fuzzy bool, pkg string, excludeTests bool, limit int, asJSON bool, explain bool, dbPath string) error {
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

	rawSymbol := symbol
	var resolveNote string
	symbol, resolveNote = resolveSymbolInput(db, symbol, pkg)

	var explainData *explainPayload
	if explain {
		seedMatch := fmt.Sprintf("calls.to_fqname = %q", symbol)
		if fuzzy {
			seedMatch = fmt.Sprintf("(calls.to_fqname = %q OR calls.to_fqname LIKE %q)", symbol, "%"+symbol+"%")
		}
		explainData = &explainPayload{
			Command:        "blast-radius",
			Input:          rawSymbol,
			ResolvedSymbol: symbol,
			Resolution:     resolveNote,
			Filters: map[string]any{
				"pkg":           pkg,
				"exclude_tests": excludeTests,
				"fuzzy":         fuzzy,
			},
			Traversal: map[string]any{
				"seed_match":    seedMatch,
				"max_depth":     depth,
				"limit":         limit,
				"recursive_cte": true,
			},
			Notes: []string{
				"pkg filter applies during recursive traversal",
				"exclude-tests removes test callers from both results and traversal",
			},
		}
	}

	ctx := context.Background()
	rows, err := rs.BlastRadius(ctx, store.BlastRadiusOpts{
		Symbol:       symbol,
		Depth:        depth,
		Fuzzy:        fuzzy,
		Pkg:          pkg,
		ExcludeTests: excludeTests,
		Limit:        limit,
	})
	if err != nil {
		return err
	}

	callers := make([]blastCallerEntry, 0, len(rows))
	for _, r := range rows {
		e := blastCallerEntry{
			FQName:  r.FQName,
			Package: r.Package,
			File:    r.File,
			Line:    r.Line,
			Depth:   r.Depth,
		}
		e.Name = shortName(e.FQName)
		e.IsTest = strings.Contains(e.File, "_test.go")
		callers = append(callers, e)
	}

	s := summarizeBlastCallers(callers, limit)

	if asJSON {
		payload := map[string]any{
			"target":  symbol,
			"callers": callers,
			"summary": map[string]any{
				"total":             len(callers),
				"test":              s.testCount,
				"non_test":          len(callers) - s.testCount,
				"max_depth_reached": s.maxDepth,
				"truncated":         s.truncated,
			},
			"env": collectEnv(dbPath),
		}
		if resolveNote != "" {
			payload["resolved"] = resolveNote
		}
		addExplain(payload, explainData)
		if len(callers) == 0 {
			if h := symbolHint(db, symbol); h != "" {
				payload["hint"] = h
			}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		return enc.Encode(payload)
	}

	if explainData != nil {
		fmt.Print(formatExplainText(explainData))
		fmt.Println()
	}
	formatBlastRadiusText(symbol, resolveNote, callers, depth, s)
	return nil
}
