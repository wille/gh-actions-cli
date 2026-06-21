package command

import (
	"fmt"
	"sync"

	"github.com/wille/gh-actions-cli/internal/ghclient"
	"github.com/wille/gh-actions-cli/internal/parse"
	"github.com/wille/gh-actions-cli/internal/ui"
	"github.com/wille/gh-actions-cli/internal/version"
)

// latestVersions fetches the latest stable semver tag for each distinct
// owner/repo referenced in refs, concurrently (bounded by the client's own
// limiter). The returned map is keyed by "owner/repo"; a value of "" means no
// semver tag was found or the lookup failed. When progress is true a spinner is
// shown (pass false to keep stdout clean, e.g. for --json). Shared by the update
// and list commands.
func latestVersions(gh *ghclient.Client, refs []parse.UsesRef, progress bool) map[string]string {
	seen := map[string]struct{}{}
	var repoKeys []string
	for _, r := range refs {
		key := r.Owner + "/" + r.Repo
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			repoKeys = append(repoKeys, key)
		}
	}
	total := len(repoKeys)

	var spin *ui.Spinner
	if progress {
		spin = ui.NewSpinner(fmt.Sprintf("Fetching latest versions from GitHub (0/%d repos)", total))
	}

	var mu sync.Mutex
	latest := make(map[string]string, total)
	done := 0
	var wg sync.WaitGroup
	for _, rk := range repoKeys {
		wg.Add(1)
		go func(repoKey string) {
			defer wg.Done()
			owner, repo := splitRepoKey(repoKey)
			v := ""
			if tags, err := gh.ListTags(owner, repo); err == nil {
				v = version.PickLatest(tags)
			}
			mu.Lock()
			latest[repoKey] = v
			done++
			spin.Message(fmt.Sprintf("Fetching latest versions from GitHub (%d/%d repos)", done, total))
			mu.Unlock()
		}(rk)
	}
	wg.Wait()
	if progress {
		spin.Stop(fmt.Sprintf("Checked %d repo(s) for newer versions.", total))
	}
	return latest
}
