package indexer

import (
	"context"
	"database/sql"
	"fmt"
	"hash/fnv"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"

	"github.com/walkindude/gosymdb/store"
)

// gitRoot returns the top-level directory of the git repository containing dir.
func gitRoot(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse --show-toplevel: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// gitChangedFiles returns the set of files changed between the given commit and HEAD,
// with paths relative to the git root.
func gitChangedFiles(dir, since string) (map[string]bool, error) {
	cmd := exec.Command("git", "diff", "--name-only", since+"..HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff --name-only: %w", err)
	}
	files := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			files[line] = true
		}
	}
	return files, nil
}

// IsPackageStale reports whether the given package needs re-indexing.
// It first attempts a git fast-path using the indexed_commit from index_meta,
// then falls back to a file-hash comparison using package_files and files_hash.
func IsPackageStale(db *sql.DB, moduleRoot, packagePath string) (bool, error) {
	// Read indexed_commit from the most recent index_meta row.
	var indexedCommit string
	err := db.QueryRow(`SELECT indexed_commit FROM index_meta ORDER BY id DESC LIMIT 1`).Scan(&indexedCommit)
	if err != nil && err != sql.ErrNoRows {
		return true, fmt.Errorf("query index_meta: %w", err)
	}

	// Try git fast-path if we have an indexed commit.
	if indexedCommit != "" {
		stale, gitErr := gitFastPath(moduleRoot, packagePath, indexedCommit)
		if gitErr == nil {
			return stale, nil
		}
		// Git fast-path failed; fall through to file-hash check.
		log.Printf("stale detection: git fast-path failed for %s: %v (degraded to file-hash check)", packagePath, gitErr)
	}

	// Fallback: file-hash check using package_files and files_hash.
	return fileHashFallback(db, moduleRoot, packagePath)
}

// gitFastPath checks staleness by comparing git diff output against the package directory.
// Returns (stale, error). On error, the caller should fall through to file-hash check.
func gitFastPath(moduleRoot, packagePath, indexedCommit string) (bool, error) {
	root, err := gitRoot(moduleRoot)
	if err != nil {
		return false, err
	}
	changed, err := gitChangedFiles(moduleRoot, indexedCommit)
	if err != nil {
		return false, err
	}
	pkgDir, err := packageDir(moduleRoot, packagePath)
	if err != nil {
		return false, err
	}
	relPkg, err := filepath.Rel(root, pkgDir)
	if err != nil {
		return false, err
	}
	// Normalize to forward slashes for git diff output comparison.
	relPkg = filepath.ToSlash(relPkg)
	prefix := relPkg + "/"
	for f := range changed {
		f = filepath.ToSlash(f)
		if f == relPkg || strings.HasPrefix(f, prefix) {
			return true, nil
		}
	}
	return false, nil
}

type storedFile struct {
	path string
	hash string
}

// fileHashFallback checks staleness by comparing stored file lists and hashes
// against the current state on disk.
func fileHashFallback(db *sql.DB, moduleRoot, packagePath string) (bool, error) {
	storedFiles, err := loadStoredFiles(db, moduleRoot, packagePath)
	if err != nil {
		return true, err
	}
	// Pre-item13 DB has no stored file rows — treat as stale to force a rebuild.
	if len(storedFiles) == 0 {
		return true, nil
	}
	if hasNewProductionFiles(moduleRoot, packagePath, storedSetByBase(storedFiles)) {
		return true, nil
	}
	stale, complete := comparePerFileHashes(storedFiles)
	if stale {
		return true, nil
	}
	if complete {
		return false, nil
	}
	return comparePackageHashFromDB(db, moduleRoot, packagePath, storedFiles)
}

func loadStoredFiles(db *sql.DB, moduleRoot, packagePath string) ([]storedFile, error) {
	rows, err := db.Query(
		`SELECT file_path, file_hash FROM package_files WHERE module_root = ? AND package_path = ?`,
		moduleRoot, packagePath,
	)
	if err != nil {
		return nil, fmt.Errorf("query package_files: %w", err)
	}
	defer rows.Close()
	var out []storedFile
	for rows.Next() {
		var sf storedFile
		if err := rows.Scan(&sf.path, &sf.hash); err != nil {
			return nil, err
		}
		out = append(out, sf)
	}
	return out, rows.Err()
}

func storedSetByBase(files []storedFile) map[string]bool {
	s := make(map[string]bool, len(files))
	for _, f := range files {
		s[filepath.Base(f.path)] = true
	}
	return s
}

// hasNewProductionFiles returns true when the package directory contains a
// non-test .go file not present in the indexed file list. Resolution failures
// are treated as stale (conservative).
func hasNewProductionFiles(moduleRoot, packagePath string, indexed map[string]bool) bool {
	pkgDir, err := packageDir(moduleRoot, packagePath)
	if err != nil {
		return true
	}
	diskFiles, err := listGoFiles(pkgDir)
	if err != nil {
		return true
	}
	for _, df := range diskFiles {
		base := filepath.Base(df)
		if !indexed[base] && !strings.HasSuffix(base, "_test.go") {
			return true
		}
	}
	return false
}

// comparePerFileHashes returns (stale, complete). complete=false means a stored
// file lacked a hash and the per-file comparison was abandoned — caller should
// fall back to the package-level files_hash check (matches legacy behavior).
func comparePerFileHashes(files []storedFile) (stale, complete bool) {
	for _, sf := range files {
		if sf.hash == "" {
			return false, false
		}
		currentHash, err := hashFile(sf.path)
		if err != nil || currentHash != sf.hash {
			return true, true
		}
	}
	return false, true
}

func comparePackageHashFromDB(db *sql.DB, moduleRoot, packagePath string, files []storedFile) (bool, error) {
	var storedHash string
	err := db.QueryRow(
		`SELECT files_hash FROM package_meta WHERE module_root = ? AND package_path = ?`,
		moduleRoot, packagePath,
	).Scan(&storedHash)
	if err != nil || storedHash == "" {
		return true, nil
	}
	return computeAndCompareFilesHash(files, storedHash)
}

func computeAndCompareFilesHash(files []storedFile, storedHash string) (bool, error) {
	paths := make([]string, 0, len(files))
	for _, sf := range files {
		paths = append(paths, sf.path)
	}
	sort.Strings(paths)
	currentHash, err := ComputeFilesHash(paths)
	if err != nil {
		return true, nil
	}
	return currentHash != storedHash, nil
}

// StalePackages returns all package paths that need re-indexing.
func StalePackages(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT module_root, package_path FROM package_meta`)
	if err != nil {
		return nil, fmt.Errorf("query package_meta: %w", err)
	}
	defer rows.Close()

	type pkgEntry struct {
		moduleRoot  string
		packagePath string
	}
	var packages []pkgEntry
	for rows.Next() {
		var e pkgEntry
		if err := rows.Scan(&e.moduleRoot, &e.packagePath); err != nil {
			return nil, err
		}
		packages = append(packages, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var stale []string
	for _, pkg := range packages {
		isStale, err := IsPackageStale(db, pkg.moduleRoot, pkg.packagePath)
		if err != nil {
			// On error, treat as stale.
			stale = append(stale, pkg.packagePath)
			continue
		}
		if isStale {
			stale = append(stale, pkg.packagePath)
		}
	}
	return stale, nil
}

// packageDir resolves the filesystem directory for a Go package, given
// the module root directory and the package's import path.
func packageDir(moduleRoot, packagePath string) (string, error) {
	modFilePath := filepath.Join(moduleRoot, "go.mod")
	data, err := os.ReadFile(modFilePath)
	if err != nil {
		return "", err
	}
	modFile, err := modfile.Parse(modFilePath, data, nil)
	if err != nil {
		return "", fmt.Errorf("parse go.mod: %w", err)
	}
	if modFile.Module == nil {
		return "", fmt.Errorf("no module directive in go.mod")
	}
	modPath := modFile.Module.Mod.Path

	if packagePath == modPath {
		return moduleRoot, nil
	}
	if !strings.HasPrefix(packagePath, modPath+"/") {
		return "", fmt.Errorf("package %s is not under module %s", packagePath, modPath)
	}
	suffix := strings.TrimPrefix(packagePath, modPath+"/")
	return filepath.Join(moduleRoot, filepath.FromSlash(suffix)), nil
}

// listGoFiles returns sorted absolute paths of .go files in dir.
func listGoFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".go") {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(files)
	return files, nil
}

// ComputeFilesHash computes a deterministic package-level hash by XOR-combining
// the FNV-64a hashes of all files in the sorted list.
// Exported so the indexer can store the hash at index time.
func ComputeFilesHash(files []string) (string, error) {
	var combined uint64
	for _, f := range files {
		fh, err := hashFileUint64(f)
		if err != nil {
			return "", err
		}
		combined ^= fh
	}
	return fmt.Sprintf("%016x", combined), nil
}

// hashFileUint64 returns the FNV-64a hash of a file's contents as a uint64.
func hashFileUint64(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	h := fnv.New64a()
	h.Write(data)
	return h.Sum64(), nil
}

// hashFile returns the hex-encoded FNV-64a hash of a file's contents.
func hashFile(path string) (string, error) {
	v, err := hashFileUint64(path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%016x", v), nil
}

// ---- store.ReadStore-backed variants (Phase 3) ----

type gitFastPathState struct {
	available    bool
	repoRoot     string
	changedFiles map[string]bool
}

func loadGitFastPathState(indexedCommit, moduleRoot string) gitFastPathState {
	if indexedCommit == "" {
		return gitFastPathState{}
	}
	root, err := gitRoot(moduleRoot)
	if err != nil {
		return gitFastPathState{}
	}
	changed, err := gitChangedFiles(moduleRoot, indexedCommit)
	if err != nil {
		return gitFastPathState{}
	}
	return gitFastPathState{available: true, repoRoot: root, changedFiles: changed}
}

// pkgStaleViaFastPath reports (handled, stale). When handled=false, caller must
// fall back to the file-hash check.
func pkgStaleViaFastPath(pkg store.PackageMetaRow, st gitFastPathState) (bool, bool) {
	if !st.available {
		return false, false
	}
	pkgDir, err := packageDir(pkg.ModuleRoot, pkg.PackagePath)
	if err != nil {
		return false, false
	}
	relPkg, err := filepath.Rel(st.repoRoot, pkgDir)
	if err != nil {
		return false, false
	}
	relPkg = filepath.ToSlash(relPkg)
	prefix := relPkg + "/"
	for f := range st.changedFiles {
		f = filepath.ToSlash(f)
		if f == relPkg || strings.HasPrefix(f, prefix) {
			return true, true
		}
	}
	return true, false
}

// StalePackagesStore is the store.ReadStore-backed version of StalePackages.
// It runs the git diff once and checks all packages against the cached result,
// avoiding per-package process spawns.
func StalePackagesStore(rs store.ReadStore) ([]string, error) {
	ctx := context.Background()
	pkgs, err := rs.AllPackagePaths(ctx)
	if err != nil {
		return nil, fmt.Errorf("StalePackagesStore: %w", err)
	}
	if len(pkgs) == 0 {
		return nil, nil
	}
	indexedCommit, _ := rs.IndexedCommit(ctx)
	fast := loadGitFastPathState(indexedCommit, pkgs[0].ModuleRoot)

	var stale []string
	for _, pkg := range pkgs {
		handled, isStale := pkgStaleViaFastPath(pkg, fast)
		if handled {
			if isStale {
				stale = append(stale, pkg.PackagePath)
			}
			continue
		}
		isStale, err := fileHashFallbackStore(rs, pkg.ModuleRoot, pkg.PackagePath)
		if err != nil || isStale {
			stale = append(stale, pkg.PackagePath)
		}
	}
	return stale, nil
}

// fileHashFallbackStore is the store.ReadStore-backed equivalent of fileHashFallback.
func fileHashFallbackStore(rs store.ReadStore, moduleRoot, packagePath string) (bool, error) {
	ctx := context.Background()
	files, err := rs.PackageFiles(ctx, moduleRoot, packagePath)
	if err != nil {
		return true, fmt.Errorf("fileHashFallbackStore PackageFiles: %w", err)
	}
	if len(files) == 0 {
		return true, nil
	}
	if hasNewProductionFiles(moduleRoot, packagePath, packageFileSetByBase(files)) {
		return true, nil
	}
	stale, complete := comparePerFileHashesStore(files)
	if stale {
		return true, nil
	}
	if complete {
		return false, nil
	}
	return comparePackageHashFromStore(rs, ctx, moduleRoot, packagePath, files)
}

func packageFileSetByBase(files []store.PackageFile) map[string]bool {
	s := make(map[string]bool, len(files))
	for _, f := range files {
		s[filepath.Base(f.FilePath)] = true
	}
	return s
}

func comparePerFileHashesStore(files []store.PackageFile) (stale, complete bool) {
	for _, f := range files {
		if f.FileHash == "" {
			return false, false
		}
		currentHash, err := hashFile(f.FilePath)
		if err != nil || currentHash != f.FileHash {
			return true, true
		}
	}
	return false, true
}

func comparePackageHashFromStore(rs store.ReadStore, ctx context.Context, moduleRoot, packagePath string, files []store.PackageFile) (bool, error) {
	storedHash, err := rs.StoredFilesHash(ctx, moduleRoot, packagePath)
	if err != nil || storedHash == "" {
		return true, nil
	}
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.FilePath)
	}
	sort.Strings(paths)
	currentHash, err := ComputeFilesHash(paths)
	if err != nil {
		return true, nil
	}
	return currentHash != storedHash, nil
}
