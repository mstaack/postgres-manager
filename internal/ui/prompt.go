package ui

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// ReadLine reads one answer. ESC cancels (ErrCancelled), a closed stdin gives ErrNoInput.
// From a pipe a line containing ESC cancels; on a terminal it reads key by key, so a
// lone ESC cancels at once and arrow/function keys are ignored. Secrets are never echoed.
func (u *UI) ReadLine(secret bool) (string, error) {
	if u.TTY && u.tty != nil {
		if s, err, ok := u.readTTY(secret); ok {
			return s, err
		}
	}
	line, err := u.In.ReadString('\n')
	if err != nil && (!errors.Is(err, io.EOF) || line == "") {
		return "", ErrNoInput
	}
	line = strings.TrimRight(line, "\r\n")
	if strings.ContainsRune(line, 0x1b) {
		return "", ErrCancelled
	}
	return line, nil
}

func (u *UI) readTTY(secret bool) (string, error, bool) {
	fd := int(u.tty.Fd())
	old, err := term.MakeRaw(fd)
	if err != nil {
		return "", nil, false
	}
	line, rerr := u.readKeys(fd, secret)
	_ = term.Restore(fd, old)
	fmt.Fprint(u.Out, "\n")
	return line, rerr, true
}

func (u *UI) readKeys(fd int, secret bool) (string, error) {
	var line []byte
	b := make([]byte, 1)
	for {
		n, err := u.tty.Read(b)
		if err != nil || n == 0 {
			return "", ErrNoInput
		}
		switch c := b[0]; {
		case c == '\r' || c == '\n':
			return string(line), nil
		case c == 0x1b:
			// A lone ESC cancels; an escape sequence (arrow keys …) arrives within milliseconds.
			fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
			if k, _ := unix.Poll(fds, 50); k <= 0 {
				return "", ErrCancelled
			}
			rest := make([]byte, 8)
			_, _ = u.tty.Read(rest)
		case c == 0x03:
			return "", ErrCancelled
		case c == 0x04:
			if len(line) == 0 {
				return "", ErrNoInput
			}
		case c == 0x7f || c == 0x08:
			if len(line) > 0 {
				_, size := utf8.DecodeLastRune(line)
				line = line[:len(line)-size]
				if !secret {
					fmt.Fprint(u.Out, "\b \b")
				}
			}
		case c < 0x20:
		default:
			line = append(line, c)
			if !secret {
				_, _ = u.Out.Write(b)
			}
		}
	}
}

// Ask prompts for a line; empty input returns def.
func (u *UI) Ask(title, def string) (string, error) {
	if def != "" {
		u.Printf("%s %s: ", title, u.Dim("["+def+"]"))
	} else {
		u.Printf("%s: ", title)
	}
	s, err := u.ReadLine(false)
	if err != nil {
		return "", err
	}
	if s == "" {
		return def, nil
	}
	return s, nil
}

// AskSecret prompts without echo; empty input keeps cur.
func (u *UI) AskSecret(title, cur string) (string, error) {
	hint := ""
	if cur != "" {
		hint = " " + u.Dim("[keep current]")
	}
	u.Printf("%s%s: ", title, hint)
	s, err := u.ReadLine(true)
	if err != nil {
		return "", err
	}
	if s == "" {
		return cur, nil
	}
	return s, nil
}

// Confirm asks a yes/no question; answers starting with y/Y are yes.
func (u *UI) Confirm(title string, def bool) (bool, error) {
	opts := "y/N"
	if def {
		opts = "Y/n"
	}
	u.Printf("%s %s: ", title, u.Dim("["+opts+"]"))
	s, err := u.ReadLine(false)
	if err != nil {
		return false, err
	}
	if s == "" {
		return def, nil
	}
	return s[0] == 'y' || s[0] == 'Y', nil
}

// Choose lists numbered options and returns the 0-based index (def is 0-based).
func (u *UI) Choose(title string, options []string, def int) (int, error) {
	for i, o := range options {
		u.Printf("  %s %s\n", u.Bold(strconv.Itoa(i+1)+")"), o)
	}
	for {
		s, err := u.Ask(title, strconv.Itoa(def+1))
		if err != nil {
			return 0, err
		}
		if n, err := strconv.Atoi(s); err == nil && n >= 1 && n <= len(options) {
			return n - 1, nil
		}
		u.Warn("Pick 1-%d", len(options))
	}
}

// AskInt asks for a number in [min, max].
func (u *UI) AskInt(title string, def, minV, maxV int) (int, error) {
	for {
		s, err := u.Ask(title, strconv.Itoa(def))
		if err != nil {
			return 0, err
		}
		if n, err := strconv.Atoi(s); err == nil && n >= minV && n <= maxV {
			return n, nil
		}
		u.Warn("Enter a number between %d and %d", minV, maxV)
	}
}

// Pick lists items and returns the one chosen by number or by name.
func (u *UI) Pick(title string, items []string) (string, error) {
	if len(items) == 0 {
		return "", errors.New("nothing to choose from")
	}
	for i, it := range items {
		u.Printf("  %s %s\n", u.Bold(strconv.Itoa(i+1)+")"), it)
	}
	for {
		s, err := u.Ask(title+" (number or name)", "")
		if err != nil {
			return "", err
		}
		if n, err := strconv.Atoi(s); err == nil && n >= 1 && n <= len(items) {
			return items[n-1], nil
		}
		for _, it := range items {
			if it == s {
				return s, nil
			}
		}
		u.Warn("Pick 1-%d or a name from the list", len(items))
	}
}

// MenuItem is one menu entry.
type MenuItem struct{ Key, Label, Desc string }

// Menu prints the entries and reads a choice. ESC answers "b"; a closed stdin returns ErrNoInput.
func (u *UI) Menu(items []MenuItem) (string, error) {
	for _, it := range items {
		u.Printf("  %s %s %s\n", u.Bold(it.Key+")"), Pad(it.Label, 26), u.Dim(it.Desc))
	}
	u.Printf("Choose %s: ", u.Dim("(ESC = back)"))
	s, err := u.ReadLine(false)
	if errors.Is(err, ErrCancelled) {
		return "b", nil
	}
	return strings.TrimSpace(s), err
}
