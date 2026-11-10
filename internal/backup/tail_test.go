package backup

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTailKeepsLastLines(t *testing.T) {
	tail := &Tail{Max: 2}
	_, _ = tail.Write([]byte("one\ntwo\nthr"))
	_, _ = tail.Write([]byte("ee\nfour"))
	if got := tail.String(); got != "three\nfour" {
		t.Fatalf("got %q", got)
	}
}

func TestFailureReportKeepsHeadAndNewestLines(t *testing.T) {
	log := strings.Repeat(strings.Repeat("x", 99)+"\n", 100) + "ERROR: [082]: WAL segment was not archived"
	got := FailureReport("diff", errors.New("pgbackrest exited with 82"), log)
	if len(got) > MaxReport || !strings.Contains(got, "backup: diff\nerror: pgbackrest exited with 82\n") || !strings.HasSuffix(got, "not archived") {
		t.Fatalf("report (%d bytes): %q", len(got), got)
	}
}

func TestHeartbeatPostsBodyWithUserAgent(t *testing.T) {
	var method, path, agent, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		method, path, agent, body = r.Method, r.URL.Path, r.UserAgent(), string(b)
	}))
	defer srv.Close()
	UserAgent = "pgm/test"

	if err := Heartbeat(t.Context(), srv.URL+"/hb/", "/fail", "report"); err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPost || path != "/hb/fail" || agent != "pgm/test" || body != "report" {
		t.Fatalf("got %s %s %q %q", method, path, agent, body)
	}
}
