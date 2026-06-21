package ui

import "testing"

func TestScrollWindow(t *testing.T) {
	cases := []struct {
		name                    string
		cursor, top, visible, n int
		wantTop, wantEnd        int
	}{
		{"fits entirely", 0, 0, 10, 5, 0, 5},
		{"cursor at top stays", 0, 0, 3, 10, 0, 3},
		{"cursor scrolls down into view", 5, 0, 3, 10, 3, 6},
		{"cursor near end clamps top", 9, 0, 3, 10, 7, 10},
		{"cursor scrolls back up", 1, 4, 3, 10, 1, 4},
		{"already in view, no move", 4, 3, 3, 10, 3, 6},
		{"visible larger than n", 2, 0, 20, 4, 0, 4},
		{"zero visible treated as one", 5, 0, 0, 10, 5, 6},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotTop, gotEnd := scrollWindow(c.cursor, c.top, c.visible, c.n)
			if gotTop != c.wantTop || gotEnd != c.wantEnd {
				t.Errorf("scrollWindow(%d,%d,%d,%d) = (%d,%d), want (%d,%d)",
					c.cursor, c.top, c.visible, c.n, gotTop, gotEnd, c.wantTop, c.wantEnd)
			}
			// Invariants: cursor must be within [top,end), end-top <= visible.
			v := max(c.visible, 1)
			if c.n > 0 && (c.cursor < gotTop || c.cursor >= gotEnd) {
				t.Errorf("cursor %d not within [%d,%d)", c.cursor, gotTop, gotEnd)
			}
			if gotEnd-gotTop > v {
				t.Errorf("window %d larger than visible %d", gotEnd-gotTop, v)
			}
		})
	}
}

func TestClipVisible(t *testing.T) {
	// Plain text truncation.
	if got := clipVisible("hello world", 5); got != "hello\x1b[0m" {
		t.Errorf("plain clip = %q", got)
	}
	// Shorter than limit: unchanged, no reset appended.
	if got := clipVisible("hi", 10); got != "hi" {
		t.Errorf("short clip = %q", got)
	}
	// SGR codes don't count toward width and pass through.
	colored := "\x1b[31mred\x1b[0m"
	if got := clipVisible(colored, 3); got != colored {
		t.Errorf("colored clip = %q, want unchanged", got)
	}
	// Truncating inside an OSC 8 hyperlink closes the link and resets.
	link := "\x1b]8;;https://x\x07linktext\x1b]8;;\x07"
	got := clipVisible(link, 4)
	want := "\x1b]8;;https://x\x07link\x1b]8;;\x07\x1b[0m"
	if got != want {
		t.Errorf("link clip = %q, want %q", got, want)
	}
}
