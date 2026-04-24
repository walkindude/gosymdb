package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

func newAgentContextCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent-context",
		Short: "Dump complete API reference for all commands (one-shot agent bootstrap)",
		Long: "Emit the full gosymdb API reference as a single JSON payload.\n" +
			"Run this once at the start of an agent session — no further --help calls needed.\n" +
			"Pipe to a file or load directly into context.",
		Example:       "  gosymdb agent-context --json\n  gosymdb agent-context > gosymdb-api.json",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			dbPath, _ := cmd.Root().PersistentFlags().GetString("db")
			return execAgentContext(dbPath)
		},
	}
	return cmd
}

// agentContextOrder is the ordered list of subcommand keys emitted by
// `gosymdb agent-context`. Adding a new query/indexer subcommand? Append it
// here (or mark it excluded in excludedFromAgentContext with a reason).
// TestAgentContextMatchesCobra enforces that this list and the Cobra command
// surface stay in sync.
var agentContextOrder = []string{
	"gosymdb index",
	"gosymdb find",
	"gosymdb def",
	"gosymdb callers",
	"gosymdb callees",
	"gosymdb blast-radius",
	"gosymdb dead",
	"gosymdb packages",
	"gosymdb health",
	"gosymdb implementors",
	"gosymdb references",
}

// excludedFromAgentContext lists Cobra subcommands that are intentionally
// omitted from the agent-context payload. Each entry is a Cobra command
// Name() with a short reason. TestAgentContextMatchesCobra enforces that
// every Cobra subcommand is either in agentContextOrder or in this map.
var excludedFromAgentContext = map[string]string{
	"agent-context":       "self — already loaded by the agent when this payload arrives",
	"version":             "operator/version info, not a code-navigation primitive",
	"cli-bridge-manifest": "registration/metadata command consumed by cli-bridge itself",
	"log-tool-use":        "internal telemetry helper, not agent-callable",
	"help":                "cobra built-in",
	"completion":          "cobra built-in shell-completion generator",
}

// execAgentContext emits the full API reference + env block.
// flagDB is the value of the --db flag as resolved by PersistentPreRunE
// (may equal defaultDB if no DB was found). When called from tests without
// a cobra context, pass "" to trigger cwd-based discovery.
func execAgentContext(flagDB string) error {
	specs := make([]agentHelp, 0, len(agentContextOrder))
	for _, key := range agentContextOrder {
		if spec, ok := helpSpecs[key]; ok {
			specs = append(specs, spec)
		}
	}

	// Resolve env.db: use the flag value when it points to an existing file,
	// otherwise walk up from cwd, otherwise leave empty.
	db := resolveEnvDB(flagDB)

	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	return enc.Encode(map[string]any{"commands": specs, "env": collectEnv(db)})
}

// resolveEnvDB resolves the database path for the env block.
// hint is the --db flag value (may be the default "gosymdb.sqlite" when unset).
// Returns the absolute path if a file is found, or "" if none exists.
func resolveEnvDB(hint string) string {
	cwd, _ := os.Getwd()
	candidate := hint
	if candidate == "" || candidate == defaultDB {
		candidate = discoverDB(cwd, defaultDB)
	}
	if abs, err := filepath.Abs(candidate); err == nil {
		if _, err := os.Stat(abs); err == nil {
			return abs
		}
	}
	return ""
}
