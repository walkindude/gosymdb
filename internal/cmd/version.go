package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/walkindude/gosymdb/store/sqlite"

	"github.com/spf13/cobra"
)

// Version is set by ldflags at build time (Makefile / GoReleaser) and falls
// back to runtime VCS info so `go install @main` binaries report a useful
// dev-<sha> tag instead of a bare "dev".
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
