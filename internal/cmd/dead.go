package cmd

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"

	"github.com/walkindude/gosymdb/store"
	"github.com/walkindude/gosymdb/store/sqlite"

	"github.com/spf13/cobra"
)

func newDeadCmd() *cobra.Command {
	var kind string
	var pkg string
	var includeExported bool
	var limit int

	cmd := &cobra.Command{
		Use:           "dead",
		Short:         "List symbols with no callers (dead code candidates)",
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
			return execDead(rs, db, kind, pkg, limit, includeExported, asJSON, dbPath)
		},
	}

	setJSONHelpFunc(cmd, "gosymdb dead")

	cmd.Flags().StringVar(&kind, "kind", "", "filter by kind: func or method (default: both)")
	cmd.Flags().StringVar(&pkg, "pkg", "", "filter by package path prefix")
	cmd.Flags().BoolVar(&includeExported, "include-exported", false, "include exported symbols (default: unexported only)")
	cmd.Flags().IntVar(&limit, "limit", 200, "max results")

	return cmd
}

func execDead(rs store.ReadStore, db *sql.DB, kind, pkg string, limit int, includeExported, asJSON bool, dbPath string) error {
	if err := validateEnumFlag("--kind", kind, deadKinds); err != nil {
		return err
	}

	if autoReindex {
		checkAndAutoReindex(db, false, false)
	}

	ctx := context.Background()
	res, err := rs.DeadSymbols(ctx, store.DeadOpts{
		Kind:            kind,
		Pkg:             pkg,
		IncludeExported: includeExported,
		Limit:           limit,
	})
	if err != nil {
		return fmt.Errorf("no index found (run gosymdb index first): %w", err)
	}

	type deadRow struct {
		FQName    string `json:"fqname"`
		Name      string `json:"name"`
		Kind      string `json:"kind"`
		Package   string `json:"package"`
		File      string `json:"file"`
		Line      int    `json:"line"`
		Col       int    `json:"col"`
		Signature string `json:"signature"`
	}
	results := make([]deadRow, 0, len(res.Symbols))
	for _, sym := range res.Symbols {
		r := deadRow{
			FQName:    sym.FQName,
			Name:      shortName(sym.FQName),
			Kind:      sym.Kind,
			Package:   sym.PackagePath,
			File:      sym.File,
			Line:      sym.Line,
			Col:       sym.Col,
			Signature: sym.Signature,
		}
		if asJSON {
			results = append(results, r)
		} else {
			fmt.Printf("%s\t%s\t%s:%d:%d\t%s\n", r.FQName, r.Kind, r.File, r.Line, r.Col, r.Signature)
		}
	}

	if asJSON {
		note := "Interface method implementations may appear here even if called through an interface."
		if includeExported {
			note += " Exported symbols with no internal callers may still be part of the public API."
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		return enc.Encode(map[string]any{
			"symbols":       results,
			"count":         len(results),
			"total_matched": res.TotalMatched,
			"truncated":     res.TotalMatched > limit,
			"note":          note,
			"env":           collectEnv(dbPath),
		})
	}
	return nil
}
