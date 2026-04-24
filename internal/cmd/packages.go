package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/walkindude/gosymdb/store"
	"github.com/walkindude/gosymdb/store/sqlite"

	"github.com/spf13/cobra"
)

func newPackagesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "packages",
		Short:         "List all indexed packages with symbol and function counts",
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
			return execPackages(st, dbPath, asJSON)
		},
	}

	setJSONHelpFunc(cmd, "gosymdb packages")

	return cmd
}

func execPackages(rs store.ReadStore, dbPath string, asJSON bool) error {
	ctx := context.Background()
	storeRows, err := rs.ListPackages(ctx)
	if err != nil {
		return err
	}

	type pkgRow struct {
		Path          string `json:"path"`
		SymbolCount   int    `json:"symbol_count"`
		FuncCount     int    `json:"func_count"`
		ExportedCount int    `json:"exported_count"`
	}

	if asJSON {
		results := make([]pkgRow, 0, len(storeRows))
		for _, r := range storeRows {
			results = append(results, pkgRow{
				Path:          r.Path,
				SymbolCount:   r.SymbolCount,
				FuncCount:     r.FuncCount,
				ExportedCount: r.ExportedCount,
			})
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		return enc.Encode(map[string]any{"packages": results, "count": len(results), "env": collectEnv(dbPath)})
	}

	for _, r := range storeRows {
		fmt.Printf("%-70s  symbols:%-4d  funcs:%-4d  exported:%-4d\n",
			r.Path, r.SymbolCount, r.FuncCount, r.ExportedCount)
	}
	return nil
}
