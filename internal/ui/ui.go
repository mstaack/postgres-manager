// Package ui holds terminal output and prompts. Normal output and prompts go to
// stdout, errors to stderr.
package ui

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// ErrCancelled is returned when the user cancels a prompt (ESC, Ctrl-C).
var ErrCancelled = errors.New("cancelled")

// ErrNoInput is returned when stdin is closed while a prompt waits.
var ErrNoInput = errors.New("no input (stdin closed)")

// UI is the terminal. All line-mode prompts share one buffered reader, so piped
// answers are never lost between prompts.
type UI struct {
	In    *bufio.Reader
	Out   io.Writer
	Err   io.Writer
	Color bool
	TTY   bool // stdin is a terminal: read key by key (ESC cancels at once, secrets not echoed)
	tty   *os.File
}

// New returns the UI for the process's stdin/stdout.
func New() *UI {
	return &UI{
		In:    bufio.NewReader(os.Stdin),
		Out:   os.Stdout,
		Err:   os.Stderr,
		Color: term.IsTerminal(int(os.Stdout.Fd())) && os.Getenv("NO_COLOR") == "",
		TTY:   term.IsTerminal(int(os.Stdin.Fd())),
		tty:   os.Stdin,
	}
}

// NewTest returns a line-mode UI reading in, writing to a buffer (see Output).
func NewTest(in string) *UI {
	var b bytes.Buffer
	return &UI{In: bufio.NewReader(strings.NewReader(in)), Out: &b, Err: &b}
}

// Output returns what a NewTest UI printed.
func (u *UI) Output() string {
	if b, ok := u.Out.(*bytes.Buffer); ok {
		return b.String()
	}
	return ""
}

const (
	bold   = "\x1b[1m"
	dim    = "\x1b[2m"
	red    = "\x1b[31m"
	green  = "\x1b[32m"
	yellow = "\x1b[33m"
	cyan   = "\x1b[36m"
	reset  = "\x1b[0m"
)

func (u *UI) paint(code, s string) string {
	if !u.Color || s == "" {
		return s
	}
	return code + s + reset
}

// Good is green text.
func (u *UI) Good(s string) string { return u.paint(green, s) }

// Bad is red text.
func (u *UI) Bad(s string) string { return u.paint(red, s) }

// Meh is yellow text.
func (u *UI) Meh(s string) string { return u.paint(yellow, s) }

// Dim is dimmed text.
func (u *UI) Dim(s string) string { return u.paint(dim, s) }

// Bold is bold text.
func (u *UI) Bold(s string) string { return u.paint(bold, s) }

// Printf writes to stdout.
func (u *UI) Printf(format string, a ...any) { fmt.Fprintf(u.Out, format, a...) }

// Println writes a line to stdout.
func (u *UI) Println(a ...any) { fmt.Fprintln(u.Out, a...) }

// Info prints "▸ msg".
func (u *UI) Info(format string, a ...any) {
	fmt.Fprintf(u.Out, "%s %s\n", u.paint(cyan, "▸"), fmt.Sprintf(format, a...))
}

// OK prints "✓ msg".
func (u *UI) OK(format string, a ...any) {
	fmt.Fprintf(u.Out, "%s %s\n", u.paint(green, "✓"), fmt.Sprintf(format, a...))
}

// Warn prints "! msg".
func (u *UI) Warn(format string, a ...any) {
	fmt.Fprintf(u.Out, "%s %s\n", u.paint(yellow, "!"), fmt.Sprintf(format, a...))
}

// Error prints "✗ msg" to stderr.
func (u *UI) Error(err error) {
	fmt.Fprintf(u.Err, "%s\n", u.paint(red, "✗ "+err.Error()))
}

// Header prints a blank line and a bold title.
func (u *UI) Header(s string) { fmt.Fprintf(u.Out, "\n%s\n", u.Bold(s)) }

// Row prints a dashboard row with an 11-character dimmed label.
func (u *UI) Row(label, value string) {
	pad := 11 - len([]rune(label))
	if pad < 0 {
		pad = 0
	}
	fmt.Fprintf(u.Out, "  %s%s %s\n", u.Dim(label), strings.Repeat(" ", pad), value)
}

// Pad pads s with spaces to width runes (ignoring colour codes in s).
func Pad(s string, width int) string {
	n := len([]rune(stripANSI(s)))
	if n >= width {
		return s
	}
	return s + strings.Repeat(" ", width-n)
}

// PadLeft right-aligns s in width runes.
func PadLeft(s string, width int) string {
	n := len([]rune(stripANSI(s)))
	if n >= width {
		return s
	}
	return strings.Repeat(" ", width-n) + s
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
