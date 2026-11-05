package sys

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestLockExclusive(t *testing.T) {
	p := filepath.Join(t.TempDir(), "l")
	unlock, err := Lock(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(p, 0); !errors.Is(err, ErrLocked) {
		t.Fatalf("err=%v", err)
	}
	unlock()
	u2, err := Lock(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	u2()
}

func TestLockWaits(t *testing.T) {
	p := filepath.Join(t.TempDir(), "l")
	unlock, err := Lock(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	go func() { time.Sleep(200 * time.Millisecond); unlock() }()
	u2, err := Lock(p, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	u2()
}
