// Package stats aggregates and formats GitHub Actions workflow run statistics.
package stats

import (
	"fmt"
	"math"
	"sort"
)

// RunSample is one completed workflow run reduced to what stats need.
type RunSample struct {
	DurationMs int64  // wall-clock (run_started_at → updated_at)
	Success    bool   // conclusion == "success"
	FinishedAt string // ISO timestamp the run finished (updated_at)
}

// LastRun summarizes the most recent run.
type LastRun struct {
	Success    bool   `json:"success"`
	FinishedAt string `json:"finishedAt"`
}

// WorkflowStats are aggregated stats for one workflow.
type WorkflowStats struct {
	Name        string   `json:"name"`
	Runs        int      `json:"runs"`
	SuccessRate float64  `json:"successRate"` // 0..1
	P50Ms       int64    `json:"p50Ms"`
	P95Ms       int64    `json:"p95Ms"`
	MaxMs       int64    `json:"maxMs"`
	Last        *LastRun `json:"last,omitempty"`
	// BillableMs is billable GitHub-hosted runner time for the current
	// billing cycle (not the --since window), keyed by OS ("UBUNTU",
	// "MACOS", "WINDOWS"). Empty for public repos, where minutes are free.
	BillableMs map[string]int64 `json:"billableMs,omitempty"`
}

// JobStats is an aggregated p95 duration for a single job across sampled runs.
type JobStats struct {
	Name  string
	Runs  int
	P95Ms int64
}

// Percentile returns the nearest-rank percentile of a pre-sorted ascending
// slice. Returns 0 for an empty input.
func Percentile(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil((p/100)*float64(len(sorted)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank > len(sorted)-1 {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}

// Summarize aggregates a workflow's run samples. samples is newest-first.
func Summarize(name string, samples []RunSample) WorkflowStats {
	durations := make([]int64, len(samples))
	successes := 0
	for i, s := range samples {
		durations[i] = s.DurationMs
		if s.Success {
			successes++
		}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })

	st := WorkflowStats{Name: name, Runs: len(samples)}
	if len(samples) > 0 {
		st.SuccessRate = float64(successes) / float64(len(samples))
		st.P50Ms = Percentile(durations, 50)
		st.P95Ms = Percentile(durations, 95)
		st.MaxMs = durations[len(durations)-1]
		st.Last = &LastRun{Success: samples[0].Success, FinishedAt: samples[0].FinishedAt}
	}
	return st
}

// JobSample is a single job's measured duration.
type JobSample struct {
	Name       string
	DurationMs int64
}

// SummarizeJobs groups job samples by name and returns per-job p95, sorted
// slowest-first.
func SummarizeJobs(jobs []JobSample) []JobStats {
	byName := map[string][]int64{}
	for _, j := range jobs {
		byName[j.Name] = append(byName[j.Name], j.DurationMs)
	}
	out := make([]JobStats, 0, len(byName))
	for name, durs := range byName {
		sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
		out = append(out, JobStats{Name: name, Runs: len(durs), P95Ms: Percentile(durs, 95)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].P95Ms > out[j].P95Ms })
	return out
}

// FmtDuration formats a millisecond duration compactly: "1h 2m", "3m 40s", "12s".
func FmtDuration(ms int64) string {
	if ms <= 0 {
		return "0s"
	}
	total := int(math.Round(float64(ms) / 1000))
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm %ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}
