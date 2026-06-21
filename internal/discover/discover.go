// Package discover finds workflow and composite-action files to process.
package discover

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Directories never worth descending into when searching for action files.
var prunedDirs = map[string]bool{
	"node_modules": true,
	".git":         true,
}

// Files resolves the set of files to process. With no paths it scans the cwd;
// explicit paths may be files (used as-is) or directories (scanned). For each
// scanned root it collects `.github/workflows/*.{yml,yaml}` and every composite
// `action.{yml,yaml}`, pruning node_modules/.git while walking so large trees
// stay cheap. Results are sorted and de-duplicated.
func Files(paths []string) ([]string, error) {
	set := map[string]struct{}{}

	if len(paths) == 0 {
		if err := scanDir(".", set); err != nil {
			return nil, err
		}
	} else {
		for _, p := range paths {
			info, err := os.Stat(p)
			if err == nil && info.IsDir() {
				if err := scanDir(p, set); err != nil {
					return nil, err
				}
			} else {
				// A file path (or non-existent path treated literally).
				set[p] = struct{}{}
			}
		}
	}

	out := make([]string, 0, len(set))
	for f := range set {
		out = append(out, f)
	}
	sort.Strings(out)
	return out, nil
}

// scanDir collects workflow and composite-action files under root.
func scanDir(root string, set map[string]struct{}) error {
	// Workflow files: a shallow glob, no walking required.
	for _, ext := range []string{"yml", "yaml"} {
		matches, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*."+ext))
		if err != nil {
			return err
		}
		for _, m := range matches {
			set[m] = struct{}{}
		}
	}

	// Composite actions: walk, but skip pruned directories entirely.
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries rather than aborting
		}
		if d.IsDir() {
			if path != root && prunedDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if name := d.Name(); name == "action.yml" || name == "action.yaml" {
			set[path] = struct{}{}
		}
		return nil
	})
}
