package backup

import (
	"path/filepath"
	"strconv"
	"strings"
)

// Mount is a mounted filesystem that can hold a local repository.
type Mount struct {
	Path string
	Free int64 // bytes available
}

// storageFS are filesystems a local repository can live on (local disks and network shares).
var storageFS = map[string]bool{"ext2": true, "ext3": true, "ext4": true, "xfs": true, "btrfs": true, "zfs": true,
	"f2fs": true, "nfs": true, "nfs4": true, "cifs": true, "smb3": true, "fuse.sshfs": true}

// ParseMounts returns the writable storage mount points of /proc/self/mounts (Free left 0).
func ParseMounts(procMounts string) []Mount {
	var ms []Mount
	seen := map[string]bool{}
	for _, l := range strings.Split(procMounts, "\n") {
		f := strings.Fields(l)
		if len(f) < 4 || !storageFS[f[2]] || hasOpt(f[3], "ro") {
			continue
		}
		p := unescapeMount(f[1])
		if seen[p] || p == "/boot" || strings.HasPrefix(p, "/boot/") || strings.HasPrefix(p, "/snap/") ||
			strings.HasPrefix(p, "/var/lib/docker/") || strings.HasPrefix(p, "/var/lib/containers/") {
			continue
		}
		seen[p] = true
		ms = append(ms, Mount{Path: p})
	}
	return ms
}

func hasOpt(opts, o string) bool {
	for _, x := range strings.Split(opts, ",") {
		if x == o {
			return true
		}
	}
	return false
}

// unescapeMount decodes the \040-style octal escapes of /proc/self/mounts.
func unescapeMount(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// MountOf returns the mount point that contains path ("" if none does).
func MountOf(ms []Mount, path string) string {
	best := ""
	path = filepath.Clean(path)
	for _, m := range ms {
		if (path == m.Path || m.Path == "/" || strings.HasPrefix(path, m.Path+"/")) && len(m.Path) > len(best) {
			best = m.Path
		}
	}
	return best
}

// minSeparateFree is the free space a separate volume needs to be suggested.
const minSeparateFree = 10 << 30

// SuggestLocalDir proposes a directory for a local repository: <mount>/pgbackrest on the
// volume with the most free space that is not the database's (an extra disk survives
// the database disk), otherwise DefaultLocalDir.
func SuggestLocalDir(ms []Mount, dataDir string) string {
	dataMount := MountOf(ms, dataDir)
	var best *Mount
	for i, m := range ms {
		if m.Path == dataMount || m.Path == "/" || m.Free < minSeparateFree {
			continue
		}
		if best == nil || m.Free > best.Free {
			best = &ms[i]
		}
	}
	if best == nil {
		return DefaultLocalDir
	}
	return filepath.Join(best.Path, "pgbackrest")
}
