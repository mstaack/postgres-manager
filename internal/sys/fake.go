package sys

import (
	"context"
	"io"
	"strings"
)

// Response is a canned FakeRunner answer.
type Response struct {
	Out string
	Err error
}

// FakeRunner records commands and answers from Responses. Unknown commands fail,
// unless Fallback is set.
type FakeRunner struct {
	Responses map[string]Response
	Fallback  func(c Cmd) (string, error)
	Calls     []Cmd
}

// Key identifies a command in FakeRunner.Responses.
func Key(c Cmd) string {
	k := strings.TrimSpace(c.Name + " " + strings.Join(c.Args, " "))
	if c.AsPostgres {
		k = "postgres: " + k
	}
	return k
}

// Run implements Runner.
func (f *FakeRunner) Run(_ context.Context, c Cmd) (string, error) {
	if c.Stdin != nil {
		b, _ := io.ReadAll(c.Stdin)
		c.Stdin = strings.NewReader(string(b))
	}
	f.Calls = append(f.Calls, c)
	if r, ok := f.Responses[Key(c)]; ok {
		return r.Out, r.Err
	}
	if f.Fallback != nil {
		return f.Fallback(c)
	}
	return "", &unexpected{Key(c)}
}

// Stream implements Runner.
func (f *FakeRunner) Stream(ctx context.Context, c Cmd) error {
	_, err := f.Run(ctx, c)
	return err
}

// StdinOf returns what call i received on stdin.
func (f *FakeRunner) StdinOf(i int) string {
	if f.Calls[i].Stdin == nil {
		return ""
	}
	b, _ := io.ReadAll(f.Calls[i].Stdin)
	f.Calls[i].Stdin = strings.NewReader(string(b))
	return string(b)
}

type unexpected struct{ key string }

func (u *unexpected) Error() string { return "fake: unexpected command: " + u.key }
