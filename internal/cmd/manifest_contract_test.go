package cmd

import (
	"encoding/json"
	"testing"

	"github.com/spf13/cobra"
)

// TestCLIBridgeManifestMatchesCobra is a drift guard between the Cobra command
// surface and the embedded cli_bridge_spec.json manifest.
//
// cli_bridge_spec.json is the canonical MCP contract for gosymdb: cli-bridge
// ingests it and exposes each command as a first-class MCP tool. If it drifts
// from the Cobra reality, agents see tools that don't exist, accept wrong
// flags, or produce wrong output.
//
// Scope: existence of commands and flag names only. Types, defaults, enums,
// and output shape are deliberately out of scope — those belong in a
// golden-JSON test once one exists.
func TestCLIBridgeManifestMatchesCobra(t *testing.T) {
	// Cobra subcommands that are intentionally NOT surfaced as cli-bridge
	// tools. Any Cobra subcommand not listed here must appear in the manifest.
	excludedFromManifest := map[string]string{
		"version":             "operator/version info, not a code-navigation primitive",
		"cli-bridge-manifest": "registration/metadata command consumed by cli-bridge itself",
		"log-tool-use":        "internal telemetry helper, not agent-callable",
		"help":                "cobra built-in",
		"completion":          "cobra built-in shell-completion generator",
	}

	var spec struct {
		GlobalFlags []struct {
			Name string `json:"name"`
		} `json:"globalFlags"`
		Commands []struct {
			Name  string `json:"name"`
			Flags []struct {
				Name string `json:"name"`
			} `json:"flags"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(cliBridgeSpecRaw, &spec); err != nil {
		t.Fatalf("embedded cli-bridge spec is not valid JSON: %v", err)
	}

	manifestCmds := make(map[string]bool, len(spec.Commands))
	for _, c := range spec.Commands {
		manifestCmds[c.Name] = true
	}

	cobraCmds := make(map[string]*cobra.Command)
	for _, c := range rootCmd.Commands() {
		cobraCmds[c.Name()] = c
	}

	// Direction 1: every manifest command exists in Cobra.
	for _, mc := range spec.Commands {
		if _, ok := cobraCmds[mc.Name]; !ok {
			t.Errorf("manifest declares command %q that is not registered in Cobra", mc.Name)
		}
	}

	// Direction 2: every Cobra subcommand (minus exclusions) exists in the manifest.
	for name := range cobraCmds {
		if _, excluded := excludedFromManifest[name]; excluded {
			continue
		}
		if !manifestCmds[name] {
			t.Errorf("Cobra registers command %q that is missing from cli_bridge_spec.json "+
				"(add it to the manifest, or add it to excludedFromManifest with a reason)", name)
		}
	}

	// Direction 3: every flag declared under a manifest command is either a
	// flag on the matching Cobra command (local or inherited persistent) or a
	// declared global flag. cmd.Flags() already includes inherited persistent
	// flags, so a single lookup is enough for the common case.
	globalFlags := make(map[string]bool, len(spec.GlobalFlags))
	for _, f := range spec.GlobalFlags {
		globalFlags[f.Name] = true
	}
	for _, mc := range spec.Commands {
		cobraCmd, ok := cobraCmds[mc.Name]
		if !ok {
			continue // already reported above
		}
		for _, mf := range mc.Flags {
			if cobraCmd.Flags().Lookup(mf.Name) != nil {
				continue
			}
			if globalFlags[mf.Name] {
				continue
			}
			t.Errorf("manifest command %q declares flag %q that is not registered in Cobra "+
				"(not local, not persistent, not a declared globalFlag)", mc.Name, mf.Name)
		}
	}

	// Direction 4: every globalFlag declared in the manifest must be an actual
	// persistent flag on the root command.
	for _, f := range spec.GlobalFlags {
		if rootCmd.PersistentFlags().Lookup(f.Name) == nil {
			t.Errorf("manifest declares globalFlag %q but root has no persistent flag with that name", f.Name)
		}
	}
}
