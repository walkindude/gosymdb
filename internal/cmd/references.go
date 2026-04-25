package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/walkindude/gosymdb/store"
	"github.com/walkindude/gosymdb/store/sqlite"

	"github.com/spf13/cobra"
)

func newReferencesCmd() *cobra.Command {
	var symbol string
	var pkg string
	var refKind string
	var from string
	var limit int
	var countOnly bool

	cmd := &cobra.Command{
		Use:           "references",
		Short:         "Find where a type is used: assertions, conversions, composite literals, embeds, field access",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			dbPath, _ := cmd.Root().PersistentFlags().GetString("db")
			asJSON, _ := cmd.Root().PersistentFlags().GetBool("json")
			st, err := sqlite.Open(dbPath)
			if err != nil {
				return err
			}
			defer st.Close()
			return execReferences(st, dbPath, symbol, pkg, refKind, from, limit, countOnly, asJSON)
		},
	}

	setJSONHelpFunc(cmd, "gosymdb references")

	cmd.Flags().StringVar(&symbol, "symbol", "", "type fqname or short name (required)")
	cmd.Flags().StringVar(&pkg, "pkg", "", "filter by package_path prefix")
	cmd.Flags().StringVar(&refKind, "ref-kind", "", "filter: type_assert, type_switch, composite_lit, conversion, field_access, embed")
	cmd.Flags().StringVar(&from, "from", "", "filter by from_fqname substring")
	cmd.Flags().IntVar(&limit, "limit", 200, "row limit")
	cmd.Flags().BoolVar(&countOnly, "count", false, "print integer count only")

	return cmd
}

type refRow struct {
	From        string `json:"from"`
	FromName    string `json:"from_name"`
	To          string `json:"to"`
	ToName      string `json:"to_name"`
	RefKind     string `json:"ref_kind"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	Col         int    `json:"col"`
	Expr        string `json:"expr"`
	PackagePath string `json:"package_path"`
}

func execReferences(rs store.ReadStore, dbPath, symbol, pkg, refKind, from string, limit int, countOnly, asJSON bool) error {
	if strings.TrimSpace(symbol) == "" {
		return errors.New("--symbol is required")
	}
	if err := validateEnumFlag("--ref-kind", refKind, refKinds); err != nil {
		return err
	}

	ctx := context.Background()
	resolvedSymbol, resolvedNote := resolveTypeRef(ctx, rs, symbol, pkg)

	opts := store.ReferencesOpts{
		Symbol:  resolvedSymbol,
		Pkg:     pkg,
		RefKind: refKind,
		From:    from,
		Limit:   limit,
	}

	if countOnly {
		n, err := rs.CountReferences(ctx, opts)
		if err != nil {
			return err
		}
		fmt.Println(n)
		return nil
	}

	result, err := rs.FindReferences(ctx, opts)
	if err != nil {
		return err
	}

	results := make([]refRow, 0, len(result.Refs))
	for _, r := range result.Refs {
		results = append(results, refRow{
			From:        r.FromFQName,
			FromName:    shortName(r.FromFQName),
			To:          r.ToFQName,
			ToName:      shortName(r.ToFQName),
			RefKind:     r.RefKind,
			File:        r.File,
			Line:        r.Line,
			Col:         r.Col,
			Expr:        r.Expr,
			PackagePath: r.PackagePath,
		})
	}

	if asJSON {
		return emitReferencesJSON(ctx, rs, dbPath, resolvedSymbol, resolvedNote, results, result.TotalMatched, limit)
	}
	printReferences(ctx, rs, resolvedSymbol, resolvedNote, results, result.TotalMatched, limit)
	return nil
}

func resolveTypeRef(ctx context.Context, rs store.ReadStore, symbol, pkg string) (string, string) {
	if strings.Contains(symbol, "/") {
		return symbol, ""
	}
	names, err := rs.ResolveSymbolName(ctx, symbol, pkg)
	if err != nil {
		return symbol, ""
	}
	switch len(names) {
	case 1:
		return names[0], "resolved short name '" + symbol + "' to '" + names[0] + "'"
	case 0:
		return symbol, ""
	default:
		return symbol, "ambiguous short name '" + symbol + "' — use exact fqname or --pkg to disambiguate"
	}
}

func emitReferencesJSON(ctx context.Context, rs store.ReadStore, dbPath, resolvedSymbol, resolvedNote string, results []refRow, totalMatched, limit int) error {
	out := map[string]any{
		"references":    results,
		"count":         len(results),
		"total_matched": totalMatched,
		"truncated":     totalMatched > limit,
		"env":           collectEnv(dbPath),
	}
	if resolvedNote != "" {
		out["resolved"] = resolvedNote
	}
	if len(results) == 0 {
		hints, _ := rs.SymbolHint(ctx, resolvedSymbol)
		if len(hints) > 0 {
			out["hint"] = "Exact fqname mismatch. Similar: " + strings.Join(hints, " | ") + ". Use exact fqname or --fuzzy."
		}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	return enc.Encode(out)
}

func printReferences(ctx context.Context, rs store.ReadStore, resolvedSymbol, resolvedNote string, results []refRow, totalMatched, limit int) {
	if resolvedNote != "" {
		fmt.Printf("note: %s\n\n", resolvedNote)
	}
	for _, r := range results {
		fmt.Printf("[%s] %s → %s  %s:%d:%d\n", r.RefKind, r.From, r.To, r.File, r.Line, r.Col)
	}
	if len(results) == 0 {
		fmt.Println("(no references found)")
		hints, _ := rs.SymbolHint(ctx, resolvedSymbol)
		if len(hints) > 0 {
			fmt.Printf("hint: Exact fqname mismatch. Similar: %s. Use exact fqname.\n", strings.Join(hints, " | "))
		}
		return
	}
	fmt.Printf("\n%d reference(s)", len(results))
	if totalMatched > limit {
		fmt.Printf(" (truncated; %d total)", totalMatched)
	}
	fmt.Println()
}
