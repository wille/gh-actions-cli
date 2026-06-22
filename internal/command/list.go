package command

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/wille/gh-actions-cli/internal/discover"
	"github.com/wille/gh-actions-cli/internal/ghclient"
	"github.com/wille/gh-actions-cli/internal/parse"
	"github.com/wille/gh-actions-cli/internal/ui"
	"github.com/wille/gh-actions-cli/internal/version"
)

// ListOptions configures the list command.
type ListOptions struct {
	JSON     bool
	Offline  bool
	Outdated bool // only show outdated actions
	Unpinned bool // only show floating (not SHA-pinned) actions
}

type listRow struct {
	file     string
	ref      parse.UsesRef
	pinned   bool
	current  string
	latest   string
	outdated bool
}

// JSON output shapes.
type listActionJSON struct {
	Action   string `json:"action"`
	Current  string `json:"current"`
	Latest   string `json:"latest,omitempty"`
	Pinned   bool   `json:"pinned"`
	Outdated bool   `json:"outdated"`
	Line     int    `json:"line"`
}

type listFileJSON struct {
	File    string           `json:"file"`
	Actions []listActionJSON `json:"actions"`
}

// RunList prints a read-only inventory of the actions referenced in the repo,
// grouped by file, with pinned/floating status and (unless --offline) the latest
// available version.
func RunList(paths []string, opts ListOptions) error {
	files, err := discover.Files(paths)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, ui.Yellow("No workflow or action files found."))
		return nil
	}

	// Gather refs in discovery (sorted) order, skipping files with none.
	var rows []listRow
	var allRefs []parse.UsesRef
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		for _, ref := range parse.Uses(string(content)) {
			rows = append(rows, listRow{file: file, ref: ref})
			allRefs = append(allRefs, ref)
		}
	}
	if len(rows) == 0 {
		fmt.Fprintln(os.Stderr, ui.Yellow("No action references found."))
		return nil
	}

	// Enrich with latest versions unless offline.
	latestByRepo := map[string]string{}
	if !opts.Offline {
		gh := ghclient.New("")
		gh.WarnIfUnauthenticated()
		latestByRepo = latestVersions(gh, allRefs, !opts.JSON)
	}

	for i := range rows {
		r := &rows[i]
		r.pinned = version.IsSha(r.ref.Ref)
		r.current = currentVersion(r.ref)
		r.latest = latestByRepo[r.ref.Owner+"/"+r.ref.Repo]
		r.outdated = r.latest != "" && version.IsOutdated(r.current, r.latest)
	}

	// Apply filters (AND semantics).
	if opts.Outdated || opts.Unpinned {
		kept := rows[:0]
		for _, r := range rows {
			if opts.Outdated && !r.outdated {
				continue
			}
			if opts.Unpinned && r.pinned {
				continue
			}
			kept = append(kept, r)
		}
		rows = kept
	}

	if opts.JSON {
		return printListJSON(rows)
	}
	printListTable(rows, opts.Offline)
	return nil
}

func printListJSON(rows []listRow) error {
	var out []listFileJSON
	for _, r := range rows {
		if len(out) == 0 || out[len(out)-1].File != r.file {
			out = append(out, listFileJSON{File: r.file})
		}
		f := &out[len(out)-1]
		f.Actions = append(f.Actions, listActionJSON{
			Action:   r.ref.Action,
			Current:  r.current,
			Latest:   r.latest,
			Pinned:   r.pinned,
			Outdated: r.outdated,
			Line:     r.ref.Line + 1,
		})
	}
	b, err := json.MarshalIndent(struct {
		Files []listFileJSON `json:"files"`
	}{Files: out}, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

func printListTable(rows []listRow, offline bool) {
	// Plain (uncolored) version cell text, for alignment.
	plainVer := func(r listRow) string {
		if r.outdated {
			return r.current + " → " + r.latest
		}
		return r.current
	}

	actionW, verW := 0, 0
	for _, r := range rows {
		actionW = max(actionW, utf8.RuneCountInString(r.ref.Action))
		verW = max(verW, utf8.RuneCountInString(plainVer(r)))
	}

	floating := 0
	currentFile := ""
	for _, r := range rows {
		if r.file != currentFile {
			currentFile = r.file
			fmt.Printf("\n%s\n", ui.Bold(r.file))
		}

		icon := ui.Green("✓")
		if !r.pinned {
			icon = ui.Yellow("⚠")
			floating++
		}

		releases := fmt.Sprintf("https://github.com/%s/%s/releases", r.ref.Owner, r.ref.Repo)
		action := pad(ui.Cyan(ui.Link(r.ref.Action, releases)), r.ref.Action, actionW)

		ver := r.current
		if r.outdated {
			ver = r.current + ui.Dim(" → ") + ui.Green(r.latest)
		}
		ver = pad(ver, plainVer(r), verW)

		status := "floating"
		if r.pinned {
			status = "pinned"
		}
		if !offline {
			switch {
			case r.latest == "":
				status += " · latest unknown"
			case r.outdated:
				status += " · outdated"
			default:
				status += " · up to date"
			}
		}

		fmt.Printf("  %s %s  %s  %s\n", icon, action, ver, ui.Dim(status))
	}

	fmt.Printf("\n%s\n", ui.Dim(fmt.Sprintf("%d action(s), %d not pinned.", len(rows), floating)))
}

// pad right-pads a (possibly colorized) cell to width, measuring by its plain
// text so ANSI codes don't throw off alignment.
func pad(colored, plain string, width int) string {
	if n := width - utf8.RuneCountInString(plain); n > 0 {
		return colored + strings.Repeat(" ", n)
	}
	return colored
}
