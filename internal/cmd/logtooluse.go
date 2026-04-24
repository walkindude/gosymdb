package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newLogToolUseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "log-tool-use --tool <name> [-- <argv...>]",
		Short:  "Append a wrapper-tool invocation record to GOSYMDB_USAGE_LOG",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			logPath := os.Getenv("GOSYMDB_USAGE_LOG")
			if logPath == "" {
				return nil
			}
			tool, _ := cmd.Flags().GetString("tool")
			if tool == "" {
				return fmt.Errorf("--tool is required")
			}
			appendWrapperLog(logPath, tool, args)
			return nil
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.Flags().String("tool", "", "tool name (e.g. rg, grep, find)")

	return cmd
}
