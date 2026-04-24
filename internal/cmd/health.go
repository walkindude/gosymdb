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

func newHealthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "health",
		Short:         "Report index quality: symbol/call counts and unresolved-call ratio",
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
			return execHealth(st, dbPath, asJSON)
		},
	}

	setJSONHelpFunc(cmd, "gosymdb health")

	return cmd
}

func execHealth(rs store.ReadStore, dbPath string, asJSON bool) error {
	ctx := context.Background()
	h, err := rs.HealthStats(ctx)
	if err != nil {
		return err
	}

	totalEdges := h.CallCount + h.UnresolvedCount
	ratio := 0.0
	if totalEdges > 0 {
		ratio = float64(h.UnresolvedCount) / float64(totalEdges) * 100
	}

	type unresolvedExpr struct {
		Expr  string `json:"expr"`
		Count int    `json:"count"`
	}
	topUnresolved := make([]unresolvedExpr, 0, len(h.TopUnresolved))
	for _, ue := range h.TopUnresolved {
		topUnresolved = append(topUnresolved, unresolvedExpr{Expr: ue.Expr, Count: ue.Count})
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		return enc.Encode(map[string]any{
			"root":             h.Root,
			"indexed_at":       h.IndexedAt,
			"tool_version":     h.ToolVersion,
			"go_version":       h.GoVersion,
			"symbols":          h.SymbolCount,
			"calls":            h.CallCount,
			"unresolved":       h.UnresolvedCount,
			"unresolved_ratio": ratio,
			"type_refs":        h.TypeRefCount,
			"warnings":         h.Warnings,
			"top_unresolved":   topUnresolved,
			"env":              collectEnv(dbPath),
		})
	}

	fmt.Printf("root:             %s\n", h.Root)
	fmt.Printf("indexed_at:       %s\n", h.IndexedAt)
	fmt.Printf("tool_version:     %s\n", h.ToolVersion)
	fmt.Printf("go_version:       %s\n", h.GoVersion)
	fmt.Printf("symbols:          %d\n", h.SymbolCount)
	fmt.Printf("calls (resolved): %d\n", h.CallCount)
	fmt.Printf("unresolved:       %d\n", h.UnresolvedCount)
	fmt.Printf("type_refs:        %d\n", h.TypeRefCount)
	fmt.Printf("unresolved_ratio: %.1f%%\n", ratio)
	if h.Warnings > 0 {
		fmt.Printf("WARNINGS:         %d module(s) had load/type errors — index is partial\n", h.Warnings)
	}
	if h.UnresolvedCount > 0 {
		fmt.Printf("\ntop unresolved exprs (sample):\n")
		for _, ue := range h.TopUnresolved {
			fmt.Printf("  %-40s %d\n", ue.Expr, ue.Count)
		}
	}
	return nil
}
