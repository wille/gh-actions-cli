package discover

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// Build a repo-like tree with a large node_modules to mimic a real JS/monorepo.
func buildTree(tb testing.TB) string {
	tb.Helper()
	root := tb.TempDir()
	_ = os.MkdirAll(filepath.Join(root, ".github", "workflows"), 0o755)
	_ = os.WriteFile(filepath.Join(root, ".github", "workflows", "ci.yml"), []byte("name: ci\n"), 0o644)
	// 30 packages × 200 files each = 6000 files under node_modules.
	for p := range 30 {
		dir := filepath.Join(root, "node_modules", "pkg"+strconv.Itoa(p), "dist")
		_ = os.MkdirAll(dir, 0o755)
		for f := range 200 {
			_ = os.WriteFile(filepath.Join(dir, "f"+strconv.Itoa(f)+".js"), []byte("x"), 0o644)
		}
	}
	return root
}

func BenchmarkDiscoverLargeTree(b *testing.B) {
	root := buildTree(b)
	for b.Loop() {
		if _, err := Files([]string{root}); err != nil {
			b.Fatal(err)
		}
	}
}
