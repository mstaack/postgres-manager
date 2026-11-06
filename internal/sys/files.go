package sys

import (
	"os"
	"path/filepath"
	"syscall"
)

// WriteFileAtomic writes via a temp file in the same directory and renames it.
// uid/gid -1 leave ownership unchanged.
func WriteFileAtomic(path string, data []byte, mode os.FileMode, uid, gid int) error {
	dir, base := filepath.Split(path)
	if dir == "" {
		dir = "."
	}
	f, err := os.CreateTemp(dir, "."+base+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	fail := func(err error) error {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if _, err := f.Write(data); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Chmod(mode); err != nil {
		return fail(err)
	}
	if uid >= 0 || gid >= 0 {
		if err := f.Chown(uid, gid); err != nil {
			return fail(err)
		}
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// MkdirOwned creates dir (and parents) and sets mode and ownership on dir itself.
func MkdirOwned(dir string, mode os.FileMode, uid, gid int) error {
	if err := os.MkdirAll(dir, mode); err != nil {
		return err
	}
	if err := os.Chmod(dir, mode); err != nil {
		return err
	}
	if uid >= 0 || gid >= 0 {
		return os.Chown(dir, uid, gid)
	}
	return nil
}

// FreeBytes returns the bytes available to unprivileged users on dir's filesystem.
func FreeBytes(dir string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil //nolint:unconvert // field types differ per arch
}
