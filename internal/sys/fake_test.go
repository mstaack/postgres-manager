package sys

import (
	"context"
	"strings"
	"testing"
)

func TestFakeRunner(t *testing.T) {
	f := &FakeRunner{Responses: map[string]Response{"echo a": {Out: "a\n"}}}
	out, err := f.Run(context.Background(), Cmd{Name: "echo", Args: []string{"a"}})
	if err != nil || out != "a\n" {
		t.Fatalf("out=%q err=%v", out, err)
	}
	_, err = f.Run(context.Background(), Cmd{Name: "psql", Args: []string{"-c", "x"}, AsPostgres: true, Stdin: strings.NewReader("pw")})
	if err == nil || err.Error() != "fake: unexpected command: postgres: psql -c x" {
		t.Fatalf("err=%v", err)
	}
	if len(f.Calls) != 2 {
		t.Fatalf("calls=%d", len(f.Calls))
	}
	if got := f.StdinOf(1); got != "pw" {
		t.Fatalf("stdin=%q", got)
	}
	if err := f.Stream(context.Background(), Cmd{Name: "echo", Args: []string{"a"}}); err != nil {
		t.Fatal(err)
	}
}
