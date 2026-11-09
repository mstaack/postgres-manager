package backup

import "testing"

const procMounts = `/dev/sda1 / ext4 rw,relatime 0 0
proc /proc proc rw 0 0
tmpfs /run tmpfs rw 0 0
/dev/sda15 /boot/efi vfat rw 0 0
/dev/sdb /mnt/HC_Volume_123 ext4 rw 0 0
/dev/sdc /mnt/small xfs rw 0 0
/dev/sdd /mnt/with\040space ext4 rw 0 0
/dev/sde /src ext4 ro,relatime 0 0
overlay /var/lib/docker/overlay2/x/merged overlay rw 0 0
/dev/sda1 / ext4 rw 0 0
`

func TestParseMounts(t *testing.T) {
	got := ParseMounts(procMounts)
	want := []string{"/", "/mnt/HC_Volume_123", "/mnt/small", "/mnt/with space"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i].Path != want[i] {
			t.Errorf("%d: %q, want %q", i, got[i].Path, want[i])
		}
	}
}

func TestMountOf(t *testing.T) {
	ms := []Mount{{Path: "/"}, {Path: "/mnt/vol"}, {Path: "/var/lib/postgresql"}}
	for path, want := range map[string]string{
		"/var/lib/postgresql/17/main": "/var/lib/postgresql",
		"/var/lib/postgresql":         "/var/lib/postgresql",
		"/mnt/vol/pgbackrest":         "/mnt/vol",
		"/mnt/volume2/x":              "/",
		"/var/lib/pgbackrest":         "/",
	} {
		if got := MountOf(ms, path); got != want {
			t.Errorf("MountOf(%s) = %q, want %q", path, got, want)
		}
	}
	if MountOf(nil, "/x") != "" {
		t.Error("no mounts")
	}
}

func TestSuggestLocalDir(t *testing.T) {
	const gb = 1 << 30
	data := "/var/lib/postgresql/17/main"
	cases := []struct {
		name string
		ms   []Mount
		want string
	}{
		{"only the root disk", []Mount{{"/", 100 * gb}}, DefaultLocalDir},
		{"separate volume", []Mount{{"/", 100 * gb}, {"/mnt/HC_Volume_1", 500 * gb}}, "/mnt/HC_Volume_1/pgbackrest"},
		{"the biggest of two volumes", []Mount{{"/", 100 * gb}, {"/mnt/a", 50 * gb}, {"/backup", 900 * gb}}, "/backup/pgbackrest"},
		{"too small", []Mount{{"/", 100 * gb}, {"/mnt/tiny", 2 * gb}}, DefaultLocalDir},
		{"not the database volume", []Mount{{"/", 100 * gb}, {"/var/lib/postgresql", 900 * gb}}, DefaultLocalDir},
	}
	for _, c := range cases {
		if got := SuggestLocalDir(c.ms, data); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
