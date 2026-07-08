// Command gha pins, updates, and analyzes GitHub Actions from the command line.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/wille/gh-actions-cli/internal/command"
)

// version is injected at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	root := &cobra.Command{
		Use:           "gha",
		Short:         "Pin, update, and analyze your GitHub Actions from the command line.",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// pin
	var pinYes, pinNoComment bool
	pinCmd := &cobra.Command{
		Use:   "pin [paths...]",
		Short: "Rewrite floating `uses:` refs to pinned commit SHAs with a version comment. Previews by default; pass --yes to write.",
		RunE: func(_ *cobra.Command, args []string) error {
			return command.RunPin(args, command.PinOptions{Apply: pinYes, Comment: !pinNoComment})
		},
	}
	pinCmd.Flags().BoolVarP(&pinYes, "yes", "y", false, "apply the changes (without this, only a preview is shown)")
	pinCmd.Flags().BoolVar(&pinNoComment, "no-comment", false, "do not append the `# <version>` trailing comment")

	// update
	var updateYes bool
	updateCmd := &cobra.Command{
		Use:   "update [paths...]",
		Short: "Interactively show current vs latest versions and re-pin selected actions.",
		RunE: func(_ *cobra.Command, args []string) error {
			return command.RunUpdate(args, command.UpdateOptions{Yes: updateYes})
		},
	}
	updateCmd.Flags().BoolVarP(&updateYes, "yes", "y", false, "update all outdated actions without prompting")

	// stats
	var stOpts command.StatsOptions
	statsCmd := &cobra.Command{
		Use:   "stats [workflow]",
		Short: "Show workflow run stats: success rate, p50/p95 duration, and slowest runs.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			workflow := ""
			if len(args) > 0 {
				workflow = args[0]
			}
			return command.RunStats(workflow, stOpts)
		},
	}
	statsCmd.Flags().StringVar(&stOpts.Repo, "repo", "", "target repo (default: the origin git remote)")
	statsCmd.Flags().StringVar(&stOpts.Branch, "branch", "", "branch to analyze (default: the repo's default branch)")
	statsCmd.Flags().StringVar(&stOpts.Since, "since", "7d", "how far back to analyze runs (e.g. 7d, 2w, 24h)")
	statsCmd.Flags().BoolVar(&stOpts.Jobs, "jobs", false, "include a per-job p95 breakdown for the slowest workflow")
	statsCmd.Flags().BoolVar(&stOpts.JSON, "json", false, "output raw stats as JSON")

	// list
	var listOpts command.ListOptions
	listCmd := &cobra.Command{
		Use:   "list [paths...]",
		Short: "List the actions used across your workflows and whether they're pinned and current.",
		RunE: func(_ *cobra.Command, args []string) error {
			return command.RunList(args, listOpts)
		},
	}
	listCmd.Flags().BoolVar(&listOpts.JSON, "json", false, "output the inventory as JSON")
	listCmd.Flags().BoolVar(&listOpts.Offline, "offline", false, "skip the GitHub API (no latest-version lookup)")
	listCmd.Flags().BoolVar(&listOpts.Outdated, "outdated", false, "only show actions with a newer version available")
	listCmd.Flags().BoolVar(&listOpts.Unpinned, "unpinned", false, "only show actions not pinned to a commit SHA")

	// policy
	var polOpts command.PolicyOptions
	policyCmd := &cobra.Command{
		Use:   "policy [paths...]",
		Short: "Show the repo's allowed-actions policy next to one generated from your workflows. Previews by default; pass --yes to apply.",
		RunE: func(_ *cobra.Command, args []string) error {
			return command.RunPolicy(args, polOpts)
		},
	}
	policyCmd.Flags().StringVar(&polOpts.Repo, "repo", "", "target repo (default: the origin git remote)")
	policyCmd.Flags().BoolVar(&polOpts.JSON, "json", false, "output the policies as JSON")
	policyCmd.Flags().BoolVarP(&polOpts.Apply, "yes", "y", false, "apply the proposed policy to the repo settings")
	policyCmd.Flags().BoolVar(&polOpts.NoRequirePin, "no-require-pin", false, "do not enable GitHub's SHA pinning requirement when applying")
	policyCmd.MarkFlagsMutuallyExclusive("json", "yes")

	root.AddCommand(pinCmd, updateCmd, statsCmd, listCmd, policyCmd)

	if err := root.Execute(); err != nil {
		// ErrCheckFailed means "exit non-zero, output already shown" — no message.
		if !errors.Is(err, command.ErrCheckFailed) {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}
