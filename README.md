# gha — GitHub Actions CLI

[![Latest release](https://img.shields.io/github/v/release/wille/gh-actions-cli?sort=semver)](https://github.com/wille/gh-actions-cli/releases)
[![Go version](https://img.shields.io/github/go-mod/go-version/wille/gh-actions-cli)](go.mod)
[![License: MIT](https://img.shields.io/github/license/wille/gh-actions-cli)](LICENSE)

`gha` is a CLI for managing the GitHub Actions used across
your repositories — straight from the terminal:

- 📌 **Pin** actions to immutable commit SHAs (and fail CI on anything unpinned).
- ⬆️ **Interactively update** actions to their latest releases.
- 📊 **Analyze** workflow run health — success rates and durations.

## Install

Download a pre-built binary for linux, macOS, or Windows from the
[releases page](https://github.com/wille/gh-actions-cli/releases), or install
with Go:

```bash
go install github.com/wille/gh-actions-cli/cmd/gha@latest
```

Build from source

```bash
go build -o gha ./cmd/gha
```

## Usage

```bash
gha list [paths...]      # inventory: which actions are pinned and current
gha pin [paths...]       # preview the actions that would be pinned (fails CI if any)
gha pin --yes            # pin them (writes files)
gha update [paths...]    # interactively pick actions to update
gha update --yes         # update every outdated action
gha stats [workflow]     # workflow run success rates and durations
```

With no paths, `gha` scans `.github/workflows/*.{yml,yaml}` and composite
`action.{yml,yaml}` files. Pass paths to narrow the scope.

### `gha list`

A read-only inventory of every action, grouped by workflow file, showing whether
each is pinned to a commit SHA (`✓`) or a floating tag (`⚠`), its current
version, and — looked up from the GitHub API — the latest available version:

```
.github/workflows/ci.yml
  ✓ actions/checkout      v7.0.0           pinned · up to date
  ⚠ actions/setup-node    v4 → v6.4.0      floating · outdated

2 action(s), 1 not pinned.
```

- `--offline` skips the API for an instant pinned/floating-only view.
- `--outdated` / `--unpinned` narrow the list to just those actions.
- `--json` emits the inventory as structured JSON.

### `gha pin`

A tag like `@v4` is mutable — it can be moved to point at different code. `gha
pin` locks each action to the exact commit, keeping the version readable as a
comment:

```yaml
uses: actions/checkout@08c6903cd8c0fde910a37f88322edcfb5dd907a8 # v4
```

It previews by default and writes nothing until you pass `--yes`. Unpinned
actions are surfaced with a clear warning:

```
.github/workflows/ci.yml
  ⚠ actions/checkout  https://github.com/actions/checkout/releases
    current:  v4
    pinned:   08c6903cd8c0fde910a37f88322edcfb5dd907a8  # v4

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
 🚨 SECURITY RISK   1 action is NOT pinned to a commit SHA
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
```

Already-pinned actions are left untouched, and your formatting and comments are
preserved. Local (`./…`) and `docker://…` references are skipped.

`gha pin` **exits non-zero when unpinned actions remain**, so you can run it in CI
to fail a build that introduces an unpinned action — and `gha pin --yes` in a fix
step to lock them down.

### `gha update`

Shows each action's current version next to its latest release, grouped by
workflow file. Pick the ones to bump and `gha` re-pins them to the new version.
Toggle a file header to select or deselect all of its actions at once:

```
Select actions to update (re-pinned to the new version's SHA):
  [~] .github/workflows/ci.yml
›     [x] actions/checkout   v4 → v7.0.0
      [ ] actions/setup-go   v5 → v6.0.0
```

Pass `--yes` to update every outdated action without the prompt — handy for
scheduled dependency-update jobs.

### `gha stats`

Reports per-workflow run statistics: run count, success rate, p50/p95/slowest
duration, billable minutes, and the last run.

```
actions/checkout · branch main · since 2026-06-15

WORKFLOW         RUNS  SUCCESS     p50     p95  SLOWEST  BILLABLE  LAST
Build and Test    100     99%   2m 28s  3m 40s   6m 02s    2h 14m  ✓ 2h ago
Dependabot         50     20%   1m 39s   2m 1s    2m 1s       31m  ✗ 4d ago
```

- `--repo owner/repo` — target a repo other than the current git remote. A host
  prefix (`ghe.example.com/owner/repo`) targets a GitHub Enterprise Server instance.
- `--branch <name>` — filter runs by branch.
- `--since <window>` — how far back to analyze runs (default `7d`; e.g. `2w`, `24h`).
- `--jobs` — per-job breakdown for the slowest workflow.
- `--json` — machine-readable output.

Durations are wall-clock time. Billable GitHub-hosted runner minutes are shown
when GitHub reports them — private repos only, and always for the current
billing cycle rather than the `--since` window. Organizations migrated to
GitHub's enhanced billing platform no longer get per-workflow billing data, so
there the `BILLABLE` column is replaced by a repo-level month-to-date summary
from the billing usage report:

```
Billable 2026-07 (repo total): 23h 57m — Actions Linux 19h 2m · Actions Linux 4-core 4h 55m · $6.83 net
```

## Authentication

`gha` uses the GitHub API and looks for a token in this order:

1. `GITHUB_TOKEN` / `GH_TOKEN`
2. `gh auth token` (the GitHub CLI)
3. unauthenticated — works, but with low rate limits

```bash
export GITHUB_TOKEN=…
```
