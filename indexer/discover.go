package indexer

import (
	"io/fs"
	"path/filepath"
	"sort"
)

// DiscoverModules walks root and returns the directory path of every go.mod found.
// Hidden directories (starting with "."), vendor, and node_modules are skipped.
func DiscoverModules(root string) ([]string, error) {
	seen := map[string]struct{}{}
	var mods []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := d.Name()
			if len(base) > 0 && base[0] == '.' || base == "vendor" || base == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "go.mod" {
			return nil
		}
		dir := filepath.Dir(path)
		if _, ok := seen[dir]; ok {
			return nil
		}
		seen[dir] = struct{}{}
		mods = append(mods, dir)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(mods)
	return mods, nil
}
