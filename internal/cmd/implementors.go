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

func newImplementorsCmd() *cobra.Command {
	var iface string
	var typ string
	var limit int

	cmd := &cobra.Command{
		Use:           "implementors",
		Short:         "Find types that implement an interface, or interfaces a type satisfies",
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
			return execImplementors(st, dbPath, iface, typ, limit, asJSON)
		},
	}

	setJSONHelpFunc(cmd, "gosymdb implementors")

	cmd.Flags().StringVar(&iface, "iface", "", "partial interface fqname: find all types that implement it")
	cmd.Flags().StringVar(&typ, "type", "", "partial type fqname: find all interfaces it implements")
	cmd.Flags().IntVar(&limit, "limit", 200, "max results")

	return cmd
}

func execImplementors(rs store.ReadStore, dbPath, iface, typ string, limit int, asJSON bool) error {
	if strings.TrimSpace(iface) == "" && strings.TrimSpace(typ) == "" {
		return errors.New("--iface or --type is required")
	}

	ctx := context.Background()
	storeRows, err := rs.FindImplementors(ctx, store.ImplementorsOpts{
		Iface: iface,
		Type:  typ,
		Limit: limit,
	})
	if err != nil {
		return err
	}

	type implRow struct {
		Iface     string `json:"iface"`
		Impl      string `json:"impl"`
		IsPointer bool   `json:"is_pointer"`
	}

	if asJSON {
		results := make([]implRow, 0, len(storeRows))
		for _, r := range storeRows {
			results = append(results, implRow{Iface: r.Iface, Impl: r.Impl, IsPointer: r.IsPointer})
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		return enc.Encode(map[string]any{"implementors": results, "count": len(results), "env": collectEnv(dbPath)})
	}

	for _, r := range storeRows {
		ptrMarker := ""
		if r.IsPointer {
			ptrMarker = " [*ptr]"
		}
		fmt.Printf("%s implements %s%s\n", r.Impl, r.Iface, ptrMarker)
	}
	return nil
}
