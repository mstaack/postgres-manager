package sys

import (
	"strings"
	"testing"
)

func TestParseOSRelease(t *testing.T) {
	tests := []struct {
		name, in string
		want     OS
		errSub   string
	}{
		{"debian", "ID=debian\nVERSION_CODENAME=bookworm\nPRETTY_NAME=\"Debian GNU/Linux 12 (bookworm)\"\n",
			OS{"debian", "bookworm", "Debian GNU/Linux 12 (bookworm)"}, ""},
		{"ubuntu", "ID=ubuntu\nVERSION_CODENAME=noble\nPRETTY_NAME='Ubuntu 24.04 LTS'\n",
			OS{"ubuntu", "noble", "Ubuntu 24.04 LTS"}, ""},
		{"mint", "ID=linuxmint\nID_LIKE=\"ubuntu debian\"\nVERSION_CODENAME=wilma\nPRETTY_NAME=\"Linux Mint 21\"\n",
			OS{"ubuntu", "wilma", "Linux Mint 21"}, ""},
		{"fedora", "ID=fedora\nID_LIKE=\"\"\nPRETTY_NAME=\"Fedora Linux 40\"\n", OS{}, "Unsupported OS: Fedora"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseOSRelease(tc.in)
			if tc.errSub != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errSub) {
					t.Fatalf("err=%v", err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %+v err=%v", got, err)
			}
		})
	}
}
