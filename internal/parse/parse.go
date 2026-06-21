// Package parse extracts and rewrites `uses:` references in workflow/action
// files while preserving the original formatting byte-for-byte.
package parse

import (
	"regexp"
	"strings"
)

// UsesRef is a parsed `uses:` reference found on a single line.
type UsesRef struct {
	Line    int    // zero-based line index
	Prefix  string // everything up to the value, e.g. "      - uses: "
	Quote   string // surrounding quote char, or "" if unquoted
	Action  string // owner/repo plus any subpath
	Ref     string // git ref after @ (tag, branch, or SHA)
	Comment string // trailing comment incl. leading "#", or ""
	Owner   string // first path segment
	Repo    string // second path segment
}

// Rewrite pins one line's ref to SHA, labeling it with Version (nil omits the
// trailing comment).
type Rewrite struct {
	Line    int
	SHA     string
	Version *string
}

// Go's RE2 engine has no backreferences, so the closing quote is captured as a
// separate group (5) and validated against the opening quote (2) in code.
//
//	1: prefix   2: openQuote   3: action   4: ref   5: closeQuote   6: comment
var usesRE = regexp.MustCompile(`^(\s*(?:-\s*)?uses:\s*)(['"]?)([^\s'"#@]+)@([^\s'"#]+)(['"]?)(\s*#.*)?\s*$`)

func isSkippable(action string) bool {
	return strings.HasPrefix(action, "./") ||
		strings.HasPrefix(action, "../") ||
		strings.HasPrefix(action, "docker://")
}

// Uses returns every pinnable `uses:` reference in the file content.
func Uses(content string) []UsesRef {
	var refs []UsesRef
	for i, text := range strings.Split(content, "\n") {
		m := usesRE.FindStringSubmatch(text)
		if m == nil {
			continue
		}
		prefix, openQ, action, ref, closeQ, comment := m[1], m[2], m[3], m[4], m[5], m[6]
		// Mimic the backreference: quotes must match (or both be absent).
		if openQ != closeQ {
			continue
		}
		if prefix == "" || action == "" || ref == "" {
			continue
		}
		if isSkippable(action) {
			continue
		}
		segments := strings.Split(action, "/")
		if len(segments) < 2 || segments[0] == "" || segments[1] == "" {
			continue
		}
		refs = append(refs, UsesRef{
			Line:    i,
			Prefix:  prefix,
			Quote:   openQ,
			Action:  action,
			Ref:     ref,
			Comment: comment,
			Owner:   segments[0],
			Repo:    segments[1],
		})
	}
	return refs
}

// ApplyRewrites returns content with the given rewrites applied. Every untouched
// line is left exactly as-is; rewritten lines keep their prefix, quoting, and
// action path, changing only the ref and trailing comment.
func ApplyRewrites(content string, refs []UsesRef, rewrites []Rewrite) string {
	byLine := make(map[int]UsesRef, len(refs))
	for _, r := range refs {
		byLine[r.Line] = r
	}
	lines := strings.Split(content, "\n")
	for _, rw := range rewrites {
		ref, ok := byLine[rw.Line]
		if !ok || rw.Line < 0 || rw.Line >= len(lines) {
			continue
		}
		comment := ""
		if rw.Version != nil {
			comment = " # " + *rw.Version
		}
		lines[rw.Line] = ref.Prefix + ref.Quote + ref.Action + "@" + rw.SHA + ref.Quote + comment
	}
	return strings.Join(lines, "\n")
}
