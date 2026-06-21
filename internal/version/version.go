// Package version provides SHA detection and semver comparison helpers.
package version

import (
	"regexp"

	"github.com/Masterminds/semver/v3"
)

var shaRE = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

// IsSha reports whether ref is a full 40-character commit SHA (already pinned).
func IsSha(ref string) bool {
	return shaRE.MatchString(ref)
}

// PickLatest returns the highest stable semver tag from the list, preserving the
// original tag string (e.g. "v4.1.2"). Pre-releases and unparseable tags are
// ignored. Returns "" when nothing qualifies.
func PickLatest(tags []string) string {
	var bestTag string
	var bestVer *semver.Version
	for _, tag := range tags {
		v, err := semver.NewVersion(tag)
		if err != nil {
			continue
		}
		if v.Prerelease() != "" {
			continue
		}
		if bestVer == nil || v.GreaterThan(bestVer) {
			bestVer = v
			bestTag = tag
		}
	}
	return bestTag
}

// IsOutdated reports whether latest is strictly newer than current. An
// unparseable latest is never outdated; an unparseable current (branch, raw SHA)
// is treated as outdated so it surfaces in `gha update`.
func IsOutdated(current, latest string) bool {
	lat, err := semver.NewVersion(latest)
	if err != nil {
		return false
	}
	cur, err := semver.NewVersion(current)
	if err != nil {
		return true
	}
	return lat.GreaterThan(cur)
}
