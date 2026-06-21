package version

import "testing"

func TestIsSha(t *testing.T) {
	if !IsSha("08c6903cd8c0fde910a37f88322edcfb5dd907a8") {
		t.Error("40-char hex should be a SHA")
	}
	if IsSha("v4") || IsSha("08c6903") {
		t.Error("tag and short SHA should not be a SHA")
	}
}

func TestPickLatest(t *testing.T) {
	if got := PickLatest([]string{"v1.0.0", "v4.1.2", "v2.3.0", "v4.0.0"}); got != "v4.1.2" {
		t.Errorf("got %q, want v4.1.2", got)
	}
	if got := PickLatest([]string{"v4.1.2", "v5.0.0-beta.1"}); got != "v4.1.2" {
		t.Errorf("prerelease should be ignored, got %q", got)
	}
	if got := PickLatest([]string{"main", "latest"}); got != "" {
		t.Errorf("non-semver should yield empty, got %q", got)
	}
}

func TestIsOutdated(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"v4.0.0", "v4.1.2", true},
		{"v4.1.2", "v4.1.2", false},
		{"v5.0.0", "v4.1.2", false},
		{"main", "v1.0.0", true},
		{"v1.0.0", "notsemver", false},
	}
	for _, c := range cases {
		if got := IsOutdated(c.current, c.latest); got != c.want {
			t.Errorf("IsOutdated(%q, %q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
}
