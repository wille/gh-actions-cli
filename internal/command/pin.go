// Package command implements the gha subcommands: pin, update, and stats.
package command

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/google/go-github/v66/github"

	"github.com/wille/gh-actions-cli/internal/discover"
	"github.com/wille/gh-actions-cli/internal/ghclient"
	"github.com/wille/gh-actions-cli/internal/parse"
	"github.com/wille/gh-actions-cli/internal/ui"
	"github.com/wille/gh-actions-cli/internal/version"
)

// PinOptions configures the pin command.
type PinOptions struct {
	Apply   bool // when false (default), only preview
	Comment bool // append a `# <version>` trailing comment
}

type plannedPin struct {
	file    string
	ref     parse.UsesRef
	sha     string
	version *string
}

// RunPin pins floating `uses:` refs to commit SHAs. Previews unless Apply.
func RunPin(paths []string, opts PinOptions) error {
	files, err := discover.Files(paths)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, ui.Yellow("No workflow or action files found."))
		return nil
	}

	gh := ghclient.New("")
	gh.WarnIfUnauthenticated()

	var plans []plannedPin
	skipped, errored := 0, 0

	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		for _, ref := range parse.Uses(string(content)) {
			if version.IsSha(ref.Ref) {
				skipped++
				continue
			}
			sha, err := gh.ResolveSha(ref.Owner, ref.Repo, ref.Ref)
			if err != nil {
				errored++
				fmt.Fprintln(os.Stderr, ui.Red(fmt.Sprintf("  ✗ %s@%s (%s:%d): %s",
					ref.Action, ref.Ref, file, ref.Line+1, describeError(err))))
				continue
			}
			var v *string
			if opts.Comment {
				r := ref.Ref
				v = &r
			}
			plans = append(plans, plannedPin{file: file, ref: ref, sha: sha, version: v})
		}
	}

	printPlan(plans)

	if len(plans) > 0 && opts.Apply {
		if err := writeChanges(plans); err != nil {
			return err
		}
	}

	printSummary(len(plans), skipped, errored, opts.Apply)

	// Fail (non-zero exit) when unpinned actions remain unfixed — lets `gha pin`
	// gate CI. In preview mode any planned pin counts; in --yes mode only
	// unresolved refs do (everything resolvable was just written).
	if errored > 0 || (!opts.Apply && len(plans) > 0) {
		return ErrCheckFailed
	}
	return nil
}

func printPlan(plans []plannedPin) {
	currentFile := ""
	for _, p := range plans {
		if p.file != currentFile {
			currentFile = p.file
			fmt.Printf("\n%s\n", ui.Bold(ui.Underline(p.file)))
		}
		releases := fmt.Sprintf("https://github.com/%s/%s/releases", p.ref.Owner, p.ref.Repo)
		fmt.Printf("  %s %s  %s\n", ui.Red("⚠"), ui.Cyan(p.ref.Action), ui.Dim(ui.Underline(releases)))
		fmt.Printf("    current:  %s\n", ui.Red(p.ref.Ref))
		comment := ""
		if p.version != nil {
			comment = ui.Dim("  # " + *p.version)
		}
		fmt.Printf("    pinned:   %s%s %s\n", ui.Green(p.sha), comment,
			ui.Dim(fmt.Sprintf("(%s:%d)", p.file, p.ref.Line+1)))
	}
}

func writeChanges(plans []plannedPin) error {
	byFile := map[string][]plannedPin{}
	var order []string
	for _, p := range plans {
		if _, seen := byFile[p.file]; !seen {
			order = append(order, p.file)
		}
		byFile[p.file] = append(byFile[p.file], p)
	}
	for _, file := range order {
		filePlans := byFile[file]
		content, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		refs := parse.Uses(string(content))
		rewrites := make([]parse.Rewrite, len(filePlans))
		for i, p := range filePlans {
			rewrites[i] = parse.Rewrite{Line: p.ref.Line, SHA: p.sha, Version: p.version}
		}
		out := parse.ApplyRewrites(string(content), refs, rewrites)
		if err := os.WriteFile(file, []byte(out), 0o644); err != nil {
			return err
		}
		fmt.Printf("\n%s\n", ui.Green(fmt.Sprintf("✓ %s — pinned %d action(s)", file, len(rewrites))))
	}
	return nil
}

func printSummary(count, skipped, errored int, apply bool) {
	if count == 0 {
		fmt.Printf("\n%s%s\n",
			ui.Green("✓ All actions are pinned to commit SHAs."),
			ui.Dim(fmt.Sprintf(" (%d pinned, %d error(s))", skipped, errored)))
		return
	}
	if apply {
		fmt.Printf("\n%s action(s) to commit SHAs, %d already pinned, %d error(s).\n",
			ui.Green(ui.Bold(fmt.Sprintf("✓ Locked %d", count))), skipped, errored)
		return
	}
	printSecurityWarning(count)
	fmt.Println(ui.Dim(fmt.Sprintf("(%d already pinned, %d error(s))", skipped, errored)))
	fmt.Println()
	fmt.Printf("%s%s%s\n",
		ui.Dim("No files written. Lock these down now with "),
		ui.Bold(ui.Green("gha pin --yes")),
		ui.Dim("."))
}

func printSecurityWarning(count int) {
	actions := "actions are"
	if count == 1 {
		actions = "action is"
	}
	bar := ui.Red(strings.Repeat("━", 64))
	fmt.Printf("\n%s\n", bar)
	fmt.Printf("%s%s\n",
		ui.Banner(" 🚨 SECURITY RISK "),
		ui.Red(ui.Bold(fmt.Sprintf("  %d %s NOT pinned to a commit SHA", count, actions))))
	fmt.Println(bar)
	// Color each line independently so lipgloss doesn't pad the block.
	for _, l := range []string{
		"These actions are pinned to mutable tags/branches. A tag can be moved",
		"(or a maintainer account compromised) to point at malicious code that",
		"then runs with your workflow's secrets and permissions — a classic",
		"supply-chain attack. Pin to an immutable commit SHA to stay safe.",
	} {
		fmt.Println(ui.Red(l))
	}
}

func describeError(err error) string {
	var ge *github.ErrorResponse
	if errors.As(err, &ge) && ge.Response != nil {
		switch ge.Response.StatusCode {
		case 404:
			return "not found (private repo or unknown ref?)"
		case 403:
			return "forbidden (rate limited? set GITHUB_TOKEN)"
		}
	}
	return err.Error()
}
