//go:build windows

package pgbundle

import (
	"strings"
	"testing"
)

// A lock's data directory is read as UTF-8 and as the system's ANSI code page: an ASCII
// path is the same both ways (one name), and one that is not matches the other reading.
func TestShmDataDirs(t *testing.T) {
	for _, c := range []string{`C:\Program Files\Maktaba`, `C:\data`} {
		if got := shmDataDirs(c); len(got) != 1 || got[0] != c {
			t.Errorf("%q: %v", c, got)
		}
	}
	if acp := acpToUTF8("plain-ascii"); acp != "plain-ascii" {
		t.Errorf("the code page reading of ASCII is %q", acp)
	}
	if acpToUTF8("") != "" {
		t.Error("an empty name")
	}
}

// The mapping's name is what Postgres makes of the directory (win32_shmem.c): the drive
// as it is, backslashes slashes, after PostgreSQL:.
func TestShmName(t *testing.T) {
	if got := shmName(`C:\Data\Maktaba`); got != `Global\PostgreSQL:C:/Data/Maktaba` {
		t.Fatalf("got %q", got)
	}
	if !strings.HasPrefix(shmName(`relative`), `Global\PostgreSQL:`) {
		t.Fatal("a relative directory is made absolute")
	}
}
