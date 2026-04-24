package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/walkindude/gosymdb/store"
	"github.com/walkindude/gosymdb/store/sqlite"

	"github.com/spf13/cobra"
)

func newFindCmd() *cobra.Command {
	var query string
	var pkg string
	var kind string
	var file string
	var limit int
	var countOnly bool

	cmd := &cobra.Command{
		Use:           "find",
		Short:         "Search the index for symbols by name, package, or signature substring",
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
			return execFind(st, dbPath, query, pkg, kind, file, limit, countOnly, asJSON)
		},
	}

	setJSONHelpFunc(cmd, "gosymdb find")

	cmd.Flags().StringVar(&query, "q", "", "substring to match against fqname/name/signature (optional)")
	cmd.Flags().StringVar(&pkg, "pkg", "", "filter by package path prefix")
	cmd.Flags().StringVar(&kind, "kind", "", "optional kind filter (func, method, type, interface, var, const)")
	cmd.Flags().StringVar(&file, "file", "", "filter by file path substring; makes --q optional")
	cmd.Flags().IntVar(&limit, "limit", 100, "row limit")
	cmd.Flags().BoolVar(&countOnly, "count", false, "print only the symbol count as an integer (no JSON envelope)")

	return cmd
}

func execFind(rs store.ReadStore, dbPath, query, pkg, kind, file string, limit int, countOnly, asJSON bool) error {
	if err := validateEnumFlag("--kind", kind, findKinds); err != nil {
		return err
	}

	opts := store.FindOpts{
		Query: query,
		Pkg:   pkg,
		Kind:  kind,
		File:  file,
		Limit: limit,
	}

	ctx := context.Background()

	// --count: issue a real COUNT(*) query (no LIMIT) so the result is accurate.
	if countOnly {
		n, err := rs.CountSymbols(ctx, opts)
		if err != nil {
			return err
		}
		fmt.Println(n)
		return nil
	}

	result, err := rs.FindSymbols(ctx, opts)
	if err != nil {
		return err
	}

	// Warn when --pkg looks like a short name (no dot and no slash), since the
	// filter requires a full package path prefix such as github.com/owner/repo/pkg.
	pkgWarning := ""
	if pkg != "" && !strings.Contains(pkg, ".") && !strings.Contains(pkg, "/") {
		pkgWarning = fmt.Sprintf("--pkg requires a full package path prefix (e.g. github.com/owner/repo/internal/%s); short names like %q will not match", pkg, pkg)
		if !asJSON {
			fmt.Fprintf(os.Stderr, "warning: %s\n", pkgWarning)
		}
	}

	type symRow struct {
		FQName      string `json:"fqname"`
		Name        string `json:"name"`
		Kind        string `json:"kind"`
		File        string `json:"file"`
		Line        int    `json:"line"`
		Col         int    `json:"col"`
		Signature   string `json:"signature"`
		PackagePath string `json:"package_path"`
	}

	if asJSON {
		out := make([]symRow, 0, len(result.Symbols))
		for _, s := range result.Symbols {
			out = append(out, symRow{
				FQName:      s.FQName,
				Name:        s.Name,
				Kind:        s.Kind,
				File:        s.File,
				Line:        s.Line,
				Col:         s.Col,
				Signature:   s.Signature,
				PackagePath: s.PackagePath,
			})
		}
		payload := map[string]any{
			"symbols":       out,
			"count":         len(out),
			"total_matched": result.TotalMatched,
			"truncated":     result.TotalMatched > limit,
			"env":           collectEnv(dbPath),
		}
		if pkgWarning != "" {
			payload["warning"] = pkgWarning
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		return enc.Encode(payload)
	}

	for _, s := range result.Symbols {
		fmt.Printf("%s\t%s\t%s:%d:%d\t%s\n", s.FQName, s.Kind, s.File, s.Line, s.Col, s.Signature)
	}
	return nil
}
