package command

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wille/gh-actions-cli/internal/discover"
	"github.com/wille/gh-actions-cli/internal/ghclient"
	"github.com/wille/gh-actions-cli/internal/parse"
	"github.com/wille/gh-actions-cli/internal/ui"
	"github.com/wille/gh-actions-cli/internal/version"
)

// UpdateOptions configures the update command.
type UpdateOptions struct {
	Yes bool // update every outdated action without prompting
}

type candidate struct {
	file     string
	ref      parse.UsesRef
	current  string
	latest   string
	released time.Time // commit date of the latest tag; zero if lookup failed
}

var commentVerRE = regexp.MustCompile(`#\s*(\S+)`)

// currentVersion is the comment label of a SHA-pinned ref, else the raw ref.
func currentVersion(ref parse.UsesRef) string {
	if version.IsSha(ref.Ref) {
		if m := commentVerRE.FindStringSubmatch(ref.Comment); m != nil {
			return m[1]
		}
		if len(ref.Ref) >= 7 {
			return ref.Ref[:7]
		}
	}
	return ref.Ref
}

func releaseURL(c candidate) string {
	return fmt.Sprintf("https://github.com/%s/%s/releases/tag/%s", c.ref.Owner, c.ref.Repo, c.latest)
}

// linkedAction renders the action name as a cyan OSC 8 hyperlink to the release
// it would be bumped to (matching the list command's linked action names).
func linkedAction(c candidate) string {
	return ui.Cyan(ui.Link(c.ref.Action, releaseURL(c)))
}

// releasedAgo renders the time since the latest version's release, e.g.
// "(released 3mo ago)", or "" when the date lookup failed.
func releasedAgo(c candidate) string {
	if c.released.IsZero() {
		return ""
	}
	return ui.Dim(fmt.Sprintf("(released %s)", fmtAgoTime(c.released)))
}

// RunUpdate shows current vs latest versions and re-pins selected actions.
func RunUpdate(paths []string, opts UpdateOptions) error {
	start := time.Now()
	elapsed := func() int64 { return time.Since(start).Milliseconds() }

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

	type entry struct {
		file string
		ref  parse.UsesRef
	}
	fileContents := map[string]string{}
	var entries []entry
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		fileContents[file] = string(content)
		for _, ref := range parse.Uses(string(content)) {
			entries = append(entries, entry{file: file, ref: ref})
		}
	}
	if len(entries) == 0 {
		fmt.Fprintln(os.Stderr, ui.Yellow("No action references found."))
		return nil
	}

	refs := make([]parse.UsesRef, len(entries))
	for i, e := range entries {
		refs[i] = e.ref
	}
	latestByRepo := latestVersions(gh, refs, true)

	var candidates []candidate
	for _, e := range entries {
		latest := latestByRepo[e.ref.Owner+"/"+e.ref.Repo]
		cur := currentVersion(e.ref)
		if latest != "" && version.IsOutdated(cur, latest) {
			candidates = append(candidates, candidate{file: e.file, ref: e.ref, current: cur, latest: latest})
		}
	}
	// Fetch each candidate's release date (commit date of the latest tag).
	// This also warms the client's commit cache, so re-pinning the chosen
	// actions below needs no further API calls.
	if len(candidates) > 0 {
		spin := ui.NewSpinner("Fetching release dates")
		var wg sync.WaitGroup
		for i := range candidates {
			wg.Add(1)
			go func(c *candidate) {
				defer wg.Done()
				if d, err := gh.CommitDate(c.ref.Owner, c.ref.Repo, c.latest); err == nil {
					c.released = d
				}
			}(&candidates[i])
		}
		wg.Wait()
		spin.Stop("")
	}
	// Group by workflow file, alphabetical by action within each file.
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].file != candidates[j].file {
			return candidates[i].file < candidates[j].file
		}
		return candidates[i].ref.Action < candidates[j].ref.Action
	})

	if len(candidates) == 0 {
		fmt.Println(ui.Green(fmt.Sprintf("✓ All actions are up to date. (%d ms)", elapsed())))
		return nil
	}

	var chosen []candidate
	if opts.Yes {
		chosen = candidates
		currentFile := ""
		for _, c := range candidates {
			if c.file != currentFile {
				currentFile = c.file
				fmt.Printf("\n%s\n", ui.Bold(c.file))
			}
			fmt.Println(strings.TrimRight(fmt.Sprintf("  %s  %s → %s %s",
				linkedAction(c), ui.Dim(c.current), ui.Green(c.latest), releasedAgo(c)), " "))
		}
	} else {
		// One row per candidate, grouped by workflow file. Toggling a file
		// header selects/deselects all of its steps. The action name links to
		// the release it would be bumped to.
		items := make([]ui.SelectItem, len(candidates))
		for i, c := range candidates {
			hint := ui.Dim(fmt.Sprintf("(line %d)", c.ref.Line+1))
			label := fmt.Sprintf("%s  %s → %s", linkedAction(c), ui.Dim(c.current), ui.Green(c.latest))
			if ago := releasedAgo(c); ago != "" {
				label += " " + ago
			}
			items[i] = ui.SelectItem{Group: c.file, Label: label + "   " + hint}
		}
		selected, ok, err := ui.SelectGrouped(
			"Select actions to update (re-pinned to the new version's SHA):", items)
		if err != nil {
			return err
		}
		if !ok || len(selected) == 0 {
			fmt.Printf("No changes made. (%d ms)\n", elapsed())
			return nil
		}
		for _, i := range selected {
			chosen = append(chosen, candidates[i])
		}
	}

	// Resolve each chosen latest version to a SHA, grouped by file.
	rewritesByFile := map[string][]parse.Rewrite{}
	var fileOrder []string
	for _, c := range chosen {
		sha, err := gh.ResolveSha(c.ref.Owner, c.ref.Repo, c.latest)
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Red(fmt.Sprintf("  ✗ failed to resolve %s@%s", c.ref.Action, c.latest)))
			continue
		}
		if _, ok := rewritesByFile[c.file]; !ok {
			fileOrder = append(fileOrder, c.file)
		}
		v := c.latest
		rewritesByFile[c.file] = append(rewritesByFile[c.file], parse.Rewrite{Line: c.ref.Line, SHA: sha, Version: &v})
	}

	updated := 0
	for _, file := range fileOrder {
		rewrites := rewritesByFile[file]
		content := fileContents[file]
		refs := parse.Uses(content)
		if err := os.WriteFile(file, []byte(parse.ApplyRewrites(content, refs, rewrites)), 0o644); err != nil {
			return err
		}
		updated += len(rewrites)
		fmt.Println(ui.Green(fmt.Sprintf("✓ %s — updated %d action(s)", file, len(rewrites))))
	}

	fmt.Println(ui.Bold(fmt.Sprintf("Done — updated %d action(s) in %d ms.", updated, elapsed())))
	return nil
}

func splitRepoKey(key string) (string, string) {
	parts := strings.SplitN(key, "/", 2)
	if len(parts) != 2 {
		return key, ""
	}
	return parts[0], parts[1]
}
