package repo

import "testing"

func TestParseRepo(t *testing.T) {
	cases := []struct {
		in        string
		want      Repo
		wantMatch bool
	}{
		{"https://github.com/actions/checkout.git", Repo{"github.com", "actions", "checkout"}, true},
		{"git@github.com:actions/checkout.git", Repo{"github.com", "actions", "checkout"}, true},
		{"actions/checkout", Repo{"github.com", "actions", "checkout"}, true},
		{"https://ghe.example.com/org/tools.git", Repo{"ghe.example.com", "org", "tools"}, true},
		{"git@ghe.example.com:org/tools.git", Repo{"ghe.example.com", "org", "tools"}, true},
		{"ghe.example.com/org/tools", Repo{"ghe.example.com", "org", "tools"}, true},
		{"github.com/actions/checkout", Repo{"github.com", "actions", "checkout"}, true},
		{"not a repo", Repo{}, false},
	}
	for _, c := range cases {
		got, ok := ParseRepo(c.in)
		if ok != c.wantMatch {
			t.Errorf("ParseRepo(%q) matched=%v, want %v", c.in, ok, c.wantMatch)
			continue
		}
		if ok && got != c.want {
			t.Errorf("ParseRepo(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}
