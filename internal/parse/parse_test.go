package parse

import (
	"os"
	"path/filepath"
	"testing"
)

func loadFixture(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "ci.yml"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(b)
}

func ptr(s string) *string { return &s }

func TestParseUses_Fixture(t *testing.T) {
	refs := Uses(loadFixture(t))

	wantActions := []string{
		"actions/checkout",
		"actions/setup-node",
		"actions/cache",
		"actions/upload-artifact",
	}
	if len(refs) != len(wantActions) {
		t.Fatalf("got %d refs, want %d: %+v", len(refs), len(wantActions), refs)
	}
	for i, want := range wantActions {
		if refs[i].Action != want {
			t.Errorf("ref %d action = %q, want %q", i, refs[i].Action, want)
		}
	}

	if refs[0].Owner != "actions" || refs[0].Repo != "checkout" {
		t.Errorf("owner/repo split wrong: %+v", refs[0])
	}
	if refs[1].Ref != "v4.0.2" {
		t.Errorf("setup-node ref = %q, want v4.0.2", refs[1].Ref)
	}

	cache := refs[2]
	if cache.Quote != `"` || cache.Comment != " # caching" {
		t.Errorf("cache quote/comment wrong: quote=%q comment=%q", cache.Quote, cache.Comment)
	}

	art := refs[3]
	if art.Ref != "08c6903cd8c0fde910a37f88322edcfb5dd907a8" || art.Comment != " # v4" {
		t.Errorf("upload-artifact ref/comment wrong: ref=%q comment=%q", art.Ref, art.Comment)
	}
}

func TestParseUses_SkipsAndEdges(t *testing.T) {
	if got := Uses("      - uses: justaword@v1"); len(got) != 0 {
		t.Errorf("single-segment should be skipped, got %+v", got)
	}
	if got := Uses("      - uses: ./.github/actions/local"); len(got) != 0 {
		t.Errorf("local ref should be skipped")
	}
	if got := Uses("      - uses: docker://alpine:3.20"); len(got) != 0 {
		t.Errorf("docker ref should be skipped")
	}
	if got := Uses(`      - run: echo "not a uses line"`); len(got) != 0 {
		t.Errorf("run line should not match")
	}

	sub := Uses("      - uses: org/repo/path/to/action@v2")
	if len(sub) != 1 || sub[0].Action != "org/repo/path/to/action" ||
		sub[0].Owner != "org" || sub[0].Repo != "repo" || sub[0].Ref != "v2" {
		t.Errorf("subpath parse wrong: %+v", sub)
	}

	// Mismatched quotes (open but no close) must not match.
	if got := Uses(`      - uses: "actions/cache@v3`); len(got) != 0 {
		t.Errorf("mismatched quotes should not match, got %+v", got)
	}
}

const sha = "1111111111111111111111111111111111111111"

func TestApplyRewrites(t *testing.T) {
	tests := []struct {
		name    string
		content string
		version *string
		want    string
	}{
		{
			"pin floating with comment",
			"      - uses: actions/checkout@v4",
			ptr("v4"),
			"      - uses: actions/checkout@" + sha + " # v4",
		},
		{
			"preserve double quotes",
			`      - uses: "actions/cache@v3" # caching`,
			ptr("v3"),
			`      - uses: "actions/cache@` + sha + `" # v3`,
		},
		{
			"preserve subpath",
			"      - uses: org/repo/path@v2",
			ptr("v2"),
			"      - uses: org/repo/path@" + sha + " # v2",
		},
		{
			"omit comment when version nil",
			"      - uses: actions/checkout@v4",
			nil,
			"      - uses: actions/checkout@" + sha,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			refs := Uses(tc.content)
			out := ApplyRewrites(tc.content, refs, []Rewrite{{Line: 0, SHA: sha, Version: tc.version}})
			if out != tc.want {
				t.Errorf("got %q\nwant %q", out, tc.want)
			}
		})
	}
}

func TestApplyRewrites_LeavesOtherLinesUntouched(t *testing.T) {
	content := "name: CI\n      - uses: actions/checkout@v4\n      - run: echo hi"
	refs := Uses(content)
	out := ApplyRewrites(content, refs, []Rewrite{{Line: 1, SHA: sha, Version: ptr("v4")}})
	lines := []string{"name: CI", "      - uses: actions/checkout@" + sha + " # v4", "      - run: echo hi"}
	want := lines[0] + "\n" + lines[1] + "\n" + lines[2]
	if out != want {
		t.Errorf("got %q\nwant %q", out, want)
	}
}
