package command

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/wille/gh-actions-cli/internal/discover"
	"github.com/wille/gh-actions-cli/internal/ghclient"
	"github.com/wille/gh-actions-cli/internal/parse"
	"github.com/wille/gh-actions-cli/internal/repo"
	"github.com/wille/gh-actions-cli/internal/ui"
	"github.com/wille/gh-actions-cli/internal/version"
)

// PolicyOptions configures the policy command.
type PolicyOptions struct {
	Repo         string
	JSON         bool
	Apply        bool // write the proposed policy to the repo settings
	NoRequirePin bool // do not turn on sha_pinning_required when applying
}

// proposedPolicy is the allowed-actions policy generated from local workflows.
type proposedPolicy struct {
	AllowedActions     string   `json:"allowedActions"` // always "selected"
	GithubOwnedAllowed bool     `json:"githubOwnedAllowed"`
	VerifiedAllowed    bool     `json:"verifiedAllowed"`
	PatternsAllowed    []string `json:"patternsAllowed"`
	ShaPinningRequired bool     `json:"shaPinningRequired"`
}

type policyCurrentJSON struct {
	Enabled            bool                      `json:"enabled"`
	AllowedActions     string                    `json:"allowedActions"`
	ShaPinningRequired bool                      `json:"shaPinningRequired"`
	Selected           *ghclient.SelectedActions `json:"selected,omitempty"`
}

type policyJSON struct {
	Owner    string            `json:"owner"`
	Repo     string            `json:"repo"`
	Current  policyCurrentJSON `json:"current"`
	Proposed *proposedPolicy   `json:"proposed,omitempty"`
	Files    int               `json:"files"`
	Actions  int               `json:"actions"`
}

// githubOwned reports whether an action owner is covered by the policy's
// github_owned_allowed switch rather than needing an explicit pattern.
func githubOwned(owner string) bool {
	return owner == "actions" || owner == "github"
}

// proposePolicy derives the tightest selected-actions policy that keeps the
// given refs runnable, with owner/repo@* patterns so version bumps don't
// require a policy change. SHA pinning enforcement is only proposed when every
// local ref is already pinned — enabling it earlier would break the workflows.
func proposePolicy(refs []parse.UsesRef) proposedPolicy {
	p := proposedPolicy{AllowedActions: "selected", ShaPinningRequired: true}
	seen := map[string]struct{}{}
	for _, r := range refs {
		if !version.IsSha(r.Ref) {
			p.ShaPinningRequired = false
		}
		if githubOwned(r.Owner) {
			p.GithubOwnedAllowed = true
			continue
		}
		pattern := r.Owner + "/" + r.Repo + "@*"
		if _, ok := seen[pattern]; !ok {
			seen[pattern] = struct{}{}
			p.PatternsAllowed = append(p.PatternsAllowed, pattern)
		}
	}
	sort.Strings(p.PatternsAllowed)
	return p
}

// RunPolicy shows the repo's allowed-actions policy next to one generated from
// the local workflow files. Read-only: nothing is written to GitHub.
func RunPolicy(paths []string, opts PolicyOptions) error {
	r, err := repo.ResolveRepo(opts.Repo)
	if err != nil {
		return err
	}

	files, err := discover.Files(paths)
	if err != nil {
		return err
	}
	var refs []parse.UsesRef
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		refs = append(refs, parse.Uses(string(content))...)
	}

	gh := ghclient.New(r.Host)
	gh.WarnIfUnauthenticated()
	current, err := gh.ActionsPolicy(r.Owner, r.Repo)
	if err != nil {
		return fmt.Errorf("fetching Actions permissions for %s/%s: %w", r.Owner, r.Repo, err)
	}

	var proposed *proposedPolicy
	if len(refs) > 0 {
		p := proposePolicy(refs)
		proposed = &p
	}

	if opts.JSON {
		out := policyJSON{
			Owner: r.Owner, Repo: r.Repo,
			Current: policyCurrentJSON{
				Enabled:            current.Enabled,
				AllowedActions:     current.AllowedActions,
				ShaPinningRequired: current.ShaPinningRequired,
				Selected:           current.Selected,
			},
			Proposed: proposed,
			Files:    len(files),
			Actions:  len(refs),
		}
		b, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}

	renderPolicy(r.Owner, r.Repo, current, proposed, len(files), len(refs))

	if !opts.Apply {
		fmt.Println()
		fmt.Println(ui.Dim("Preview only — pass --yes to apply the proposed policy."))
		return nil
	}
	if proposed == nil {
		return fmt.Errorf("no action references found in local workflow files — nothing to apply")
	}

	requirePin := proposed.ShaPinningRequired && !opts.NoRequirePin
	err = gh.SetActionsPolicy(r.Owner, r.Repo, ghclient.SelectedActions{
		GithubOwnedAllowed: proposed.GithubOwnedAllowed,
		VerifiedAllowed:    proposed.VerifiedAllowed,
		PatternsAllowed:    proposed.PatternsAllowed,
	}, requirePin)
	if err != nil {
		if strings.Contains(err.Error(), "409") {
			return fmt.Errorf("applying policy to %s/%s: %w\nAn organization-level policy likely pins this repo's Actions settings — apply it at the org level instead", r.Owner, r.Repo, err)
		}
		return fmt.Errorf("applying policy to %s/%s: %w", r.Owner, r.Repo, err)
	}

	fmt.Println()
	fmt.Println(ui.Green(fmt.Sprintf("✓ Applied: allowed_actions=selected with %d pattern(s).", len(proposed.PatternsAllowed))))
	switch {
	case requirePin:
		fmt.Println(ui.Green("✓ SHA pinning is now required by GitHub for this repo."))
	case opts.NoRequirePin:
		fmt.Println(ui.Dim("SHA pinning requirement left unchanged (--no-require-pin)."))
	default:
		fmt.Println(ui.Yellow("⚠ SHA pinning requirement not enabled — some refs are floating. Run `gha pin --yes`, then re-apply."))
	}
	return nil
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func printPatterns(patterns []string) {
	if len(patterns) == 0 {
		return
	}
	fmt.Println("  Allowed patterns")
	for _, p := range patterns {
		fmt.Printf("    %s\n", p)
	}
}

func renderPolicy(owner, repoName string, current ghclient.ActionsPolicy, proposed *proposedPolicy, files, actions int) {
	fmt.Printf("\n%s %s\n\n", ui.Bold(owner+"/"+repoName), ui.Dim("· Actions policy"))

	fmt.Println(ui.Bold("Current (repo settings)"))
	fmt.Printf("  Actions enabled       %s\n", yesNo(current.Enabled))
	allowed := current.AllowedActions
	if allowed == "all" {
		allowed += "   " + ui.Yellow("⚠ any action by any author may run")
	}
	fmt.Printf("  Allowed actions       %s\n", allowed)
	fmt.Printf("  SHA pinning required  %s\n", yesNo(current.ShaPinningRequired))
	if sel := current.Selected; sel != nil {
		fmt.Printf("  GitHub-owned allowed  %s\n", yesNo(sel.GithubOwnedAllowed))
		fmt.Printf("  Verified creators     %s\n", yesNo(sel.VerifiedAllowed))
		printPatterns(sel.PatternsAllowed)
	}

	if proposed == nil {
		fmt.Println(ui.Yellow("\nNo action references found in local workflow files — nothing to propose."))
		return
	}

	fmt.Printf("\n%s %s\n", ui.Bold("Proposed"),
		ui.Dim(fmt.Sprintf("· from %d reference(s) across %d file(s)", actions, files)))
	fmt.Println("  Allowed actions       selected")
	fmt.Printf("  GitHub-owned allowed  %s\n", yesNo(proposed.GithubOwnedAllowed))
	fmt.Printf("  Verified creators     %s\n", yesNo(proposed.VerifiedAllowed))
	printPatterns(proposed.PatternsAllowed)
	pin := yesNo(proposed.ShaPinningRequired)
	if !proposed.ShaPinningRequired {
		pin += "   " + ui.Dim("(some refs are floating — run `gha pin` first)")
	}
	fmt.Printf("  SHA pinning required  %s\n", pin)

	// When an allowlist is already in force, show what applying the proposal
	// would change.
	if sel := current.Selected; sel != nil {
		have := map[string]struct{}{}
		for _, p := range sel.PatternsAllowed {
			have[p] = struct{}{}
		}
		want := map[string]struct{}{}
		for _, p := range proposed.PatternsAllowed {
			want[p] = struct{}{}
		}
		var add, drop []string
		for _, p := range proposed.PatternsAllowed {
			if _, ok := have[p]; !ok {
				add = append(add, p)
			}
		}
		for _, p := range sel.PatternsAllowed {
			if _, ok := want[p]; !ok {
				drop = append(drop, p)
			}
		}
		if len(add)+len(drop) > 0 {
			fmt.Printf("\n%s\n", ui.Bold("Pattern changes"))
			for _, p := range add {
				fmt.Println(ui.Green("  + " + p))
			}
			for _, p := range drop {
				fmt.Println(ui.Red("  - " + p + "   (allowed but not referenced locally)"))
			}
		} else {
			fmt.Println(ui.Green("\n✓ Allowlist patterns match local workflows."))
		}
	}

}
