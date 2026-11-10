package sys

import (
	"context"
	"errors"
	"os"
	"sync"
	"syscall"
	"time"
)

// ErrLocked means another process holds the lock.
var ErrLocked = errors.New("locked by another process")

// Lock takes an exclusive flock on path, polling up to wait or until ctx is cancelled.
//
// The fd is O_CLOEXEC, so it never leaks into pg_ctlcluster/postmaster (the bash
// version needed 9>&-). Do not pass it via ExtraFiles.
func Lock(ctx context.Context, path string, wait time.Duration) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			var once sync.Once // callers unlock early and again through defer
			return func() {
				once.Do(func() {
					_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
					_ = f.Close()
				})
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = f.Close()
			return nil, err
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, ErrLocked
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}
