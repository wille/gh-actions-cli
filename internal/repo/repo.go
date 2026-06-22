// Package repo resolves the target GitHub repository from a flag or git remote.
package repo

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// Repo identifies a GitHub (or GitHub Enterprise) repository.
type Repo struct {
	Host  string // e.g. "github.com" or "ghe.example.com"
	Owner string
	Repo  string
}

var (
	// host (must contain a dot) then owner/repo, from https/ssh/scp-style URLs.
	urlRE = regexp.MustCompile(`^(?:(?:https?|ssh)://)?(?:git@)?([A-Za-z0-9.-]+\.[A-Za-z0-9-]+)[:/]([^/\s]+)/(.+?)(?:\.git)?/?$`)
	// "owner/repo" shorthand — first segment has no dot so it can't be a host.
	shorthandRE = regexp.MustCompile(`^([^/\s.]+)/([^/\s]+?)(?:\.git)?$`)
)

// ParseRepo extracts host/owner/repo from a remote URL (https or ssh) or an
// "owner/repo" shorthand (host defaults to github.com). Returns false on no match.
func ParseRepo(input string) (Repo, bool) {
	if m := urlRE.FindStringSubmatch(input); m != nil {
		return Repo{Host: m[1], Owner: m[2], Repo: m[3]}, true
	}
	if m := shorthandRE.FindStringSubmatch(input); m != nil {
		return Repo{Host: "github.com", Owner: m[1], Repo: m[2]}, true
	}
	return Repo{}, false
}

// ResolveRepo returns the target repo: an explicit override (flag value or URL)
// wins, otherwise the origin git remote of the current directory.
func ResolveRepo(override string) (Repo, error) {
	if override != "" {
		r, ok := ParseRepo(override)
		if !ok {
			return Repo{}, fmt.Errorf("could not parse repo from %q — use owner/repo", override)
		}
		return r, nil
	}
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return Repo{}, fmt.Errorf("no git remote found — run inside a repo or pass --repo owner/repo")
	}
	url := strings.TrimSpace(string(out))
	r, ok := ParseRepo(url)
	if !ok {
		return Repo{}, fmt.Errorf("could not parse a GitHub repo from origin %q — pass --repo owner/repo", url)
	}
	return r, nil
}
