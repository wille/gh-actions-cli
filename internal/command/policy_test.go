package command

import (
	"reflect"
	"testing"

	"github.com/wille/gh-actions-cli/internal/parse"
)

func ref(owner, repo, gitRef string) parse.UsesRef {
	return parse.UsesRef{Owner: owner, Repo: repo, Action: owner + "/" + repo, Ref: gitRef}
}

const sha = "9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0"

func TestProposePolicy(t *testing.T) {
	refs := []parse.UsesRef{
		ref("actions", "checkout", sha),
		ref("golangci", "golangci-lint-action", sha),
		ref("goreleaser", "goreleaser-action", sha),
		ref("golangci", "golangci-lint-action", sha), // duplicate collapses
	}
	p := proposePolicy(refs)
	if p.AllowedActions != "selected" {
		t.Errorf("AllowedActions = %q, want selected", p.AllowedActions)
	}
	if !p.GithubOwnedAllowed {
		t.Error("GithubOwnedAllowed = false, want true (actions/checkout is used)")
	}
	want := []string{"golangci/golangci-lint-action@*", "goreleaser/goreleaser-action@*"}
	if !reflect.DeepEqual(p.PatternsAllowed, want) {
		t.Errorf("PatternsAllowed = %v, want %v", p.PatternsAllowed, want)
	}
	if !p.ShaPinningRequired {
		t.Error("ShaPinningRequired = false, want true (all refs pinned)")
	}
}

func TestProposePolicyFloatingRefDisablesPinRequirement(t *testing.T) {
	p := proposePolicy([]parse.UsesRef{ref("docker", "build-push-action", "v6")})
	if p.ShaPinningRequired {
		t.Error("ShaPinningRequired = true, want false when a ref is floating")
	}
	if p.GithubOwnedAllowed {
		t.Error("GithubOwnedAllowed = true, want false (no github-owned actions used)")
	}
}
