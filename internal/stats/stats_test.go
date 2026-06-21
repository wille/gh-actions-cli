package stats

import (
	"math"
	"testing"
)

func TestPercentile(t *testing.T) {
	sorted := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if got := Percentile(sorted, 50); got != 5 {
		t.Errorf("p50 = %d, want 5", got)
	}
	if got := Percentile(sorted, 95); got != 10 {
		t.Errorf("p95 = %d, want 10", got)
	}
	if got := Percentile(sorted, 100); got != 10 {
		t.Errorf("p100 = %d, want 10", got)
	}
	if got := Percentile(nil, 95); got != 0 {
		t.Errorf("empty = %d, want 0", got)
	}
}

func TestSummarize(t *testing.T) {
	samples := []RunSample{
		{DurationMs: 60000, Success: true, FinishedAt: "2026-06-21T10:00:00Z"},
		{DurationMs: 120000, Success: false, FinishedAt: "2026-06-21T09:00:00Z"},
		{DurationMs: 30000, Success: true, FinishedAt: "2026-06-21T08:00:00Z"},
	}
	s := Summarize("CI", samples)
	if s.Runs != 3 {
		t.Errorf("runs = %d, want 3", s.Runs)
	}
	if math.Abs(s.SuccessRate-2.0/3.0) > 1e-9 {
		t.Errorf("successRate = %v, want 2/3", s.SuccessRate)
	}
	if s.P50Ms != 60000 || s.MaxMs != 120000 {
		t.Errorf("durations wrong: p50=%d max=%d", s.P50Ms, s.MaxMs)
	}
	if s.Last == nil || !s.Last.Success || s.Last.FinishedAt != "2026-06-21T10:00:00Z" {
		t.Errorf("last wrong: %+v", s.Last)
	}

	empty := Summarize("X", nil)
	if empty.Runs != 0 || empty.SuccessRate != 0 || empty.Last != nil {
		t.Errorf("empty summary wrong: %+v", empty)
	}
}

func TestSummarizeJobs(t *testing.T) {
	out := SummarizeJobs([]JobSample{
		{Name: "build", DurationMs: 100},
		{Name: "build", DurationMs: 200},
		{Name: "lint", DurationMs: 10},
	})
	if len(out) != 2 || out[0].Name != "build" || out[0].Runs != 2 || out[1].Name != "lint" {
		t.Errorf("job grouping/sort wrong: %+v", out)
	}
}

func TestFmtDuration(t *testing.T) {
	cases := map[int64]string{
		0:       "0s",
		12000:   "12s",
		220000:  "3m 40s",
		3725000: "1h 2m",
	}
	for ms, want := range cases {
		if got := FmtDuration(ms); got != want {
			t.Errorf("FmtDuration(%d) = %q, want %q", ms, got, want)
		}
	}
}
