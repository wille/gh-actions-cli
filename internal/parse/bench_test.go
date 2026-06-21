package parse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func BenchmarkUsesFixture(b *testing.B) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "ci.yml"))
	if err != nil {
		b.Fatalf("read fixture: %v", err)
	}
	content := string(data)
	b.ReportAllocs()
	for b.Loop() {
		_ = Uses(content)
	}
}

func BenchmarkUsesLarge(b *testing.B) {
	// ~10k lines, a quarter of which are uses: refs.
	var sb strings.Builder
	for range 2500 {
		sb.WriteString("    runs-on: ubuntu-latest\n")
		sb.WriteString("      - uses: actions/checkout@v4\n")
		sb.WriteString("      - name: step\n")
		sb.WriteString(`      - run: echo "hello world"` + "\n")
	}
	content := sb.String()
	b.ReportAllocs()
	for b.Loop() {
		_ = Uses(content)
	}
}
