package cmd

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/walkindude/gosymdb/indexer"
	"github.com/walkindude/gosymdb/store/sqlite"

	"github.com/spf13/cobra"
)

func newIndexCmd() *cobra.Command {
	var root string
	var enableCGO bool
	var force bool
	var withTests bool
	var benchJSON bool

	cmd := &cobra.Command{
		Use:           "index",
		Short:         "Build or update the symbol/call index for one or more Go modules",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			dbPath, _ := cmd.Root().PersistentFlags().GetString("db")
			asJSON, _ := cmd.Root().PersistentFlags().GetBool("json")
			return runIndex(dbPath, root, enableCGO, force, withTests, asJSON, benchJSON)
		},
	}

	setJSONHelpFunc(cmd, "gosymdb index")

	cmd.Flags().StringVar(&root, "root", ".", "root directory to scan for go.mod files")
	cmd.Flags().BoolVar(&enableCGO, "cgo", false, "set CGO_ENABLED=1 while loading packages")
	cmd.Flags().BoolVar(&force, "force", false, "force full rebuild (drop and recreate all tables)")
	cmd.Flags().BoolVar(&withTests, "tests", false, "include *_test.go files in symbol indexing")
	cmd.Flags().BoolVar(&benchJSON, "bench-json", false, "")
	_ = cmd.Flags().MarkHidden("bench-json")

	return cmd
}

func runIndex(dbPath, root string, enableCGO, force, withTests, asJSON, benchJSON bool) error {
	var m0 runtime.MemStats
	if benchJSON {
		runtime.GC()
		runtime.ReadMemStats(&m0)
	}
	benchStart := time.Now()

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	modules, err := indexer.DiscoverModules(absRoot)
	if err != nil {
		return err
	}
	if len(modules) == 0 {
		return fmt.Errorf("no go.mod found under %s", absRoot)
	}

	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return err
	}
	db, err := sql.Open(sqlite.DriverName, dbPath)
	if err != nil {
		return err
	}
	dbClosed := false
	defer func() {
		if !dbClosed {
			_ = db.Close()
		}
	}()

	if force {
		if err := indexer.ResetSchema(db); err != nil {
			return err
		}
	} else {
		if err := indexer.EnsureSchema(db); err != nil {
			return err
		}
		// Detect and purge orphaned modules (previously indexed but no longer on disk).
		discovered := make(map[string]bool, len(modules))
		for _, m := range modules {
			discovered[m] = true
		}
		rows, err := db.Query(`SELECT DISTINCT module_root FROM package_meta`)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var prev string
				if rows.Scan(&prev) == nil && !discovered[prev] {
					log.Printf("purging deleted module %s ...", prev)
					if err := indexer.PurgeModule(db, prev); err != nil {
						log.Printf("warn: purge %s: %v", prev, err)
					}
				}
			}
		}
	}

	totalSymbols := 0
	totalCalls := 0
	totalUnresolved := 0
	totalTypeRefs := 0
	totalWarnings := 0
	for i, mod := range modules {
		log.Printf("[%d/%d] indexing %s ...", i+1, len(modules), mod)
		symN, callN, unresN, typeRefN, err := indexer.IndexModule(db, mod, enableCGO, withTests)
		totalSymbols += symN
		totalCalls += callN
		totalUnresolved += unresN
		totalTypeRefs += typeRefN
		if err != nil {
			totalWarnings++
			log.Printf("warn: module %s: %v", mod, err)
		}
		log.Printf("  done: %d symbols, %d calls, %d unresolved, %d type_refs", symN, callN, unresN, typeRefN)
	}

	// Capture the current git commit for stale-detection fast path.
	indexedCommit := ""
	if gitCmd := exec.Command("git", "rev-parse", "HEAD"); gitCmd != nil {
		gitCmd.Dir = absRoot
		if out, err := gitCmd.Output(); err == nil {
			indexedCommit = strings.TrimSpace(string(out))
		}
	}

	if _, err := db.Exec(`INSERT INTO index_meta(tool_version, go_version, indexed_at, root, warnings, indexed_commit) VALUES (?, ?, ?, ?, ?, ?)`,
		Version, runtime.Version(), time.Now().UTC().Format(time.RFC3339), absRoot, totalWarnings, indexedCommit); err != nil {
		log.Printf("warn: index_meta insert: %v", err)
	}

	log.Printf("done: %d modules, %d symbols, %d calls, %d unresolved, %d type_refs", len(modules), totalSymbols, totalCalls, totalUnresolved, totalTypeRefs)

	if benchJSON {
		if err := db.Close(); err != nil {
			log.Printf("warn: db close: %v", err)
		}
		dbClosed = true
		wallNs := time.Since(benchStart).Nanoseconds()
		var m1 runtime.MemStats
		runtime.ReadMemStats(&m1)
		dbSize := int64(-1)
		if fi, err := os.Stat(dbPath); err == nil {
			dbSize = fi.Size()
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		return enc.Encode(map[string]any{
			"wall_ns":           wallNs,
			"total_alloc_bytes": m1.TotalAlloc - m0.TotalAlloc,
			"heap_alloc_bytes":  m1.HeapAlloc,
			"sys_bytes":         m1.Sys,
			"num_gc":            m1.NumGC - m0.NumGC,
			"pause_total_ns":    m1.PauseTotalNs - m0.PauseTotalNs,
			"mallocs":           m1.Mallocs - m0.Mallocs,
			"frees":             m1.Frees - m0.Frees,
			"db_path":           dbPath,
			"db_size_bytes":     dbSize,
			"modules":           len(modules),
			"symbols":           totalSymbols,
			"calls":             totalCalls,
			"unresolved":        totalUnresolved,
			"type_refs":         totalTypeRefs,
			"go_version":        runtime.Version(),
			"tool_version":      Version,
		})
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		return enc.Encode(map[string]any{
			"indexed":    len(modules),
			"symbols":    totalSymbols,
			"calls":      totalCalls,
			"unresolved": totalUnresolved,
			"type_refs":  totalTypeRefs,
		})
	}
	return nil
}
