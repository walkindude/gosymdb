package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/walkindude/gosymdb/store"
	"github.com/walkindude/gosymdb/store/sqlite"

	"github.com/spf13/cobra"
)

func newDefCmd() *cobra.Command {
	var pkg string

	cmd := &cobra.Command{
		Use:           "def <name>",
		Short:         "Exact single-symbol lookup by name or fqname",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return errors.New("name argument is required: gosymdb def <name>")
			}
			dbPath, _ := cmd.Root().PersistentFlags().GetString("db")
			asJSON, _ := cmd.Root().PersistentFlags().GetBool("json")
			st, err := sqlite.Open(dbPath)
			if err != nil {
				return err
			}
			defer st.Close()
			return execDef(st, dbPath, args[0], pkg, asJSON)
		},
	}

	setJSONHelpFunc(cmd, "gosymdb def")
	cmd.Flags().StringVar(&pkg, "pkg", "", "disambiguate by package path prefix when name is ambiguous")

	return cmd
}

func execDef(rs store.ReadStore, dbPath, name, pkg string, asJSON bool) error {
	ctx := context.Background()
	storeRows, err := rs.DefSymbol(ctx, name, pkg)
	if err != nil {
		return err
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

	toOut := func(r store.SymbolRow) symRow {
		return symRow{
			FQName:      r.FQName,
			Name:        r.Name,
			Kind:        r.Kind,
			File:        r.File,
			Line:        r.Line,
			Col:         r.Col,
			Signature:   r.Signature,
			PackagePath: r.PackagePath,
		}
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		switch len(storeRows) {
		case 0:
			return enc.Encode(map[string]any{
				"symbol":    nil,
				"ambiguous": false,
				"hint":      "Symbol not found. Try: gosymdb find --q " + name + " --json",
				"env":       collectEnv(dbPath),
			})
		case 1:
			return enc.Encode(map[string]any{
				"symbol":    toOut(storeRows[0]),
				"ambiguous": false,
				"env":       collectEnv(dbPath),
			})
		default:
			matches := make([]symRow, 0, len(storeRows))
			for _, r := range storeRows {
				matches = append(matches, toOut(r))
			}
			return enc.Encode(map[string]any{
				"symbol":    nil,
				"ambiguous": true,
				"matches":   matches,
				"hint":      fmt.Sprintf("%d symbols named %q — use --pkg to disambiguate", len(storeRows), name),
				"env":       collectEnv(dbPath),
			})
		}
	}

	for _, r := range storeRows {
		fmt.Printf("%s\t%s\t%s:%d:%d\t%s\n", r.FQName, r.Kind, r.File, r.Line, r.Col, r.Signature)
	}
	return nil
}
