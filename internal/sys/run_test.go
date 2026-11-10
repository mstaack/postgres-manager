package sys

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestExecRunnerCapturesStdout(t *testing.T) {
	out, err := ExecRunner{}.Run(context.Background(), Cmd{Name: "sh", Args: []string{"-c", "echo hi"}})
	if err != nil || out != "hi\n" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestExecRunnerExitError(t *testing.T) {
	_, err := ExecRunner{}.Run(context.Background(), Cmd{Name: "sh", Args: []string{"-c", "echo boom >&2; exit 3"}})
	var ee *ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("want ExitError, got %v", err)
	}
	if ee.Code != 3 || !strings.Contains(ee.Error(), "boom") {
		t.Fatalf("code=%d msg=%q", ee.Code, ee.Error())
	}
}

func TestExecRunnerStdin(t *testing.T) {
	out, err := ExecRunner{}.Run(context.Background(), Cmd{Name: "cat", Stdin: strings.NewReader("secret")})
	if err != nil || out != "secret" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestExecRunnerAsPostgresBuildsRunuser(t *testing.T) {
	got := buildArgv(Cmd{Name: "psql", Args: []string{"-X"}, AsPostgres: true})
	want := []string{"runuser", "-u", "postgres", "--", "psql", "-X"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestExitErrorMessage(t *testing.T) {
	e := &ExitError{Cmd: "x", Code: 1}
	if e.Error() != "x exited with 1" {
		t.Fatalf("%q", e.Error())
	}
	e = &ExitError{Cmd: "x", Code: 2, Stderr: "1\n2\n3\n4\n5\n6\n7\n"}
	if e.Error() != "x exited with 2: 3\n4\n5\n6\n7" {
		t.Fatalf("%q", e.Error())
	}
}

func TestHave(t *testing.T) {
	if !Have("sh") || Have("definitely-not-a-binary-xyz") {
		t.Fatal("Have wrong")
	}
}

func TestExecRunnerCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	start := time.Now()
	_, err := ExecRunner{}.Run(ctx, Cmd{Name: "sleep", Args: []string{"30"}})
	if !errors.Is(err, context.Canceled) || time.Since(start) > 5*time.Second {
		t.Fatalf("err=%v after %s", err, time.Since(start))
	}
}

func TestTailWriter(t *testing.T) {
	w := &tailWriter{max: 4}
	for _, s := range []string{"abc", "defgh", "ij"} {
		_, _ = w.Write([]byte(s))
	}
	if w.String() != "ghij" {
		t.Fatalf("%q", w.String())
	}
}
