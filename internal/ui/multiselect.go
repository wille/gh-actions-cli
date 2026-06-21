package ui

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"
)

// SelectItem is one selectable row belonging to a group.
type SelectItem struct {
	Group string // group label (e.g. workflow file)
	Label string // display label for the item
}

type selectRow struct {
	isHeader bool
	group    string
	item     int // index into items, when !isHeader
}

// SelectGrouped shows an interactive grouped multi-select. Items must be ordered
// so that members of the same group are contiguous; a header row is rendered per
// group, and toggling it selects or deselects every item in that group.
//
// Returns the indices of selected items (into items). ok is false if the user
// aborted (esc/q/ctrl+c). Implemented with raw terminal mode + the alternate
// screen (rather than a TUI framework that probes the terminal at startup), with
// a scrolling viewport so lists taller than the terminal work correctly.
func SelectGrouped(title string, items []SelectItem) (selected []int, ok bool, err error) {
	if !IsStdoutTTY() {
		return nil, false, fmt.Errorf("interactive selection requires a terminal; re-run with --yes")
	}

	groupItems := map[string][]int{}
	var rows []selectRow
	group := ""
	for i, it := range items {
		if it.Group != group {
			group = it.Group
			rows = append(rows, selectRow{isHeader: true, group: it.Group})
		}
		groupItems[it.Group] = append(groupItems[it.Group], i)
		rows = append(rows, selectRow{item: i})
	}

	sel := make([]bool, len(items))
	cursor := 0
	for i, r := range rows { // start on the first selectable row
		if !r.isHeader {
			cursor = i
			break
		}
	}
	top := 0 // index of the first visible row (scroll offset)

	in := int(os.Stdin.Fd())
	old, err := term.MakeRaw(in)
	if err != nil {
		return nil, false, fmt.Errorf("interactive selection requires a terminal; re-run with --yes")
	}
	defer func() { _ = term.Restore(in, old) }()

	// Alternate screen so the menu never scrolls the user's scrollback, and is
	// torn down cleanly on exit. Hide the cursor while interacting.
	fmt.Print("\x1b[?1049h\x1b[?25l")
	defer fmt.Print("\x1b[?25h\x1b[?1049l")

	rowText := func(i int) string {
		r := rows[i]
		pointer := "  "
		if i == cursor {
			pointer = Cyan("›") + " "
		}
		if r.isHeader {
			total := len(groupItems[r.group])
			n := 0
			for _, ix := range groupItems[r.group] {
				if sel[ix] {
					n++
				}
			}
			box := "[ ]"
			switch {
			case total > 0 && n == total:
				box = Green("[x]")
			case n > 0:
				box = Green("[~]")
			}
			return pointer + box + " " + Bold(r.group)
		}
		box := "[ ]"
		if sel[r.item] {
			box = Green("[x]")
		}
		return pointer + "  " + box + " " + items[r.item].Label
	}

	render := func() {
		width, height := termSize()
		// Reserve one line for the title and one for the help/status footer.
		visible := max(height-2, 1)
		var end int
		top, end = scrollWindow(cursor, top, visible, len(rows))

		var b strings.Builder
		b.WriteString("\x1b[H\x1b[J") // home + clear screen
		b.WriteString(clipVisible(Bold(title), width) + "\r\n")
		for i := top; i < end; i++ {
			b.WriteString(clipVisible(rowText(i), width) + "\r\n")
		}
		help := "↑/↓ move · space toggle · enter confirm · q cancel"
		if len(rows) > visible {
			help += fmt.Sprintf(" · %d–%d/%d", top+1, end, len(rows))
		}
		b.WriteString(clipVisible(Dim(help), width))
		fmt.Print(b.String())
	}

	moveCursor := func(delta int) {
		cursor += delta
		if cursor < 0 {
			cursor = 0
		}
		if cursor > len(rows)-1 {
			cursor = len(rows) - 1
		}
	}

	toggle := func() {
		r := rows[cursor]
		if r.isHeader {
			idxs := groupItems[r.group]
			all := true
			for _, ix := range idxs {
				if !sel[ix] {
					all = false
					break
				}
			}
			for _, ix := range idxs {
				sel[ix] = !all
			}
		} else {
			sel[r.item] = !sel[r.item]
		}
	}

	render()
	reader := bufio.NewReader(os.Stdin)
	for {
		c, rerr := reader.ReadByte()
		if rerr != nil {
			return nil, false, nil
		}
		switch c {
		case 3, 'q': // ctrl-c, q
			return nil, false, nil
		case '\r', '\n': // confirm
			for i, s := range sel {
				if s {
					selected = append(selected, i)
				}
			}
			return selected, true, nil
		case ' ', 'x':
			toggle()
			render()
		case 'k':
			moveCursor(-1)
			render()
		case 'j':
			moveCursor(1)
			render()
		case 0x1b: // escape: arrow-key sequence, or a lone esc to cancel
			if reader.Buffered() == 0 {
				return nil, false, nil
			}
			b1, _ := reader.ReadByte()
			if b1 != '[' {
				return nil, false, nil
			}
			b2, _ := reader.ReadByte()
			switch b2 {
			case 'A':
				moveCursor(-1)
				render()
			case 'B':
				moveCursor(1)
				render()
			}
		}
	}
}

// scrollWindow returns the first visible row index (top) and the exclusive end
// index for a viewport of `visible` rows over n total rows, scrolling as needed
// to keep cursor in view.
func scrollWindow(cursor, top, visible, n int) (int, int) {
	if visible < 1 {
		visible = 1
	}
	if cursor < top {
		top = cursor
	}
	if cursor >= top+visible {
		top = cursor - visible + 1
	}
	top = min(top, n-visible)
	top = max(top, 0)
	return top, min(top+visible, n)
}

// termSize returns the terminal's width and height, with sane fallbacks.
func termSize() (width, height int) {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 {
		w = 80
	}
	if err != nil || h <= 0 {
		h = 24
	}
	return w, h
}

// clipVisible truncates s to at most max visible columns, passing ANSI SGR and
// OSC 8 hyperlink escapes through without counting them, and closing any open
// hyperlink / style reset when it cuts. This keeps each rendered row to a single
// terminal line so the viewport's line accounting stays correct.
func clipVisible(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	var b strings.Builder
	width := 0
	inLink := false
	truncated := false
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) {
			switch s[i+1] {
			case '[': // CSI: ... <final byte 0x40-0x7E>
				j := i + 2
				for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
					j++
				}
				if j < len(s) {
					j++
				}
				b.WriteString(s[i:j])
				i = j
				continue
			case ']': // OSC: ... <BEL or ESC \>
				j := i + 2
				for j < len(s) {
					if s[j] == 0x07 {
						j++
						break
					}
					if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
						j += 2
						break
					}
					j++
				}
				seq := s[i:j]
				// A hyperlink open carries a URL; the close is empty (";;").
				inLink = !strings.HasPrefix(seq, "\x1b]8;;\x07") && !strings.HasPrefix(seq, "\x1b]8;;\x1b\\")
				b.WriteString(seq)
				i = j
				continue
			}
		}
		if width >= limit {
			truncated = true
			break
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		b.WriteString(s[i : i+size])
		width++
		i += size
	}
	if truncated {
		if inLink {
			b.WriteString("\x1b]8;;\x07")
		}
		b.WriteString("\x1b[0m")
	}
	return b.String()
}
