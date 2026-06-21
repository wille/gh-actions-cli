package command

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/wille/gh-actions-cli/internal/ghclient"
	"github.com/wille/gh-actions-cli/internal/repo"
	"github.com/wille/gh-actions-cli/internal/stats"
	"github.com/wille/gh-actions-cli/internal/ui"
)

// StatsOptions configures the stats command.
type StatsOptions struct {
	Repo   string
	Branch string
	Since  string // time window, e.g. "7d", "2w", "24h"
	Jobs   bool
	JSON   bool
}

const jobSample = 20

var sinceRE = regexp.MustCompile(`^(\d+)\s*([smhdw])$`)

// parseSince accepts 7d / 2w / 24h / 90m / 45s, or any Go duration (e.g.
// 1h30m), returning the window length.
func parseSince(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if m := sinceRE.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		unit := map[string]time.Duration{
			"s": time.Second, "m": time.Minute, "h": time.Hour,
			"d": 24 * time.Hour, "w": 7 * 24 * time.Hour,
		}[m[2]]
		return time.Duration(n) * unit, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid --since %q (use e.g. 7d, 2w, 24h)", s)
	}
	return d, nil
}

type wfResult struct {
	meta  ghclient.WorkflowMeta
	stats stats.WorkflowStats
}

type statsJSON struct {
	Owner     string                `json:"owner"`
	Repo      string                `json:"repo"`
	Branch    string                `json:"branch"`
	Since     string                `json:"since"`
	Workflows []stats.WorkflowStats `json:"workflows"`
}

// RunStats reports workflow run health and timing.
func RunStats(workflowArg string, opts StatsOptions) error {
	r, err := repo.ResolveRepo(opts.Repo)
	if err != nil {
		return err
	}
	owner, repoName := r.Owner, r.Repo

	window, err := parseSince(opts.Since)
	if err != nil {
		return err
	}
	since := time.Now().Add(-window)
	sinceLabel := since.Format("2006-01-02")

	gh := ghclient.New()
	gh.WarnIfUnauthenticated()

	// No spinner in JSON mode — it would corrupt machine output.
	var spin *ui.Spinner
	if !opts.JSON {
		spin = ui.NewSpinner(fmt.Sprintf("Loading workflows for %s/%s", owner, repoName))
	}

	branch := opts.Branch
	if branch == "" {
		b, err := gh.DefaultBranch(owner, repoName)
		if err != nil {
			spin.Stop("")
			return err
		}
		branch = b
	}
	allWorkflows, err := gh.ListWorkflows(owner, repoName)
	if err != nil {
		spin.Stop("")
		return err
	}

	if len(allWorkflows) == 0 {
		emptyResult(spin, opts.JSON, fmt.Sprintf("No workflows found in %s/%s.", owner, repoName), owner, repoName, branch)
		return nil
	}

	workflows := allWorkflows
	if workflowArg != "" {
		needle := strings.ToLower(workflowArg)
		var filtered []ghclient.WorkflowMeta
		for _, w := range allWorkflows {
			lp := strings.ToLower(w.Path)
			if strings.ToLower(w.Name) == needle ||
				strings.HasSuffix(lp, "/"+needle) ||
				lp == ".github/workflows/"+needle {
				filtered = append(filtered, w)
			}
		}
		if len(filtered) == 0 {
			emptyResult(spin, opts.JSON, fmt.Sprintf("No workflow matching %q.", workflowArg), owner, repoName, branch)
			return nil
		}
		workflows = filtered
	}

	total := len(workflows)
	results := make([]wfResult, total)
	var mu sync.Mutex
	doneN := 0
	spin.Message(fmt.Sprintf("Fetching runs (0/%d workflows)", total))

	var wg sync.WaitGroup
	for i, w := range workflows {
		wg.Add(1)
		go func(i int, w ghclient.WorkflowMeta) {
			defer wg.Done()
			var samples []stats.RunSample
			if runs, err := gh.ListWorkflowRuns(owner, repoName, w.ID, branch, since); err == nil {
				samples = toSamples(runs)
			}
			results[i] = wfResult{meta: w, stats: stats.Summarize(w.Name, samples)}
			mu.Lock()
			doneN++
			spin.Message(fmt.Sprintf("Fetching runs (%d/%d workflows)", doneN, total))
			mu.Unlock()
		}(i, w)
	}
	wg.Wait()

	spin.Stop(fmt.Sprintf("Analyzed %d workflow(s) on %s.", total, branch))

	// Most-active workflows first.
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].stats.Runs > results[j].stats.Runs
	})

	if opts.JSON {
		out := statsJSON{Owner: owner, Repo: repoName, Branch: branch, Since: sinceLabel, Workflows: make([]stats.WorkflowStats, len(results))}
		for i, r := range results {
			out.Workflows[i] = r.stats
		}
		b, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}

	fmt.Printf("\n%s %s\n\n",
		ui.Bold(fmt.Sprintf("%s/%s", owner, repoName)),
		ui.Dim(fmt.Sprintf("· branch %s · since %s", branch, sinceLabel)))
	renderTable(results, owner, repoName)

	if opts.Jobs {
		focus := results[0]
		if workflowArg == "" {
			// slowest by p95
			for _, r := range results {
				if r.stats.P95Ms > focus.stats.P95Ms {
					focus = r
				}
			}
		}
		if err := renderJobs(gh, owner, repoName, branch, focus.meta, since); err != nil {
			return err
		}
	}
	return nil
}

func toSamples(runs []ghclient.WorkflowRun) []stats.RunSample {
	var out []stats.RunSample
	for _, r := range runs {
		if r.Status != "completed" || !r.HasRunStarted {
			continue
		}
		if r.UpdatedAt.Before(r.RunStartedAt) {
			continue
		}
		out = append(out, stats.RunSample{
			DurationMs: r.UpdatedAt.Sub(r.RunStartedAt).Milliseconds(),
			Success:    r.Conclusion == "success",
			FinishedAt: r.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}

func renderTable(results []wfResult, owner, repoName string) {
	type row struct {
		name, runs, success, p50, p95, slowest string
		url                                    string
		last                                   string
	}
	rows := make([]row, len(results))
	for i, res := range results {
		s := res.stats
		rw := row{name: s.Name, runs: strconv.Itoa(s.Runs), url: workflowURL(owner, repoName, res.meta.Path)}
		if s.Runs > 0 {
			rw.success = fmt.Sprintf("%d%%", int(s.SuccessRate*100+0.5))
			rw.p50 = stats.FmtDuration(s.P50Ms)
			rw.p95 = stats.FmtDuration(s.P95Ms)
			rw.slowest = stats.FmtDuration(s.MaxMs)
		} else {
			rw.success, rw.p50, rw.p95, rw.slowest = "—", "—", "—", "—"
		}
		if s.Last != nil {
			sym := ui.Green("✓")
			if !s.Last.Success {
				sym = ui.Red("✗")
			}
			rw.last = sym + " " + fmtAgo(s.Last.FinishedAt)
		} else {
			rw.last = ui.Dim("no runs")
		}
		rows[i] = rw
	}

	w := struct{ name, runs, success, p50, p95, slowest int }{
		name:    len("WORKFLOW"),
		runs:    len("RUNS"),
		success: len("SUCCESS"),
		p50:     len("p50"),
		p95:     len("p95"),
		slowest: len("SLOWEST"),
	}
	for _, r := range rows {
		w.name = max(w.name, utf8.RuneCountInString(r.name))
		w.runs = max(w.runs, utf8.RuneCountInString(r.runs))
		w.success = max(w.success, utf8.RuneCountInString(r.success))
		w.p50 = max(w.p50, utf8.RuneCountInString(r.p50))
		w.p95 = max(w.p95, utf8.RuneCountInString(r.p95))
		w.slowest = max(w.slowest, utf8.RuneCountInString(r.slowest))
	}

	header := strings.Join([]string{
		padRight("WORKFLOW", w.name),
		padLeft("RUNS", w.runs),
		padLeft("SUCCESS", w.success),
		padLeft("p50", w.p50),
		padLeft("p95", w.p95),
		padLeft("SLOWEST", w.slowest),
		"LAST",
	}, "  ")
	fmt.Println(ui.Bold(header))

	for _, r := range rows {
		// Pad on plain text, then colorize, so ANSI codes don't break alignment.
		successCell := padLeft(r.success, w.success)
		if r.success != "—" {
			if n, err := strconv.Atoi(strings.TrimSuffix(r.success, "%")); err == nil && n < 90 {
				successCell = ui.Yellow(successCell)
			}
		}
		// Link the workflow name to its Actions page; pad on plain width first.
		nameCell := ui.Cyan(ui.Link(r.name, r.url))
		if n := w.name - utf8.RuneCountInString(r.name); n > 0 {
			nameCell += strings.Repeat(" ", n)
		}
		line := strings.Join([]string{
			nameCell,
			padLeft(r.runs, w.runs),
			successCell,
			padLeft(r.p50, w.p50),
			padLeft(r.p95, w.p95),
			padLeft(r.slowest, w.slowest),
			r.last,
		}, "  ")
		fmt.Println(line)
	}
}

func renderJobs(gh *ghclient.Client, owner, repoName, branch string, meta ghclient.WorkflowMeta, since time.Time) error {
	runs, err := gh.ListWorkflowRuns(owner, repoName, meta.ID, branch, since)
	if err != nil {
		return err
	}
	var sample []ghclient.WorkflowRun
	for _, r := range runs {
		if r.Status == "completed" {
			sample = append(sample, r)
			if len(sample) >= jobSample {
				break
			}
		}
	}
	if len(sample) == 0 {
		return nil
	}

	var jobs []stats.JobSample
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, r := range sample {
		wg.Add(1)
		go func(runID int64) {
			defer wg.Done()
			runJobs, err := gh.ListRunJobs(owner, repoName, runID)
			if err != nil {
				return
			}
			mu.Lock()
			for _, j := range runJobs {
				if !j.HasStarted || !j.HasCompleted {
					continue
				}
				ms := j.CompletedAt.Sub(j.StartedAt).Milliseconds()
				if ms >= 0 {
					jobs = append(jobs, stats.JobSample{Name: j.Name, DurationMs: ms})
				}
			}
			mu.Unlock()
		}(r.ID)
	}
	wg.Wait()

	jobStats := stats.SummarizeJobs(jobs)
	if len(jobStats) == 0 {
		return nil
	}

	fmt.Printf("\n%s %s\n",
		ui.Bold("Slowest jobs"),
		ui.Dim(fmt.Sprintf("(%s, p95 over %d runs)", meta.Name, len(sample))))
	nameW := 0
	for _, j := range jobStats {
		nameW = max(nameW, utf8.RuneCountInString(j.Name))
	}
	for _, j := range jobStats {
		fmt.Printf("  %s  %s\n", padRight(j.Name, nameW), stats.FmtDuration(j.P95Ms))
	}
	return nil
}

func emptyResult(spin *ui.Spinner, asJSON bool, message, owner, repoName, branch string) {
	if asJSON {
		out := statsJSON{Owner: owner, Repo: repoName, Branch: branch, Workflows: []stats.WorkflowStats{}}
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
		return
	}
	spin.Stop(ui.Yellow(message))
}

func workflowURL(owner, repoName, path string) string {
	file := path
	if i := strings.LastIndex(path, "/"); i >= 0 {
		file = path[i+1:]
	}
	return fmt.Sprintf("https://github.com/%s/%s/actions/workflows/%s", owner, repoName, file)
}

// fmtAgo renders compact relative time like "2h ago", "3d ago".
func fmtAgo(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return ""
	}
	sec := int(time.Since(t).Seconds())
	if sec < 0 {
		sec = 0
	}
	if sec < 60 {
		return fmt.Sprintf("%ds ago", sec)
	}
	mins := sec / 60
	if mins < 60 {
		return fmt.Sprintf("%dm ago", mins)
	}
	hrs := mins / 60
	if hrs < 24 {
		return fmt.Sprintf("%dh ago", hrs)
	}
	return fmt.Sprintf("%dd ago", hrs/24)
}

func padLeft(s string, w int) string {
	if n := w - utf8.RuneCountInString(s); n > 0 {
		return strings.Repeat(" ", n) + s
	}
	return s
}

func padRight(s string, w int) string {
	if n := w - utf8.RuneCountInString(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}
