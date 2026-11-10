package backup

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

// MaxReport caps a FailureReport, so the receiver gets it whole (the API keeps 4 KB).
const MaxReport = 3500

// Tail keeps the last Max lines written to it (safe for concurrent stdout/stderr copies).
type Tail struct {
	Max int

	mu      sync.Mutex
	lines   []string
	partial string
}

func (t *Tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	parts := strings.Split(t.partial+string(p), "\n")
	t.partial = parts[len(parts)-1]
	t.lines = append(t.lines, parts[:len(parts)-1]...)
	if over := len(t.lines) - t.Max; over > 0 {
		t.lines = t.lines[over:]
	}
	return len(p), nil
}

// String returns the kept lines, an unterminated last line included.
func (t *Tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	lines := t.lines
	if t.partial != "" {
		lines = append(append([]string{}, lines...), t.partial)
	}
	if over := len(lines) - t.Max; over > 0 {
		lines = lines[over:]
	}
	return strings.Join(lines, "\n")
}

// FailureReport is the heartbeat /fail body: host, backup type and error first, then as
// many of the last log lines as fit into MaxReport.
func FailureReport(typ string, err error, log string) string {
	host, _ := os.Hostname()
	head := fmt.Sprintf("host: %s\nbackup: %s\nerror: %v\n", host, typ, err)
	lines := strings.Split(strings.TrimSpace(log), "\n")
	for len(lines) > 0 && len(head)+len(strings.Join(lines, "\n"))+1 > MaxReport {
		lines = lines[1:]
	}
	if body := strings.Join(lines, "\n"); body != "" {
		return head + "\n" + body
	}
	if len(head) > MaxReport {
		return head[:MaxReport]
	}
	return head
}
