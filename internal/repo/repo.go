// Package repo resolves the target GitHub repository from a flag or git remote.
package repo

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// Repo identifies a GitHub repository.
type Repo struct {
	Owner string
	Repo  string
}

var (
	urlRE       = regexp.MustCompile(`github\.com[:/]([^/]+)/(.+?)(?:\.git)?/?$`)
	shorthandRE = regexp.MustCompile(`^([^/\s]+)/([^/\s]+?)(?:\.git)?$`)
)

// ParseRepo extracts owner/repo from a GitHub remote URL (https or ssh) or
// "owner/repo" shorthand. Returns false when nothing matches.
func ParseRepo(input string) (Repo, bool) {
	if m := urlRE.FindStringSubmatch(input); m != nil {
		return Repo{Owner: m[1], Repo: m[2]}, true
	}
	if m := shorthandRE.FindStringSubmatch(input); m != nil {
		return Repo{Owner: m[1], Repo: m[2]}, true
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
