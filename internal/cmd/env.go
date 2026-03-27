package cmd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/walkindude/gosymdb/indexer"
	"github.com/walkindude/gosymdb/store/sqlite"
)

type envBlock struct {
	TS            int64     `json:"ts"`
	CWD           string    `json:"cwd"`
	OS            string    `json:"os"`
	Arch          string    `json:"arch"`
	DB            string    `json:"db"`
	Git           gitEnv    `json:"git"`
	StalePackages *[]string `json:"stale_packages"`
}

type gitEnv struct {
	GitAvailable bool   `json:"git_available"`
	Branch       string `json:"branch"`
	IsWorktree   bool   `json:"is_worktree"`
	AheadBehind  string `json:"ahead_behind"`
	WorktreeRoot string `json:"worktree_root"`
	DirtyCount   int    `json:"dirty_count"`
	StagedCount  int    `json:"staged_count"`
	LastFetchTS  int64  `json:"last_fetch_ts"`  // mtime of FETCH_HEAD; 0 = never fetched
	LastFetchAge *int64 `json:"last_fetch_age"` // seconds since last fetch; null = never fetched
}

// shortName returns the symbol's short name from a fully-qualified name.
// "pkg/path.FuncName"         → "FuncName"
// "pkg/path.*T.Method"        → "*T.Method"
// "pkg/path.init$var:varname" → "init$var:varname"
func shortName(fqname string) string {
	lastSlash := strings.LastIndex(fqname, "/")
	rest := fqname[lastSlash+1:] // "pkg.Name" or "pkg.*T.Method"
	dot := strings.Index(rest, ".")
	if dot < 0 {
		return rest
	}
	return rest[dot+1:]
}

// collectEnv snapshots the execution context for agent orientation.
// dbPath is resolved to absolute path if non-empty.
func collectEnv(dbPath string) envBlock {
	cwd, _ := os.Getwd()
	db := dbPath
	if dbPath != "" {
		if abs, err := filepath.Abs(dbPath); err == nil {
			db = abs
		}
	}
	env := envBlock{
		TS:   time.Now().Unix(),
		CWD:  cwd,
		OS:   runtime.GOOS,
		Arch: runtime.GOARCH,
		DB:   db,
		Git:  collectGitEnv(),
	}

	// Populate stale_packages when a DB is available. The git fast-path in
	// StalePackagesStore runs git diff once and checks all packages against the
	// cached result — fast even on large repos (sub-second on kubernetes).
	if dbPath != "" {
		rs, err := sqlite.Open(dbPath)
		if err == nil {
			defer rs.Close()
			ctx := context.Background()
			hasTracking, _ := rs.HasFileTracking(ctx)
			if hasTracking {
				stale, err := indexer.StalePackagesStore(rs)
				if err == nil {
					if stale == nil {
						stale = []string{}
					}
					env.StalePackages = &stale
				}
			}
		}
	}

	return env
}

func collectGitEnv() gitEnv {
	g := gitEnv{}

	// Probe whether we are inside a git repo at all.
	if _, err := gitOut("rev-parse", "--git-dir"); err == nil {
		g.GitAvailable = true
	}

	if b, err := gitOut("rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		g.Branch = b
	}
	if d, err := gitOut("rev-parse", "--git-dir"); err == nil {
		g.IsWorktree = strings.Contains(d, "worktrees")
	}
	if r, err := gitOut("rev-parse", "--show-toplevel"); err == nil {
		g.WorktreeRoot = r
	}
	// Output format: "<behind>\t<ahead>". Report as "ahead/behind".
	if ab, err := gitOut("rev-list", "--count", "--left-right", "@{u}...HEAD"); err == nil {
		parts := strings.Fields(ab)
		if len(parts) == 2 {
			g.AheadBehind = parts[1] + "/" + parts[0]
		}
	}
	if dirty, err := gitOut("diff", "--name-only"); err == nil {
		g.DirtyCount = countLines(dirty)
	}
	if staged, err := gitOut("diff", "--cached", "--name-only"); err == nil {
		g.StagedCount = countLines(staged)
	}
	// FETCH_HEAD mtime = when the repo was last synced with remote.
	// Use --git-common-dir so worktrees point to the main .git, not the worktree subdir.
	if commonDir, err := gitOut("rev-parse", "--git-common-dir"); err == nil {
		if fi, err := os.Stat(filepath.Join(commonDir, "FETCH_HEAD")); err == nil {
			g.LastFetchTS = fi.ModTime().Unix()
			age := time.Now().Unix() - g.LastFetchTS
			g.LastFetchAge = &age
		}
	}
	return g
}

func gitOut(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}
