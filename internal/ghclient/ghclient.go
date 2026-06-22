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

	sem       chan struct{}
	sf        singleflight.Group
	shaCache  sync.Map // "owner/repo@ref" -> string
	tagsCache sync.Map // "owner/repo" -> []string
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
	key := owner + "/" + repo + "@" + ref
	if v, ok := c.shaCache.Load(key); ok {
		return v.(string), nil
	}
	v, err, _ := c.sf.Do("sha:"+key, func() (any, error) {
		if v, ok := c.shaCache.Load(key); ok {
			return v.(string), nil
		}
		c.acquire()
		defer c.release()
		commit, _, err := c.gh.Repositories.GetCommit(context.Background(), owner, repo, ref, nil)
		if err != nil {
			return "", err
		}
		sha := commit.GetSHA()
		c.shaCache.Store(key, sha)
		return sha, nil
	})
	if err != nil {
		return "", err
	}
	return v.(string), nil
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
