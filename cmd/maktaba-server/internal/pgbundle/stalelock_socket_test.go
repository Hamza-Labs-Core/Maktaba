package pgbundle

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// The socket's lock (.s.PGSQL.<port>.lock) a postmaster that did not end cleanly left goes
// before the next start when its PID is another live process now (a container started
// again after a kill: Postgres takes over only a socket lock whose PID is gone, and
// crash-looped) and no postmaster holds the cluster; it stays while one does, or when its
// PID is gone (Postgres takes it over) or this process's.
func TestAStaleSocketLockGoesOnlyWhenNoPostmasterHoldsTheCluster(t *testing.T) {
	const port, reused, postmaster = 54329, 4242, 4343
	cases := []struct {
		name     string
		lockPID  int
		dataLock string // postmaster.pid's first line, "" none
		procs    map[int]string
		keep     bool
	}{
		{"its PID is another program, no postmaster.pid", reused, "", map[int]string{reused: "maktaba-server"}, false},
		{"its PID is another program, postmaster.pid names a gone process", reused, "999",
			map[int]string{reused: "plugind"}, false},
		{"its PID is a postgres of another cluster, no postmaster.pid", reused, "",
			map[int]string{reused: "postgres"}, false},
		{"a postmaster holds the cluster", postmaster, strconv.Itoa(postmaster),
			map[int]string{postmaster: "postgres"}, true},
		{"postmaster.pid names a process this system cannot name", reused, strconv.Itoa(postmaster),
			map[int]string{reused: "sh", postmaster: ""}, true},
		{"its PID is gone", reused, "", map[int]string{}, true},
		{"its PID is this process's", os.Getpid(), "", map[int]string{os.Getpid(): "pgbundle.test"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oldLook := lookProcess
			t.Cleanup(func() { lookProcess = oldLook })
			lookProcess = func(pid int) (string, bool) {
				name, ok := tc.procs[pid]
				return name, ok
			}
			b := Bundle{Data: t.TempDir(), Port: port}
			sock := filepath.Join(b.Data, ".s.PGSQL."+strconv.Itoa(port)+".lock")
			if err := os.WriteFile(sock, []byte(strconv.Itoa(tc.lockPID)+"\n"+b.Data+"\n1700000000\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.dataLock != "" {
				if err := os.WriteFile(filepath.Join(b.Data, "postmaster.pid"), []byte(tc.dataLock+"\n"+b.Data+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			b.ClearStaleLock()
			_, err := os.Stat(sock)
			if kept := err == nil; kept != tc.keep {
				t.Fatalf("socket lock kept = %v, want %v", kept, tc.keep)
			}
		})
	}
}

// Both locks a killed container's postmaster left, their PID another program's now (the
// PID namespace gave it again), go together: the real lookups, a real process.
func TestBothLocksOfAKilledContainerGo(t *testing.T) {
	other := helperProcess(t)
	b := Bundle{Data: t.TempDir(), Port: 54329}
	pid := strconv.Itoa(other)
	data := filepath.Join(b.Data, "postmaster.pid")
	sock := filepath.Join(b.Data, ".s.PGSQL.54329.lock")
	for _, f := range []string{data, sock} {
		if err := os.WriteFile(f, []byte(pid+"\n"+b.Data+"\n1700000000\n54329\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	b.ClearStaleLock()
	for _, f := range []string{data, sock} {
		if _, err := os.Stat(f); err == nil {
			t.Fatalf("%s was kept", filepath.Base(f))
		}
	}
}
