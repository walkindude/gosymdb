package cmd

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/walkindude/gosymdb/indexer"

	"github.com/spf13/cobra"
	// Driver registered by store/sqlite/driver_*.go (build-tag selected).
)

const defaultDB = "gosymdb.sqlite"

var autoReindex bool

var rootCmd = &cobra.Command{
	Use:   "gosymdb",
	Short: "Local Go symbol and call-graph index",
	Long: strings.Join([]string{
		"gosymdb — local Go symbol and call-edge index.",
		"",
		"For agents, load the full API reference in one shot:",
		"  gosymdb agent-context --json",
		"",
		"Or discover per-command contracts:",
		"  gosymdb <command> --help --json",
	}, "\n"),
	// When called with no subcommand, emit the agent help spec.
	RunE: func(cmd *cobra.Command, args []string) error {
		emitAgentHelp("gosymdb")
		return nil
	},
	SilenceUsage:  true,
	SilenceErrors: true,
}

// discoverDB returns the path to a gosymdb.sqlite database file. If the
// provided defaultDB path refers to an existing file it is returned unchanged.
// Otherwise, the function walks up from startDir through each parent directory,
// looking for a file named "gosymdb.sqlite". The first match is returned. If
// no match is found, defaultDB is returned unchanged.
func discoverDB(startDir, defaultDB string) string {
	// If the default/explicit path already exists, use it as-is.
	// Resolve relative paths against startDir so the check is consistent
	// with the directory we're walking from.
	check := defaultDB
	if !filepath.IsAbs(defaultDB) {
		check = filepath.Join(startDir, defaultDB)
	}
	if _, err := os.Stat(check); err == nil {
		return defaultDB
	}
	dir := startDir
	for {
		candidate := filepath.Join(dir, "gosymdb.sqlite")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break // reached filesystem root
		}
		dir = parent
	}
	return defaultDB
}

// Execute runs the root cobra command. Called from main.
func Execute() {
	log.SetFlags(0)
	start := time.Now()
	err := rootCmd.Execute()
	appendUsageLog(os.Args[1:], time.Since(start), err)
	if err != nil {
		if jsonFlagInArgs(os.Args[1:]) {
			writeJSONError(rootCmd, err)
			// writeJSONError exits; this return is for clarity.
			return
		}
		log.Fatal(err)
	}
}

func init() {
	rootCmd.PersistentFlags().String("db", defaultDB, "SQLite database path")
	rootCmd.PersistentFlags().Bool("json", false, "output JSON instead of text")
	rootCmd.PersistentFlags().BoolVar(&autoReindex, "auto-reindex", false, "automatically detect and re-index stale packages before query execution (uses git fast-path when available, falls back to file-hash comparison)")

	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		dbFlag := cmd.Root().PersistentFlags().Lookup("db")
		// Only auto-discover when --db was not explicitly provided by the user.
		// If the user set --db to a non-existent path, respect that (don't
		// silently swap it for a parent directory's DB).
		if dbFlag.Changed {
			return nil
		}
		dbPath := dbFlag.Value.String()
		// If the default DB already exists in CWD, nothing to do.
		if _, err := os.Stat(dbPath); err == nil {
			return nil
		}
		// Walk up from CWD looking for gosymdb.sqlite.
		cwd, err := os.Getwd()
		if err != nil {
			return nil // non-fatal
		}
		discovered := discoverDB(cwd, dbPath)
		if discovered != dbPath {
			_ = dbFlag.Value.Set(discovered)
			return nil
		}
		// No DB found anywhere — error for read commands; exempt commands that
		// don't need a DB or create one.
		if !isDBExemptCmd(cmd) {
			return fmt.Errorf("no database found (searched from %s to filesystem root); use --db to specify one or run: gosymdb index --root . --db gosymdb.sqlite", cwd)
		}
		return nil
	}

	// Override help on root to handle --help --json.
	defaultRootHelp := rootCmd.HelpFunc()
	rootCmd.SetHelpFunc(func(c *cobra.Command, args []string) {
		if jsonFlagInArgs(os.Args[1:]) {
			emitAgentHelp("gosymdb")
			return
		}
		defaultRootHelp(c, args)
	})

	rootCmd.AddCommand(newAgentContextCmd())
	rootCmd.AddCommand(newIndexCmd())
	rootCmd.AddCommand(newFindCmd())
	rootCmd.AddCommand(newCallersCmd())
	rootCmd.AddCommand(newCalleesCmd())
	rootCmd.AddCommand(newBlastRadiusCmd())
	rootCmd.AddCommand(newDeadCmd())
	rootCmd.AddCommand(newPackagesCmd())
	rootCmd.AddCommand(newHealthCmd())
	rootCmd.AddCommand(newImplementorsCmd())
	rootCmd.AddCommand(newReferencesCmd())
	rootCmd.AddCommand(newLogToolUseCmd())
	rootCmd.AddCommand(newDefCmd())
	rootCmd.AddCommand(newVersionCmd())
	rootCmd.AddCommand(newCLIBridgeManifestCmd())
}

// isDBExemptCmd returns true for commands that do not require a database to be
// present: commands that create the DB (index), print static info (version,
// agent-context), and the root command itself (runs emitAgentHelp when invoked
// with no subcommand — no DB needed).
func isDBExemptCmd(cmd *cobra.Command) bool {
	name := cmd.Name()
	return name == "index" || name == "version" || name == "agent-context" || name == "help" ||
		name == "cli-bridge-manifest" ||
		cmd.Parent() == nil
}

// checkAndAutoReindex detects stale packages and re-indexes the affected
// modules. Called by query commands when --auto-reindex is set.
func checkAndAutoReindex(db *sql.DB, enableCGO, withTests bool) {
	stale, err := indexer.StalePackages(db)
	if err != nil {
		log.Printf("auto-reindex: stale check: %v", err)
		return
	}
	if len(stale) == 0 {
		return
	}

	// Group stale packages by module_root.
	moduleRoots := map[string]bool{}
	for _, pkgPath := range stale {
		var modRoot string
		err := db.QueryRow(`SELECT module_root FROM package_meta WHERE package_path = ? LIMIT 1`, pkgPath).Scan(&modRoot)
		if err != nil {
			continue
		}
		moduleRoots[modRoot] = true
	}

	for mod := range moduleRoots {
		log.Printf("auto-reindex: re-indexing stale module %s", mod)
		if _, _, _, _, err := indexer.IndexModule(db, mod, enableCGO, withTests); err != nil {
			log.Printf("auto-reindex: module %s: %v", mod, err)
		}
	}
}

// setJSONHelpFunc overrides cmd's HelpFunc so that --help --json emits the
// JSON agent spec for helpKey instead of the default cobra help text.
func setJSONHelpFunc(c *cobra.Command, helpKey string) {
	defaultHelp := c.HelpFunc()
	c.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		if jsonFlagInArgs(os.Args[1:]) {
			emitAgentHelp(helpKey)
			return
		}
		defaultHelp(cmd, args)
	})
}
