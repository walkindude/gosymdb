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

type indexCounts struct {
	symbols, calls, unresolved, typeRefs, warnings int
}

func runIndex(dbPath, root string, enableCGO, force, withTests, asJSON, benchJSON bool) error {
	benchStart := time.Now()
	var m0 runtime.MemStats
	if benchJSON {
		runtime.GC()
		runtime.ReadMemStats(&m0)
	}

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

	db, err := openIndexDB(dbPath, force, modules)
	if err != nil {
		return err
	}
	dbClosed := false
	defer func() {
		if !dbClosed {
			_ = db.Close()
		}
	}()

	counts := indexAllModules(db, modules, enableCGO, withTests)

	indexedCommit := captureGitCommit(absRoot)
	if _, err := db.Exec(`INSERT INTO index_meta(tool_version, go_version, indexed_at, root, warnings, indexed_commit) VALUES (?, ?, ?, ?, ?, ?)`,
		Version, runtime.Version(), time.Now().UTC().Format(time.RFC3339), absRoot, counts.warnings, indexedCommit); err != nil {
		log.Printf("warn: index_meta insert: %v", err)
	}

	log.Printf("done: %d modules, %d symbols, %d calls, %d unresolved, %d type_refs",
		len(modules), counts.symbols, counts.calls, counts.unresolved, counts.typeRefs)

	if benchJSON {
		if err := db.Close(); err != nil {
			log.Printf("warn: db close: %v", err)
		}
		dbClosed = true
		return emitBenchJSON(dbPath, benchStart, m0, len(modules), counts)
	}
	if asJSON {
		return emitIndexJSON(len(modules), counts)
	}
	return nil
}

func openIndexDB(dbPath string, force bool, modules []string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open(sqlite.DriverName, dbPath)
	if err != nil {
		return nil, err
	}
	if force {
		if err := indexer.ResetSchema(db); err != nil {
			_ = db.Close()
			return nil, err
		}
		return db, nil
	}
	if err := indexer.EnsureSchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	purgeOrphanedModules(db, modules)
	return db, nil
}

// purgeOrphanedModules drops index entries for modules that were previously
// indexed but no longer exist on disk under the current root.
func purgeOrphanedModules(db *sql.DB, modules []string) {
	discovered := make(map[string]bool, len(modules))
	for _, m := range modules {
		discovered[m] = true
	}
	rows, err := db.Query(`SELECT DISTINCT module_root FROM package_meta`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var prev string
		if rows.Scan(&prev) != nil || discovered[prev] {
			continue
		}
		log.Printf("purging deleted module %s ...", prev)
		if err := indexer.PurgeModule(db, prev); err != nil {
			log.Printf("warn: purge %s: %v", prev, err)
		}
	}
}

func indexAllModules(db *sql.DB, modules []string, enableCGO, withTests bool) indexCounts {
	var c indexCounts
	for i, mod := range modules {
		log.Printf("[%d/%d] indexing %s ...", i+1, len(modules), mod)
		symN, callN, unresN, typeRefN, err := indexer.IndexModule(db, mod, enableCGO, withTests)
		c.symbols += symN
		c.calls += callN
		c.unresolved += unresN
		c.typeRefs += typeRefN
		if err != nil {
			c.warnings++
			log.Printf("warn: module %s: %v", mod, err)
		}
		log.Printf("  done: %d symbols, %d calls, %d unresolved, %d type_refs", symN, callN, unresN, typeRefN)
	}
	return c
}

func captureGitCommit(absRoot string) string {
	gitCmd := exec.Command("git", "rev-parse", "HEAD")
	gitCmd.Dir = absRoot
	out, err := gitCmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func emitBenchJSON(dbPath string, benchStart time.Time, m0 runtime.MemStats, modules int, c indexCounts) error {
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
		"modules":           modules,
		"symbols":           c.symbols,
		"calls":             c.calls,
		"unresolved":        c.unresolved,
		"type_refs":         c.typeRefs,
		"go_version":        runtime.Version(),
		"tool_version":      Version,
	})
}

func emitIndexJSON(modules int, c indexCounts) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	return enc.Encode(map[string]any{
		"indexed":    modules,
		"symbols":    c.symbols,
		"calls":      c.calls,
		"unresolved": c.unresolved,
		"type_refs":  c.typeRefs,
	})
}
