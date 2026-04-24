package cmd

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

//go:embed cli_bridge_spec.json
var cliBridgeSpecRaw []byte

func newCLIBridgeManifestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cli-bridge-manifest",
		Short: "Print the canonical cli-bridge spec for this gosymdb build",
		Long: "Emit the canonical cli-bridge tool spec for this gosymdb build as JSON on stdout.\n" +
			"cli-bridge (https://github.com/walkindude/cli-bridge) is an MCP stdio server that\n" +
			"promotes CLI tools to first-class MCP tools via declarative JSON specs. The\n" +
			"/cli-bridge:register skill invokes this command to register gosymdb without\n" +
			"relying on --help scraping.",
		Example:       "  gosymdb cli-bridge-manifest\n  gosymdb cli-bridge-manifest > ~/.config/cli-bridge/specs/gosymdb/dev.json",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			var spec map[string]any
			if err := json.Unmarshal(cliBridgeSpecRaw, &spec); err != nil {
				return fmt.Errorf("embedded cli-bridge spec is invalid: %w", err)
			}
			spec["binaryVersion"] = Version
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			enc.SetEscapeHTML(false)
			return enc.Encode(spec)
		},
	}
	return cmd
}
