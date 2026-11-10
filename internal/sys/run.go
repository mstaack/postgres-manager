// Package sys is the only place that starts processes or touches OS-level primitives.
package sys

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Cmd describes one process. Secrets go into Stdin, never into Args.
type Cmd struct {
	Name       string
	Args       []string
	Stdin      io.Reader
	AsPostgres bool      // run as: runuser -u postgres -- Name Args...
	Env        []string  // appended to the base environment
	Stdout     io.Writer // Stream only: where stdout goes (default the terminal)
	Stderr     io.Writer // Stream only: where stderr goes (default the terminal)
}

// Runner starts processes. Tests use FakeRunner.
type Runner interface {
	// Run captures stdout; a non-zero exit returns *ExitError with stderr.
	Run(ctx context.Context, c Cmd) (string, error)
	// Stream connects stdout/stderr to the terminal.
	Stream(ctx context.Context, c Cmd) error
}

// ExitError is a failed command with the tail of its stderr.
type ExitError struct {
	Cmd    string
	Code   int
	Stderr string
}

func (e *ExitError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		return fmt.Sprintf("%s exited with %d", e.Cmd, e.Code)
	}
	lines := strings.Split(msg, "\n")
	if len(lines) > 5 {
		lines = lines[len(lines)-5:]
	}
	return fmt.Sprintf("%s exited with %d: %s", e.Cmd, e.Code, strings.Join(lines, "\n"))
}

// Default is the real runner.
var Default Runner = ExecRunner{}

// ExecRunner runs commands with os/exec.
type ExecRunner struct{}

func buildArgv(c Cmd) []string {
	argv := append([]string{c.Name}, c.Args...)
	if c.AsPostgres {
		argv = append([]string{"runuser", "-u", "postgres", "--"}, argv...)
	}
	return argv
}

func command(ctx context.Context, c Cmd) *exec.Cmd {
	argv := buildArgv(c)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = "/"
	cmd.Env = append(append(os.Environ(), "LC_ALL=C.UTF-8"), c.Env...)
	cmd.Stdin = c.Stdin
	return cmd
}

func wrap(c Cmd, err error, stderr string) error {
	if err == nil {
		return nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return &ExitError{Cmd: c.Name, Code: ee.ExitCode(), Stderr: stderr}
	}
	return fmt.Errorf("%s: %w", c.Name, err)
}

// Run implements Runner.
func (ExecRunner) Run(ctx context.Context, c Cmd) (string, error) {
	cmd := command(ctx, c)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	return out.String(), wrap(c, err, errb.String())
}

// Stream implements Runner.
func (ExecRunner) Stream(ctx context.Context, c Cmd) error {
	cmd := command(ctx, c)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if c.Stdout != nil {
		cmd.Stdout = c.Stdout
	}
	if c.Stderr != nil {
		cmd.Stderr = c.Stderr
	}
	return wrap(c, cmd.Run(), "")
}

// Have reports whether a binary is on PATH.
func Have(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
