package sys

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestLockExclusive(t *testing.T) {
	p := filepath.Join(t.TempDir(), "l")
	unlock, err := Lock(context.Background(), p, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(context.Background(), p, 0); !errors.Is(err, ErrLocked) {
		t.Fatalf("err=%v", err)
	}
	unlock()
	u2, err := Lock(context.Background(), p, 0)
	if err != nil {
		t.Fatal(err)
	}
	u2()
}

func TestLockWaits(t *testing.T) {
	p := filepath.Join(t.TempDir(), "l")
	unlock, err := Lock(context.Background(), p, 0)
	if err != nil {
		t.Fatal(err)
	}
	go func() { time.Sleep(200 * time.Millisecond); unlock() }()
	u2, err := Lock(context.Background(), p, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	u2()
}

func TestLockCancelled(t *testing.T) {
	p := filepath.Join(t.TempDir(), "l")
	unlock, err := Lock(context.Background(), p, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	start := time.Now()
	if _, err := Lock(ctx, p, time.Hour); !errors.Is(err, context.Canceled) || time.Since(start) > 5*time.Second {
		t.Fatalf("err=%v after %s", err, time.Since(start))
	}
}
