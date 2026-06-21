// Package ui provides terminal styling, hyperlinks, and a progress spinner.
// Colors are emitted as plain ANSI escapes, gated on a TTY check — nothing here
// queries the terminal, so there is no startup latency.
package ui

import (
	"fmt"
	"os"
	"time"

	"github.com/briandowns/spinner"
	"golang.org/x/term"
)

// colorEnabled is computed once at startup from cheap checks only (no terminal
// queries): on when stdout is a TTY (or FORCE_COLOR is set) and NO_COLOR is not.
var colorEnabled = (term.IsTerminal(int(os.Stdout.Fd())) || os.Getenv("FORCE_COLOR") != "") &&
	os.Getenv("NO_COLOR") == ""

// sgr wraps s in an ANSI SGR sequence when color is enabled.
func sgr(codes, s string) string {
	if !colorEnabled {
		return s
	}
	return "\x1b[" + codes + "m" + s + "\x1b[0m"
}

// Red renders s in red.
func Red(s string) string { return sgr("31", s) }

// Green renders s in green.
func Green(s string) string { return sgr("32", s) }

// Yellow renders s in yellow.
func Yellow(s string) string { return sgr("33", s) }

// Cyan renders s in cyan.
func Cyan(s string) string { return sgr("36", s) }

// Bold renders s in bold.
func Bold(s string) string { return sgr("1", s) }

// Dim renders s faintly.
func Dim(s string) string { return sgr("2", s) }

// Underline renders s underlined.
func Underline(s string) string { return sgr("4", s) }

// Banner renders s as a white-on-red bold label.
func Banner(s string) string { return sgr("41;37;1", s) }

// IsStdoutTTY reports whether stdout is an interactive terminal.
func IsStdoutTTY() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// Link wraps text in an OSC 8 terminal hyperlink (zero visible width) when
// stdout is a TTY; otherwise returns plain text.
func Link(text, url string) string {
	if !IsStdoutTTY() {
		return text
	}
	return "\x1b]8;;" + url + "\x07" + text + "\x1b]8;;\x07"
}

// Spinner is a progress indicator that animates only on a TTY. On a non-TTY it
// is silent until Stop prints the final message once.
type Spinner struct {
	sp *spinner.Spinner
}

// NewSpinner starts a spinner with the given initial message (animated on a TTY).
func NewSpinner(initial string) *Spinner {
	if !IsStdoutTTY() {
		return &Spinner{}
	}
	sp := spinner.New(spinner.CharSets[14], 80*time.Millisecond)
	sp.Suffix = " " + initial
	sp.Start()
	return &Spinner{sp: sp}
}

// Message updates the spinner's text. Safe to call on a nil spinner.
func (s *Spinner) Message(msg string) {
	if s == nil || s.sp == nil {
		return
	}
	s.sp.Suffix = " " + msg
}

// Stop halts the animation and prints the final message (if any). Safe to call
// on a nil spinner, in which case nothing is printed.
func (s *Spinner) Stop(final string) {
	if s == nil {
		return
	}
	if s.sp != nil {
		s.sp.Stop()
	}
	if final != "" {
		fmt.Println(final)
	}
}
