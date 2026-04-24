package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/walkindude/gosymdb/store/sqlite"

	"github.com/spf13/cobra"
)

// Version is set by ldflags at build time (Makefile / GoReleaser). When not
// set, init() below derives a useful value from runtime build info so that
// `go install github.com/walkindude/gosymdb@vX.Y.Z` reports vX.Y.Z and
// `go install .` from a checkout reports dev-<sha>, instead of bare "dev".
var Version = "dev"

const schemaVersion = 6

func init() {
	// If a release tag or explicit ldflag already set Version, keep it.
	if Version != "dev" {
		return
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	// Module-installed binaries (e.g. `go install ...@v0.1.1`) have the
	// resolved version in Main.Version. Local builds from a module source
	// tree set it to "(devel)" — skip that and fall back to vcs.revision.
	if v := info.Main.Version; v != "" && v != "(devel)" {
		Version = v
		return
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 7 {
			Version = "dev-" + s.Value[:7]
			return
		}
	}
}

func newVersionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "version",
		Short:         "Print gosymdb version and schema version",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			asJSON, _ := cmd.Root().PersistentFlags().GetBool("json")
			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetEscapeHTML(false)
				return enc.Encode(map[string]any{
					"version":        Version,
					"schema_version": schemaVersion,
					"driver":         sqlite.DriverName,
					"env":            collectEnv(""),
				})
			}
			fmt.Printf("gosymdb %s (schema v%d)\n", Version, schemaVersion)
			return nil
		},
	}
	setJSONHelpFunc(cmd, "gosymdb version")
	return cmd
}
