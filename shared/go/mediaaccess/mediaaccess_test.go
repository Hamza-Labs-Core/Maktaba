package mediaaccess

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// modes need a user the modes bind: root reads everything, Windows has no modes.
func needModes(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file modes do not bind this user")
	}
}

func mkfiles(t *testing.T, dir string, n int) []string {
	t.Helper()
	var out []string
	for i := 0; i < n; i++ {
		p := filepath.Join(dir, fmt.Sprintf("f%02d.mkv", i))
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

// T01: 755 / 555 / 000 give rw / ro / none.
func TestCheckModes(t *testing.T) {
	needModes(t)
	base := t.TempDir()
	for _, c := range []struct {
		mode os.FileMode
		want Access
	}{{0o755, ReadWrite}, {0o555, ReadOnly}, {0o000, None}} {
		dir := filepath.Join(base, c.mode.String())
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		mkfiles(t, dir, 2)
		if err := os.Chmod(dir, c.mode); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
		got := Check(dir)
		if got.Access != c.want {
			t.Errorf("mode %v: access %q, want %q", c.mode, got.Access, c.want)
		}
		if got.Partial {
			t.Errorf("mode %v: partial %v", c.mode, got.Unreadable)
		}
	}
}

// The probe leaves nothing behind on a writable root, and keeps a .maktaba that was there.
func TestCheckCleansUp(t *testing.T) {
	dir := t.TempDir()
	if got := Check(dir); got.Access != ReadWrite {
		t.Fatalf("access %q", got.Access)
	}
	if _, err := os.Stat(filepath.Join(dir, ".maktaba")); !os.IsNotExist(err) {
		t.Fatalf(".maktaba left behind: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, ".maktaba"), 0o755); err != nil {
		t.Fatal(err)
	}
	Check(dir)
	entries, err := os.ReadDir(filepath.Join(dir, ".maktaba"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("existing .maktaba: %v %v", entries, err)
	}
}

// T02: 3 of 50 sampled files unreadable: partial with the 3 as examples.
func TestCheckPartial(t *testing.T) {
	needModes(t)
	dir := t.TempDir()
	sub := filepath.Join(dir, "Season 1")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	files := append(mkfiles(t, dir, 25), mkfiles(t, sub, 25)...)
	bad := []string{files[3], files[20], files[40]}
	for _, p := range bad {
		if err := os.Chmod(p, 0o000); err != nil {
			t.Fatal(err)
		}
	}
	got := Check(dir)
	if got.Access != ReadWrite || !got.Partial {
		t.Fatalf("got %+v", got)
	}
	if len(got.Unreadable) != 3 {
		t.Fatalf("examples %v, want %v", got.Unreadable, bad)
	}
	want := map[string]bool{bad[0]: true, bad[1]: true, bad[2]: true}
	for _, p := range got.Unreadable {
		if !want[p] {
			t.Errorf("unexpected example %s", p)
		}
	}
}

// At most MaxExamples examples, and no more than SampleFiles files read.
func TestCheckCapsExamples(t *testing.T) {
	needModes(t)
	dir := t.TempDir()
	for _, p := range mkfiles(t, dir, 12) {
		if err := os.Chmod(p, 0o000); err != nil {
			t.Fatal(err)
		}
	}
	got := CheckWith(dir, Options{SampleFiles: 8, SampleBudget: time.Second})
	if !got.Partial || len(got.Unreadable) != MaxExamples {
		t.Fatalf("got %+v", got)
	}
}

// T03: .maktaba absent on a read-only root: nothing is created.
func TestCheckReadOnlyCreatesNothing(t *testing.T) {
	needModes(t)
	dir := t.TempDir()
	mkfiles(t, dir, 1)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if got := Check(dir); got.Access != ReadOnly {
		t.Fatalf("access %q", got.Access)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("root now holds %v", entries)
	}
}

// A root that is missing, or a file, cannot be listed.
func TestCheckMissing(t *testing.T) {
	if got := Check(filepath.Join(t.TempDir(), "gone")); got.Access != None {
		t.Fatalf("missing: %q", got.Access)
	}
	f := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(f, nil, 0o644)
	if got := Check(f); got.Access != None {
		t.Fatalf("file: %q", got.Access)
	}
}

// The sample skips the folders the walker skips.
func TestSampleSkipsNASFolders(t *testing.T) {
	needModes(t)
	dir := t.TempDir()
	for _, name := range []string{"#recycle", "#snapshot", "lost+found", "@eaDir", ".zfs"} {
		d := filepath.Join(dir, name)
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, p := range mkfiles(t, d, 1) {
			_ = os.Chmod(p, 0o000)
		}
	}
	if got := Check(dir); got.Partial {
		t.Fatalf("sampled a skipped folder: %v", got.Unreadable)
	}
}

func TestWorst(t *testing.T) {
	if Worst(ReadWrite, ReadOnly, ReadWrite) != ReadOnly || Worst(ReadOnly, None) != None ||
		Worst() != "" || Worst("x", ReadWrite) != ReadWrite {
		t.Fatal("worst")
	}
	if !Access("").Writable() || ReadOnly.Writable() || None.Writable() || !ReadWrite.Writable() {
		t.Fatal("writable")
	}
}

func TestMountReadOnly(t *testing.T) {
	info := filepath.Join(t.TempDir(), "mountinfo")
	body := "22 1 8:1 / / rw,relatime - ext4 /dev/sda1 rw\n" +
		"40 22 0:50 /Movies /volume1/Movies ro,relatime - btrfs /dev/md2 rw\n" +
		"41 22 0:51 /Shows /volume1/My\\040Shows rw,relatime - btrfs /dev/md2 ro\n" +
		"42 22 0:52 /x /volume1/Music rw,relatime master:3 - btrfs /dev/md2 rw,space_cache\n"
	if err := os.WriteFile(info, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{
		"/volume1/Movies":      true,
		"/volume1/Movies/Old":  true,
		"/volume1/My Shows/S1": true, // the superblock is read-only
		"/volume1/Music":       false,
		"/volume1/MoviesElse":  false,
		"/srv/other":           false,
	} {
		if got := mountReadOnly(path, info); got != want {
			t.Errorf("%s: %v, want %v", path, got, want)
		}
	}
	if mountReadOnly("/volume1/Movies", filepath.Join(t.TempDir(), "none")) {
		t.Error("no mountinfo is not read-only")
	}
}

func TestFixFor(t *testing.T) {
	who := RunsAs{UID: 1026, GID: 100, Name: "maktaba"}
	cases := []struct {
		platform, path, key, param, want string
	}{
		{"dsm", "/volume1/Movies/Old", "library.fix.dsm", "share", "Movies"},
		{"dsm", "/volume1/Movies", "library.fix.dsm", "name", "maktaba"},
		{"dsm", "/srv/films", "library.fix.dsm", "share", "/srv/films"},
		{"truenas", "/mnt/tank/media", "library.fix.truenas", "dataset", "tank/media"},
		{"truenas", "/mnt/tank/media", "library.fix.truenas", "uid", "1026"},
		{"unraid", "/mnt/user/Movies", "library.fix.unraid", "share", "Movies"},
		{"docker", "/media", "library.fix.other", "path", "/media"},
		{"", "/media", "library.fix.other", "uid", "1026"},
	}
	for _, c := range cases {
		f := FixFor(c.platform, c.path, who)
		if f.Key != c.key || f.Params[c.param] != c.want {
			t.Errorf("%s %s: %+v", c.platform, c.path, f)
		}
		for _, k := range []string{"share", "dataset", "name", "uid", "path"} {
			if _, ok := f.Params[k]; !ok {
				t.Errorf("%s: no %s", c.key, k)
			}
		}
	}
	if f := FixFor("dsm", "/volume1/A", RunsAs{UID: 568}); f.Params["name"] != "568" {
		t.Errorf("nameless user: %+v", f)
	}
}

func TestSkipDir(t *testing.T) {
	for _, n := range []string{"#recycle", "#snapshot", "lost+found", "@eaDir", ".zfs", ".snapshot", ".Trash-1000"} {
		if !SkipDir(n) {
			t.Errorf("%s not skipped", n)
		}
	}
	if SkipDir("Movies") || SkipDir("recycle") {
		t.Error("skipped a library folder")
	}
}

// A relative root is None, and nothing is written relative to the working folder.
func TestRelativeRootIsNone(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.Mkdir("lib", 0o755); err != nil {
		t.Fatal(err)
	}
	if r := Check("lib"); r.Access != None {
		t.Fatalf("relative root: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(dir, "lib", ".maktaba")); !os.IsNotExist(err) {
		t.Fatalf(".maktaba written under a relative root: %v", err)
	}
}

// A role is given the platform maktaba-server detected; on its own it reads MAKTABA_PLATFORM.
func TestPlatformFromEnv(t *testing.T) {
	t.Setenv(EnvHostPlatform, "")
	t.Setenv("MAKTABA_PLATFORM", "")
	if p := PlatformFromEnv(); p != "" {
		t.Fatalf("none set: %q", p)
	}
	t.Setenv("MAKTABA_PLATFORM", " unraid ")
	if p := PlatformFromEnv(); p != PlatformUnraid {
		t.Fatalf("MAKTABA_PLATFORM: %q", p)
	}
	t.Setenv(EnvHostPlatform, PlatformDSM)
	if p := PlatformFromEnv(); p != PlatformDSM {
		t.Fatalf("detected platform first: %q", p)
	}
}
