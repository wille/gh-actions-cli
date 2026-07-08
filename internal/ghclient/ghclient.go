// Package ghclient wraps go-github with token resolution, an 8-way concurrency
// cap, and in-flight de-duplication/caching for repeated lookups.
package ghclient

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/google/go-github/v66/github"
	"golang.org/x/sync/singleflight"

	"github.com/wille/gh-actions-cli/internal/ui"
)

// Client is a concurrency- and cache-aware GitHub API client.
type Client struct {
	gh            *github.Client
	Authenticated bool

	sem         chan struct{}
	sf          singleflight.Group
	commitCache sync.Map // "owner/repo@ref" -> commitInfo
	tagsCache   sync.Map // "owner/repo" -> []string
}

// commitInfo caches both facts one GetCommit call returns, so resolving a
// ref's SHA and its commit date share a single API request.
type commitInfo struct {
	sha  string
	date time.Time
}

// WorkflowMeta is the subset of a workflow we need.
type WorkflowMeta struct {
	ID   int64
	Name string
	Path string
}

// WorkflowRun is the subset of a run we need.
type WorkflowRun struct {
	ID            int64
	Status        string
	Conclusion    string
	RunStartedAt  time.Time
	HasRunStarted bool
	UpdatedAt     time.Time
}

// RunJob is the subset of a job we need.
type RunJob struct {
	Name         string
	StartedAt    time.Time
	HasStarted   bool
	CompletedAt  time.Time
	HasCompleted bool
}

// resolveToken returns a token from env, then the gh CLI, else "".
func resolveToken() string {
	if t := os.Getenv("GITHUB_TOKEN"); t != "" {
		return t
	}
	if t := os.Getenv("GH_TOKEN"); t != "" {
		return t
	}
	if out, err := exec.Command("gh", "auth", "token").Output(); err == nil {
		if t := strings.TrimSpace(string(out)); t != "" {
			return t
		}
	}
	return ""
}

// New constructs a client for the given host, resolving a token if available.
// host "" or "github.com" uses the public API; any other host is treated as a
// GitHub Enterprise Server instance (API at https://<host>/api/v3/).
func New(host string) *Client {
	token := resolveToken()
	gh := github.NewClient(nil)
	if host != "" && host != "github.com" {
		base := "https://" + host + "/api/v3/"
		if c, err := gh.WithEnterpriseURLs(base, base); err == nil {
			gh = c
		}
	}
	if token != "" {
		gh = gh.WithAuthToken(token)
	}
	return &Client{gh: gh, Authenticated: token != "", sem: make(chan struct{}, 8)}
}

// WarnIfUnauthenticated prints a rate-limit warning to stderr when no token.
func (c *Client) WarnIfUnauthenticated() {
	if !c.Authenticated {
		fmt.Fprintln(os.Stderr, ui.Yellow("⚠ No GitHub token found (set GITHUB_TOKEN or run `gh auth login`). "+
			"Running unauthenticated — API rate limits are low."))
	}
}

func (c *Client) acquire() { c.sem <- struct{}{} }
func (c *Client) release() { <-c.sem }

// ResolveSha resolves a tag/branch/sha to a full commit SHA (cached, deduped).
func (c *Client) ResolveSha(owner, repo, ref string) (string, error) {
	info, err := c.commit(owner, repo, ref)
	return info.sha, err
}

// CommitDate returns the commit date of a tag/branch/sha (cached, deduped).
func (c *Client) CommitDate(owner, repo, ref string) (time.Time, error) {
	info, err := c.commit(owner, repo, ref)
	return info.date, err
}

func (c *Client) commit(owner, repo, ref string) (commitInfo, error) {
	key := owner + "/" + repo + "@" + ref
	if v, ok := c.commitCache.Load(key); ok {
		return v.(commitInfo), nil
	}
	v, err, _ := c.sf.Do("commit:"+key, func() (any, error) {
		if v, ok := c.commitCache.Load(key); ok {
			return v.(commitInfo), nil
		}
		c.acquire()
		defer c.release()
		commit, _, err := c.gh.Repositories.GetCommit(context.Background(), owner, repo, ref, nil)
		if err != nil {
			return commitInfo{}, err
		}
		info := commitInfo{
			sha:  commit.GetSHA(),
			date: commit.GetCommit().GetCommitter().GetDate().Time,
		}
		c.commitCache.Store(key, info)
		return info, nil
	})
	if err != nil {
		return commitInfo{}, err
	}
	return v.(commitInfo), nil
}

// ListTags returns all tag names for a repo (paginated, cached, deduped).
func (c *Client) ListTags(owner, repo string) ([]string, error) {
	key := owner + "/" + repo
	if v, ok := c.tagsCache.Load(key); ok {
		return v.([]string), nil
	}
	v, err, _ := c.sf.Do("tags:"+key, func() (any, error) {
		if v, ok := c.tagsCache.Load(key); ok {
			return v.([]string), nil
		}
		c.acquire()
		defer c.release()
		var names []string
		opt := &github.ListOptions{PerPage: 100}
		for {
			tags, resp, err := c.gh.Repositories.ListTags(context.Background(), owner, repo, opt)
			if err != nil {
				return nil, err
			}
			for _, t := range tags {
				names = append(names, t.GetName())
			}
			if resp.NextPage == 0 {
				break
			}
			opt.Page = resp.NextPage
		}
		c.tagsCache.Store(key, names)
		return names, nil
	})
	if err != nil {
		return nil, err
	}
	return v.([]string), nil
}

// WorkflowBillableMs returns a workflow's billable GitHub-hosted runner
// milliseconds for the current billing cycle, keyed by OS ("UBUNTU", "MACOS",
// "WINDOWS"). The map is empty for public repos, where Actions minutes are
// free and GitHub reports no billable usage.
func (c *Client) WorkflowBillableMs(owner, repo string, workflowID int64) (map[string]int64, error) {
	c.acquire()
	defer c.release()
	usage, _, err := c.gh.Actions.GetWorkflowUsageByID(context.Background(), owner, repo, workflowID)
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	if usage.Billable != nil {
		for osName, bill := range *usage.Billable {
			if ms := bill.GetTotalMS(); ms > 0 {
				out[osName] = ms
			}
		}
	}
	return out, nil
}

// RepoBillableUsage is a repo's billed Actions minutes for one calendar month,
// aggregated from the enhanced billing platform's usage report.
type RepoBillableUsage struct {
	MinutesBySKU map[string]float64 // e.g. "Actions Linux", "Actions Linux 4-core"
	NetUSD       float64            // amount actually charged after discounts/included minutes
}

// RepoBillableMinutes returns the repo's billed GitHub-hosted runner minutes
// for the given month via the enhanced billing platform's usage report
// (GET /organizations/{org}/settings/billing/usage). This is the successor to
// the per-workflow timing endpoint, which always reports empty billable data
// for owners migrated to the new platform; the report is repo-granular only.
// Falls back to the user-owner variant when the owner is not an organization.
func (c *Client) RepoBillableMinutes(owner, repo string, year int, month int) (RepoBillableUsage, error) {
	usage, err := c.billableUsage("organizations", owner, repo, year, month)
	if err != nil {
		return c.billableUsage("users", owner, repo, year, month)
	}
	return usage, nil
}

func (c *Client) billableUsage(ownerKind, owner, repo string, year, month int) (RepoBillableUsage, error) {
	c.acquire()
	defer c.release()
	var report struct {
		UsageItems []struct {
			Product        string  `json:"product"`
			SKU            string  `json:"sku"`
			Quantity       float64 `json:"quantity"`
			UnitType       string  `json:"unitType"`
			NetAmount      float64 `json:"netAmount"`
			RepositoryName string  `json:"repositoryName"`
		} `json:"usageItems"`
	}
	u := fmt.Sprintf("%s/%s/settings/billing/usage?year=%d&month=%d", ownerKind, owner, year, month)
	if err := c.rawGet(u, &report); err != nil {
		return RepoBillableUsage{}, err
	}
	usage := RepoBillableUsage{MinutesBySKU: map[string]float64{}}
	for _, item := range report.UsageItems {
		if item.Product != "actions" || item.UnitType != "Minutes" || item.RepositoryName != repo {
			continue
		}
		usage.MinutesBySKU[item.SKU] += item.Quantity
		usage.NetUSD += item.NetAmount
	}
	return usage, nil
}

// SelectedActions is the allowlist in force when a policy's AllowedActions is
// "selected".
type SelectedActions struct {
	GithubOwnedAllowed bool     `json:"github_owned_allowed"`
	VerifiedAllowed    bool     `json:"verified_allowed"`
	PatternsAllowed    []string `json:"patterns_allowed"`
}

// ActionsPolicy is a repo's Actions permissions policy.
type ActionsPolicy struct {
	Enabled            bool   // whether Actions can run at all
	AllowedActions     string // "all", "local_only", or "selected"
	ShaPinningRequired bool   // GitHub rejects workflows with non-SHA refs
	Selected           *SelectedActions
}

// ActionsPolicy fetches the repo's Actions permissions, including the
// selected-actions allowlist when one is in force. Uses raw requests because
// go-github v66 predates the sha_pinning_required field.
func (c *Client) ActionsPolicy(owner, repo string) (ActionsPolicy, error) {
	c.acquire()
	defer c.release()
	var perms struct {
		Enabled            bool   `json:"enabled"`
		AllowedActions     string `json:"allowed_actions"`
		ShaPinningRequired bool   `json:"sha_pinning_required"`
	}
	if err := c.rawGet(fmt.Sprintf("repos/%s/%s/actions/permissions", owner, repo), &perms); err != nil {
		return ActionsPolicy{}, err
	}
	policy := ActionsPolicy{
		Enabled:            perms.Enabled,
		AllowedActions:     perms.AllowedActions,
		ShaPinningRequired: perms.ShaPinningRequired,
	}
	// The selected-actions endpoint 409s unless allowed_actions is "selected".
	if perms.AllowedActions == "selected" {
		var sel SelectedActions
		if err := c.rawGet(fmt.Sprintf("repos/%s/%s/actions/permissions/selected-actions", owner, repo), &sel); err != nil {
			return ActionsPolicy{}, err
		}
		policy.Selected = &sel
	}
	return policy, nil
}

// SetActionsPolicy applies a selected-actions policy: allowed_actions is set
// to "selected" and the allowlist written. requirePin additionally turns on
// SHA pinning enforcement; false leaves the repo's current setting untouched
// (this never downgrades an already-enabled requirement). A 409 from GitHub
// usually means an org-level policy pins the repo's settings.
func (c *Client) SetActionsPolicy(owner, repo string, selected SelectedActions, requirePin bool) error {
	c.acquire()
	defer c.release()
	perms := map[string]any{"enabled": true, "allowed_actions": "selected"}
	if requirePin {
		perms["sha_pinning_required"] = true
	}
	if err := c.rawDo("PUT", fmt.Sprintf("repos/%s/%s/actions/permissions", owner, repo), perms, nil); err != nil {
		return err
	}
	// PatternsAllowed must not be null in the request body.
	if selected.PatternsAllowed == nil {
		selected.PatternsAllowed = []string{}
	}
	return c.rawDo("PUT", fmt.Sprintf("repos/%s/%s/actions/permissions/selected-actions", owner, repo), selected, nil)
}

// rawGet performs a GET for endpoints (or fields) go-github doesn't cover.
// Callers hold the concurrency semaphore.
func (c *Client) rawGet(url string, into any) error {
	return c.rawDo("GET", url, nil, into)
}

// rawDo performs a request for endpoints (or fields) go-github doesn't cover.
// Callers hold the concurrency semaphore.
func (c *Client) rawDo(method, url string, body, into any) error {
	req, err := c.gh.NewRequest(method, url, body)
	if err != nil {
		return err
	}
	_, err = c.gh.Do(context.Background(), req, into)
	return err
}

// DefaultBranch returns the repo's default branch (e.g. "main").
func (c *Client) DefaultBranch(owner, repo string) (string, error) {
	c.acquire()
	defer c.release()
	r, _, err := c.gh.Repositories.Get(context.Background(), owner, repo)
	if err != nil {
		return "", err
	}
	return r.GetDefaultBranch(), nil
}

// ListWorkflows lists the repo's workflows.
func (c *Client) ListWorkflows(owner, repo string) ([]WorkflowMeta, error) {
	c.acquire()
	defer c.release()
	var out []WorkflowMeta
	opt := &github.ListOptions{PerPage: 100}
	for {
		wfs, resp, err := c.gh.Actions.ListWorkflows(context.Background(), owner, repo, opt)
		if err != nil {
			return nil, err
		}
		for _, w := range wfs.Workflows {
			out = append(out, WorkflowMeta{ID: w.GetID(), Name: w.GetName(), Path: w.GetPath()})
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return out, nil
}

// maxRunsPerWorkflow backstops pagination so a hyperactive workflow can't pull
// an unbounded number of runs even within the time window.
const maxRunsPerWorkflow = 1000

// ListWorkflowRuns returns a workflow's runs created on or after `since`,
// newest first (server-side filtered via the API's `created` query).
func (c *Client) ListWorkflowRuns(owner, repo string, workflowID int64, branch string, since time.Time) ([]WorkflowRun, error) {
	c.acquire()
	defer c.release()
	var out []WorkflowRun
	opt := &github.ListWorkflowRunsOptions{
		Created:     ">=" + since.UTC().Format("2006-01-02"),
		ListOptions: github.ListOptions{PerPage: 100},
	}
	if branch != "" {
		opt.Branch = branch
	}
	for {
		runs, resp, err := c.gh.Actions.ListWorkflowRunsByID(context.Background(), owner, repo, workflowID, opt)
		if err != nil {
			return nil, err
		}
		for _, r := range runs.WorkflowRuns {
			wr := WorkflowRun{
				ID:         r.GetID(),
				Status:     r.GetStatus(),
				Conclusion: r.GetConclusion(),
				UpdatedAt:  r.GetUpdatedAt().Time,
			}
			if r.RunStartedAt != nil {
				wr.RunStartedAt = r.RunStartedAt.Time
				wr.HasRunStarted = true
			}
			out = append(out, wr)
			if len(out) >= maxRunsPerWorkflow {
				return out, nil
			}
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return out, nil
}

// ListRunJobs returns the jobs for one workflow run.
func (c *Client) ListRunJobs(owner, repo string, runID int64) ([]RunJob, error) {
	c.acquire()
	defer c.release()
	var out []RunJob
	opt := &github.ListWorkflowJobsOptions{ListOptions: github.ListOptions{PerPage: 100}}
	for {
		jobs, resp, err := c.gh.Actions.ListWorkflowJobs(context.Background(), owner, repo, runID, opt)
		if err != nil {
			return nil, err
		}
		for _, j := range jobs.Jobs {
			rj := RunJob{Name: j.GetName()}
			if j.StartedAt != nil {
				rj.StartedAt = j.StartedAt.Time
				rj.HasStarted = true
			}
			if j.CompletedAt != nil {
				rj.CompletedAt = j.CompletedAt.Time
				rj.HasCompleted = true
			}
			out = append(out, rj)
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return out, nil
}
