package command

import "testing"

func TestFmtBillable(t *testing.T) {
	cases := []struct {
		name string
		byOS map[string]int64
		want string
	}{
		{"nil map", nil, "—"},
		{"empty map", map[string]int64{}, "—"},
		{"zero entries", map[string]int64{"UBUNTU": 0}, "—"},
		{"whole minutes", map[string]int64{"UBUNTU": 42 * 60_000}, "42m"},
		{"sums across OS", map[string]int64{"UBUNTU": 30 * 60_000, "MACOS": 12 * 60_000}, "42m"},
		{"rounds partial minute up", map[string]int64{"UBUNTU": 61_000}, "2m"},
		{"hours", map[string]int64{"UBUNTU": 185 * 60_000}, "3h 5m"},
		{"exact hour", map[string]int64{"WINDOWS": 60 * 60_000}, "1h 0m"},
	}
	for _, tc := range cases {
		if got := fmtBillable(tc.byOS); got != tc.want {
			t.Errorf("%s: fmtBillable(%v) = %q, want %q", tc.name, tc.byOS, got, tc.want)
		}
	}
}
